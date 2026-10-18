// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package oauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"wardlock.nz/server/internal/access"
)

// GoogleIssuer is Google's OIDC issuer.
const GoogleIssuer = "https://accounts.google.com"

// OIDCConfig configures an OpenID Connect provider.
type OIDCConfig struct {
	// ID is the provider's form value and callback path segment: "google"
	// or "oidc".
	ID string
	// Name is shown on the login page.
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

// OIDC signs visitors in with an OpenID Connect provider, Google included.
// Discovery runs on first use, not at startup, so a provider outage never
// stops the server; a failed discovery is retried on the next sign-in.
// The identity is the ID token's email when email_verified is true (or,
// when the token has no email, the userinfo endpoint's); a provider that
// does not assert verification yields no verified email.
type OIDC struct {
	c      OIDCConfig
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

var _ access.Provider = (*OIDC)(nil)

// NewOIDC returns an OIDC provider.
func NewOIDC(c OIDCConfig) *OIDC {
	o := &OIDC{c: c, client: c.HTTPClient}
	if o.client == nil {
		o.client = NewHTTPClient()
	}
	return o
}

// NewGoogle returns the Google provider: OIDC at GoogleIssuer.
func NewGoogle(clientID, clientSecret string, client *http.Client) *OIDC {
	return NewOIDC(OIDCConfig{ID: "google", Name: "Google", Issuer: GoogleIssuer, ClientID: clientID, ClientSecret: clientSecret, HTTPClient: client})
}

func (o *OIDC) ID() string   { return o.c.ID }
func (o *OIDC) Name() string { return o.c.Name }
func (*OIDC) GitHub() bool   { return false }

func (o *OIDC) ctx(ctx context.Context) context.Context {
	return oidc.ClientContext(context.WithValue(ctx, oauth2.HTTPClient, o.client), o.client)
}

// discover returns the provider's metadata, fetching it once.
func (o *OIDC) discover(ctx context.Context) (*oidc.Provider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.provider, nil
	}
	p, err := oidc.NewProvider(o.ctx(ctx), o.c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: OIDC discovery for %s: %w", ErrProvider, o.c.Issuer, err)
	}
	o.provider = p
	return p, nil
}

func (o *OIDC) config(p *oidc.Provider, redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: o.c.ClientID, ClientSecret: o.c.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: redirectURI,
		Scopes: []string{oidc.ScopeOpenID, "email"},
	}
}

// AuthCodeURL implements access.Provider.
func (o *OIDC) AuthCodeURL(ctx context.Context, a access.AuthRequest) (string, error) {
	p, err := o.discover(ctx)
	if err != nil {
		return "", err
	}
	opts := append(pkceOptions(a.Challenge), oidc.Nonce(a.Nonce))
	return o.config(p, a.RedirectURI).AuthCodeURL(a.State, opts...), nil
}

// emailClaims are the claims Wardlock reads. email_verified is a boolean,
// though some providers send the string "true".
type emailClaims struct {
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
}

func (c emailClaims) verified() bool {
	switch string(c.EmailVerified) {
	case "true", `"true"`:
		return true
	}
	return false
}

// Identify implements access.Provider.
func (o *OIDC) Identify(ctx context.Context, x access.Exchange) (access.Identity, error) {
	p, err := o.discover(ctx)
	if err != nil {
		return access.Identity{}, err
	}
	tok, err := exchange(ctx, o.client, o.config(p, x.RedirectURI), x.Code, x.Verifier)
	if err != nil {
		return access.Identity{}, err
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return access.Identity{}, fmt.Errorf("%w: none in the token response", ErrIDToken)
	}
	idt, err := p.VerifierContext(o.ctx(ctx), &oidc.Config{ClientID: o.c.ClientID}).Verify(o.ctx(ctx), raw)
	if err != nil {
		return access.Identity{}, fmt.Errorf("%w: %w", ErrIDToken, err)
	}
	if x.Nonce == "" || subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(x.Nonce)) != 1 {
		return access.Identity{}, ErrNonce
	}
	var c emailClaims
	if err := idt.Claims(&c); err != nil {
		return access.Identity{}, fmt.Errorf("%w: claims: %w", ErrIDToken, err)
	}
	if c.Email == "" && p.UserInfoEndpoint() != "" {
		info, err := p.UserInfo(o.ctx(ctx), oauth2.StaticTokenSource(tok))
		if err != nil {
			return access.Identity{}, fmt.Errorf("%w: userinfo: %w", ErrProvider, err)
		}
		// The userinfo subject must be the ID token's (OIDC Core 5.3.2).
		if info.Subject != idt.Subject {
			return access.Identity{}, fmt.Errorf("%w: userinfo subject differs from the ID token's", ErrProvider)
		}
		if err := info.Claims(&c); err != nil {
			return access.Identity{}, fmt.Errorf("%w: userinfo claims: %w", ErrProvider, err)
		}
	}
	var id access.Identity
	if addr := normaliseEmail(c.Email); addr != "" && c.verified() {
		id.Emails = []string{addr}
	}
	return id, nil
}
