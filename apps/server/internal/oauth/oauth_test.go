// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package oauth_test

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/oauth"
	"wardlock.nz/server/internal/oauth/oauthtest"
)

const redirectURI = "http://auth.wardlock.localhost/callback/test"

// Synthetic values for tests only.
const (
	verifier = "test-verifier-0123456789-0123456789-0123456789"
	nonce    = "test-nonce"
	state    = "test-state"
)

func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func githubFor(f *oauthtest.Provider) *oauth.GitHub {
	return oauth.NewGitHub(oauth.GitHubConfig{
		ClientID: oauthtest.ClientID, ClientSecret: oauthtest.ClientSecret(),
		AuthURL: f.URL() + "/authorize", TokenURL: f.URL() + "/token", APIURL: f.URL(),
	})
}

func oidcFor(f *oauthtest.Provider) *oauth.OIDC {
	return oauth.NewOIDC(oauth.OIDCConfig{
		ID: "oidc", Name: "Test SSO", Issuer: f.URL(), ClientID: oauthtest.ClientID, ClientSecret: oauthtest.ClientSecret(),
	})
}

// signIn runs the browser's part: the authorisation URL, then the fake's
// redirect back, returning the provider code.
func signIn(t *testing.T, p access.Provider, orgs bool) string {
	t.Helper()
	u, err := p.AuthCodeURL(t.Context(), access.AuthRequest{
		RedirectURI: redirectURI, State: state, Challenge: challenge(verifier), Nonce: nonce, Orgs: orgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := oauth.NewHTTPClient().Get(u)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("authorise: %d", res.StatusCode)
	}
	back, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if back.Query().Get("state") != state {
		t.Fatalf("state %q came back", back.Query().Get("state"))
	}
	return back.Query().Get("code")
}

func TestGitHubIdentity(t *testing.T) {
	f := oauthtest.New(t, oauthtest.GitHub, oauthtest.Account{
		Emails: []oauthtest.Email{
			{Address: "Second@Example.test", Verified: true},
			{Address: "unverified@example.test"},
			{Address: "Primary@Example.test", Primary: true, Verified: true},
		},
		Orgs: []string{"Harbour-Org"},
	})
	g := githubFor(f)
	code := signIn(t, g, true)
	q := f.LastAuth()
	if q.Get("scope") != "user:email read:org" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != challenge(verifier) {
		t.Fatalf("authorisation query %v", q)
	}
	id, err := g.Identify(t.Context(), access.Exchange{
		RedirectURI: redirectURI, Code: code, Verifier: verifier, Orgs: []string{"harbour-org", "other-org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(id.Emails, []string{"primary@example.test", "second@example.test"}) || !slices.Equal(id.Orgs, []string{"harbour-org"}) {
		t.Fatalf("identity %+v", id)
	}
	if signIn(t, g, false); f.LastAuth().Get("scope") != "user:email" {
		t.Fatalf("scope without organisations: %q", f.LastAuth().Get("scope"))
	}
}

func TestExchangeFailures(t *testing.T) {
	for _, kind := range []oauthtest.Kind{oauthtest.GitHub, oauthtest.OIDC} {
		f := oauthtest.New(t, kind, oauthtest.Account{Subject: "s1", Emails: []oauthtest.Email{{Address: "a@example.test", Primary: true, Verified: true}}})
		var p access.Provider = githubFor(f)
		if kind == oauthtest.OIDC {
			p = oidcFor(f)
		}
		code := signIn(t, p, false)
		// A wrong PKCE verifier is refused and spends the code.
		if _, err := p.Identify(t.Context(), access.Exchange{RedirectURI: redirectURI, Code: code, Verifier: verifier + "x", Nonce: nonce}); !errors.Is(err, oauth.ErrExchange) {
			t.Fatalf("%s wrong verifier: %v", p.ID(), err)
		}
		if _, err := p.Identify(t.Context(), access.Exchange{RedirectURI: redirectURI, Code: code, Verifier: verifier, Nonce: nonce}); !errors.Is(err, oauth.ErrExchange) {
			t.Fatalf("%s replayed code: %v", p.ID(), err)
		}
		// The provider's error body never reaches the error text.
		code = signIn(t, p, false)
		_, err := p.Identify(t.Context(), access.Exchange{RedirectURI: redirectURI + "/other", Code: code, Verifier: verifier, Nonce: nonce})
		if !errors.Is(err, oauth.ErrExchange) || err.Error() != "code exchange failed: invalid_grant" {
			t.Fatalf("%s wrong redirect URI: %v", p.ID(), err)
		}
	}
}

func TestOIDCIdentity(t *testing.T) {
	verified := []oauthtest.Email{{Address: "Visitor@Example.test", Primary: true, Verified: true}}
	tests := []struct {
		name    string
		opts    func(*oauthtest.Options)
		emails  []string
		wantErr error
	}{
		{"verified", func(o *oauthtest.Options) {}, []string{"visitor@example.test"}, nil},
		{"verified as a string", func(o *oauthtest.Options) { o.Account.EmailVerifiedString = true }, []string{"visitor@example.test"}, nil},
		{"email from userinfo", func(o *oauthtest.Options) { o.Account.EmailInUserInfo = true }, []string{"visitor@example.test"}, nil},
		{"unverified", func(o *oauthtest.Options) {
			o.Account.Emails = []oauthtest.Email{{Address: "v@example.test", Primary: true}}
		}, nil, nil},
		{"unverified as a string", func(o *oauthtest.Options) {
			o.Account.EmailVerifiedString = true
			o.Account.Emails = []oauthtest.Email{{Address: "v@example.test", Primary: true}}
		}, nil, nil},
		{"verification not asserted", func(o *oauthtest.Options) { o.Account.OmitEmailVerified = true }, nil, nil},
		{"nonce mismatch", func(o *oauthtest.Options) { o.Nonce = "another-nonce" }, nil, oauth.ErrNonce},
		{"wrong issuer", func(o *oauthtest.Options) { o.Issuer = "https://issuer.example.test" }, nil, oauth.ErrIDToken},
		{"wrong audience", func(o *oauthtest.Options) { o.Audience = "another-client" }, nil, oauth.ErrIDToken},
		{"expired ID token", func(o *oauthtest.Options) { o.Expired = true }, nil, oauth.ErrIDToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := oauthtest.New(t, oauthtest.OIDC, oauthtest.Account{Subject: "s1", Emails: verified})
			f.Configure(tt.opts)
			p := oidcFor(f)
			code := signIn(t, p, false)
			if q := f.LastAuth(); q.Get("nonce") != nonce || q.Get("scope") != "openid email" || q.Get("code_challenge_method") != "S256" {
				t.Fatalf("authorisation query %v", q)
			}
			id, err := p.Identify(t.Context(), access.Exchange{RedirectURI: redirectURI, Code: code, Verifier: verifier, Nonce: nonce})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(id.Emails, tt.emails) || id.Orgs != nil {
				t.Fatalf("identity %+v, want emails %v", id, tt.emails)
			}
		})
	}
}

func TestOIDCDiscoveryIsLazyAndRetried(t *testing.T) {
	p := oauth.NewOIDC(oauth.OIDCConfig{ID: "oidc", Issuer: "http://127.0.0.1:1", ClientID: "x", ClientSecret: "y"})
	if _, err := p.AuthCodeURL(t.Context(), access.AuthRequest{}); !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("unreachable issuer: %v", err)
	}
	g := oauth.NewGoogle("id", "secret", nil)
	if g.ID() != "google" || g.Name() != "Google" || g.GitHub() {
		t.Fatalf("google provider %s %s", g.ID(), g.Name())
	}
}
