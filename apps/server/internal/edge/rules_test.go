// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
)

const rulesHost = "rules." + base

// ruleRedirects cover every redirect form; those whose from a file serves
// never apply.
var ruleRedirects = []edge.Redirect{
	{From: "/old", To: "/new", Status: 301},
	{From: "/old", To: "/never", Status: 302}, // first match wins
	{From: "/s302", To: "/new", Status: 302},
	{From: "/s307", To: "/new", Status: 307},
	{From: "/s308", To: "/new", Status: 308},
	{From: "/blog/*", To: "/news/:splat", Status: 301},
	{From: "/ext/*", To: "https://elsewhere.example.test/x/:splat", Status: 308},
	{From: "/root/*", To: "/:splat", Status: 302},
	{From: "/own-query", To: "/new?b=2", Status: 301},
	{From: "/fragment", To: "/new#top", Status: 301},
	{From: "/page", To: "/never", Status: 301},       // page.html answers
	{From: "/index.html", To: "/never", Status: 301}, // the file answers
	{From: "/docs", To: "/never", Status: 301},       // the directory redirect answers
	{From: "/docs/", To: "/never", Status: 301},      // docs/index.html answers
}

// allowlisted is a value for every header a deploy may set.
var allowlisted = map[string]string{
	"Content-Security-Policy":             "default-src 'self'",
	"Content-Security-Policy-Report-Only": "script-src 'none'",
	"Permissions-Policy":                  "camera=()",
	"X-Frame-Options":                     "DENY",
	"Cross-Origin-Opener-Policy":          "same-origin",
	"Cross-Origin-Embedder-Policy":        "require-corp",
	"X-Content-Type-Options":              "nosniff",
	"Link":                                "</style.css>; rel=preload; as=style",
	"Referrer-Policy":                     "strict-origin",
	"Access-Control-Allow-Origin":         "*",
	"Access-Control-Allow-Credentials":    "true",
	"Access-Control-Allow-Headers":        "X-Custom",
	"Access-Control-Allow-Methods":        "GET",
	"Access-Control-Expose-Headers":       "X-Custom",
	"Access-Control-Max-Age":              "600",
}

// rulesHarness serves "rules" (index.html, page.html, 404.html, docs/) with
// ruleRedirects and header rules: every allowlisted header on every path,
// X-Frame-Options overwritten under /docs/ and Permissions-Policy on /new
// only. spa turns the SPA fallback on.
func rulesHarness(t *testing.T, a edge.Access, spa bool) *harness {
	t.Helper()
	h := newHarness(t, a)
	b := h.fx.blobs
	h.src.set(edge.ServedSite{
		ID: "site-rules", Name: "rules", ActiveDeployID: "dep-rules-1", ActivatedAt: activated, SPA: spa,
		Redirects: ruleRedirects,
		Headers: []edge.HeaderRule{
			{Path: "/*", Set: allowlisted},
			{Path: "/docs/*", Set: map[string]string{"X-Frame-Options": "SAMEORIGIN"}},
			{Path: "/new", Set: map[string]string{"Permissions-Policy": "geolocation=()"}},
		},
	}, []edge.ServedFile{
		b.add("index.html", []byte("<p>Rules home</p>")),
		b.add("page.html", []byte("<p>A page</p>")),
		b.add("404.html", []byte("<p>Rules lost</p>")),
		b.add("docs/index.html", []byte("<p>Rules docs</p>")),
		b.add("style.css", []byte("body { color: teal }")),
	})
	return h
}

func TestRedirects(t *testing.T) {
	h := rulesHarness(t, nil, false)
	for _, tc := range []struct {
		target, location string
		status           int
		body             string
	}{
		{target: "/old", location: "/new", status: 301},
		{target: "/s302", location: "/new", status: 302},
		{target: "/s307", location: "/new", status: 307},
		{target: "/s308", location: "/new", status: 308},
		{target: "/blog/a/b", location: "/news/a/b", status: 301},
		{target: "/blog/a/b/", location: "/news/a/b/", status: 301},
		{target: "/blog/", location: "/news/", status: 301},
		{target: "/blog/a%20b/c%3Fd", location: "/news/a%20b/c%3Fd", status: 301},
		{target: "/blog/x/../y", location: "/news/y", status: 301},
		{target: "/ext/a/b", location: "https://elsewhere.example.test/x/a/b", status: 308},
		// A backslash in the remainder never turns the path into another host.
		{target: `/root/%5Celsewhere.example.test`, location: "/%5Celsewhere.example.test", status: 302},
		{target: "/old?a=1&b=2", location: "/new?a=1&b=2", status: 301},
		{target: "/own-query?a=1", location: "/new?b=2", status: 301},
		{target: "/fragment?a=1", location: "/new?a=1#top", status: 301},
		{target: "/ext/p?t=wl_secret&k=1", location: "https://elsewhere.example.test/x/p?k=1", status: 308},
		{target: "/old?t=wl_secret", location: "/new?t=wl_secret", status: 301},
		// Files win: a clean URL, the file itself, a directory.
		{target: "/page", status: 200, body: "A page"},
		{target: "/index.html", status: 200, body: "Rules home"},
		{target: "/docs", location: "/docs/", status: 301},
		{target: "/docs/", status: 200, body: "Rules docs"},
		// Exact means exact, and a prefix needs its slash.
		{target: "/old/", status: 404, body: "Rules lost"},
		{target: "/blog", status: 404, body: "Rules lost"},
		{target: "/OLD", status: 404, body: "Rules lost"},
	} {
		rec := h.do(http.MethodGet, rulesHost, tc.target, nil)
		if rec.Code != tc.status || rec.Header().Get("Location") != tc.location || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("GET %s = %d Location %q %q, want %d %q %q", tc.target, rec.Code, rec.Header().Get("Location"), rec.Body.String(), tc.status, tc.location, tc.body)
		}
	}
	if rec := h.do(http.MethodHead, rulesHost, "/old", nil); rec.Code != 301 || rec.Header().Get("Location") != "/new" {
		t.Errorf("HEAD /old = %d %v", rec.Code, rec.Header())
	}
	// Other methods are refused before any rule.
	if rec := h.do(http.MethodPost, rulesHost, "/old", nil); rec.Code != 405 || rec.Header().Get("Location") != "" {
		t.Errorf("POST /old = %d %v", rec.Code, rec.Header())
	}
}

func TestRedirectsBeforeTheSPAFallback(t *testing.T) {
	h := rulesHarness(t, nil, true)
	if rec := h.do(http.MethodGet, rulesHost, "/old", nil); rec.Code != 301 || rec.Header().Get("Location") != "/new" {
		t.Fatalf("GET /old on an SPA = %d %v", rec.Code, rec.Header())
	}
	rec := h.do(http.MethodGet, rulesHost, "/app/route", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Rules home") {
		t.Fatalf("SPA route = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("SPA fallback without the deploy's headers: %v", rec.Header())
	}
}

func TestDeployHeaders(t *testing.T) {
	h := rulesHarness(t, nil, false)
	rec := h.do(http.MethodGet, rulesHost, "/style.css", nil)
	for name, want := range allowlisted {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if rec.Header().Get("Content-Type") != "text/css; charset=utf-8" || rec.Header().Get("Cache-Control") != revalidate {
		t.Errorf("Wardlock's headers changed: %v", rec.Header())
	}
	for target, want := range map[string]struct{ frame, permissions string }{
		"/":           {"DENY", "camera=()"},
		"/docs/":      {"SAMEORIGIN", "camera=()"}, // a later rule overwrites
		"/docs/x":     {"SAMEORIGIN", "camera=()"}, // the 404 page, by the request path
		"/new":        {"DENY", "geolocation=()"},  // an exact path
		"/new/":       {"DENY", "camera=()"},
		"/old":        {"DENY", "camera=()"}, // a redirect
		"/docs":       {"DENY", "camera=()"}, // the directory redirect, outside /docs/*
		"/docs/index": {"SAMEORIGIN", "camera=()"},
	} {
		rec := h.do(http.MethodGet, rulesHost, target, nil)
		if rec.Header().Get("X-Frame-Options") != want.frame || rec.Header().Get("Permissions-Policy") != want.permissions {
			t.Errorf("GET %s: X-Frame-Options %q Permissions-Policy %q, want %q %q", target, rec.Header().Get("X-Frame-Options"), rec.Header().Get("Permissions-Policy"), want.frame, want.permissions)
		}
	}
	// Nothing under /.wardlock/, whose pages set their own.
	for _, target := range []string{"/.wardlock/health", "/.wardlock/login", "/.wardlock/missing"} {
		rec := h.do(http.MethodGet, rulesHost, target, nil)
		if rec.Header().Get("Permissions-Policy") != "" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("GET %s carries the deploy's headers: %v", target, rec.Header())
		}
	}
}

func TestDeployHeadersOnProtectedPolicies(t *testing.T) {
	a := &fakeAccess{fn: func(r access.Request) (access.Decision, error) {
		if r.Cookie == "good" {
			return allow(true), nil
		}
		return deny(r), nil
	}}
	h := rulesHarness(t, a, false)
	rec := h.do(http.MethodGet, rulesHost, "/style.css", withCookie)
	for name, want := range allowlisted {
		got := rec.Header().Get(name)
		switch {
		case name == "Referrer-Policy":
			want = "no-referrer" // Wardlock's own wins
		case strings.HasPrefix(name, "Access-Control-"):
			want = "" // public policies only
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"X-Robots-Tag": "noindex", "Cross-Origin-Resource-Policy": "same-origin", "X-Content-Type-Options": "nosniff",
		"Cache-Control": "private, max-age=0, must-revalidate",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// Without access: no redirect, no deploy headers, on the login page
	// and the bare 401 alike.
	for name, header := range map[string]http.Header{"login page": browser, "401": nil} {
		rec := h.do(http.MethodGet, rulesHost, "/old", header)
		if rec.Code != 401 || rec.Header().Get("Location") != "" || rec.Header().Get("Permissions-Policy") != "" {
			t.Errorf("%s: %d %v", name, rec.Code, rec.Header())
		}
	}
	if rec := h.do(http.MethodGet, rulesHost, "/old", withCookie); rec.Code != 301 || rec.Header().Get("Location") != "/new" {
		t.Errorf("protected redirect with a session: %d %v", rec.Code, rec.Header())
	}
}

// TestDeployCSPAllowsTheStripScript: a page a share-link token opened from
// the URL carries the strip script, so the deploy's CSP gains its hash
// there; every other response sends the deploy's CSP as given, and the
// report-only header is never changed.
func TestDeployCSPAllowsTheStripScript(t *testing.T) {
	const token = "wl_abcdefgh23_dGVzdC1vbmx5LW5vdC1yZWFsLXRva2VuLXNlY3JldHg"
	a := &fakeAccess{fn: func(r access.Request) (access.Decision, error) {
		d := allow(true)
		d.LinkPublicID, d.TokenAccepted = "abcdefgh23", r.Token != ""
		return d, nil
	}}
	h := rulesHarness(t, a, false)
	owner := allowlisted["Content-Security-Policy"]
	report := allowlisted["Content-Security-Policy-Report-Only"]
	stripped := h.do(http.MethodGet, rulesHost, "/?t="+token, nil)
	csp := stripped.Header().Get("Content-Security-Policy")
	if !strings.Contains(stripped.Body.String(), "history.replaceState") ||
		!strings.HasPrefix(csp, owner+" 'sha256-") || !strings.HasSuffix(csp, "='") {
		t.Fatalf("stripped page: CSP %q, body %q", csp, stripped.Body.String())
	}
	if got := stripped.Header().Get("Content-Security-Policy-Report-Only"); got != report {
		t.Fatalf("report-only CSP %q, want %q", got, report)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"page by Bearer": h.do(http.MethodGet, rulesHost, "/", http.Header{"Authorization": {"Bearer " + token}}),
		"page":           h.do(http.MethodGet, rulesHost, "/", nil),
		"CSS by ?t=":     h.do(http.MethodGet, rulesHost, "/style.css?t="+token, nil),
	} {
		if strings.Contains(rec.Body.String(), "history.replaceState") || rec.Header().Get("Content-Security-Policy") != owner {
			t.Errorf("%s: CSP %q, want the deploy's %q", name, rec.Header().Get("Content-Security-Policy"), owner)
		}
	}
}
