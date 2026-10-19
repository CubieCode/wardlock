// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/oauth"
	"wardlock.nz/server/internal/oauth/oauthtest"
	"wardlock.nz/server/internal/store"
)

const authHost = "auth.wardlock.localhost"

// recordedSessions keeps the site sessions created through it.
type recordedSessions struct {
	store.Sessions
	mu      sync.Mutex
	created []store.SiteSession
}

func (r *recordedSessions) CreateSite(ctx context.Context, s store.SiteSession) error {
	r.mu.Lock()
	r.created = append(r.created, s)
	r.mu.Unlock()
	return r.Sessions.CreateSite(ctx, s)
}

func (r *recordedSessions) take() []store.SiteSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.created
	r.created = nil
	return out
}

// oauthEdge is a policyEdge with three fake providers configured.
type oauthEdge struct {
	*policyEdge
	github, google, oidc *oauthtest.Provider
	sessions             *recordedSessions
}

func newOAuthEdge(t *testing.T) *oauthEdge {
	t.Helper()
	nobody := oauthtest.Account{Subject: "nobody"}
	o := &oauthEdge{
		github: oauthtest.New(t, oauthtest.GitHub, nobody),
		google: oauthtest.New(t, oauthtest.OIDC, nobody),
		oidc:   oauthtest.New(t, oauthtest.OIDC, nobody),
	}
	o.policyEdge = newPolicyEdge(t, func(c *access.Config) {
		o.sessions = &recordedSessions{Sessions: c.Sessions}
		c.Sessions = o.sessions
		c.OAuth = access.OAuthConfig{
			Providers: []access.Provider{
				oauth.NewGitHub(oauth.GitHubConfig{
					ClientID: oauthtest.ClientID, ClientSecret: oauthtest.ClientSecret(),
					AuthURL: o.github.URL() + "/authorize", TokenURL: o.github.URL() + "/token", APIURL: o.github.URL(),
				}),
				// Google is OIDC at Google's issuer; the fake stands in.
				oauth.NewOIDC(oauth.OIDCConfig{ID: "google", Name: "Google", Issuer: o.google.URL(), ClientID: oauthtest.ClientID, ClientSecret: oauthtest.ClientSecret()}),
				oauth.NewOIDC(oauth.OIDCConfig{ID: "oidc", Name: "Harbour SSO", Issuer: o.oidc.URL(), ClientID: oauthtest.ClientID, ClientSecret: oauthtest.ClientSecret()}),
			},
			AuthOrigin: "http://" + authHost,
			SiteOrigin: func(h string) string { return "http://" + h },
		}
	})
	return o
}

func (o *oauthEdge) fake(provider string) *oauthtest.Provider {
	return map[string]*oauthtest.Provider{"github": o.github, "google": o.google, "oidc": o.oidc}[provider]
}

// protect makes site's prefix an OAuth policy with list.
func (o *oauthEdge) protect(site, prefix string, list store.Allowlist) store.Policy {
	o.t.Helper()
	pol, err := o.acc.Set(o.t.Context(), o.owner.ID, site, access.Rule{PathPrefix: prefix, Visibility: store.Protected, OAuth: true, Allowlist: &list})
	if err != nil {
		o.t.Fatal(err)
	}
	return pol
}

// hop is one request a browser made and what came back.
type hop struct {
	url string
	res result
}

// setCookie is a Set-Cookie header and the host that sent it.
type setCookie struct {
	host   string
	cookie *http.Cookie
}

// browser keeps cookies per host, host-only as the edge's cookies are,
// and follows redirects between the edge (in process) and the fakes.
type browser struct {
	o       *oauthEdge
	ip      string
	jar     map[string]map[string]string
	sets    []setCookie
	secrets []string // codes, states and cookie values seen, for log checks
}

func (o *oauthEdge) browser(ip string) *browser {
	return &browser{o: o, ip: ip, jar: map[string]map[string]string{}}
}

func (b *browser) cookie(host, name string) string { return b.jar[host][name] }

func (b *browser) setCookieValue(host, name, value string) {
	if b.jar[host] == nil {
		b.jar[host] = map[string]string{}
	}
	b.jar[host][name] = value
}

// request sends one request, as a browser on the login page would for a
// POST, and stores the cookies it sets.
func (b *browser) request(method, raw string, form url.Values) result {
	t := b.o.t
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"code", "state"} {
		if v := u.Query().Get(k); v != "" {
			b.secrets = append(b.secrets, v)
		}
	}
	var res result
	if strings.HasSuffix(u.Hostname(), "wardlock.localhost") {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, u.RequestURI(), body)
		req.Host = u.Host
		req.RemoteAddr = net.JoinHostPort(b.ip, "40000")
		req.Header.Set("Accept", "text/html")
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", "null")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		for name, value := range b.jar[u.Hostname()] {
			req.AddCookie(&http.Cookie{Name: name, Value: value})
		}
		rec := httptest.NewRecorder()
		b.o.h.ServeHTTP(rec, req)
		res = result{rec.Code, rec.Body.String(), rec.Result().Header}
	} else {
		r, err := oauth.NewHTTPClient().Get(raw)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		res = result{r.StatusCode, string(data), r.Header}
	}
	for _, c := range (&http.Response{Header: res.h}).Cookies() {
		b.sets = append(b.sets, setCookie{u.Hostname(), c})
		b.secrets = append(b.secrets, c.Value)
		if b.jar[u.Hostname()] == nil {
			b.jar[u.Hostname()] = map[string]string{}
		}
		if c.MaxAge < 0 {
			delete(b.jar[u.Hostname()], c.Name)
		} else {
			b.jar[u.Hostname()][c.Name] = c.Value
		}
	}
	return res
}

// start presses provider's button on h's login page for next.
func (b *browser) start(h, provider, next string) result {
	return b.request(http.MethodPost, "http://"+h+"/.wardlock/login",
		url.Values{"method": {"oauth"}, "provider": {provider}, "next": {next}})
}

// follow follows redirects from first (a response to from) until a
// response that is not a redirect, or until stop reports true for the
// next URL; it returns every hop and the URL it stopped before, if any.
func (b *browser) follow(from string, first result, stop func(*url.URL) bool) ([]hop, string) {
	t := b.o.t
	t.Helper()
	hops := []hop{{from, first}}
	cur, res := from, first
	for range 10 {
		if res.code < 300 || res.code > 399 {
			return hops, ""
		}
		base, _ := url.Parse(cur)
		loc, err := base.Parse(res.h.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if stop != nil && stop(loc) {
			return hops, loc.String()
		}
		cur = loc.String()
		res = b.request(http.MethodGet, cur, nil)
		hops = append(hops, hop{cur, res})
	}
	t.Fatal("too many redirects")
	return nil, ""
}

// atSiteCallback stops before the site's /.wardlock/callback.
func atSiteCallback(u *url.URL) bool { return u.Path == "/.wardlock/callback" }

// signIn runs the flow from the login button to the site callback's
// answer.
func (b *browser) signIn(h, provider, next string) []hop {
	b.o.t.Helper()
	hops, _ := b.follow("http://"+h+"/.wardlock/login", b.start(h, provider, next), func(u *url.URL) bool {
		return u.Hostname() == h && u.Path != "/.wardlock/callback"
	})
	return hops
}

func last(hops []hop) hop { return hops[len(hops)-1] }

// noCookiesOffSite asserts that only site hosts ever set cookies, and
// only host-only __Host- cookies with the fixed attributes.
func (b *browser) noCookiesOffSite(t *testing.T) {
	t.Helper()
	for _, s := range b.sets {
		c := s.cookie
		if s.host == authHost || s.host == "wardlock.localhost" {
			t.Fatalf("%s set cookie %s", s.host, c.Name)
		}
		if c.Domain != "" || c.Path != "/" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || !strings.HasPrefix(c.Name, "__Host-") {
			t.Fatalf("cookie from %s has attributes %+v", s.host, c)
		}
	}
}

// noSecretsLogged asserts that no code, state or cookie value reached
// the logs.
func (b *browser) noSecretsLogged(t *testing.T) {
	t.Helper()
	logs := b.o.logs.String()
	for _, s := range append(b.secrets, oauthtest.ClientSecret()) {
		if len(s) >= 8 && strings.Contains(logs, s) {
			t.Fatalf("logs contain a secret value %.6s…", s)
		}
	}
}

func verifiedAccount(emails ...string) oauthtest.Account {
	a := oauthtest.Account{Subject: "subject-1"}
	for i, e := range emails {
		a.Emails = append(a.Emails, oauthtest.Email{Address: e, Primary: i == 0, Verified: true})
	}
	return a
}

// TestOAuthFlows signs in end to end with each provider and allowlist
// kind: the login page lists the providers, the auth origin sets no
// cookie, and the site callback sets a host-only session that lets the
// visitor in on that site only.
func TestOAuthFlows(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		account  oauthtest.Account
		list     store.Allowlist
		subject  string
	}{
		{"github by email", "github", verifiedAccount("Visitor@Example.test"), store.Allowlist{Emails: []string{"visitor@example.test"}}, "visitor@example.test"},
		{"github by domain", "github", verifiedAccount("visitor@example.test"), store.Allowlist{Domains: []string{"example.test"}}, "visitor@example.test"},
		{"github by subdomain", "github", verifiedAccount("visitor@team.example.test"), store.Allowlist{Domains: []string{"*.example.test"}}, "visitor@team.example.test"},
		{"github by a secondary verified email", "github", verifiedAccount("visitor@elsewhere.test", "visitor@example.test"), store.Allowlist{Emails: []string{"visitor@example.test"}}, "visitor@example.test"},
		{"github by organisation", "github", func() oauthtest.Account {
			a := verifiedAccount("visitor@elsewhere.test")
			a.Orgs = []string{"Harbour-Crew"}
			return a
		}(), store.Allowlist{GitHubOrgs: []string{"harbour-crew"}}, "visitor@elsewhere.test"},
		{"google by email", "google", verifiedAccount("visitor@example.test"), store.Allowlist{Emails: []string{"visitor@example.test"}}, "visitor@example.test"},
		{"google by domain", "google", verifiedAccount("visitor@example.test"), store.Allowlist{Domains: []string{"example.test"}}, "visitor@example.test"},
		{"oidc by subdomain", "oidc", verifiedAccount("visitor@a.example.test"), store.Allowlist{Domains: []string{"*.example.test"}}, "visitor@a.example.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOAuthEdge(t)
			site := o.publish("docs")
			o.publish("other")
			pol := o.protect("docs", "/", tt.list)
			o.protect("other", "/", tt.list)
			o.fake(tt.provider).Configure(func(opt *oauthtest.Options) { opt.Account = tt.account })
			b := o.browser("192.0.2.20")

			// The login page lists every configured provider.
			r := b.request(http.MethodGet, "http://"+host+"/internal/", nil)
			r.expect(t, 401, "Continue with GitHub", "Internal plans")
			for _, want := range []string{"Continue with Google", "Continue with Harbour SSO", `name="method" value="oauth"`, `name="next" value="/internal/"`} {
				if !strings.Contains(r.body, want) {
					t.Fatalf("login page lacks %s", want)
				}
			}
			// Browsers apply form-action to the button's redirect to the
			// provider, so the login page must allow it.
			// It names exactly the providers' authorisation endpoints and
			// the auth origin, never a whole scheme.
			wantCSP := "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' " + o.github.URL() + " http://" + authHost + " " +
				o.google.URL() + " " + o.oidc.URL() + "; frame-ancestors 'none'; base-uri 'none'"
			if csp := r.h.Get("Content-Security-Policy"); csp != wantCSP || strings.Contains(csp, "https:") {
				t.Fatalf("login page CSP %q, want %q", csp, wantCSP)
			}

			hops := b.signIn(host, tt.provider, "/internal/")
			// Start, provider, auth origin, site callback.
			if len(hops) != 4 {
				t.Fatalf("%d hops: %+v", len(hops), hops)
			}
			if hops[0].res.code != http.StatusSeeOther || !strings.HasPrefix(hops[0].res.h.Get("Set-Cookie"), access.OAuthBindingCookieName+"=") ||
				!strings.HasPrefix(hops[1].url, o.fake(tt.provider).URL()+"/authorize?") {
				t.Fatalf("start: %d %v", hops[0].res.code, hops[0].res.h)
			}
			auth := hops[2]
			if !strings.HasPrefix(auth.url, "http://"+authHost+"/callback/"+tt.provider+"?") || auth.res.code != http.StatusSeeOther ||
				auth.res.h.Get("Set-Cookie") != "" || auth.res.h.Get("Cache-Control") != "no-store" {
				t.Fatalf("auth origin hop %s: %d %v", auth.url, auth.res.code, auth.res.h)
			}
			if !strings.HasPrefix(auth.res.h.Get("Location"), "http://"+host+"/.wardlock/callback?code=") {
				t.Fatalf("auth origin sends the visitor to %q", auth.res.h.Get("Location"))
			}
			cb := last(hops)
			if cb.res.code != http.StatusSeeOther || cb.res.h.Get("Location") != "/internal/" || cb.res.h.Get("Cache-Control") != "no-store" {
				t.Fatalf("site callback: %d %v", cb.res.code, cb.res.h)
			}
			session := b.cookie(host, access.SessionCookieName)
			if session == "" || b.cookie(host, access.OAuthBindingCookieName) != "" {
				t.Fatalf("after the callback the jar holds %v", b.jar[host])
			}
			b.noCookiesOffSite(t)

			// The session lets the visitor in here, as oauth, with the
			// verified email as its subject.
			o.do(http.MethodGet, host, "/internal/", session, nil).expect(t, 200, "Internal plans", "")
			recs := o.accessRecords()
			if got := recs[len(recs)-1]; got.Via != access.ViaOAuth {
				t.Fatalf("access record via %q", got.Via)
			}
			sessions := o.sessions.take()
			if len(sessions) != 1 || sessions[0].Method != store.SessionOAuth || sessions[0].Subject != tt.subject ||
				sessions[0].SiteID != site.ID || sessions[0].PolicyID != pol.ID {
				t.Fatalf("sessions %+v, want one oauth session for %s", sessions, tt.subject)
			}
			// ...and nowhere else.
			o.do(http.MethodGet, otherHost, "/internal/", session, html).expect(t, 401, "Continue with", "Internal plans")
			b.noSecretsLogged(t)
		})
	}
}

// toCallback runs the flow up to, not including, the site callback and
// returns the hops and the callback URL.
func (b *browser) toCallback(h, provider, next string) ([]hop, string) {
	b.o.t.Helper()
	hops, cb := b.follow("http://"+h+"/.wardlock/login", b.start(h, provider, next), atSiteCallback)
	if cb == "" {
		b.o.t.Fatalf("flow stopped at %d before the site callback: %s", last(hops).res.code, last(hops).res.body)
	}
	return hops, cb
}

// toAuthOrigin runs the flow up to, not including, the auth origin.
func (b *browser) toAuthOrigin(h, provider, next string) string {
	b.o.t.Helper()
	_, cb := b.follow("http://"+h+"/.wardlock/login", b.start(h, provider, next), func(u *url.URL) bool { return u.Hostname() == authHost })
	if cb == "" {
		b.o.t.Fatal("flow never reached the auth origin")
	}
	return cb
}

// refused asserts a site callback answer that set no session: the login
// page again, and the binding cookie cleared.
func refused(t *testing.T, r result) {
	t.Helper()
	r.expect(t, http.StatusBadRequest, "That sign-in has expired or was already used.", "Internal plans")
	for _, c := range (&http.Response{Header: r.h}).Cookies() {
		if c.Name == access.SessionCookieName && c.MaxAge > 0 {
			t.Fatal("a refused code set a session")
		}
	}
	if !strings.Contains(strings.Join(r.h.Values("Set-Cookie"), "\n"), access.OAuthBindingCookieName+"=; Path=/; Max-Age=0") {
		t.Fatalf("binding cookie not cleared: %v", r.h.Values("Set-Cookie"))
	}
}

func authRefused(t *testing.T, r result, code int, msg string) {
	t.Helper()
	r.expect(t, code, msg, "")
	if r.h.Get("Set-Cookie") != "" || r.h.Get("Cache-Control") != "no-store" || r.h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("auth origin error headers %v", r.h)
	}
}

// TestOAuthRefusals covers every way a flow is refused: replayed,
// expired, wrong-site and unbound codes; replayed, unknown and expired
// state; PKCE and nonce failures; unverified emails; accounts off the
// allowlist; policy changes mid-flow. None sets a session, the auth
// origin never sets a cookie, and nothing secret is logged.
func TestOAuthRefusals(t *testing.T) {
	visitor := verifiedAccount("visitor@example.test")
	allowVisitor := store.Allowlist{Emails: []string{"visitor@example.test"}}
	tests := []struct {
		name string
		run  func(t *testing.T, o *oauthEdge, b *browser)
	}{
		{"replayed code", func(t *testing.T, o *oauthEdge, b *browser) {
			_, cb := b.toCallback(host, "github", "/internal/")
			binding := b.cookie(host, access.OAuthBindingCookieName)
			if r := b.request(http.MethodGet, cb, nil); r.code != http.StatusSeeOther {
				t.Fatalf("first redemption: %d", r.code)
			}
			b.setCookieValue(host, access.OAuthBindingCookieName, binding)
			refused(t, b.request(http.MethodGet, cb, nil))
		}},
		{"code within 60 seconds", func(t *testing.T, o *oauthEdge, b *browser) {
			_, cb := b.toCallback(host, "github", "/internal/")
			o.advance(59 * time.Second)
			if r := b.request(http.MethodGet, cb, nil); r.code != http.StatusSeeOther {
				t.Fatalf("redemption at 59 seconds: %d", r.code)
			}
		}},
		{"expired code", func(t *testing.T, o *oauthEdge, b *browser) {
			_, cb := b.toCallback(host, "github", "/internal/")
			o.advance(60 * time.Second)
			refused(t, b.request(http.MethodGet, cb, nil))
		}},
		{"wrong-site code", func(t *testing.T, o *oauthEdge, b *browser) {
			_, cb := b.toCallback(host, "github", "/internal/")
			binding := b.cookie(host, access.OAuthBindingCookieName)
			b.setCookieValue(otherHost, access.OAuthBindingCookieName, binding)
			refused(t, b.request(http.MethodGet, strings.Replace(cb, host, otherHost, 1), nil))
			// The attempt spent it.
			refused(t, b.request(http.MethodGet, cb, nil))
		}},
		{"code without the binding cookie", func(t *testing.T, o *oauthEdge, b *browser) {
			_, cb := b.toCallback(host, "github", "/internal/")
			delete(b.jar[host], access.OAuthBindingCookieName)
			refused(t, b.request(http.MethodGet, cb, nil))
		}},
		{"code in another browser", func(t *testing.T, o *oauthEdge, b *browser) {
			// An attacker's finished flow, handed to a victim who started
			// their own: the victim's binding cookie does not match.
			_, cb := b.toCallback(host, "github", "/internal/")
			victim := o.browser("192.0.2.30")
			victim.toAuthOrigin(host, "github", "/")
			refused(t, victim.request(http.MethodGet, cb, nil))
			refused(t, b.request(http.MethodGet, cb, nil))
		}},
		{"tampered binding cookie", func(t *testing.T, o *oauthEdge, b *browser) {
			_, cb := b.toCallback(host, "github", "/internal/")
			b.setCookieValue(host, access.OAuthBindingCookieName, b.cookie(host, access.OAuthBindingCookieName)+"x")
			refused(t, b.request(http.MethodGet, cb, nil))
		}},
		{"replayed state", func(t *testing.T, o *oauthEdge, b *browser) {
			cb := b.toAuthOrigin(host, "github", "/internal/")
			if r := b.request(http.MethodGet, cb, nil); r.code != http.StatusSeeOther {
				t.Fatalf("first callback: %d", r.code)
			}
			authRefused(t, b.request(http.MethodGet, cb, nil), 400, "This sign-in has expired or was already used.")
		}},
		{"unknown state", func(t *testing.T, o *oauthEdge, b *browser) {
			cb, _ := url.Parse(b.toAuthOrigin(host, "github", "/internal/"))
			q := cb.Query()
			q.Set("state", q.Get("state")+"x")
			cb.RawQuery = q.Encode()
			authRefused(t, b.request(http.MethodGet, cb.String(), nil), 400, "This sign-in has expired or was already used.")
			cb.RawQuery = ""
			authRefused(t, b.request(http.MethodGet, cb.String(), nil), 400, "This sign-in has expired or was already used.")
		}},
		{"state for another provider", func(t *testing.T, o *oauthEdge, b *browser) {
			cb := b.toAuthOrigin(host, "github", "/internal/")
			authRefused(t, b.request(http.MethodGet, strings.Replace(cb, "/callback/github", "/callback/google", 1), nil), 400, "expired or was already used")
			// Spent by the attempt.
			authRefused(t, b.request(http.MethodGet, cb, nil), 400, "expired or was already used")
		}},
		{"expired flow", func(t *testing.T, o *oauthEdge, b *browser) {
			cb := b.toAuthOrigin(host, "github", "/internal/")
			o.advance(access.OAuthFlowLifetime)
			authRefused(t, b.request(http.MethodGet, cb, nil), 400, "expired or was already used")
		}},
		{"PKCE mismatch", func(t *testing.T, o *oauthEdge, b *browser) {
			for _, p := range []string{"github", "google", "oidc"} {
				o.fake(p).Configure(func(opt *oauthtest.Options) { opt.Challenge = "not-the-challenge" })
				hops, _ := b.follow("http://"+host+"/.wardlock/login", b.start(host, p, "/internal/"), nil)
				authRefused(t, last(hops).res, http.StatusBadGateway, "The provider could not confirm who you are.")
			}
		}},
		{"OIDC nonce mismatch", func(t *testing.T, o *oauthEdge, b *browser) {
			for _, p := range []string{"google", "oidc"} {
				o.fake(p).Configure(func(opt *oauthtest.Options) { opt.Nonce = "another-flows-nonce" })
				hops, _ := b.follow("http://"+host+"/.wardlock/login", b.start(host, p, "/internal/"), nil)
				authRefused(t, last(hops).res, http.StatusBadGateway, "The provider could not confirm who you are.")
			}
		}},
		{"provider refuses", func(t *testing.T, o *oauthEdge, b *browser) {
			o.github.Configure(func(opt *oauthtest.Options) { opt.Deny = true })
			hops, _ := b.follow("http://"+host+"/.wardlock/login", b.start(host, "github", "/internal/"), nil)
			r := last(hops).res
			authRefused(t, r, 400, "cancelled or refused")
			if !strings.Contains(r.body, `href="http://docs.wardlock.localhost/.wardlock/login?next=%2Finternal%2F"`) {
				t.Fatalf("error page lacks the way back:\n%s", r.body)
			}
		}},
		{"unverified emails", func(t *testing.T, o *oauthEdge, b *browser) {
			unverified := oauthtest.Account{Subject: "s", Emails: []oauthtest.Email{{Address: "visitor@example.test", Primary: true}}}
			for _, p := range []string{"github", "google", "oidc"} {
				o.fake(p).Configure(func(opt *oauthtest.Options) { opt.Account = unverified })
				hops, _ := b.follow("http://"+host+"/.wardlock/login", b.start(host, p, "/internal/"), nil)
				authRefused(t, last(hops).res, http.StatusForbidden, "no verified email address")
			}
			o.oidc.Configure(func(opt *oauthtest.Options) { opt.Account = visitor; opt.Account.OmitEmailVerified = true })
			hops, _ := b.follow("http://"+host+"/.wardlock/login", b.start(host, "oidc", "/internal/"), nil)
			authRefused(t, last(hops).res, http.StatusForbidden, "no verified email address")
		}},
		{"not on the allowlist", func(t *testing.T, o *oauthEdge, b *browser) {
			for _, c := range []struct {
				list    store.Allowlist
				account oauthtest.Account
				p       string
			}{
				{allowVisitor, verifiedAccount("other@example.test"), "github"},
				{store.Allowlist{Domains: []string{"example.test"}}, verifiedAccount("visitor@team.example.test"), "google"},
				{store.Allowlist{Domains: []string{"*.example.test"}}, verifiedAccount("visitor@example.test"), "oidc"},
				{store.Allowlist{Domains: []string{"*.example.test"}}, verifiedAccount("visitor@badexample.test"), "oidc"},
				{store.Allowlist{GitHubOrgs: []string{"harbour-crew"}}, verifiedAccount("visitor@example.test"), "github"},
				// Organisations count only for GitHub accounts.
				{store.Allowlist{GitHubOrgs: []string{"harbour-crew"}}, func() oauthtest.Account {
					a := verifiedAccount("visitor@example.test")
					a.Orgs = []string{"harbour-crew"}
					return a
				}(), "google"},
			} {
				o.protect("docs", "/", c.list)
				o.fake(c.p).Configure(func(opt *oauthtest.Options) { opt.Account = c.account })
				hops, _ := b.follow("http://"+host+"/.wardlock/login", b.start(host, c.p, "/internal/"), nil)
				authRefused(t, last(hops).res, http.StatusForbidden, "This account is not allowed to view this site.")
			}
		}},
		{"policy changed before redemption", func(t *testing.T, o *oauthEdge, b *browser) {
			_, cb := b.toCallback(host, "github", "/internal/")
			if _, err := o.acc.Set(t.Context(), o.owner.ID, "docs", access.Rule{PathPrefix: "/", Visibility: store.Protected, OAuth: true, Links: true}); err != nil {
				t.Fatal(err)
			}
			refused(t, b.request(http.MethodGet, cb, nil))
		}},
		{"OAuth switched off mid-flow", func(t *testing.T, o *oauthEdge, b *browser) {
			cb := b.toAuthOrigin(host, "github", "/internal/")
			o.set("docs", "/", store.Protected, true)
			authRefused(t, b.request(http.MethodGet, cb, nil), http.StatusForbidden, "no longer available")
		}},
		{"start where OAuth is off", func(t *testing.T, o *oauthEdge, b *browser) {
			o.set("other", "/", store.Protected, true)
			r := b.start(otherHost, "github", "/")
			r.expect(t, 400, "That sign-in method is not available here.", "")
			// Without OAuth the login page posts to its own origin only.
			if csp := r.h.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self';") {
				t.Fatalf("login page without OAuth has CSP %q", csp)
			}
			if r.h.Get("Set-Cookie") != "" || strings.Contains(r.body, "Continue with GitHub") {
				t.Fatalf("refused start set %v or offered providers", r.h.Values("Set-Cookie"))
			}
			b.start(host, "gitlab", "/").expect(t, 400, "That sign-in method is not available here.", "")
		}},
		{"start from another origin", func(t *testing.T, o *oauthEdge, b *browser) {
			r := o.post(host, b.ip, url.Values{"method": {"oauth"}, "provider": {"github"}, "next": {"/"}},
				http.Header{"Origin": {"http://other.wardlock.localhost"}, "Sec-Fetch-Site": {"same-site"}})
			if r.code != http.StatusForbidden || r.h.Get("Set-Cookie") != "" {
				t.Fatalf("cross-origin start: %d %v", r.code, r.h)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOAuthEdge(t)
			o.publish("docs")
			o.publish("other")
			o.protect("docs", "/", allowVisitor)
			o.protect("other", "/", allowVisitor)
			for _, p := range []string{"github", "google", "oidc"} {
				o.fake(p).Configure(func(opt *oauthtest.Options) { opt.Account = visitor })
			}
			b := o.browser("192.0.2.20")
			tt.run(t, o, b)
			if n := len(o.sessions.take()); n > 1 || n == 1 && !strings.Contains(tt.name, "within") && !strings.Contains(tt.name, "replayed code") {
				t.Fatalf("%d sessions created", n)
			}
			b.noCookiesOffSite(t)
			b.noSecretsLogged(t)
		})
	}
}

// TestOAuthReturnPaths checks that the only place a flow returns to is a
// path on the site it started from.
func TestOAuthReturnPaths(t *testing.T) {
	o := newOAuthEdge(t)
	o.publish("docs")
	o.protect("docs", "/", store.Allowlist{Emails: []string{"visitor@example.test"}})
	o.github.Configure(func(opt *oauthtest.Options) { opt.Account = verifiedAccount("visitor@example.test") })
	for next, want := range map[string]string{
		"/internal/":               "/internal/",
		"/a b/":                    "/a%20b/",
		"//evil.example/x":         "/",
		"https://evil.example/":    "/",
		`/\evil.example`:           "/",
		"/.wardlock/login":         "/",
		"/internal/?next=//evil":   "/",
		"/x/../../internal/":       "/internal/",
		"javascript:alert(1)":      "/",
		"/%2f%2fevil.example/path": "/%252f%252fevil.example/path",
	} {
		b := o.browser("192.0.2.40")
		hops := b.signIn(host, "github", next)
		auth := hops[2].res.h.Get("Location")
		if !strings.HasPrefix(auth, "http://"+host+"/.wardlock/callback?code=") {
			t.Fatalf("next %q: auth origin sends the visitor to %q", next, auth)
		}
		if got := last(hops).res.h.Get("Location"); got != want {
			t.Fatalf("next %q returns to %q, want %q", next, got, want)
		}
	}
}

// TestOAuthStartLimit checks the per-client limit on starting flows.
func TestOAuthStartLimit(t *testing.T) {
	o := newOAuthEdge(t)
	o.publish("docs")
	o.protect("docs", "/", store.Allowlist{Emails: []string{"visitor@example.test"}})
	b := o.browser("192.0.2.50")
	for i := range 30 {
		if r := b.start(host, "github", "/"); r.code != http.StatusSeeOther {
			t.Fatalf("start %d: %d", i+1, r.code)
		}
	}
	r := b.start(host, "github", "/")
	r.expect(t, http.StatusTooManyRequests, "Too many sign-in attempts.", "")
	if r.h.Get("Retry-After") == "" || r.h.Get("Set-Cookie") != "" {
		t.Fatalf("limited start headers %v", r.h)
	}
	// Another client is unaffected; the window resets.
	if r := o.browser("192.0.2.51").start(host, "github", "/"); r.code != http.StatusSeeOther {
		t.Fatalf("another client: %d", r.code)
	}
	o.advance(15 * time.Minute)
	if r := b.start(host, "github", "/"); r.code != http.StatusSeeOther {
		t.Fatalf("after the window: %d", r.code)
	}
}

// TestAuthOrigin checks that the auth origin answers only its callbacks,
// sets no cookie and is never a site.
func TestAuthOrigin(t *testing.T) {
	o := newOAuthEdge(t)
	o.publish("docs")
	for _, c := range []struct {
		method, target string
		code           int
	}{
		{http.MethodGet, "/", 404},
		{http.MethodGet, "/.wardlock/health", 404},
		{http.MethodGet, "/callback/", 404},
		{http.MethodGet, "/callback/github/x", 404},
		{http.MethodPost, "/callback/github", 405},
		{http.MethodGet, "/callback/github", 400},
		{http.MethodGet, "/callback/github?state=unknown&code=x", 400},
	} {
		r := o.do(c.method, authHost, c.target, "", html)
		if r.code != c.code || r.h.Get("Set-Cookie") != "" || r.h.Get("Cache-Control") != "no-store" || r.h.Get("X-Robots-Tag") != "noindex" ||
			r.h.Get("Referrer-Policy") != "no-referrer" || r.h.Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatalf("%s %s: %d %v", c.method, c.target, r.code, r.h)
		}
		// The error page Wardlock renders is never framed and runs no
		// scripts.
		if c.code == 400 {
			if csp := r.h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") ||
				r.h.Get("X-Frame-Options") != "DENY" {
				t.Fatalf("%s %s: error page headers %v", c.method, c.target, r.h)
			}
		}
	}
}

// TestOAuthFlowsPerSite checks that a flood of starts aimed at one site,
// from many clients on many networks, fills only that site's share of
// pending sign-ins: another site's visitors can still start.
func TestOAuthFlowsPerSite(t *testing.T) {
	o := newOAuthEdge(t)
	o.publish("docs")
	o.publish("other")
	list := store.Allowlist{Emails: []string{"visitor@example.test"}}
	o.protect("docs", "/", list)
	o.protect("other", "/", list)
	started := 0
	for i := 0; ; i++ {
		// 30 starts per client fit under the per-client limit, and one
		// client per /48 under its network's share.
		r := o.browser(fmt.Sprintf("2001:db8:%x::1", i/30+1)).start(host, "github", "/")
		if r.code != http.StatusSeeOther {
			r.expect(t, http.StatusTooManyRequests, "Too many sign-in attempts.", "")
			break
		}
		if started++; started > 5000 {
			t.Fatal("no per-site bound on pending sign-ins")
		}
	}
	if started != 1000 {
		t.Fatalf("%d starts accepted on the flooded site, want 1000", started)
	}
	if r := o.browser("192.0.2.60").start(otherHost, "github", "/"); r.code != http.StatusSeeOther {
		t.Fatalf("another site's start: %d", r.code)
	}
	// The flooded site recovers when its flows expire.
	o.advance(access.OAuthFlowLifetime)
	if r := o.browser("192.0.2.61").start(host, "github", "/"); r.code != http.StatusSeeOther {
		t.Fatalf("flooded site after expiry: %d", r.code)
	}
}

// TestOAuthConcurrentRedeem checks that a code is single use even when
// many requests carry it at once: exactly one gets a session.
func TestOAuthConcurrentRedeem(t *testing.T) {
	o := newOAuthEdge(t)
	o.publish("docs")
	o.protect("docs", "/", store.Allowlist{Emails: []string{"visitor@example.test"}})
	o.github.Configure(func(opt *oauthtest.Options) { opt.Account = verifiedAccount("visitor@example.test") })
	b := o.browser("192.0.2.70")
	_, cb := b.toCallback(host, "github", "/internal/")
	u, err := url.Parse(cb)
	if err != nil {
		t.Fatal(err)
	}
	binding := b.cookie(host, access.OAuthBindingCookieName)
	const n = 16
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)
			req.Host = host
			req.AddCookie(&http.Cookie{Name: access.OAuthBindingCookieName, Value: binding})
			rec := httptest.NewRecorder()
			o.h.ServeHTTP(rec, req)
			codes <- rec.Code
		})
	}
	wg.Wait()
	close(codes)
	ok := 0
	for c := range codes {
		switch c {
		case http.StatusSeeOther:
			ok++
		case http.StatusBadRequest:
		default:
			t.Fatalf("redeem answered %d", c)
		}
	}
	if sessions := o.sessions.take(); ok != 1 || len(sessions) != 1 {
		t.Fatalf("%d redemptions succeeded and %d sessions stored, want 1 and 1", ok, len(sessions))
	}
}
