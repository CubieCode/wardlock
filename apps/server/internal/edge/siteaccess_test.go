// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
)

// fakeAccess answers with fn and records what the edge asked.
type fakeAccess struct {
	fn func(access.Request) (access.Decision, error)
	// issueErr, when set, is what IssueSession answers.
	issueErr error
	mu       sync.Mutex
	seen     []access.Request
	ended    []string
}

func (a *fakeAccess) Decide(_ context.Context, r access.Request) (access.Decision, error) {
	a.mu.Lock()
	a.seen = append(a.seen, r)
	a.mu.Unlock()
	return a.fn(r)
}

func (a *fakeAccess) EndSession(_ context.Context, siteID, cookie string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ended = append(a.ended, siteID+" "+cookie)
	return nil
}

func (a *fakeAccess) IssueSession(_ context.Context, n access.NewSession) (access.Cookie, error) {
	if a.issueErr != nil {
		return access.Cookie{}, a.issueErr
	}
	return access.Cookie{Name: access.SessionCookieName, Value: "issued-" + n.ShareLinkID, Expires: time.Now().Add(time.Hour)}, nil
}

func (a *fakeAccess) View(context.Context, string, string) (access.Viewed, error) {
	return access.Viewed{}, access.ErrTicket
}

func (a *fakeAccess) PasswordLogin(context.Context, access.PasswordAttempt) (access.Cookie, error) {
	return access.Cookie{}, access.ErrWrongPassword
}

func (a *fakeAccess) SendEmailCode(context.Context, access.EmailCodeRequest) error { return nil }

func (a *fakeAccess) EmailCodeLogin(context.Context, access.EmailCodeAttempt) (access.Cookie, error) {
	return access.Cookie{}, access.ErrInvalidCode
}

func (a *fakeAccess) requests() []access.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.seen
	a.seen = nil
	return out
}

// The edge may not import store, so the policy is filled in field by field.
func withLinks(d access.Decision) access.Decision {
	d.Policy.ID, d.Policy.PathPrefix, d.Policy.Visibility, d.Policy.LinksEnabled, d.Policy.Version = "pol-default", "/", "protected", true, 1
	return d
}

func allow(protected bool) access.Decision {
	return withLinks(access.Decision{Outcome: access.Allow, Protected: protected, Via: access.ViaLink})
}

// deny is what access.Service answers without a valid session.
func deny(r access.Request) access.Decision {
	d := withLinks(access.Decision{Outcome: access.Unauthorized, Protected: true, Via: access.ViaNone})
	if r.Browser {
		d.Outcome = access.Login
	}
	return d
}

var (
	browser    = http.Header{"Accept": {"text/html,application/xhtml+xml,*/*;q=0.8"}}
	withCookie = http.Header{"Cookie": {access.SessionCookieName + "=good"}}
	clearValue = access.SessionCookieName + "=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax"
)

func TestProtectedResponses(t *testing.T) {
	a := &fakeAccess{fn: func(r access.Request) (access.Decision, error) {
		switch {
		case r.Cookie == "good":
			return allow(true), nil
		case r.Cookie == "stale":
			d := deny(r)
			d.ClearCookie = true
			return d, nil
		case strings.HasPrefix(r.Path, "/assets/"):
			return access.Decision{Outcome: access.Allow, Via: access.ViaPublic}, nil
		}
		return deny(r), nil
	}}
	h := newHarness(t, a)
	const (
		basic   = "basic." + base
		private = "private, max-age=0, must-revalidate"
		report  = "Harbour Lights quarterly report"
		about   = "About this report"
	)
	protected := map[string]string{"X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer", "Vary": "Cookie"}
	for _, tc := range []struct {
		name         string
		method       string
		target       string
		header       http.Header
		status       int
		body, hidden string
		headers      map[string]string
	}{
		{name: "allowed page", target: "/", header: withCookie, status: 200, body: report, headers: map[string]string{"Cache-Control": private}},
		{name: "allowed 404.html", target: "/missing", header: withCookie, status: 404, body: "Page not found", headers: map[string]string{"Cache-Control": private}},
		{name: "allowed redirect", target: "/about", header: withCookie, status: 301, headers: map[string]string{"Cache-Control": private, "Location": "/about/"}},
		{name: "browser gets the login page", target: "/about/", header: browser, status: 401, body: "This page is private", hidden: about, headers: map[string]string{
			"Cache-Control": "no-store", "Content-Type": "text/html; charset=utf-8", "X-Frame-Options": "DENY",
			"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		}},
		{name: "login page offers share links when enabled", target: "/", header: browser, status: 401, body: `<h2 id="method-links">Share link</h2>`, hidden: report},
		{name: "login page names the host", target: "/", header: browser, status: 401, body: `<span class="host">basic.wardlock.localhost</span>`},
		{name: "missing path looks like any other", target: "/no/such/page", header: browser, status: 401, body: "This page is private", hidden: "Page not found"},
		{name: "HEAD login page has no body", method: http.MethodHead, target: "/", header: browser, status: 401, body: "", headers: map[string]string{"Cache-Control": "no-store"}},
		{name: "other clients get a bare 401", target: "/about/", status: 401, body: "This site is protected.", hidden: about, headers: map[string]string{
			"Cache-Control": "no-store", "Content-Type": "text/plain; charset=utf-8", "WWW-Authenticate": `Bearer realm="basic"`,
		}},
		{name: "bad cookie is cleared", target: "/", header: http.Header{"Cookie": {access.SessionCookieName + "=stale"}}, status: 401, hidden: report, headers: map[string]string{"Set-Cookie": clearValue}},
		{name: "public path on a protected site", target: "/assets/site.css", status: 200, body: "font-family", headers: map[string]string{
			"Cache-Control": "public, max-age=0, must-revalidate", "X-Robots-Tag": "", "Referrer-Policy": "", "Vary": "",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			rec := h.do(method, basic, tc.target, tc.header)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tc.status, rec.Body.String())
			}
			body := rec.Body.String()
			if method == http.MethodHead && body != "" {
				t.Errorf("HEAD body %q", body)
			}
			if !strings.Contains(body, tc.body) || (tc.hidden != "" && strings.Contains(body, tc.hidden)) {
				t.Errorf("body %q: want %q, never %q", body, tc.body, tc.hidden)
			}
			want := map[string]string{}
			if _, public := tc.headers["X-Robots-Tag"]; !public {
				for k, v := range protected {
					want[k] = v
				}
			}
			for k, v := range tc.headers {
				want[k] = v
			}
			for k, v := range want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
		})
	}

	// Responses to a challenge depend on the cookie and on Accept; a file
	// with a precompressed sibling also varies by Accept-Encoding.
	for _, tc := range []struct {
		target string
		header http.Header
		want   []string
	}{
		{"/", browser, []string{"Cookie", "Accept"}},
		{"/", nil, []string{"Cookie", "Accept"}},
		{"/assets/app.js", withCookie, []string{"Cookie", "Accept-Encoding"}},
	} {
		rec := h.do(http.MethodGet, basic, tc.target, tc.header)
		if got := rec.Header().Values("Vary"); !slices.Equal(got, tc.want) {
			t.Errorf("%s Vary = %q, want %q", tc.target, got, tc.want)
		}
	}

	a.requests()
	h.do(http.MethodGet, "BASIC."+base, "/assets/../about/", http.Header{
		"Accept": {"text/html"}, "Cookie": {"wardlock_site=tossed; " + access.SessionCookieName + "=good"},
	})
	got := a.requests()
	want := access.Request{SiteID: "site-basic", Path: "/about/", Cookie: "good", Browser: true, Return: "/assets/../about/", ClientIP: "192.0.2.1"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Decide saw %+v, want [%+v]", got, want)
	}
}

func TestReservedPathIgnoresPolicies(t *testing.T) {
	a := &fakeAccess{fn: func(r access.Request) (access.Decision, error) { return deny(r), nil }}
	h := newHarness(t, a)
	const basic = "basic." + base
	reserved := map[string]string{"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer"}
	for _, tc := range []struct {
		name, method, target string
		header               http.Header
		status               int
		body                 string
		headers              map[string]string
	}{
		{name: "health", target: "/.wardlock/health", status: 200, body: `{"status":"ok"}`},
		{name: "health via dot segments", target: "/docs/../.wardlock/health", status: 200, body: `{"status":"ok"}`},
		{name: "deployed reserved file", target: "/.wardlock/index.html", status: 404, body: "404 page not found"},
		{name: "login page", target: "/.wardlock/login", header: browser, status: 200, body: "This page is private", headers: map[string]string{"X-Frame-Options": "DENY"}},
		{name: "login takes GET, HEAD and POST", method: http.MethodPut, target: "/.wardlock/login", status: 405, headers: map[string]string{"Allow": "GET, HEAD, POST"}},
		{name: "login from another site", method: http.MethodPost, target: "/.wardlock/login", header: sameSite(http.Header{}, "Origin", "https://evil.example"), status: 403, headers: map[string]string{"Set-Cookie": ""}},
		{name: "login with no origin information", method: http.MethodPost, target: "/.wardlock/login", status: 403},
		{name: "logout", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(withCookie, "Origin", "http://basic."+base), status: 303, headers: map[string]string{"Location": "/", "Set-Cookie": clearValue}},
		{name: "logout by fetch metadata", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(withCookie, "Sec-Fetch-Site", "same-origin"), status: 303, headers: map[string]string{"Set-Cookie": clearValue}},
		{name: "logout from another site", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(withCookie, "Origin", "https://evil.example"), status: 403, headers: map[string]string{"Set-Cookie": ""}},
		{name: "logout from a sibling site", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(withCookie, "Origin", "http://spa."+base), status: 403, headers: map[string]string{"Set-Cookie": ""}},
		{name: "logout from another port", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(withCookie, "Origin", "http://basic."+base+":8080"), status: 403},
		{name: "logout cross-site by fetch metadata", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(withCookie, "Sec-Fetch-Site", "same-site"), status: 403},
		{name: "logout with no origin information", method: http.MethodPost, target: "/.wardlock/logout", header: withCookie, status: 403},
		{name: "logout with only a null origin", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(withCookie, "Origin", "null"), status: 403},
		// A no-referrer page posts with Origin: null; fetch metadata decides.
		{name: "logout with a null origin from the same origin", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(sameSite(withCookie, "Origin", "null"), "Sec-Fetch-Site", "same-origin"), status: 303, headers: map[string]string{"Set-Cookie": clearValue}},
		{name: "logout with a null origin, cross-site", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(sameSite(withCookie, "Origin", "null"), "Sec-Fetch-Site", "cross-site"), status: 403},
		{name: "logout same-origin by metadata but another Origin", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(sameSite(withCookie, "Origin", "https://evil.example"), "Sec-Fetch-Site", "same-origin"), status: 403},
		{name: "logout same-site by metadata with a matching Origin", method: http.MethodPost, target: "/.wardlock/logout", header: sameSite(sameSite(withCookie, "Origin", "http://basic."+base), "Sec-Fetch-Site", "same-site"), status: 403},
		{name: "login with a null origin, cross-site", method: http.MethodPost, target: "/.wardlock/login", header: sameSite(sameSite(http.Header{}, "Origin", "null"), "Sec-Fetch-Site", "cross-site"), status: 403},
		{name: "login page ignores ?format", target: "/.wardlock/login?format=md", header: browser, status: 200, body: "This page is private", headers: map[string]string{"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store"}},
		{name: "logout is POST only", target: "/.wardlock/logout", header: withCookie, status: 405, headers: map[string]string{"Allow": "POST"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			a.requests()
			rec := h.do(method, basic, tc.target, tc.header)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("%d %q, want %d containing %q", rec.Code, rec.Body.String(), tc.status, tc.body)
			}
			for k, v := range reserved {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			for k, v := range tc.headers {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			for _, r := range a.requests() {
				if r.Path != "/" {
					t.Errorf("reserved path decided as %q", r.Path)
				}
			}
		})
	}
	// Only the three same-origin logouts ended a session.
	if len(a.ended) != 3 || a.ended[0] != "site-basic good" || a.ended[2] != "site-basic good" {
		t.Fatalf("EndSession calls %v", a.ended)
	}
}

// sameSite returns h plus one header.
func sameSite(h http.Header, k, v string) http.Header {
	out := h.Clone()
	out.Set(k, v)
	return out
}

func TestFallbackFilesNeedTheirOwnPolicy(t *testing.T) {
	// The request paths are public, but the files that would answer them
	// sit behind a policy the visitor does not satisfy.
	behind := map[string]bool{"/404.html": true, "/index.html": true, "/guide.html": true}
	a := &fakeAccess{fn: func(r access.Request) (access.Decision, error) {
		if behind[r.Path] && r.Cookie != "good" {
			return deny(r), nil
		}
		return access.Decision{Outcome: access.Allow, Via: access.ViaPublic}, nil
	}}
	h := newHarness(t, a)
	for _, tc := range []struct {
		name, host, target string
		header             http.Header
		status             int
		body, hidden       string
	}{
		{"404.html", "basic." + base, "/missing", nil, 404, "404 page not found", "Page not found"},
		{"404.html with a session", "basic." + base, "/missing", withCookie, 404, "Page not found", ""},
		{"SPA index", "spa." + base, "/settings/profile", browser, 404, "404 page not found", "Tide tables"},
		{"clean URL page", "basic." + base, "/guide", nil, 404, "404 page not found", "Reading guide"},
		{"the file's own URL challenges", "basic." + base, "/guide.html", nil, 401, "protected", "Reading guide"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(http.MethodGet, tc.host, tc.target, tc.header)
			body := rec.Body.String()
			if rec.Code != tc.status || !strings.Contains(body, tc.body) || (tc.hidden != "" && strings.Contains(body, tc.hidden)) {
				t.Fatalf("%d %q, want %d with %q and never %q", rec.Code, body, tc.status, tc.body, tc.hidden)
			}
			if tc.status == 404 && tc.header == nil && rec.Header().Get("X-Robots-Tag") != "noindex" {
				t.Errorf("fallback behind a protected policy lacks X-Robots-Tag")
			}
		})
	}

	// A directory index answers under its own directory's policy: one
	// decision.
	a.requests()
	if rec := h.do(http.MethodGet, "basic."+base, "/about/", nil); rec.Code != 200 {
		t.Fatalf("/about/ = %d", rec.Code)
	}
	if got := a.requests(); len(got) != 1 {
		t.Fatalf("decisions for a directory index: %+v", got)
	}
}

func TestAccessFailureIs500(t *testing.T) {
	a := &fakeAccess{fn: func(access.Request) (access.Decision, error) {
		return access.Decision{}, errors.New("database is down")
	}}
	h := newHarness(t, a)
	rec := h.do(http.MethodGet, "basic."+base, "/", nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "database") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodGet, "basic."+base, "/.wardlock/health", nil); rec.Code != 200 {
		t.Fatalf("health with access failing = %d", rec.Code)
	}
}

func TestAccessIsRequired(t *testing.T) {
	_, err := edge.New(edge.Config{Invalidator: &fakeNotifier{}})
	if !errors.Is(err, edge.ErrNoAccess) {
		t.Fatalf("New without Access = %v, want ErrNoAccess", err)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	got := edge.SessionCookie(access.Cookie{Name: access.SessionCookieName, Value: "v1value", Expires: expires}).String()
	for _, want := range []string{
		access.SessionCookieName + "=v1value", "; Path=/", "; Expires=" + expires.Format(http.TimeFormat),
		"; HttpOnly", "; Secure", "; SameSite=Lax",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Set-Cookie %q lacks %q", got, want)
		}
	}
	if strings.Contains(strings.ToLower(got), "domain") || !strings.HasPrefix(got, "__Host-") {
		t.Errorf("Set-Cookie %q is not a host-only __Host- cookie", got)
	}
	if got := edge.ClearSessionCookie().String(); got != clearValue {
		t.Errorf("clear = %q, want %q", got, clearValue)
	}
}
