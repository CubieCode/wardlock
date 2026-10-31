// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// ownerMark is a header only the deploy's rules set: Wardlock's own pages
// never send Permissions-Policy.
const ownerMark = "camera=()"

// ruleSet is a deploy's redirects and headers for the tests below: a
// redirect on a public-looking path, one inside /internal/, a splat to
// another origin, and a catch-all header rule with public-only names.
func ruleSet() deploys.Manifest {
	return deploys.Manifest{
		Redirects: &[]store.Redirect{
			{From: "/old", To: "/internal/", Status: 302},
			{From: "/internal/old", To: "/internal/"},
			{From: "/away/*", To: "https://elsewhere.example.test/:splat", Status: 308},
		},
		Headers: &[]store.HeaderRule{{Path: "/*", Set: map[string]string{
			"Permissions-Policy":          ownerMark,
			"Referrer-Policy":             "strict-origin",
			"Access-Control-Allow-Origin": "*",
		}}},
	}
}

// publishRules publishes policyFiles with ruleSet as site name.
func (p *policyEdge) publishRules(name string) {
	p.t.Helper()
	p.publishWith(p.owner.ID, name, ruleSet(), policyFiles)
}

// owned reports whether r carries the deploy's headers.
func (r result) owned() bool { return r.h.Get("Permissions-Policy") == ownerMark }

// noRules fails t if r carries a deploy's redirect or headers.
func (r result) noRules(t *testing.T, name string) {
	t.Helper()
	if r.owned() || r.h.Get("Access-Control-Allow-Origin") != "" || strings.Contains(r.h.Get("Location"), "elsewhere") ||
		r.h.Get("Location") == "/internal/" {
		t.Fatalf("%s: %d reached a deploy rule: %v", name, r.code, r.h)
	}
}

// TestRulesWithAccessThroughTheEdge runs the wardlock-access matrix with
// redirects and headers on the site: a redirect or header on a protected
// path reaches only a visitor who may read it, Wardlock's protected-site
// headers win, and nothing applies under /.wardlock/ or to Wardlock's
// pages.
func TestRulesWithAccessThroughTheEdge(t *testing.T) {
	type env struct {
		p     *policyEdge
		l     store.ShareLink
		token string
	}
	setup := func(t *testing.T) env {
		p := newPolicyEdge(t)
		p.publishRules("docs")
		p.publishRules("other")
		p.set("docs", "/", store.Protected, true)
		p.set("other", "/", store.Protected, true)
		l, token := p.link("docs", access.NewLink{})
		return env{p, l, token}
	}
	// reached checks a protected redirect answered with the deploy's rules
	// and Wardlock's headers on top.
	reached := func(t *testing.T, name string, r result, code int, location string) {
		t.Helper()
		if r.code != code || r.h.Get("Location") != location || !r.owned() {
			t.Fatalf("%s: %d Location %q owner header %v, want %d to %q", name, r.code, r.h.Get("Location"), r.owned(), code, location)
		}
		if r.h.Get("Referrer-Policy") != "no-referrer" || r.h.Get("Access-Control-Allow-Origin") != "" || r.h.Get("X-Robots-Tag") != "noindex" {
			t.Fatalf("%s: protected headers lost to the deploy's: %v", name, r.h)
		}
	}

	t.Run("no access: no redirect, no headers", func(t *testing.T) {
		e := setup(t)
		for name, v := range map[string]visit{
			"browser":          {target: "/old", header: html},
			"fetcher":          {target: "/old"},
			"splat":            {target: "/away/x"},
			"page":             {target: "/", header: html},
			"bad token":        {target: withT("/old", "junk")},
			"bad bearer":       {target: "/old", header: bearer("wl_aaaaaaaaaa_" + strings.Repeat("B", 43))},
			"bad cookie":       {target: "/old", cookie: "djE.AAAA"},
			"format=md, no id": {target: "/?format=md"},
		} {
			r := e.p.visit(v)
			if r.code != 401 {
				t.Fatalf("%s: %d, want 401", name, r.code)
			}
			r.noRules(t, name)
		}
	})

	t.Run("no-cookie fetchers: ?t= and Bearer on the first request", func(t *testing.T) {
		e := setup(t)
		for name, v := range map[string]visit{
			"?t=":    {target: withT("/old", e.token)},
			"Bearer": {target: "/old", header: bearer(e.token)},
		} {
			// The redirect is the content: the token stays on the same
			// site, so a fetcher without cookies can follow it.
			r := e.p.visit(v)
			want := "/internal/"
			if v.header == nil {
				want += "?t=" + url.QueryEscape(e.token)
			}
			reached(t, name, r, 302, want)
		}
		for name, v := range map[string]visit{
			"?t= page":          {target: withT("/", e.token)},
			"Bearer page":       {target: "/", header: bearer(e.token)},
			"?t= ?format=md":    {target: withT("/", e.token) + "&format=md"},
			"Bearer ?format=md": {target: "/?format=txt", header: bearer(e.token)},
		} {
			r := e.p.visit(v)
			r.expect(t, 200, "Home page", "")
			reached(t, name, r, 200, "")
		}
	})

	t.Run("a token never leaves the site in a redirect", func(t *testing.T) {
		e := setup(t)
		r := e.p.visit(visit{target: withT("/away/a%20b/c", e.token) + "&keep=1"})
		reached(t, "splat", r, 308, "https://elsewhere.example.test/a%20b/c?keep=1")
		if strings.Contains(r.h.Get("Location"), e.token[len("wl_0123456789_"):]) {
			t.Fatalf("Location %q carries the token", r.h.Get("Location"))
		}
		e.p.noRawTokens(t, e.p.accessRecords())
	})

	t.Run("the token is stripped only from HTML responses", func(t *testing.T) {
		e := setup(t)
		page := e.p.visit(visit{target: withT("/", e.token), header: html})
		if !strings.Contains(page.body, stripMarker) || !page.owned() {
			t.Fatalf("HTML via ?t=: stripped %v, owner headers %v", strings.Contains(page.body, stripMarker), page.owned())
		}
		css := e.p.visit(visit{target: withT("/assets/site.css", e.token)})
		if css.body != policyFiles["assets/site.css"] || !css.owned() {
			t.Fatalf("CSS via ?t=: %q owner headers %v", css.body, css.owned())
		}
		redirect := e.p.visit(visit{target: withT("/old", e.token), header: html})
		if redirect.body != "" || redirect.code != 302 {
			t.Fatalf("redirect via ?t=: %d %q", redirect.code, redirect.body)
		}
	})

	t.Run("expired, revoked and rotated tokens", func(t *testing.T) {
		e := setup(t)
		_, expiring := e.p.link("docs", access.NewLink{ExpiresAt: e.p.now().Add(time.Hour)})
		reached(t, "before expiry", e.p.visit(visit{target: "/old", header: bearer(expiring)}), 302, "/internal/")
		e.p.advance(time.Hour)
		r := e.p.visit(visit{target: "/old", header: bearer(expiring)})
		r.expect(t, 401, "protected", "")
		r.noRules(t, "expired")

		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		reached(t, "session", e.p.visit(visit{target: "/old", cookie: cookie}), 302, "/internal/")
		_, fresh, err := e.p.acc.RotateLink(t.Context(), store.ActorFor(e.p.owner.ID), "docs", e.l.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		e.p.tokens = append(e.p.tokens, fresh)
		for name, v := range map[string]visit{
			"old token":   {target: withT("/old", e.token)},
			"old session": {target: "/old", cookie: cookie},
		} {
			r := e.p.visit(v)
			r.expect(t, 401, "protected", "")
			r.noRules(t, name)
		}
		reached(t, "rotated", e.p.visit(visit{target: "/old", header: bearer(fresh)}), 302, "/internal/")
		if err := e.p.acc.RevokeLink(t.Context(), store.ActorFor(e.p.owner.ID), "docs", e.l.PublicID); err != nil {
			t.Fatal(err)
		}
		r = e.p.visit(visit{target: "/old", header: bearer(fresh)})
		r.expect(t, 401, "protected", "")
		r.noRules(t, "revoked")
		e.p.noRawTokens(t, e.p.accessRecords())
	})

	t.Run("wrong site and wrong path", func(t *testing.T) {
		e := setup(t)
		cookie := e.p.visit(visit{target: withT("/", e.token), header: html}).sessionCookie()
		for name, v := range map[string]visit{
			"token on another site":  {host: otherHost, target: withT("/old", e.token)},
			"bearer on another site": {host: otherHost, target: "/old", header: bearer(e.token)},
			"cookie on another site": {host: otherHost, target: "/old", cookie: cookie},
		} {
			r := e.p.visit(v)
			r.expect(t, 401, "protected", "")
			r.noRules(t, name)
		}
		_, scoped := e.p.link("docs", access.NewLink{PathPrefix: "/internal/"})
		// /internal/old is in the link's scope, /old is not: from is
		// matched to its policy like any request path.
		reached(t, "in scope", e.p.visit(visit{target: "/internal/old", header: bearer(scoped)}), 301, "/internal/")
		r := e.p.visit(visit{target: "/old", header: bearer(scoped)})
		r.expect(t, 401, "protected", "")
		r.noRules(t, "out of scope")
	})

	t.Run("a protected path inside a public site", func(t *testing.T) {
		e := setup(t)
		e.p.set("docs", "/", store.Public, false)
		e.p.set("docs", "/internal/", store.Protected, true)
		public := e.p.visit(visit{target: "/old"})
		if public.code != 302 || public.h.Get("Location") != "/internal/" || !public.owned() {
			t.Fatalf("public redirect: %d %v", public.code, public.h)
		}
		// Public-only headers reach public answers with the owner's values.
		if public.h.Get("Referrer-Policy") != "strict-origin" || public.h.Get("Access-Control-Allow-Origin") != "*" || public.h.Get("X-Robots-Tag") != "" {
			t.Fatalf("public headers: %v", public.h)
		}
		r := e.p.visit(visit{target: "/internal/old", header: html})
		r.expect(t, 401, "This page is private", "")
		r.noRules(t, "protected path")
		_, scoped := e.p.link("docs", access.NewLink{PathPrefix: "/internal/"})
		reached(t, "protected path with a link", e.p.visit(visit{target: "/internal/old", header: bearer(scoped)}), 301, "/internal/")
	})

	t.Run("one-time links: GET shows the interstitial, never the rules", func(t *testing.T) {
		e := setup(t)
		_, once := e.p.link("docs", access.NewLink{MaxUses: 1})
		r := e.p.visit(visit{target: withT("/old", once), header: html})
		if r.code != 200 || !strings.Contains(r.body, "Click to view") {
			t.Fatalf("interstitial: %d %q", r.code, r.body)
		}
		r.noRules(t, "interstitial")
		_, after, _ := strings.Cut(r.body, `name="ticket" value="`)
		ticket, _, _ := strings.Cut(after, `"`)
		form := url.Values{"ticket": {ticket}}
		post := e.p.visit(visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: form})
		if post.code != 303 || post.sessionCookie() == "" {
			t.Fatalf("view POST: %d %v", post.code, post.h)
		}
		post.noRules(t, "view POST")
		again := e.p.visit(visit{method: http.MethodPost, target: "/.wardlock/view", header: sameOrigin, form: form})
		if again.code != 410 {
			t.Fatalf("second view POST: %d", again.code)
		}
		again.noRules(t, "second view POST")
		reached(t, "after the view", e.p.visit(visit{target: "/old", cookie: post.sessionCookie()}), 302, "/internal/")
	})

	t.Run("nothing applies under /.wardlock/ or to Wardlock's pages", func(t *testing.T) {
		e := setup(t)
		for name, v := range map[string]visit{
			"login page":     {target: "/.wardlock/login?next=/old"},
			"health":         {target: "/.wardlock/health"},
			"unknown":        {target: "/.wardlock/old"},
			"view GET":       {target: "/.wardlock/view"},
			"dot segments":   {target: "/x/../.wardlock/health"},
			"401 as a page":  {target: "/old", header: html},
			"401 to a fetch": {target: "/old"},
		} {
			e.p.visit(v).noRules(t, name)
		}
		// Rules cannot name the reserved path at all.
		for _, m := range []deploys.Manifest{
			{Redirects: &[]store.Redirect{{From: "/.wardlock/login", To: "/"}}},
			{Headers: &[]store.HeaderRule{{Path: "/.Wardlock/*", Set: map[string]string{"X-Frame-Options": "DENY"}}}},
		} {
			m.Files = []store.DeployFile{{Path: "index.html", SHA256: hashOf("x"), Size: 1}}
			if _, _, err := e.p.deploys.Create(t.Context(), store.ActorFor(e.p.owner.ID), "docs", m); err == nil {
				t.Fatalf("rules under /.wardlock/ accepted: %+v", m)
			}
		}
	})

	t.Run("the unavailable page and a withheld file carry no rules", func(t *testing.T) {
		e := setup(t)
		e.p.set("docs", "/", store.Public, false)
		css := hashOf(policyFiles["assets/site.css"])
		if err := e.p.quarantine.Load(t.Context(), func(context.Context) ([]string, error) { return []string{css}, nil }); err != nil {
			t.Fatal(err)
		}
		r := e.p.visit(visit{target: "/assets/site.css"})
		if r.code != http.StatusUnavailableForLegalReasons {
			t.Fatalf("withheld: %d", r.code)
		}
		r.noRules(t, "451")
		if err := e.p.db.Sites().TakeDown(t.Context(), e.p.siteID("docs"), e.p.now()); err != nil {
			t.Fatal(err)
		}
		e.p.notifier.Notify(t.Context(), e.p.siteID("docs"))
		r = e.p.visit(visit{target: "/old"})
		if r.code == 302 {
			t.Fatalf("taken-down site redirected: %v", r.h)
		}
		r.noRules(t, "unavailable")
	})

	t.Run("rate limits trigger and reset", func(t *testing.T) {
		e := setup(t)
		limits := access.DefaultLimits()
		for range limits.TokenFailuresPerIP.N {
			e.p.visit(visit{target: withT("/old", "junk"), ip: "198.51.100.9"}).noRules(t, "guess")
		}
		r := e.p.visit(visit{target: withT("/old", e.token), ip: "198.51.100.9"})
		if r.code != http.StatusTooManyRequests {
			t.Fatalf("over the limit: %d", r.code)
		}
		r.noRules(t, "rate limited")
		e.p.advance(limits.TokenFailuresPerIP.Per)
		reached(t, "after the window", e.p.visit(visit{target: "/old", header: bearer(e.token), ip: "198.51.100.9"}), 302, "/internal/")
		e.p.noRawTokens(t, e.p.accessRecords())
	})
}

// TestRulesFollowTheActiveDeploy checks that rules are the active deploy's:
// a path deploy keeps its base's unless it gives its own, an empty list
// clears them, and rollback restores them.
func TestRulesFollowTheActiveDeploy(t *testing.T) {
	p := newPolicyEdge(t)
	p.publishRules("docs")
	ctx := t.Context()
	deploy := func(m deploys.Manifest, files map[string]string) string {
		t.Helper()
		for path, body := range files {
			m.Files = append(m.Files, store.DeployFile{Path: path, SHA256: hashOf(body), Size: int64(len(body))})
		}
		d, _, err := p.deploys.Create(ctx, store.ActorFor(p.owner.ID), "docs", m)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m.Files {
			if err := p.deploys.Upload(ctx, store.ActorFor(p.owner.ID), "docs", d.ID, f.SHA256, strings.NewReader(files[f.Path])); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := p.deploys.Activate(ctx, store.ActorFor(p.owner.ID), "docs", d.ID); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	check := func(step string, code int, location string, owned bool) {
		t.Helper()
		r := p.visit(visit{target: "/old"})
		if r.code != code || r.h.Get("Location") != location || r.owned() != owned {
			t.Fatalf("%s: %d Location %q owner headers %v; want %d %q %v", step, r.code, r.h.Get("Location"), r.owned(), code, location, owned)
		}
	}
	check("first deploy", 302, "/internal/", true)

	deploy(deploys.Manifest{Path: "/mockup-x/"}, map[string]string{"index.html": "<p>Mockup</p>"})
	check("path deploy without rules keeps the base's", 302, "/internal/", true)
	if r := p.visit(visit{target: "/mockup-x/"}); r.code != 200 || !r.owned() {
		t.Fatalf("path deploy's page: %d %v", r.code, r.h)
	}

	withY := deploy(deploys.Manifest{Path: "/mockup-y/", Redirects: &[]store.Redirect{{From: "/old", To: "/mockup-y/", Status: 307}}}, map[string]string{"index.html": "<p>Y</p>"})
	check("path deploy with its own redirects", 307, "/mockup-y/", true)

	deploy(deploys.Manifest{Path: "/mockup-z/", Redirects: &[]store.Redirect{}, Headers: &[]store.HeaderRule{}}, map[string]string{"index.html": "<p>Z</p>"})
	check("path deploy clearing them", 404, "", false)

	full := map[string]string{"index.html": "<p>Plain</p>"}
	deploy(deploys.Manifest{}, full)
	check("full deploy without rules", 404, "", false)

	for _, step := range []struct {
		to       string
		code     int
		location string
		owned    bool
	}{{"", 404, "", false}, {withY, 307, "/mockup-y/", true}} {
		if _, _, err := p.deploys.Rollback(ctx, store.ActorFor(p.owner.ID), "docs", step.to); err != nil {
			t.Fatal(err)
		}
		check("rollback to "+step.to, step.code, step.location, step.owned)
	}
}
