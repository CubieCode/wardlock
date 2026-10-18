// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestOIDCDiscoveryIsSharedAndUnlocked(t *testing.T) {
	f := oauthtest.New(t, oauthtest.OIDC, oauthtest.Account{Subject: "s1"})
	f.Configure(func(o *oauthtest.Options) { o.AuthorizeEndpoint = "https://login.example.test/authorize" })
	p := oidcFor(f)
	// Before discovery only the issuer is known.
	if got := p.Origins(); len(got) < 1 || got[0] != f.URL() {
		t.Fatalf("origins before discovery %v", got)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := p.AuthCodeURL(t.Context(), access.AuthRequest{RedirectURI: redirectURI, State: state}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := f.Requests("/.well-known/openid-configuration"); n != 1 {
		t.Fatalf("%d discovery requests, want 1 (Origins may have started it)", n)
	}
	if got := p.Origins(); !slices.Equal(got, []string{f.URL(), "https://login.example.test"}) {
		t.Fatalf("origins after discovery %v", got)
	}
}

func TestOIDCSlowDiscoveryHoldsNoLock(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	defer close(release)
	p := oauth.NewOIDC(oauth.OIDCConfig{ID: "oidc", Issuer: slow.URL, ClientID: "x", ClientSecret: "y"})
	go func() { _, _ = p.AuthCodeURL(context.Background(), access.AuthRequest{}) }()
	// A caller that gives up does not wait for the fetch.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	begun := time.Now()
	if _, err := p.AuthCodeURL(ctx, access.AuthRequest{}); !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("waiting caller: %v", err)
	}
	// Rendering a login page (Origins) never waits for the network.
	p.Origins()
	if waited := time.Since(begun); waited > 2*time.Second {
		t.Fatalf("callers blocked %s behind a slow discovery", waited)
	}
}

func TestOIDCDiscoveryFailureIsCached(t *testing.T) {
	var hits atomic.Int32
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer down.Close()
	p := oauth.NewOIDC(oauth.OIDCConfig{ID: "oidc", Issuer: down.URL, ClientID: "x", ClientSecret: "y"})
	for range 3 {
		if _, err := p.AuthCodeURL(t.Context(), access.AuthRequest{}); !errors.Is(err, oauth.ErrProvider) {
			t.Fatalf("issuer down: %v", err)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("%d discovery requests within the retry window, want 1", n)
	}
}

func TestGitHubOrgChecksAreConcurrentAndBounded(t *testing.T) {
	orgs := make([]string, 20)
	for i := range orgs {
		orgs[i] = fmt.Sprintf("org-%d", i)
	}
	f := oauthtest.New(t, oauthtest.GitHub, oauthtest.Account{
		Emails: []oauthtest.Email{{Address: "a@example.test", Primary: true, Verified: true}}, Orgs: []string{"org-17"},
	})
	f.Configure(func(o *oauthtest.Options) { o.MembershipDelay = 100 * time.Millisecond })
	g := githubFor(f)

	// Twenty lookups four at a time take about five delays, not twenty.
	code := signIn(t, g, true)
	begun := time.Now()
	id, err := g.Identify(t.Context(), access.Exchange{RedirectURI: redirectURI, Code: code, Verifier: verifier, Orgs: orgs})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begun); !slices.Equal(id.Orgs, []string{"org-17"}) || took > 1500*time.Millisecond {
		t.Fatalf("orgs %v after %s", id.Orgs, took)
	}

	// One deadline bounds them all: slow lookups fail the sign-in.
	f.Configure(func(o *oauthtest.Options) { o.MembershipDelay = 5 * time.Second })
	code = signIn(t, g, true)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	begun = time.Now()
	if _, err := g.Identify(ctx, access.Exchange{RedirectURI: redirectURI, Code: code, Verifier: verifier, Orgs: orgs}); !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("slow lookups: %v", err)
	}
	if took := time.Since(begun); took > 2*time.Second {
		t.Fatalf("deadline ignored: %s", took)
	}
}
