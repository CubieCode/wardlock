// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// Synthetic passwords for tests only.
const (
	sitePassword  = "harbour-lights-7"
	otherPassword = "tide-chart-42"
)

func (p *policyEdge) setPassword(site, prefix, pw string, links bool) store.Policy {
	p.t.Helper()
	pol, err := p.acc.Set(p.t.Context(), p.owner.ID, site, access.Rule{
		PathPrefix: prefix, Visibility: store.Protected, Links: links, Password: true, NewPassword: pw,
	})
	if err != nil {
		p.t.Fatal(err)
	}
	return pol
}

// post submits a sign-in form from ip as a browser does from the login
// page on h, unless header says otherwise: the page sends no-referrer, so
// Chromium posts with Origin: null and Sec-Fetch-Site: same-origin.
func (p *policyEdge) post(h, ip string, form url.Values, header http.Header) result {
	p.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/.wardlock/login", strings.NewReader(form.Encode()))
	req.Host = h
	req.RemoteAddr = ip + ":40000"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	p.e.ServeHTTP(rec, req)
	return result{rec.Code, rec.Body.String(), rec.Result().Header}
}

func passwordForm(pw, next string) url.Values {
	return url.Values{"method": {"password"}, "password": {pw}, "next": {next}}
}

// sessionFrom returns the site session cookie value r sets, or "".
func (r result) sessionFrom() string {
	for _, c := range (&http.Response{Header: r.h}).Cookies() {
		if c.Name == access.SessionCookieName && c.MaxAge > 0 {
			return c.Value
		}
	}
	return ""
}

// TestPasswordThroughTheEdge runs the password method end to end: the form
// on the login page, correct and wrong passwords, the cookie and its
// isolation, returning to the page asked for, rate limits and their reset,
// a password change ending sessions, and logs free of the password.
func TestPasswordThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	p.setPassword("docs", "/", sitePassword, false)
	p.setPassword("other", "/", otherPassword, false)

	// A browser at a protected page gets the form, posting back the page.
	r := p.do(http.MethodGet, host, "/internal/", "", html)
	r.expect(t, 401, `<h2 id="method-password">Password</h2>`, "Internal plans")
	r.protected(t, "no-store")
	for _, want := range []string{
		`<form method="post" action="/.wardlock/login">`, `name="method" value="password"`,
		`name="next" value="/internal/"`, `type="password" autocomplete="current-password"`,
	} {
		if !strings.Contains(r.body, want) {
			t.Fatalf("login page lacks %s:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "Share link") {
		t.Fatal("login page offers share links on a password-only policy")
	}
	r = p.do(http.MethodGet, host, "/.wardlock/login?next=/internal/", "", html)
	r.expect(t, 200, `name="next" value="/internal/"`, "")
	if r.h.Get("Cache-Control") != "no-store" || r.h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("login page headers %v", r.h)
	}

	// Wrong password: the page again, with one message for every failure,
	// no cookie and no-store.
	for _, form := range []url.Values{
		passwordForm("harbour-lights-8", "/internal/"),
		passwordForm(otherPassword, "/internal/"),
		passwordForm("", "/internal/"),
	} {
		r = p.post(host, "192.0.2.10", form, nil)
		r.expect(t, 401, "That password is not right.", "Internal plans")
		if r.h.Get("Set-Cookie") != "" || r.h.Get("Cache-Control") != "no-store" {
			t.Fatalf("wrong password headers %v", r.h)
		}
		if !strings.Contains(r.body, `aria-invalid="true"`) || !strings.Contains(r.body, `name="next" value="/internal/"`) {
			t.Fatalf("wrong password page:\n%s", r.body)
		}
	}

	// The right one sets a host-only __Host- cookie and returns to the page.
	r = p.post(host, "192.0.2.11", passwordForm(sitePassword, "/internal/"), nil)
	if r.code != http.StatusSeeOther || r.h.Get("Location") != "/internal/" || r.h.Get("Cache-Control") != "no-store" {
		t.Fatalf("correct password: %d, Location %q, Cache-Control %q", r.code, r.h.Get("Location"), r.h.Get("Cache-Control"))
	}
	set := r.h.Get("Set-Cookie")
	for _, attr := range []string{access.SessionCookieName + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(set, attr) {
			t.Fatalf("Set-Cookie %q lacks %s", set, attr)
		}
	}
	if strings.Contains(strings.ToLower(set), "domain=") {
		t.Fatalf("Set-Cookie %q is not host-only", set)
	}
	cookie := r.sessionFrom()
	r = p.do(http.MethodGet, host, "/internal/", cookie, html)
	r.expect(t, 200, "Internal plans", "")
	r.protected(t, "private, max-age=0, must-revalidate")

	// The cookie opens nothing on another site, nor the dashboard.
	r = p.do(http.MethodGet, otherHost, "/", cookie, html)
	r.expect(t, 401, "This page is private", "Home page")
	if !r.cleared() {
		t.Fatal("another site's cookie was not cleared")
	}
	before := p.dashboardHits
	p.do(http.MethodGet, "wardlock.localhost", "/", cookie, html)
	if p.dashboardHits != before+1 {
		t.Fatal("dashboard request did not reach the dashboard")
	}
	// Another site's password does not open this one.
	p.post(otherHost, "192.0.2.12", passwordForm(sitePassword, "/"), nil).expect(t, 401, "That password is not right.", "")

	// next only ever returns to a path on this site.
	for next, want := range map[string]string{
		"":                       "/",
		"/docs/a b":              "/docs/a%20b",
		"//evil.example/":        "/",
		"/\\evil.example/":       "/",
		"https://evil.example/":  "/",
		"/x/../.wardlock/logout": "/",
		"/report?t=wl_x":         "/",
		"/a/./b/../c":            "/a/c",
	} {
		r = p.post(host, "192.0.2.13", passwordForm(sitePassword, next), nil)
		if r.code != http.StatusSeeOther || r.h.Get("Location") != want {
			t.Fatalf("next %q: %d, Location %q, want %q", next, r.code, r.h.Get("Location"), want)
		}
	}
	p.advance(15 * time.Minute)

	// Only a form on this host may sign in, and only a known method.
	for name, h := range map[string]http.Header{
		"another site":               {"Origin": {"https://evil.example"}, "Sec-Fetch-Site": {"cross-site"}},
		"a sibling site":             {"Origin": {"http://" + otherHost}, "Sec-Fetch-Site": {"same-site"}},
		"a null origin, no metadata": {"Origin": {"null"}, "Sec-Fetch-Site": nil},
	} {
		if r := p.post(host, "192.0.2.14", passwordForm(sitePassword, "/"), h); r.code != http.StatusForbidden || r.sessionFrom() != "" {
			t.Fatalf("%s: %d", name, r.code)
		}
	}
	p.post(host, "192.0.2.14", url.Values{"method": {"telepathy"}}, nil).expect(t, 400, "", "")

	// Ten attempts per IP; the eleventh is refused even with the right
	// password, with Retry-After, and the limit resets after its window.
	for i := range 10 {
		if r := p.post(host, "198.51.100.20", passwordForm("wrong-guess", "/"), nil); r.code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, r.code)
		}
	}
	r = p.post(host, "198.51.100.20", passwordForm(sitePassword, "/"), nil)
	r.expect(t, 429, "Too many attempts. Try again in 15 minutes.", "")
	if r.h.Get("Retry-After") != "900" || r.sessionFrom() != "" || r.h.Get("Cache-Control") != "no-store" {
		t.Fatalf("rate limited headers %v", r.h)
	}
	if r := p.post(host, "198.51.100.21", passwordForm(sitePassword, "/"), nil); r.code != http.StatusSeeOther {
		t.Fatalf("another IP was limited: %d", r.code)
	}
	p.advance(15 * time.Minute)
	if r := p.post(host, "198.51.100.20", passwordForm(sitePassword, "/"), nil); r.code != http.StatusSeeOther {
		t.Fatalf("limit did not reset: %d", r.code)
	}

	// Changing the password ends password sessions on the next request.
	p.setPassword("docs", "/", otherPassword, false)
	r = p.do(http.MethodGet, host, "/internal/", cookie, html)
	r.expect(t, 401, "This page is private", "Internal plans")
	if !r.cleared() {
		t.Fatal("session from the old password was not cleared")
	}
	p.post(host, "192.0.2.15", passwordForm(sitePassword, "/"), nil).expect(t, 401, "That password is not right.", "")
	cookie = p.post(host, "192.0.2.15", passwordForm(otherPassword, "/"), nil).sessionFrom()
	p.do(http.MethodGet, host, "/", cookie, html).expect(t, 200, "Home page", "")

	// Password and links side by side: both methods shown, divided.
	p.setPassword("docs", "/", sitePassword, true)
	r = p.do(http.MethodGet, host, "/", "", html)
	r.expect(t, 401, `<h2 id="method-links">Share link</h2>`, "")
	if !strings.Contains(r.body, `<h2 id="method-password">Password</h2>`) {
		t.Fatal("password form missing beside share links")
	}

	// A password posted for a path whose policy has none fails the same
	// way, with the message shown although there is no form to put it under.
	p.set("docs", "/internal/", store.Protected, true)
	r = p.post(host, "192.0.2.16", passwordForm(sitePassword, "/internal/"), nil)
	r.expect(t, 401, `<p class="error alone" role="alert">That password is not right.`, "Internal plans")
	if strings.Contains(r.body, `id="method-password"`) || r.sessionFrom() != "" {
		t.Fatalf("links-only policy page:\n%s", r.body)
	}

	// Nothing logged the password or the session cookie.
	enc, sig, _ := strings.Cut(cookie, ".")
	out := p.logs.String()
	if !strings.Contains(out, "password sign-in") {
		t.Fatalf("no sign-in was logged:\n%s", out)
	}
	for _, s := range []string{sitePassword, otherPassword, url.QueryEscape(sitePassword), enc, sig} {
		if strings.Contains(out, s) {
			t.Fatalf("log contains a secret (%d bytes):\n%s", len(s), out)
		}
	}
}
