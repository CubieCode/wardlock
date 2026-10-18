// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/store"
)

// The wardlock-access test matrix for share links (A2.2), through the real
// edge, access service and SQLite store as serve wires them.

const stripMarker = `history.replaceState`

// link creates a share link on site and returns it with its token.
func (p *policyEdge) link(site string, n access.NewLink) (store.ShareLink, string) {
	p.t.Helper()
	l, token, err := p.acc.CreateLink(p.t.Context(), p.owner.ID, site, n)
	if err != nil {
		p.t.Fatal(err)
	}
	p.tokens = append(p.tokens, token)
	return l, token
}

// visit is one request from a client at ip.
type visit struct {
	method, host, target, cookie, ip string
	header                           http.Header
	form                             url.Values
}

func (p *policyEdge) visit(v visit) result {
	p.t.Helper()
	if v.method == "" {
		v.method = http.MethodGet
	}
	if v.host == "" {
		v.host = host
	}
	var req *http.Request
	if v.form != nil {
		req = httptest.NewRequest(v.method, v.target, strings.NewReader(v.form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(v.method, v.target, nil)
	}
	req.Host = v.host
	if v.ip != "" {
		req.RemoteAddr = v.ip + ":4711"
	}
	for k, vals := range v.header {
		req.Header[k] = vals
	}
	if v.cookie != "" {
		req.AddCookie(&http.Cookie{Name: access.SessionCookieName, Value: v.cookie})
	}
	rec := httptest.NewRecorder()
	p.e.ServeHTTP(rec, req)
	return result{rec.Code, rec.Body.String(), rec.Result().Header}
}

// sessionCookie returns the site session cookie r sets, or "".
func (r result) sessionCookie() string {
	for _, c := range (&http.Response{Header: r.h}).Cookies() {
		if c.Name == access.SessionCookieName && c.MaxAge > 0 {
			return c.Value
		}
	}
	return ""
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func withT(path, token string) string { return path + "?t=" + url.QueryEscape(token) }

// noRawTokens fails t if any token minted in p reached its logs or access
// records.
func (p *policyEdge) noRawTokens(t *testing.T, records []edge.AccessRecord) {
	t.Helper()
	logs := p.logs.String()
	for _, tok := range p.tokens {
		secret := tok[len("wl_0123456789_"):]
		if strings.Contains(logs, secret) {
			t.Fatalf("a raw token reached the logs:\n%s", logs)
		}
		for _, r := range records {
			if strings.Contains(fmt.Sprintf("%+v", r), secret) {
				t.Fatalf("a raw token reached the access log: %+v", r)
			}
		}
	}
}

func TestShareLinkMatrixThroughTheEdge(t *testing.T) {
	type env struct {
		p     *policyEdge
		site  store.Site
		l     store.ShareLink
		token string
	}
	// Every case starts from a protected docs site (links on) and a
	// never-expiring whole-site link, and a published "other" site.
	setup := func(t *testing.T) env {
		p := newPolicyEdge(t)
		site := p.publish("docs")
		p.publish("other")
		p.set("docs", "/", store.Protected, true)
		p.set("other", "/", store.Protected, true)
		l, token := p.link("docs", access.NewLink{Label: "Linear ENG-123"})
		return env{p, site, l, token}
	}

	t.Run("no-cookie fetchers get the page on the first request", func(t *testing.T) {
		e := setup(t)
		navigate := http.Header{"Accept": {"text/html"}, "Sec-Fetch-Mode": {"navigate"}}
		fetch := http.Header{"Accept": {"text/html"}, "Sec-Fetch-Mode": {"cors"}}
		// Only a browser navigation earns a session; every other client
		// gets the content each time it sends the token, and leaves no
		// session row behind.
		for name, v := range map[string]struct {
			visit
			session bool
		}{
			"?t= without Accept (curl)": {visit: visit{target: withT("/", e.token)}},
			"?t= from a browser":        {visit: visit{target: withT("/", e.token), header: html}, session: true},
			"?t= navigation":            {visit: visit{target: withT("/", e.token), header: navigate}, session: true},
			"?t= from fetch()":          {visit: visit{target: withT("/", e.token), header: fetch}},
			"?t= HEAD from a browser":   {visit: visit{method: http.MethodHead, target: withT("/", e.token), header: html}},
			"Authorization: Bearer":     {visit: visit{target: "/", header: bearer(e.token)}},
			"?t= with ?format=md":       {visit: visit{target: withT("/", e.token) + "&format=md"}},
			"Bearer with ?format=md":    {visit: visit{target: "/?format=txt", header: bearer(e.token)}},
		} {
			r := e.p.visit(v.visit)
			if v.method == http.MethodHead {
				r.body = "Home page"
			}
			r.expect(t, 200, "Home page", "")
			if r.h.Get("Location") != "" {
				t.Fatalf("%s: redirected to %s", name, r.h.Get("Location"))
			}
			r.protected(t, r.h.Get("Cache-Control"))
			if !strings.HasPrefix(r.h.Get("Cache-Control"), "private") {
				t.Fatalf("%s: Cache-Control %q", name, r.h.Get("Cache-Control"))
			}
			if cookie := r.sessionCookie(); v.session != (cookie != "") {
				t.Fatalf("%s: session cookie %q, want one: %v", name, cookie, v.session)
			}
		}
		recs := e.p.accessRecords()
		for _, rec := range recs {
			if rec.Via != access.ViaLink || rec.LinkPublicID != e.l.PublicID || rec.Status != 200 {
				t.Fatalf("access record %+v, want via link %s", rec, e.l.PublicID)
			}
		}
		e.p.noRawTokens(t, recs)
	})

	t.Run("the cookie from ?t= keeps working without the token", func(t *testing.T) {
		e := setup(t)
		r := e.p.visit(visit{target: withT("/", e.token), header: html})
		set := r.h.Get("Set-Cookie")
		for _, attr := range []string{access.SessionCookieName + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
			if !strings.Contains(set, attr) {
				t.Fatalf("Set-Cookie %q lacks %s", set, attr)
			}
		}
		if strings.Contains(strings.ToLower(set), "domain=") {
			t.Fatalf("Set-Cookie %q is not host-only", set)
		}
		cookie := r.sessionCookie()
		e.p.visit(visit{target: "/internal/", cookie: cookie, header: html}).expect(t, 200, "Internal plans", "")
		// A token on a request that already has its session issues no new one.
		again := e.p.visit(visit{target: withT("/", e.token), cookie: cookie})
		again.expect(t, 200, "Home page", "")
		if again.h.Get("Set-Cookie") != "" {
			t.Fatalf("second visit set %q", again.h.Get("Set-Cookie"))
		}
	})

	t.Run("the token is stripped only from HTML responses", func(t *testing.T) {
		e := setup(t)
		page := e.p.visit(visit{target: withT("/", e.token), header: html})
		page.expect(t, 200, "<p>Home page</p>", "")
		if !strings.Contains(page.body, stripMarker) || page.h.Get("ETag") != "" {
			t.Fatalf("HTML via ?t= not stripped: %q %v", page.body, page.h)
		}
		if page.h.Get("Content-Length") != fmt.Sprint(len(page.body)) {
			t.Fatalf("Content-Length %s for %d bytes", page.h.Get("Content-Length"), len(page.body))
		}
		css := e.p.visit(visit{target: withT("/assets/site.css", e.token)})
		if css.code != 200 || css.body != policyFiles["assets/site.css"] {
			t.Fatalf("CSS via ?t= altered: %d %q", css.code, css.body)
		}
		missing := e.p.visit(visit{target: withT("/missing", e.token), header: html})
		missing.expect(t, 404, "Custom not found", "")
		if !strings.Contains(missing.body, stripMarker) {
			t.Fatal("404.html via ?t= not stripped")
		}
		viaHeader := e.p.visit(visit{target: "/", header: bearer(e.token)})
		if viaHeader.body != policyFiles["index.html"] {
			t.Fatalf("HTML via Bearer altered: %q", viaHeader.body)
		}
		// A public page never looks at t, so a site's own ?t= is untouched.
		e.p.set("docs", "/", store.Public, false)
		public := e.p.visit(visit{target: "/?t=42", header: html})
		if public.code != 200 || public.body != policyFiles["index.html"] {
			t.Fatalf("public page with ?t= altered: %d %q", public.code, public.body)
		}
	})

	t.Run("expired", func(t *testing.T) {
		e := setup(t)
		_, token := e.p.link("docs", access.NewLink{ExpiresAt: e.p.now().Add(time.Hour)})
		cookie := e.p.visit(visit{target: withT("/", token), header: html}).sessionCookie()
		e.p.advance(time.Hour)
		e.p.visit(visit{target: withT("/", token)}).expect(t, 401, "protected", "Home page")
		e.p.visit(visit{target: "/", header: bearer(token)}).expect(t, 401, "protected", "Home page")
		r := e.p.visit(visit{target: "/", cookie: cookie})
		r.expect(t, 401, "protected", "Home page")
		if !r.cleared() {
			t.Fatal("session past its link's expiry not cleared")
		}
		// The never-expiring link still works.
		e.p.visit(visit{target: withT("/", e.token)}).expect(t, 200, "Home page", "")
	})

	t.Run("revoked", func(t *testing.T) {
		e := setup(t)
		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		if err := e.p.acc.RevokeLink(t.Context(), e.p.owner.ID, "docs", e.l.PublicID); err != nil {
			t.Fatal(err)
		}
		e.p.visit(visit{target: withT("/", e.token)}).expect(t, 401, "protected", "Home page")
		e.p.visit(visit{target: "/", header: bearer(e.token)}).expect(t, 401, "protected", "Home page")
		r := e.p.visit(visit{target: "/", cookie: cookie, header: html})
		r.expect(t, 401, "This page is private", "Home page")
		if !r.cleared() {
			t.Fatal("revoked link's session not cleared")
		}
	})

	t.Run("rotated: the old token and its sessions fail, the new token works", func(t *testing.T) {
		e := setup(t)
		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		l, fresh, err := e.p.acc.RotateLink(t.Context(), e.p.owner.ID, "docs", e.l.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		e.p.tokens = append(e.p.tokens, fresh)
		if l.PublicID != e.l.PublicID || fresh == e.token || !strings.HasPrefix(fresh, "wl_"+e.l.PublicID+"_") {
			t.Fatalf("rotation gave %s / %q", l.PublicID, fresh)
		}
		e.p.visit(visit{target: withT("/", e.token)}).expect(t, 401, "protected", "Home page")
		if r := e.p.visit(visit{target: "/", cookie: cookie}); r.code != 401 || !r.cleared() {
			t.Fatalf("old session after rotation: %d cleared=%v", r.code, r.cleared())
		}
		e.p.visit(visit{target: withT("/", fresh)}).expect(t, 200, "Home page", "")
		e.p.visit(visit{target: "/", header: bearer(fresh)}).expect(t, 200, "Home page", "")
		e.p.noRawTokens(t, e.p.accessRecords())
	})

	t.Run("wrong site", func(t *testing.T) {
		e := setup(t)
		e.p.visit(visit{host: otherHost, target: withT("/", e.token)}).expect(t, 401, "protected", "Home page")
		e.p.visit(visit{host: otherHost, target: "/", header: bearer(e.token)}).expect(t, 401, "protected", "Home page")
		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		r := e.p.visit(visit{host: otherHost, target: "/", cookie: cookie})
		r.expect(t, 401, "protected", "Home page")
		if !r.cleared() {
			t.Fatal("another site's cookie not cleared")
		}
	})

	t.Run("wrong path", func(t *testing.T) {
		e := setup(t)
		_, token := e.p.link("docs", access.NewLink{PathPrefix: "/internal/"})
		e.p.visit(visit{target: withT("/internal/", token)}).expect(t, 200, "Internal plans", "")
		e.p.visit(visit{target: withT("/internal", token)}).expect(t, 301, "", "")
		e.p.visit(visit{target: withT("/", token)}).expect(t, 401, "protected", "Home page")
		e.p.visit(visit{target: "/assets/site.css", header: bearer(token)}).expect(t, 401, "protected", "teal")
		cookie := e.p.visit(visit{target: withT("/internal/", token), header: html}).sessionCookie()
		e.p.visit(visit{target: "/internal/", cookie: cookie}).expect(t, 200, "Internal plans", "")
		r := e.p.visit(visit{target: "/", cookie: cookie})
		r.expect(t, 401, "protected", "Home page")
		if r.cleared() {
			t.Fatal("a scoped session was cleared outside its scope")
		}
		// 404.html sits outside the scope, so a missing page in it is a
		// plain 404.
		e.p.visit(visit{target: withT("/internal/missing", token)}).expect(t, 404, "", "Custom not found")
		// The scope is a directory of the decoded, dot-resolved path,
		// matched case-sensitively.
		for target, code := range map[string]int{
			"/internal-other/":          401, // a sibling sharing the prefix
			"/internalx":                401,
			"/INTERNAL/":                401, // case
			"/internal/../":             401, // dot segments resolve first
			"/internal/..%2Findex.html": 401, // an encoded slash decodes before matching
			"/internal%2F":              200, // which also works the other way
			"/internal/./index.html":    200,
		} {
			r := e.p.visit(visit{target: withT(target, token)})
			if r.code != code || (code == 401 && strings.Contains(r.body, "Home page")) {
				t.Fatalf("%s: %d %q, want %d", target, r.code, r.body, code)
			}
		}
	})

	t.Run("links switched off on the policy", func(t *testing.T) {
		e := setup(t)
		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		e.p.set("docs", "/internal/", store.Protected, true)
		e.p.visit(visit{target: withT("/internal/", e.token)}).expect(t, 200, "Internal plans", "")
		// A policy without links (once other methods exist) refuses links;
		// until then, a public path ignores the token altogether.
		e.p.set("docs", "/", store.Public, false)
		r := e.p.visit(visit{target: withT("/", e.token)})
		r.expect(t, 200, "Home page", "")
		if r.sessionCookie() != "" || strings.Contains(r.body, stripMarker) {
			t.Fatal("a public page acted on the token")
		}
		e.p.visit(visit{target: "/", cookie: cookie}).expect(t, 200, "Home page", "")
	})

	t.Run("cookie isolation from the dashboard", func(t *testing.T) {
		e := setup(t)
		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		hits := e.p.dashboardHits
		r := e.p.visit(visit{host: "wardlock.localhost", target: withT("/", e.token), cookie: cookie})
		if e.p.dashboardHits != hits+1 || r.sessionCookie() != "" {
			t.Fatalf("dashboard host handled by the site edge: %+v", r)
		}
	})

	t.Run("a bad guess on a fallback file counts once", func(t *testing.T) {
		e := setup(t)
		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		limit := access.DefaultLimits().TokenFailuresPerIP.N
		// Allowed by the cookie despite the bad token, then answered by
		// 404.html: the fallback decision must not count the guess again.
		for range limit/2 + 1 {
			e.p.visit(visit{target: withT("/missing", "junk"), cookie: cookie, ip: "198.51.100.30"}).expect(t, 404, "Custom not found", "")
		}
		e.p.visit(visit{target: withT("/", e.token), ip: "198.51.100.30"}).expect(t, 200, "Home page", "")
	})

	t.Run("a deploy cannot override /.wardlock/", func(t *testing.T) {
		e := setup(t)
		e.p.visit(visit{target: "/.wardlock/view"}).expect(t, 405, "", "")
		e.p.visit(visit{target: "/.wardlock/view", method: http.MethodPost, header: sameOrigin, form: url.Values{"ticket": {"x"}}}).
			expect(t, 410, "This link no longer works", "")
	})
}

func TestUseLimitedLinkThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.set("docs", "/", store.Protected, true)
	l, token := p.link("docs", access.NewLink{Label: "one-time", MaxUses: 1})
	uses := func() int {
		got, err := p.db.Links().GetByPublicID(t.Context(), l.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		return got.UseCount
	}

	// GET, with or without a browser, shows the interstitial and spends
	// nothing, however often a preview bot or scanner fetches it.
	var ticket string
	for _, v := range []visit{
		{target: withT("/internal/", token) + "&x=1", header: html},
		{target: withT("/internal/", token) + "&x=1"},
		{target: "/internal/?x=1", header: bearer(token)},
		{method: http.MethodHead, target: withT("/internal/", token) + "&x=1"},
	} {
		r := p.visit(v)
		if r.code != 200 || r.sessionCookie() != "" || strings.Contains(r.body, "Internal plans") {
			t.Fatalf("interstitial %d cookie=%q body %q", r.code, r.sessionCookie(), r.body)
		}
		if r.h.Get("Cache-Control") != "no-store" || r.h.Get("X-Frame-Options") != "DENY" {
			t.Fatalf("interstitial headers %v", r.h)
		}
		if v.method == http.MethodHead {
			continue
		}
		if !strings.Contains(r.body, "Click to view") || strings.Contains(r.body, token[len("wl_0123456789_"):]) {
			t.Fatalf("interstitial body %q", r.body)
		}
		_, after, _ := strings.Cut(r.body, `name="ticket" value="`)
		ticket, _, _ = strings.Cut(after, `"`)
	}
	if uses() != 0 {
		t.Fatalf("GET spent %d uses", uses())
	}
	for _, rec := range p.accessRecords() {
		if rec.Via != access.ViaNone {
			t.Fatalf("interstitial recorded as %+v", rec)
		}
	}

	// A cross-site form (an attacker posting a ticket for their own link
	// to swap the visitor's session), a sibling site and a request with no
	// origin headers are all refused, spending nothing.
	for name, h := range map[string]http.Header{
		"another site":    {"Origin": {"https://evil.example"}},
		"a sibling site":  {"Origin": {"http://other.wardlock.localhost"}},
		"cross-site mode": {"Sec-Fetch-Site": {"cross-site"}},
		"no origin":       nil,
	} {
		r := p.visit(visit{method: http.MethodPost, target: "/.wardlock/view", header: h, form: url.Values{"ticket": {ticket}}})
		if r.code != http.StatusForbidden || r.sessionCookie() != "" {
			t.Fatalf("%s: %d %v", name, r.code, r.h)
		}
	}
	if uses() != 0 {
		t.Fatalf("refused POSTs spent %d uses", uses())
	}

	// POST spends the use, sets the session and returns to the page
	// without the token.
	r := p.visit(visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: url.Values{"ticket": {ticket}}})
	if r.code != http.StatusSeeOther || r.h.Get("Location") != "/internal/?x=1" || r.sessionCookie() == "" {
		t.Fatalf("view POST: %d %v", r.code, r.h)
	}
	if uses() != 1 {
		t.Fatalf("POST spent %d uses", uses())
	}
	cookie := r.sessionCookie()
	p.visit(visit{target: "/internal/?x=1", cookie: cookie}).expect(t, 200, "Internal plans", "")
	// With the session, the token in the URL no longer interposes.
	p.visit(visit{target: withT("/", token), cookie: cookie}).expect(t, 200, "Home page", "")

	// A second POST fails, and the spent link opens nothing new.
	p.visit(visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: url.Values{"ticket": {ticket}}}).
		expect(t, http.StatusGone, "This link no longer works", "")
	p.visit(visit{target: withT("/", token)}).expect(t, 401, "protected", "Home page")
	if uses() != 1 {
		t.Fatalf("second POST spent a use: %d", uses())
	}

	// A ticket dies with rotation and with time.
	l2, token2 := p.link("docs", access.NewLink{MaxUses: 3})
	ticketFor := func(tok string) string {
		r := p.visit(visit{target: withT("/", tok)})
		_, after, _ := strings.Cut(r.body, `name="ticket" value="`)
		tk, _, _ := strings.Cut(after, `"`)
		return tk
	}
	old := ticketFor(token2)
	if _, _, err := p.acc.RotateLink(t.Context(), p.owner.ID, "docs", l2.PublicID); err != nil {
		t.Fatal(err)
	}
	p.visit(visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: url.Values{"ticket": {old}}}).expect(t, http.StatusGone, "", "")
	_, token3 := p.link("docs", access.NewLink{MaxUses: 3})
	late := ticketFor(token3)
	p.advance(access.TicketLifetime)
	p.visit(visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: url.Values{"ticket": {late}}}).expect(t, http.StatusGone, "", "")
	p.noRawTokens(t, p.accessRecords())
}

func TestShareLinkRateLimitsThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	p.set("docs", "/", store.Protected, true)
	p.set("other", "/", store.Protected, true)
	_, token := p.link("docs", access.NewLink{})
	_, otherToken := p.link("other", access.NewLink{})
	limits := access.DefaultLimits()
	guess := strings.Replace(token, token[len(token)-6:], "AAAAAA", 1)

	// Per IP: wrong secrets, unknown public IDs and junk all count.
	for i := range limits.TokenFailuresPerIP.N {
		bad := []string{guess, "wl_aaaaaaaaaa_" + strings.Repeat("B", 43), "junk"}[i%3]
		p.visit(visit{target: withT("/", bad), ip: "198.51.100.7"}).expect(t, 401, "protected", "")
	}
	r := p.visit(visit{target: withT("/", token), ip: "198.51.100.7"})
	if r.code != http.StatusTooManyRequests || r.h.Get("Retry-After") == "" {
		t.Fatalf("over the IP limit: %d %v", r.code, r.h)
	}
	// Other addresses are unaffected.
	p.visit(visit{target: withT("/", token), ip: "198.51.100.8"}).expect(t, 200, "Home page", "")
	p.advance(limits.TokenFailuresPerIP.Per)
	p.visit(visit{target: withT("/", token), ip: "198.51.100.7"}).expect(t, 200, "Home page", "")

	// Per site: guesses spread over many IPs close the site's tokens,
	// not other sites'.
	for i := range limits.TokenFailuresPerSite.N {
		ip := fmt.Sprintf("203.0.113.%d", i/limits.TokenFailuresPerIP.N+1)
		p.visit(visit{target: withT("/", guess), ip: ip}).expect(t, 401, "protected", "")
	}
	if r := p.visit(visit{target: "/", header: bearer(token), ip: "192.0.2.200"}); r.code != http.StatusTooManyRequests {
		t.Fatalf("over the site limit: %d", r.code)
	}
	p.visit(visit{host: otherHost, target: withT("/", otherToken), ip: "192.0.2.200"}).expect(t, 200, "Home page", "")
	p.advance(limits.TokenFailuresPerSite.Per)
	p.visit(visit{target: "/", header: bearer(token), ip: "192.0.2.200"}).expect(t, 200, "Home page", "")

	if !strings.Contains(p.logs.String(), "share link refused") {
		t.Fatal("refusals not logged")
	}
	p.noRawTokens(t, p.accessRecords())
}
