// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"wardlock.nz/server/internal/store"
)

// OAuth and OIDC sign-in (A2.5). A visitor starts on the site's login page;
// the provider sends them back to the central auth origin, which checks
// state, PKCE, the nonce and the policy's allowlist, then hands the site a
// single-use, 60-second code bound to the site, its host and the browser
// that started (through a binding cookie on the site host). The site host
// exchanges the code for a host-only session. Flows and codes live in
// memory, like the rate limits: a restart drops the ones in flight.

var (
	// ErrUnknownProvider means the form named a provider the operator has
	// not configured.
	ErrUnknownProvider = errors.New("unknown sign-in provider")
	// ErrOAuthDisabled means the policy governing the path does not enable
	// OAuth, or no longer does.
	ErrOAuthDisabled = errors.New("oauth not enabled for this path")
	// ErrOAuthState covers every callback that does not match a live
	// flow: unknown, replayed, expired, or for another provider.
	ErrOAuthState = errors.New("unknown or expired sign-in")
	// ErrOAuthDenied means the provider reported an error (the visitor
	// cancelled, or the provider refused).
	ErrOAuthDenied = errors.New("provider refused sign-in")
	// ErrOAuthFailed means the code exchange or identity check failed:
	// a PKCE or nonce mismatch, a bad ID token, a provider outage.
	ErrOAuthFailed = errors.New("provider sign-in failed")
	// ErrNoVerifiedEmail means the account has no verified email address,
	// the only subject a session can have.
	ErrNoVerifiedEmail = errors.New("no verified email")
	// ErrNotAllowed means no verified email, domain or GitHub organisation
	// of the account is on the policy's allowlist.
	ErrNotAllowed = errors.New("account not on the allowlist")
	// ErrOAuthCode covers every refused site code: unknown, replayed,
	// expired, for another site or host, or without the browser's binding
	// cookie. Codes are spent by any attempt.
	ErrOAuthCode = errors.New("invalid sign-in code")
)

// Lifetimes and bounds of the in-memory OAuth state.
const (
	// OAuthFlowLifetime is how long a visitor has at the provider; the
	// binding cookie lives as long.
	OAuthFlowLifetime = 10 * time.Minute
	// OAuthCodeLifetime is how long a site code lives (SECURITY-MODEL).
	OAuthCodeLifetime = 60 * time.Second
	// maxOAuthEntries bounds flows and codes alike, so a flood of starts
	// costs bounded memory; maxOAuthEntriesPerSite keeps a flood aimed at
	// one site from using it all, the per-IP start limit keeps one client
	// from filling a site's share, and the network share (limit.go) keeps
	// fewer than ten networks from filling it.
	maxOAuthEntries        = 50_000
	maxOAuthEntriesPerSite = 1_000
	// maxGitHubOrgChecks bounds the membership lookups one sign-in makes.
	maxGitHubOrgChecks = 20
	// OAuthIdentifyTimeout bounds a provider's whole code exchange and
	// identity lookups at the auth origin.
	OAuthIdentifyTimeout = 15 * time.Second
)

// OAuthBindingCookieName is the cookie that ties a flow to the browser that
// started it. Host-only on the site host, like the session cookie; the
// auth origin never sets cookies.
const OAuthBindingCookieName = "__Host-wardlock_oauth"

// Provider is one operator-configured OAuth or OIDC provider. Package
// oauth implements it over HTTP; access keeps every allow and deny.
type Provider interface {
	// ID names the provider in forms and its callback path:
	// /callback/<ID> on the auth origin.
	ID() string
	// Name is shown on the login page.
	Name() string
	// GitHub reports whether the provider is GitHub, the only one whose
	// identities carry organisations.
	GitHub() bool
	// Origins are the schemes and hosts of its authorisation endpoint as
	// far as it knows them (configured, or found by OIDC discovery), which
	// the login page's form-action must allow the redirect to.
	Origins() []string
	// AuthCodeURL is where the visitor's browser goes to sign in.
	AuthCodeURL(ctx context.Context, a AuthRequest) (string, error)
	// Identify exchanges the provider's code and returns the verified
	// identity behind it. It checks PKCE (the provider does) and, for
	// OIDC, the ID token's signature, issuer, audience, expiry and nonce.
	Identify(ctx context.Context, x Exchange) (Identity, error)
}

// AuthRequest is what a provider's authorisation URL carries.
type AuthRequest struct {
	RedirectURI string
	State       string
	// Challenge is the PKCE S256 challenge of the flow's verifier.
	Challenge string
	// Nonce is checked in the OIDC ID token.
	Nonce string
	// Orgs asks GitHub for organisation access (read:org), only when the
	// policy lists organisations.
	Orgs bool
}

// Exchange is what Identify needs to redeem a provider code.
type Exchange struct {
	RedirectURI string
	Code        string
	Verifier    string
	Nonce       string
	// Orgs are the GitHub organisations (lowercased logins) to check the
	// account's active membership of.
	Orgs []string
}

// Identity is a provider account as Wardlock sees it.
type Identity struct {
	// Emails are the account's verified addresses, primary first. An
	// unverified address is never included.
	Emails []string
	// Orgs are the Exchange.Orgs the account is an active member of
	// (GitHub only).
	Orgs []string
}

// ProviderInfo is a provider as the login page lists it.
type ProviderInfo struct {
	ID   string
	Name string
	// Origins are where pressing its button may lead within the form's
	// redirect chain: Provider.Origins and the auth origin (a provider that
	// remembers the visitor redirects straight back to it).
	Origins []string
}

// OAuthConfig configures OAuth sign-in. Without providers the method is
// unavailable, and enabling it on a policy is refused.
type OAuthConfig struct {
	Providers []Provider
	// AuthOrigin is the auth origin's scheme and host, such as
	// https://auth.example-sites.net; provider callbacks go to
	// AuthOrigin/callback/<provider>.
	AuthOrigin string
	// SiteOrigin returns the scheme and host visitors use for a site
	// host; codes go to SiteOrigin(host)/.wardlock/callback.
	SiteOrigin func(host string) string
}

// oauthFlow is a sign-in waiting for the provider, keyed by the hash of its
// state.
type oauthFlow struct {
	provider string
	siteID   string
	host     string
	path     string
	policyID string
	verifier string
	nonce    string
	binding  string // hash of the binding cookie
	expires  time.Time
}

// oauthCode is a site code waiting for /.wardlock/callback, keyed by its
// hash.
type oauthCode struct {
	siteID        string
	host          string
	path          string
	policyID      string
	policyVersion int
	subject       string
	binding       string
	expires       time.Time
}

// oauthState holds the flows and codes in flight. Entries are taken out
// when looked up, so each is single-use whatever the outcome.
type oauthState struct {
	flows *pending[oauthFlow]
	codes *pending[oauthCode]
}

func newOAuthState() *oauthState {
	return &oauthState{
		flows: newPending[oauthFlow](maxOAuthEntries, maxOAuthEntriesPerSite),
		codes: newPending[oauthCode](maxOAuthEntries, maxOAuthEntriesPerSite),
	}
}

// Purposes for the OAuth hashes, apart from every other use of the key.
const (
	purposeOAuthState   = "oauth_state"
	purposeOAuthCode    = "oauth_code"
	purposeOAuthBinding = "oauth_binding"
)

func (k keyed) hashOf(purpose, value string) string {
	return base64.RawURLEncoding.EncodeToString(k.mac(purpose, []byte(value)))
}

// pkceChallenge is the S256 challenge for verifier (RFC 7636).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// OAuthProviders lists the configured providers in configuration order.
func (s *Service) OAuthProviders() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(s.c.OAuth.Providers))
	for _, p := range s.c.OAuth.Providers {
		origins := append(p.Origins(), strings.TrimSuffix(s.c.OAuth.AuthOrigin, "/"))
		out = append(out, ProviderInfo{ID: p.ID(), Name: p.Name(), Origins: origins})
	}
	return out
}

func (s *Service) provider(id string) (Provider, bool) {
	for _, p := range s.c.OAuth.Providers {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// OAuthStart is a visitor pressing a provider's button on a site's login
// page.
type OAuthStart struct {
	SiteID string
	// Host is the canonical host the visitor is on; the code and the
	// session go back to it.
	Host string
	// Path is the cleaned path to sign in for and return to.
	Path     string
	Provider string
	IP       string
}

// OAuthStarted says where to send the visitor and which binding cookie to
// set on the site host first.
type OAuthStarted struct {
	URL     string
	Binding Cookie
}

// StartOAuth begins a flow for the policy governing a.Path, which must
// enable OAuth. Starts are limited per client, since each holds memory.
func (s *Service) StartOAuth(ctx context.Context, a OAuthStart) (OAuthStarted, error) {
	prov, ok := s.provider(a.Provider)
	if !ok {
		return OAuthStarted{}, ErrUnknownProvider
	}
	if ok, wait := s.limiter.allow("oauth-start:"+ipKey(a.IP), s.limits.OAuthStartsPerIP); !ok {
		return OAuthStarted{}, &RateLimitError{RetryAfter: wait}
	}
	ps, err := s.sitePolicies(ctx, a.SiteID)
	if err != nil {
		return OAuthStarted{}, err
	}
	p, ok := Governing(ps, a.Path)
	if !ok || !enables(p, store.SessionOAuth) {
		return OAuthStarted{}, ErrOAuthDisabled
	}
	now := s.now()
	// A site holds at most maxOAuthEntriesPerSite flows; while it holds
	// half, each network may start only its share of them (limit.go).
	perSite := Rate{N: maxOAuthEntriesPerSite, Per: OAuthFlowLifetime}
	if ok, wait := s.netLimiter.allow("oauth-start-net:"+a.SiteID+":"+netKey(a.IP), netRate(perSite, s.oauth.flows.siteCount(a.SiteID, now))); !ok {
		return OAuthStarted{}, &RateLimitError{RetryAfter: wait}
	}
	state, verifier, nonce, binding := newToken(), newToken(), newToken(), newToken()
	f := oauthFlow{
		provider: prov.ID(), siteID: a.SiteID, host: a.Host, path: a.Path, policyID: p.ID,
		verifier: verifier, nonce: nonce, binding: s.key.hashOf(purposeOAuthBinding, binding),
		expires: now.Add(OAuthFlowLifetime),
	}
	if !s.oauth.flows.put(s.key.hashOf(purposeOAuthState, state), f.siteID, f, f.expires, now) {
		return OAuthStarted{}, &RateLimitError{RetryAfter: time.Minute}
	}
	u, err := prov.AuthCodeURL(ctx, AuthRequest{
		RedirectURI: s.redirectURI(prov), State: state, Challenge: pkceChallenge(verifier), Nonce: nonce,
		Orgs: prov.GitHub() && len(p.Allowlist.GitHubOrgs) > 0,
	})
	if err != nil {
		return OAuthStarted{}, fmt.Errorf("%w: authorisation URL: %w", ErrOAuthFailed, err)
	}
	return OAuthStarted{URL: u, Binding: Cookie{Name: OAuthBindingCookieName, Value: binding, Expires: f.expires}}, nil
}

func (s *Service) redirectURI(p Provider) string {
	return strings.TrimSuffix(s.c.OAuth.AuthOrigin, "/") + "/callback/" + p.ID()
}

// OAuthCallback is the provider's redirect to the auth origin.
type OAuthCallback struct {
	Provider string
	State    string
	Code     string
	// Error is the provider's error parameter, when it sent one.
	Error string
}

// OAuthFinished says where the auth origin sends the visitor: the site's
// callback with a code on success. Return, when the flow was found, is the
// site's login page for the path, for the error page to link back to.
type OAuthFinished struct {
	URL    string
	Return string
}

// FinishOAuth completes a flow at the auth origin. The flow is spent by
// any callback naming its state. On success it returns the site callback
// URL with a fresh code bound to the flow's site, host, path, policy and
// binding cookie.
func (s *Service) FinishOAuth(ctx context.Context, cb OAuthCallback) (OAuthFinished, error) {
	if cb.State == "" {
		return OAuthFinished{}, ErrOAuthState
	}
	f, ok := s.oauth.flows.take(s.key.hashOf(purposeOAuthState, cb.State), s.now())
	if !ok || f.provider != cb.Provider {
		return OAuthFinished{}, ErrOAuthState
	}
	out := OAuthFinished{Return: s.siteOrigin(f.host) + "/.wardlock/login?next=" + url.QueryEscape(f.path)}
	if cb.Error != "" || cb.Code == "" {
		return out, ErrOAuthDenied
	}
	prov, ok := s.provider(f.provider)
	if !ok {
		return out, ErrOAuthState
	}
	p, err := s.oauthPolicy(ctx, f.siteID, f.path, f.policyID)
	if err != nil {
		return out, err
	}
	list := p.Allowlist
	var orgs []string
	if prov.GitHub() {
		orgs = list.GitHubOrgs[:min(len(list.GitHubOrgs), maxGitHubOrgChecks)]
	}
	// One deadline for the whole provider exchange, however many requests
	// it makes, so a slow provider cannot hold the callback open.
	idCtx, cancel := context.WithTimeout(ctx, OAuthIdentifyTimeout)
	defer cancel()
	id, err := prov.Identify(idCtx, Exchange{
		RedirectURI: s.redirectURI(prov), Code: cb.Code, Verifier: f.verifier, Nonce: f.nonce, Orgs: orgs,
	})
	if err != nil {
		// The error names what failed, never a code or token.
		s.log.LogAttrs(ctx, slog.LevelWarn, "oauth sign-in failed",
			slog.String("provider", prov.ID()), slog.String("site_id", f.siteID), slog.Any("error", err))
		return out, ErrOAuthFailed
	}
	subject, err := matchIdentity(list, id, prov.GitHub())
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelInfo, "oauth sign-in refused",
			slog.String("provider", prov.ID()), slog.String("site_id", f.siteID), slog.String("reason", err.Error()))
		return out, err
	}
	code := newToken()
	now := s.now()
	c := oauthCode{
		siteID: f.siteID, host: f.host, path: f.path, policyID: p.ID, policyVersion: p.Version,
		subject: subject, binding: f.binding, expires: now.Add(OAuthCodeLifetime),
	}
	if !s.oauth.codes.put(s.key.hashOf(purposeOAuthCode, code), c.siteID, c, c.expires, now) {
		return out, &RateLimitError{RetryAfter: time.Minute}
	}
	out.URL = s.siteOrigin(f.host) + "/.wardlock/callback?code=" + url.QueryEscape(code)
	return out, nil
}

func (s *Service) siteOrigin(host string) string {
	if s.c.OAuth.SiteOrigin == nil {
		return "https://" + host
	}
	return strings.TrimSuffix(s.c.OAuth.SiteOrigin(host), "/")
}

// oauthPolicy returns the policy governing path on siteID if it is still
// policyID and still enables OAuth.
func (s *Service) oauthPolicy(ctx context.Context, siteID, path, policyID string) (store.Policy, error) {
	ps, err := s.sitePolicies(ctx, siteID)
	if err != nil {
		return store.Policy{}, err
	}
	p, ok := Governing(ps, path)
	if !ok || p.ID != policyID || !enables(p, store.SessionOAuth) {
		return store.Policy{}, ErrOAuthDisabled
	}
	return p, nil
}

// OAuthRedeem is a site code arriving at /.wardlock/callback.
type OAuthRedeem struct {
	SiteID string
	Host   string
	Code   string
	// Binding is the OAuthBindingCookieName cookie's value; "" without.
	Binding string
}

// OAuthRedeemed is the session a code earned and the path to return to.
type OAuthRedeemed struct {
	Cookie Cookie
	Next   string
}

// RedeemOAuth exchanges a site code for a session. The code is spent by
// the first attempt, whatever its outcome; it must be for this site and
// host, within its 60 seconds, come with the binding cookie of the browser
// that started the flow, and its policy must be unchanged since the
// allowlist check.
func (s *Service) RedeemOAuth(ctx context.Context, r OAuthRedeem) (OAuthRedeemed, error) {
	if r.Code == "" {
		return OAuthRedeemed{}, ErrOAuthCode
	}
	c, ok := s.oauth.codes.take(s.key.hashOf(purposeOAuthCode, r.Code), s.now())
	if !ok || c.siteID != r.SiteID || c.host != r.Host || r.Binding == "" ||
		!hmacEqual(c.binding, s.key.hashOf(purposeOAuthBinding, r.Binding)) {
		return OAuthRedeemed{}, ErrOAuthCode
	}
	p, err := s.oauthPolicy(ctx, c.siteID, c.path, c.policyID)
	if errors.Is(err, ErrOAuthDisabled) || err == nil && p.Version != c.policyVersion {
		return OAuthRedeemed{}, ErrOAuthCode
	}
	if err != nil {
		return OAuthRedeemed{}, err
	}
	cookie, err := s.IssueSession(ctx, NewSession{SiteID: c.siteID, Policy: p, Method: store.SessionOAuth, Subject: c.subject})
	if err != nil {
		return OAuthRedeemed{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "oauth session issued",
		slog.String("site_id", c.siteID), slog.String("policy_id", p.ID))
	return OAuthRedeemed{Cookie: cookie, Next: c.path}, nil
}

// matchIdentity returns the session subject for id under list: the first
// verified address the list allows, or, for a GitHub account in a listed
// organisation, its primary verified address. An account with no verified
// address never matches, since a session's subject is a verified email.
func matchIdentity(list store.Allowlist, id Identity, github bool) (string, error) {
	var addrs []string
	for _, e := range id.Emails {
		if addr, ok := providerAddress(e); ok {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return "", ErrNoVerifiedEmail
	}
	for _, addr := range addrs {
		if allows(list, addr) {
			return addr, nil
		}
	}
	if github {
		for _, o := range id.Orgs {
			if slices.Contains(list.GitHubOrgs, strings.ToLower(o)) {
				return addrs[0], nil
			}
		}
	}
	return "", ErrNotAllowed
}

// providerAddress normalises an address a provider verified as allowlists
// store them, with an internationalised domain in its A-label form.
func providerAddress(raw string) (string, bool) {
	local, domain, ok := strings.Cut(strings.TrimSpace(raw), "@")
	if !ok {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(domain)
	if err != nil {
		return "", false
	}
	return NormaliseAddress(local + "@" + ascii)
}

func hmacEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
