// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package oauthtest is an in-process fake OAuth and OIDC provider for tests:
// GitHub's OAuth app endpoints and API, or an OIDC issuer with discovery,
// JWKS, signed ID tokens and userinfo. It enforces PKCE S256, single-use
// codes, the redirect URI and client credentials like a real provider, so
// a test can drive the whole browser flow. Not for production use.
package oauthtest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// ClientID is the client the fake accepts.
const ClientID = "fake-client-id"

// ClientSecret is the client secret the fake accepts.
func ClientSecret() string { return "fake-client-secret" }

// Email is an address on the fake account.
type Email struct {
	Address  string
	Primary  bool
	Verified bool
}

// Account is who signs in at the fake provider's authorisation endpoint.
type Account struct {
	Subject string
	Emails  []Email
	// Orgs are GitHub organisation logins with an active membership.
	Orgs []string
	// EmailVerifiedString sends OIDC email_verified as "true"/"false".
	EmailVerifiedString bool
	// OmitEmailVerified leaves email_verified out of the ID token.
	OmitEmailVerified bool
	// EmailInUserInfo puts the email only in userinfo, not the ID token.
	EmailInUserInfo bool
}

// Kind selects what the fake imitates.
type Kind int

const (
	GitHub Kind = iota + 1
	OIDC
)

// Options change how the fake answers, to imitate faults and attacks.
type Options struct {
	// Account signs in on every authorisation request.
	Account Account
	// Nonce, when set, replaces the flow's nonce in ID tokens.
	Nonce string
	// Issuer, when set, replaces the real issuer in ID tokens.
	Issuer string
	// Audience, when set, replaces the client ID in ID tokens.
	Audience string
	// Expired issues ID tokens that expired an hour ago.
	Expired bool
	// Deny makes the authorisation endpoint answer error=access_denied.
	Deny bool
	// Challenge, when set, replaces the flow's PKCE challenge, so the
	// client's verifier no longer matches.
	Challenge string
}

// Provider is a running fake.
type Provider struct {
	Kind   Kind
	Server *httptest.Server

	key *rsa.PrivateKey

	mu     sync.Mutex
	opts   Options
	codes  map[string]grant
	tokens map[string]Account
	// Requests counts calls per path.
	requests map[string]int
	lastAuth url.Values
}

type grant struct {
	redirectURI string
	challenge   string
	nonce       string
	scope       string
	account     Account
}

// New starts a fake of kind; it stops with the test.
func New(t testing.TB, kind Kind, account Account) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{
		Kind: kind, opts: Options{Account: account}, key: key,
		codes: map[string]grant{}, tokens: map[string]Account{}, requests: map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /userinfo", p.userinfo)
	mux.HandleFunc("GET /user/emails", p.emails)
	mux.HandleFunc("GET /user/memberships/orgs/{org}", p.membership)
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.requests[r.URL.Path]++
		p.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(p.Server.Close)
	return p
}

// Configure changes the fake's options.
func (p *Provider) Configure(fn func(*Options)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.opts)
}

func (p *Provider) options() Options {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opts
}

// URL is the fake's base URL (the OIDC issuer, or GitHub's API base).
func (p *Provider) URL() string { return p.Server.URL }

// LastAuth is the most recent authorisation request's query.
func (p *Provider) LastAuth() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAuth
}

// Requests returns how many requests path has had.
func (p *Provider) Requests(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[path]
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// authorize signs Account in at once and redirects back with a code.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	o := p.options()
	p.mu.Lock()
	p.lastAuth = q
	p.mu.Unlock()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Host == "" || q.Get("client_id") != ClientID || q.Get("response_type") != "code" {
		http.Error(w, "bad authorisation request", http.StatusBadRequest)
		return
	}
	back := redirect.Query()
	back.Set("state", q.Get("state"))
	if o.Deny {
		back.Set("error", "access_denied")
	} else {
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "PKCE required", http.StatusBadRequest)
			return
		}
		code := random()
		p.mu.Lock()
		p.codes[code] = grant{
			redirectURI: q.Get("redirect_uri"), challenge: or(o.Challenge, q.Get("code_challenge")),
			nonce: q.Get("nonce"), scope: q.Get("scope"), account: o.Account,
		}
		p.mu.Unlock()
		back.Set("code", code)
	}
	redirect.RawQuery = back.Encode()
	// The redirect URI is checked at the token endpoint, as providers do.
	w.Header().Set("Location", redirect.String())
	w.WriteHeader(http.StatusFound)
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "fake provider refused"})
}

// token redeems a code once, checking the client, redirect URI and PKCE.
func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != ClientID || secret != ClientSecret() {
		tokenError(w, "invalid_client")
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, found := p.codes[code]
	delete(p.codes, code)
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	switch {
	case r.PostForm.Get("grant_type") != "authorization_code" || !found:
		tokenError(w, "invalid_grant")
		return
	case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
		tokenError(w, "invalid_grant")
		return
	case r.PostForm.Get("redirect_uri") != g.redirectURI:
		tokenError(w, "invalid_grant")
		return
	}
	access := random()
	p.mu.Lock()
	p.tokens[access] = g.account
	p.mu.Unlock()
	res := map[string]any{"access_token": access, "token_type": "bearer", "scope": g.scope}
	if p.Kind == OIDC {
		res["id_token"] = p.idToken(g)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (p *Provider) idToken(g grant) string {
	o := p.options()
	now := time.Now()
	if o.Expired {
		now = now.Add(-2 * time.Hour)
	}
	claims := map[string]any{
		"iss": or(o.Issuer, p.URL()), "aud": or(o.Audience, ClientID), "sub": g.account.Subject,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": or(o.Nonce, g.nonce),
	}
	if e, ok := primary(g.account); ok && !g.account.EmailInUserInfo {
		claims["email"] = e.Address
		if !g.account.OmitEmailVerified {
			claims["email_verified"] = verifiedClaim(g.account, e)
		}
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "fake"))
	if err != nil {
		panic(err)
	}
	payload, _ := json.Marshal(claims)
	jws, err := signer.Sign(payload)
	if err != nil {
		panic(err)
	}
	s, err := jws.CompactSerialize()
	if err != nil {
		panic(err)
	}
	return s
}

func verifiedClaim(a Account, e Email) any {
	if a.EmailVerifiedString {
		if e.Verified {
			return "true"
		}
		return "false"
	}
	return e.Verified
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func primary(a Account) (Email, bool) {
	for _, e := range a.Emails {
		if e.Primary {
			return e, true
		}
	}
	if len(a.Emails) > 0 {
		return a.Emails[0], true
	}
	return Email{}, false
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	if p.Kind != OIDC {
		http.NotFound(w, nil)
		return
	}
	base := p.URL()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token",
		"jwks_uri": base + "/jwks", "userinfo_endpoint": base + "/userinfo",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &p.key.PublicKey, KeyID: "fake", Algorithm: "RS256", Use: "sig"},
	}})
}

// bearer returns the account an access token was issued for.
func (p *Provider) bearer(r *http.Request) (Account, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return Account{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.tokens[tok]
	return a, ok
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	a, ok := p.bearer(r)
	if !ok || p.Kind != OIDC {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	info := map[string]any{"sub": a.Subject}
	if e, ok := primary(a); ok {
		info["email"] = e.Address
		if !a.OmitEmailVerified {
			info["email_verified"] = verifiedClaim(a, e)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

func (p *Provider) emails(w http.ResponseWriter, r *http.Request) {
	a, ok := p.bearer(r)
	if !ok || p.Kind != GitHub {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	out := []map[string]any{}
	for _, e := range a.Emails {
		out = append(out, map[string]any{"email": e.Address, "primary": e.Primary, "verified": e.Verified})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (p *Provider) membership(w http.ResponseWriter, r *http.Request) {
	a, ok := p.bearer(r)
	if !ok || p.Kind != GitHub {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	org := r.PathValue("org")
	for _, o := range a.Orgs {
		if strings.EqualFold(o, org) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "active", "organization": map[string]string{"login": o}})
			return
		}
	}
	http.NotFound(w, r)
}
