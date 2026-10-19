// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// TestProtectedHeadersThroughTheEdge checks the response headers of every
// kind of answer a protected site gives (SPEC "Protected-site response
// headers", SECURITY-MODEL): noindex, no-referrer, nosniff and Vary:
// Cookie on everything the policy decides; same-origin resource policy, so
// no other origin (a sibling site included) can embed a protected answer; private caching for content
// and no-store for Wardlock's own pages, refusals, server errors and any
// response that sets or clears a cookie; CSP and DENY framing on the
// pages Wardlock renders; and the fixed attributes on every Set-Cookie.
func TestProtectedHeadersThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.set("docs", "/", store.Protected, true)
	_, token := p.link("docs", access.NewLink{})
	_, once := p.link("docs", access.NewLink{MaxUses: 1})
	session := p.visit(visit{target: withT("/", token), header: html}).sessionCookie()
	if session == "" {
		t.Fatal("no session")
	}
	navigate := http.Header{"Accept": {"text/html"}, "Sec-Fetch-Mode": {"navigate"}}
	interstitial := p.visit(visit{target: withT("/internal/", once), header: html})
	_, after, _ := strings.Cut(interstitial.body, `name="ticket" value="`)
	ticket, _, _ := strings.Cut(after, `"`)
	etag := p.visit(visit{target: "/assets/site.css", cookie: session}).h.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	const (
		private = "private, max-age=0, must-revalidate"
		noStore = "no-store"
	)
	tests := []struct {
		name   string
		v      visit
		code   int
		cache  string
		vary   bool // the policy decided this answer
		page   bool // a page Wardlock renders: CSP and DENY framing
		cookie string
	}{
		{"static file", visit{target: "/assets/site.css", cookie: session}, 200, private, true, false, ""},
		{"directory index", visit{target: "/internal/", cookie: session, header: html}, 200, private, true, false, ""},
		{"clean-URL redirect", visit{target: "/internal", cookie: session}, 301, private, true, false, ""},
		{"not found", visit{target: "/missing.txt", cookie: session}, 404, private, true, false, ""},
		{"partial content", visit{target: "/assets/site.css", cookie: session, header: http.Header{"Range": {"bytes=0-3"}}}, 206, private, true, false, ""},
		{"not modified", visit{target: "/assets/site.css", cookie: session, header: http.Header{"If-None-Match": {etag}}}, 304, private, true, false, ""},
		{"range not satisfiable", visit{target: "/assets/site.css", cookie: session, header: http.Header{"Range": {"bytes=5000-"}}}, 416, private, true, false, ""},
		{"rendition by token", visit{target: withT("/", token) + "&format=md"}, 200, private, true, false, ""},
		{"refused, as a page", visit{target: "/internal/", header: html}, 401, noStore, true, true, ""},
		{"refused, to a fetcher", visit{target: "/internal/"}, 401, noStore, true, false, ""},
		{"refused, bad cookie cleared", visit{target: "/internal/", cookie: "djE.AAAA", header: html}, 401, noStore, true, true, "clear"},
		{"share-link redemption", visit{target: withT("/internal/", token), header: navigate}, 200, "private, no-store", true, false, "set"},
		{"share-link session on a file", visit{target: withT("/assets/site.css", token), header: navigate}, 200, noStore, true, false, "set"},
		{"share-link session on a redirect", visit{target: withT("/internal", token), header: navigate}, 301, noStore, true, false, "set"},
		{"share-link session on the 404 page", visit{target: withT("/missing", token), header: navigate}, 404, "private, no-store", true, false, "set"},
		{"bearer", visit{target: "/internal/", header: bearer(token)}, 200, private, true, false, ""},
		{"interstitial", visit{target: withT("/internal/", once), header: html}, 200, noStore, true, true, ""},
		{"interstitial POST", visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: url.Values{"ticket": {ticket}}}, 303, noStore, false, false, "set"},
		{"interstitial POST again", visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: url.Values{"ticket": {ticket}}}, 410, noStore, false, true, ""},
		{"login page", visit{target: "/.wardlock/login?next=/internal/"}, 200, noStore, false, true, ""},
		{"wrong password", visit{method: http.MethodPost, target: "/.wardlock/login", header: sameOrigin, form: url.Values{"method": {"password"}, "password": {"guess"}}}, 401, noStore, false, true, ""},
		{"health", visit{target: "/.wardlock/health"}, 200, noStore, false, false, ""},
		{"unknown reserved path", visit{target: "/.wardlock/nothing"}, 404, noStore, false, false, ""},
		{"site callback without a flow", visit{target: "/.wardlock/callback?code=abc"}, 400, noStore, false, true, ""},
		{"logout", visit{method: http.MethodPost, target: "/.wardlock/logout", cookie: session, header: sameOrigin}, 303, noStore, false, false, "clear"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := p.visit(tt.v)
			if r.code != tt.code {
				t.Fatalf("status %d, want %d: %q", r.code, tt.code, r.body)
			}
			for k, want := range map[string]string{
				"X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff", "Cache-Control": tt.cache,
				"Cross-Origin-Resource-Policy": "same-origin",
			} {
				if got := r.h.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if vary := strings.Join(r.h.Values("Vary"), ","); tt.vary && !strings.Contains(vary, "Cookie") {
				t.Errorf("Vary = %q, want Cookie", vary)
			}
			if tt.page {
				csp := r.h.Get("Content-Security-Policy")
				if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "script-src") {
					t.Errorf("Content-Security-Policy = %q", csp)
				}
				if r.h.Get("X-Frame-Options") != "DENY" {
					t.Errorf("X-Frame-Options = %q", r.h.Get("X-Frame-Options"))
				}
			}
			checkSetCookies(t, r.h, tt.cookie)
		})
	}

	// The clean-URL redirect keeps the query, the token included, so a
	// cookie-less fetcher that follows it still gets the page
	// (SECURITY-MODEL "Exception: clean-URL redirects keep the token").
	r := p.visit(visit{target: withT("/internal", token)})
	if r.code != 301 || r.h.Get("Location") != withT("/internal/", token) {
		t.Fatalf("redirect %d to %q", r.code, r.h.Get("Location"))
	}
	if next := p.visit(visit{target: r.h.Get("Location")}); next.code != 200 || !strings.Contains(next.body, "Internal plans") {
		t.Fatalf("following the redirect: %d", next.code)
	}
}

// checkSetCookies asserts every Set-Cookie in h carries the fixed
// attributes, and that h sets ("set"), clears ("clear") or leaves alone
// ("") the site session.
func checkSetCookies(t *testing.T, h http.Header, want string) {
	t.Helper()
	got := ""
	for _, raw := range h.Values("Set-Cookie") {
		name, _, _ := strings.Cut(raw, "=")
		attrs := strings.ToLower(raw)
		if !strings.HasPrefix(name, "__Host-") || strings.Contains(attrs, "domain=") ||
			!strings.Contains(attrs, "; path=/") || !strings.Contains(attrs, "; httponly") ||
			!strings.Contains(attrs, "; secure") || !strings.Contains(attrs, "; samesite=lax") {
			t.Errorf("Set-Cookie %q lacks a fixed attribute", raw)
		}
		if name != access.SessionCookieName {
			continue
		}
		switch {
		case strings.Contains(raw, "Max-Age=0") || strings.HasPrefix(raw, name+"=;"):
			got = "clear"
		case strings.Contains(raw, "Max-Age=") && strings.Contains(raw, "Expires="):
			got = "set"
		default:
			t.Errorf("Set-Cookie %q neither sets with an expiry nor clears", raw)
		}
	}
	if got != want {
		t.Errorf("site session cookie %q, want %q: %v", got, want, h.Values("Set-Cookie"))
	}
}
