// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// Synthetic visitor addresses for tests only.
const (
	visitorAddr  = "ana@example.test"
	strangerAddr = "eve@elsewhere.test"
)

func (p *policyEdge) setEmail(site, prefix string, allow store.Allowlist, links bool) store.Policy {
	p.t.Helper()
	pol, err := p.acc.Set(p.t.Context(), p.owner.ID, site, access.Rule{
		PathPrefix: prefix, Visibility: store.Protected, Links: links, Email: true, Allowlist: &allow,
	})
	if err != nil {
		p.t.Fatal(err)
	}
	return pol
}

func sendForm(addr, next string) url.Values {
	return url.Values{"method": {"email"}, "step": {"send"}, "email": {addr}, "next": {next}}
}

func verifyForm(addr, code, next string) url.Values {
	return url.Values{"method": {"email"}, "step": {"verify"}, "email": {addr}, "code": {code}, "next": {next}}
}

var mailCode = regexp.MustCompile(`\n {4}(\d{6})\r?\n`)

// codeFor waits for the dev:// sink to write a message to addr and returns
// the code in it, deleting the file.
func (p *policyEdge) codeFor(addr string) string {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(p.mailDir, "*.eml"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil || !strings.Contains(string(b), "\r\nTo: <"+addr+">\r\n") {
				continue
			}
			_ = os.Remove(f)
			m := mailCode.FindStringSubmatch(string(b))
			if m == nil {
				p.t.Fatalf("message to %s has no code:\n%s", addr, b)
			}
			return m[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatalf("no message to %s", addr)
	return ""
}

// mailTo reports whether any message to addr is waiting in the sink.
func (p *policyEdge) mailTo(addr string) bool {
	files, _ := filepath.Glob(filepath.Join(p.mailDir, "*.eml"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "\r\nTo: <"+addr+">\r\n") {
			return true
		}
	}
	return false
}

// TestEmailCodeThroughTheEdge runs the email-code method end to end through
// the real edge, access service, store and dev:// mail sink: the forms, the
// neutral answer for disallowed addresses, the code and the session cookie,
// isolation, policy changes, rate limits and logs free of codes and
// addresses.
func TestEmailCodeThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	allow := store.Allowlist{Emails: []string{visitorAddr}, Domains: []string{"*.crew.test"}}
	p.setEmail("docs", "/", allow, false)
	p.setEmail("other", "/", allow, false)

	// The login page offers the address form.
	r := p.do(http.MethodGet, host, "/internal/", "", html)
	r.expect(t, 401, `<h2 id="method-email">Email code</h2>`, "Internal plans")
	r.protected(t, "no-store")
	for _, want := range []string{`name="method" value="email"`, `name="step" value="send"`, `type="email" autocomplete="email"`, `name="next" value="/internal/"`} {
		if !strings.Contains(r.body, want) {
			t.Fatalf("login page lacks %s:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "Share link") || strings.Contains(r.body, `id="method-password"`) {
		t.Fatal("login page offers methods the policy does not enable")
	}

	// Allowed and disallowed addresses get the same answer: status,
	// headers and body, but for the address each typed.
	allowed := p.post(host, "192.0.2.10", sendForm(visitorAddr, "/internal/"), nil)
	stranger := p.post(host, "192.0.2.11", sendForm(strangerAddr, "/internal/"), nil)
	allowed.expect(t, 200, "we've sent a 6-digit code to it", "Internal plans")
	if allowed.code != stranger.code || strings.ReplaceAll(allowed.body, visitorAddr, strangerAddr) != stranger.body {
		t.Fatalf("allowed and disallowed answers differ:\n%d %s\n%d %s", allowed.code, allowed.body, stranger.code, stranger.body)
	}
	for _, k := range []string{"Cache-Control", "Content-Type", "Content-Security-Policy", "X-Robots-Tag", "Referrer-Policy", "Set-Cookie", "Retry-After"} {
		if allowed.h.Get(k) != stranger.h.Get(k) {
			t.Fatalf("header %s differs: %q vs %q", k, allowed.h.Get(k), stranger.h.Get(k))
		}
	}
	for _, want := range []string{`name="step" value="verify"`, `autocomplete="one-time-code"`, `name="email" value="` + visitorAddr + `"`, "Send a new code", `href="/.wardlock/login?next=%2finternal%2f"`} {
		if !strings.Contains(allowed.body, want) {
			t.Fatalf("code form lacks %s:\n%s", want, allowed.body)
		}
	}
	code := p.codeFor(visitorAddr)
	if p.mailTo(strangerAddr) {
		t.Fatal("a code was mailed to a disallowed address")
	}

	// A wrong code: the code form again, one message, 401, no cookie.
	r = p.post(host, "192.0.2.10", verifyForm(visitorAddr, otherDigits(code), "/internal/"), nil)
	r.expect(t, 401, "That code is not right, or it has expired.", "Internal plans")
	if r.sessionFrom() != "" || !strings.Contains(r.body, `aria-invalid="true"`) || !strings.Contains(r.body, `name="step" value="verify"`) {
		t.Fatalf("wrong code page:\n%s", r.body)
	}
	// The right one sets a host-only __Host- cookie and returns to the page.
	r = p.post(host, "192.0.2.10", verifyForm(" ANA@example.test", code, "/internal/"), nil)
	if r.code != http.StatusSeeOther || r.h.Get("Location") != "/internal/" || r.h.Get("Cache-Control") != "no-store" {
		t.Fatalf("right code: %d, Location %q, Cache-Control %q", r.code, r.h.Get("Location"), r.h.Get("Cache-Control"))
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
	if recs := p.accessRecords(); len(recs) == 0 || recs[len(recs)-1].Via != access.ViaEmail {
		t.Fatalf("access log %+v, want via email", recs)
	}
	// The code worked once.
	p.post(host, "192.0.2.10", verifyForm(visitorAddr, code, "/"), nil).expect(t, 401, "That code is not right", "")

	// The cookie opens nothing on another site, nor the dashboard; a
	// code for one site does not open another.
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
	p.post(host, "192.0.2.12", sendForm("cook@deck.crew.test", "/"), nil).expect(t, 200, "we've sent", "")
	code = p.codeFor("cook@deck.crew.test")
	p.post(otherHost, "192.0.2.12", verifyForm("cook@deck.crew.test", code, "/"), nil).expect(t, 401, "That code is not right", "")

	// Only a form on this host may send codes or sign in, and only a known
	// step.
	for name, h := range map[string]http.Header{
		"another site":               {"Origin": {"https://evil.example"}, "Sec-Fetch-Site": {"cross-site"}},
		"a sibling site":             {"Origin": {"http://" + otherHost}, "Sec-Fetch-Site": {"same-site"}},
		"a null origin, no metadata": {"Origin": {"null"}, "Sec-Fetch-Site": nil},
	} {
		if r := p.post(host, "192.0.2.13", sendForm(visitorAddr, "/"), h); r.code != http.StatusForbidden {
			t.Fatalf("%s send: %d", name, r.code)
		}
		if r := p.post(host, "192.0.2.13", verifyForm("cook@deck.crew.test", code, "/"), h); r.code != http.StatusForbidden || r.sessionFrom() != "" {
			t.Fatalf("%s verify: %d", name, r.code)
		}
	}
	p.post(host, "192.0.2.13", url.Values{"method": {"email"}, "step": {"guess"}}, nil).expect(t, 400, "", "")
	// Not an address: the address form again, with the entry kept.
	r = p.post(host, "192.0.2.13", sendForm("ana at example", "/"), nil)
	r.expect(t, 400, "Enter an email address like name@example.com.", "")
	if !strings.Contains(r.body, `value="ana at example"`) || !strings.Contains(r.body, `name="step" value="send"`) {
		t.Fatalf("bad address page:\n%s", r.body)
	}

	// Sends are limited per IP (20 an hour), allowed or not: the 21st is
	// refused with Retry-After, and the limit resets after its window.
	for i := range 20 {
		if r := p.post(host, "198.51.100.20", sendForm(strings.Repeat("x", i+1)+"@elsewhere.test", "/"), nil); r.code != http.StatusOK {
			t.Fatalf("send %d: %d", i+1, r.code)
		}
	}
	r = p.post(host, "198.51.100.20", sendForm(visitorAddr, "/"), nil)
	r.expect(t, 429, "Too many attempts. Try again in 60 minutes.", "")
	if r.h.Get("Retry-After") != "3600" || r.h.Get("Cache-Control") != "no-store" || !strings.Contains(r.body, `name="step" value="send"`) {
		t.Fatalf("rate limited: %v\n%s", r.h, r.body)
	}
	p.advance(time.Hour)
	if r := p.post(host, "198.51.100.20", sendForm(visitorAddr, "/"), nil); r.code != http.StatusOK {
		t.Fatalf("limit did not reset: %d", r.code)
	}
	code = p.codeFor(visitorAddr)

	// An allowlist change ends email sessions and codes sent before it.
	r = p.post(host, "192.0.2.14", verifyForm(visitorAddr, code, "/"), nil)
	cookie = r.sessionFrom()
	p.do(http.MethodGet, host, "/", cookie, html).expect(t, 200, "Home page", "")
	p.post(host, "192.0.2.14", sendForm("cook@deck.crew.test", "/"), nil)
	pending := p.codeFor("cook@deck.crew.test")
	p.setEmail("docs", "/", store.Allowlist{Emails: []string{visitorAddr}, Domains: []string{"*.crew.test", "crew.test"}}, false)
	r = p.do(http.MethodGet, host, "/", cookie, html)
	r.expect(t, 401, "This page is private", "Home page")
	if !r.cleared() {
		t.Fatal("session from before the allowlist change was not cleared")
	}
	p.post(host, "192.0.2.14", verifyForm("cook@deck.crew.test", pending, "/"), nil).expect(t, 401, "That code is not right", "")

	// A code posted for a path whose policy has no email method fails, with
	// the message shown although there is no form to put it under.
	p.set("docs", "/internal/", store.Protected, true)
	r = p.post(host, "192.0.2.15", verifyForm(visitorAddr, "123456", "/internal/"), nil)
	r.expect(t, 401, `<p class="error alone" role="alert">That code is not right`, "Internal plans")
	if strings.Contains(r.body, `id="method-email"`) {
		t.Fatalf("links-only policy page:\n%s", r.body)
	}
	r = p.post(host, "192.0.2.15", sendForm(visitorAddr, "/internal/"), nil)
	r.expect(t, 400, `<p class="error alone" role="alert">Email codes are not available for this page.`, "")

	// Nothing but the dev sink logged an address, and nothing a code or the
	// session cookie.
	enc, sig, _ := strings.Cut(cookie, ".")
	out := p.logs.String()
	for _, want := range []string{"email code sent", "email code sign-in", "email code refused"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %q:\n%s", want, out)
		}
	}
	for _, s := range []string{visitorAddr, strangerAddr, "crew.test", code, pending, enc, sig} {
		if strings.Contains(out, s) {
			t.Fatalf("log contains a secret or address (%d bytes):\n%s", len(s), out)
		}
	}
	if mailLog := p.mailLogs.String(); strings.Contains(mailLog, code) || !strings.Contains(mailLog, "mail written to dev sink") {
		t.Fatalf("dev sink log:\n%s", mailLog)
	}
}

// otherDigits returns a well-formed code that is not code.
func otherDigits(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}
