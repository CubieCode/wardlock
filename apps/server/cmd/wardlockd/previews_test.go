// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/oauth/oauthtest"
	"wardlock.nz/server/internal/store"
)

// Preview deploys (C1.19) through the real store, deploys service and edge.
// The share-link matrices also run on a preview hostname (withHosts).

var previewFiles = map[string]string{
	"index.html":          "<p>Preview page</p>",
	"404.html":            "<p>Preview not found</p>",
	"internal/index.html": "<p>Preview plans</p>",
}

// preview deploys files to site's preview name, with lifetime days (nil
// for the default).
func (p *policyEdge) preview(site, name string, days *int, files map[string]string) store.Deploy {
	p.t.Helper()
	return p.deployFiles(site, deploys.Manifest{Preview: name, PreviewDays: days}, files)
}

// sameAs fails t unless every target on h gets exactly want's answer.
func (p *policyEdge) sameAs(t *testing.T, want result, h string) {
	t.Helper()
	for _, target := range []string{"/", "/.wardlock/health", "/missing?x=1"} {
		r := p.visit(visit{host: h, target: target})
		if r.code != want.code || r.body != want.body || !maps.EqualFunc(r.h, want.h, slices.Equal) {
			t.Fatalf("%s%s = %d %v %q; want exactly the unknown-host answer", h, target, r.code, r.h, r.body)
		}
	}
}

func noindexed(t *testing.T, what string, r result) {
	t.Helper()
	if got := r.h.Values("X-Robots-Tag"); len(got) != 1 || got[0] != "noindex" {
		t.Fatalf("%s: X-Robots-Tag %v, want noindex once", what, got)
	}
}

// TestPreviewHostResolution: an exact site name wins, then <site>--<name>
// for a preview that has not expired; anything else, and every custom
// domain, answers as before.
func TestPreviewHostResolution(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	p.preview("docs", "pr-1", nil, previewFiles)
	unknown := p.visit(visit{host: "unknown.example.org", target: "/"})

	live := p.visit(visit{host: host, target: "/"})
	live.expect(t, 200, "Home page", "")
	if live.h.Get("X-Robots-Tag") != "" {
		t.Fatalf("public live site sent X-Robots-Tag %q", live.h.Get("X-Robots-Tag"))
	}
	r := p.visit(visit{host: previewHost, target: "/"})
	r.expect(t, 200, "Preview page", "Home page")
	noindexed(t, "preview", r)

	for _, h := range []string{
		"docs--pr-2.wardlock.localhost",   // no such preview
		"other--pr-1.wardlock.localhost",  // another site's name
		"nosite--pr-1.wardlock.localhost", // no such site
		"docs---pr-1.wardlock.localhost",  // site "docs-"
		"docs--pr-1-.wardlock.localhost",  // not a preview name
		"docs--.wardlock.localhost",
		"pr-1.docs.wardlock.localhost", // nested
	} {
		p.sameAs(t, unknown, h)
	}

	// Sites named with "--" before the rule keep working, previews of them
	// too, and an exact site name always wins over a preview's label.
	p.publish("team")
	p.preview("team", "x", nil, previewFiles)
	p.visit(visit{host: "team--x.wardlock.localhost", target: "/"}).expect(t, 200, "Preview page", "")
	p.publish("team--x") // straight in the store, as an old site would be
	p.notifier.Notify(t.Context(), p.siteID("team"))
	p.visit(visit{host: "team--x.wardlock.localhost", target: "/"}).expect(t, 200, "Home page", "Preview page")
	p.preview("team--x", "pr-1", nil, previewFiles)
	p.visit(visit{host: "team--x--pr-1.wardlock.localhost", target: "/"}).expect(t, 200, "Preview page", "")

	// Custom domains never serve previews, even one named like a preview.
	p.addDomain("docs", custom, store.DomainActive)
	p.addDomain("docs", "docs--pr-1.example.com", store.DomainActive)
	for _, h := range []string{custom, "docs--pr-1.example.com"} {
		r := p.visit(visit{host: h, target: "/"})
		r.expect(t, 200, "Home page", "Preview page")
		if r.h.Get("X-Robots-Tag") != "" {
			t.Fatalf("%s sent X-Robots-Tag", h)
		}
	}

	// An expired preview is an unknown host at once, cached or not.
	one := 1
	p.preview("docs", "short", &one, previewFiles)
	p.visit(visit{host: "docs--short.wardlock.localhost", target: "/"}).expect(t, 200, "Preview page", "")
	p.advance(24*time.Hour - time.Millisecond)
	p.visit(visit{host: "docs--short.wardlock.localhost", target: "/"}).expect(t, 200, "Preview page", "")
	p.advance(time.Millisecond)
	p.sameAs(t, unknown, "docs--short.wardlock.localhost")

	// Deleting a preview stops it at once; so does tearing down its site.
	if err := p.deploys.DeletePreview(t.Context(), p.owner.ID, "docs", "pr-1"); err != nil {
		t.Fatal(err)
	}
	p.sameAs(t, unknown, previewHost)
	p.preview("team", "y", nil, previewFiles)
	p.visit(visit{host: "team--y.wardlock.localhost", target: "/"}).expect(t, 200, "Preview page", "")
	if err := p.operatorSites(nil).Delete(t.Context(), p.owner.ID, "team"); err != nil {
		t.Fatal(err)
	}
	p.sameAs(t, unknown, "team--y.wardlock.localhost")
	if _, ps, err := p.deploys.Previews(t.Context(), p.owner.ID, "team--x", "", 10); err != nil || len(ps) != 1 {
		t.Fatalf("the legacy site's previews after another teardown = %+v, %v", ps, err)
	}
}

// TestPreviewResponses: every preview response is noindex; the preview's
// own deploy decides SPA, redirects and headers; the primary hostname never
// redirects it; access records name it; and a preview serves even while
// nothing is live.
func TestPreviewResponses(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	p.preview("docs", "pr-1", nil, previewFiles)
	p.deployFiles("docs", deploys.Manifest{
		Preview: "spa", SPA: new(true),
		Redirects: &[]store.Redirect{{From: "/old", To: "/", Status: 302}},
		Headers:   &[]store.HeaderRule{{Path: "/*", Set: map[string]string{"Permissions-Policy": "camera=()"}}},
	}, previewFiles)
	spaHost := "docs--spa.wardlock.localhost"

	// The preview's own rules; the live deploy has none of them.
	p.visit(visit{host: spaHost, target: "/any/route", header: html}).expect(t, 200, "Preview page", "")
	if r := p.visit(visit{host: spaHost, target: "/old"}); r.code != 302 || r.h.Get("Location") != "/" || r.h.Get("Permissions-Policy") != "camera=()" {
		t.Fatalf("preview redirect: %d %v", r.code, r.h)
	}
	p.visit(visit{target: "/any/route", header: html}).expect(t, 404, "Custom not found", "")
	if r := p.visit(visit{target: "/old"}); r.code != 404 || r.h.Get("Permissions-Policy") != "" {
		t.Fatalf("live site applied the preview's rules: %d %v", r.code, r.h)
	}

	// The primary hostname redirects the live site's other hosts, never a
	// preview.
	p.primaryOnBase()
	p.visit(visit{host: custom, target: "/"}).toPrimary(t, "http://"+host+"/")

	p.set("docs", "/internal/", store.Protected, true)
	p.accessRecords()
	for _, v := range []visit{
		{target: "/"},
		{target: "/", method: http.MethodHead},
		{target: "/missing", header: html},
		{target: "/internal/", header: html}, // the login page
		{target: "/internal", header: html},  // the directory redirect
		{target: "/", method: http.MethodPost},
		{target: "/.wardlock/health"},
		{target: "/.wardlock/login?next=/internal/", header: html},
		{target: "/.wardlock/nothing"},
	} {
		v.host = previewHost
		r := p.visit(v)
		if r.code == http.StatusPermanentRedirect {
			t.Fatalf("%s %s redirected to the primary", v.method, v.target)
		}
		noindexed(t, v.method+" "+v.target, r)
	}
	for _, rec := range p.accessRecords() {
		if rec.Preview != "pr-1" || rec.Host != previewHost {
			t.Fatalf("access record %+v, want preview pr-1", rec)
		}
	}
	p.visit(visit{target: "/"}).expect(t, 200, "Home page", "")
	if recs := p.accessRecords(); len(recs) != 1 || recs[0].Preview != "" {
		t.Fatalf("live access records %+v, want no preview", recs)
	}

	// A site with nothing live serves its previews; its own host does not.
	empty := store.Site{ID: store.NewID(), OwnerID: p.owner.ID, Name: "fresh", CreatedAt: p.now()}
	if err := p.db.Sites().Create(t.Context(), empty, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	p.preview("fresh", "first", nil, previewFiles)
	p.visit(visit{host: "fresh--first.wardlock.localhost", target: "/"}).expect(t, 200, "Preview page", "")
	p.sameAs(t, p.visit(visit{host: "unknown.example.org", target: "/"}), "fresh.wardlock.localhost")

	// A taken-down site's previews answer the unavailable page.
	if err := p.db.Sites().TakeDown(t.Context(), p.siteID("docs"), p.now()); err != nil {
		t.Fatal(err)
	}
	p.notifier.Notify(t.Context(), p.siteID("docs"))
	p.visit(visit{host: previewHost, target: "/", header: html}).unavailable(t)
}

// TestPreviewAccess: previews follow the site's policies and share links:
// path policies apply, a site-wide link opens every preview, sessions are
// host-only and bound to the site, and another site's preview grants
// nothing.
func TestPreviewAccess(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	p.preview("docs", "pr-1", nil, previewFiles)
	p.preview("docs", "pr-2", nil, previewFiles)
	p.preview("other", "pr-1", nil, previewFiles)
	pr2 := "docs--pr-2.wardlock.localhost"
	otherPreview := "other--pr-1.wardlock.localhost"

	// Path policies, as on the live site.
	p.set("docs", "/internal/", store.Protected, true)
	p.visit(visit{host: previewHost, target: "/"}).expect(t, 200, "Preview page", "")
	r := p.visit(visit{host: previewHost, target: "/internal/", header: html})
	r.expect(t, 401, "This page is private", "Preview plans")
	r.protected(t, "no-store")

	p.set("docs", "/", store.Protected, true)
	p.set("other", "/", store.Protected, true)
	review, token := p.link("docs", access.NewLink{Label: "Review"})
	_, scoped := p.link("docs", access.NewLink{PathPrefix: "/internal/"})
	for _, h := range []string{previewHost, pr2, host} {
		p.visit(visit{host: h, target: "/", header: bearer(token)}).expect(t, 200, "", "This page is private")
		p.visit(visit{host: h, target: withT("/internal/", scoped)}).expect(t, 200, "plans", "")
		p.visit(visit{host: h, target: withT("/", scoped)}).expect(t, 401, "protected", "page")
	}
	// The link earns a host-only session on the preview host.
	r = p.visit(visit{host: previewHost, target: withT("/", token), header: html})
	r.expect(t, 200, "Preview page", "")
	if set := r.h.Get("Set-Cookie"); !strings.HasPrefix(set, access.SessionCookieName+"=") || strings.Contains(strings.ToLower(set), "domain=") {
		t.Fatalf("preview Set-Cookie %q, want a host-only site session", set)
	}
	noindexed(t, "share-link page", r)
	cookie := r.sessionCookie()
	p.visit(visit{host: previewHost, target: "/", cookie: cookie}).expect(t, 200, "Preview page", "")
	// The session is the site's, bound to the policy it was earned under:
	// /internal/ has its own.
	p.visit(visit{host: previewHost, target: "/internal/", cookie: cookie}).expect(t, 401, "protected", "Preview plans")
	// Another site's preview: the token and the cookie grant nothing.
	p.visit(visit{host: otherPreview, target: "/", header: bearer(token)}).expect(t, 401, "protected", "Preview page")
	if r := p.visit(visit{host: otherPreview, target: "/", cookie: cookie}); r.code != 401 || !r.cleared() {
		t.Fatalf("docs's cookie on other's preview: %d cleared=%v", r.code, r.cleared())
	}
	// The dashboard never takes a site cookie.
	hits := p.dashboardHits
	p.visit(visit{host: "wardlock.localhost", target: "/", cookie: cookie})
	if p.dashboardHits != hits+1 {
		t.Fatal("dashboard host not handled by the dashboard")
	}
	// Revoking the link ends the preview session too.
	if err := p.acc.RevokeLink(t.Context(), p.owner.ID, "docs", review.PublicID); err != nil {
		t.Fatal(err)
	}
	if r := p.visit(visit{host: previewHost, target: "/", cookie: cookie}); r.code != 401 || !r.cleared() {
		t.Fatalf("preview session after revoking its link: %d cleared=%v", r.code, r.cleared())
	}
	p.noRawTokens(t, p.accessRecords())
}

// TestOAuthOnAPreviewHost signs in through the auth origin from a preview
// host: the code comes back to the preview's own callback and the session
// is set there only.
func TestOAuthOnAPreviewHost(t *testing.T) {
	o := newOAuthEdge(t)
	site := o.publish("docs")
	o.publish("other")
	o.preview("docs", "pr-1", nil, previewFiles)
	pol := o.protect("docs", "/", store.Allowlist{Emails: []string{"visitor@example.test"}})
	o.protect("other", "/", store.Allowlist{Emails: []string{"visitor@example.test"}})
	o.github.Configure(func(opt *oauthtest.Options) { opt.Account = verifiedAccount("visitor@example.test") })
	b := o.browser("192.0.2.40")

	r := b.request(http.MethodGet, "http://"+previewHost+"/internal/", nil)
	r.expect(t, 401, "Continue with GitHub", "Preview plans")
	noindexed(t, "login page", r)
	hops := b.signIn(previewHost, "github", "/internal/")
	if len(hops) != 4 {
		t.Fatalf("%d hops: %+v", len(hops), hops)
	}
	if loc := hops[2].res.h.Get("Location"); !strings.HasPrefix(loc, "http://"+previewHost+"/.wardlock/callback?code=") {
		t.Fatalf("auth origin sends the visitor to %q, want the preview's callback", loc)
	}
	cb := last(hops)
	if cb.res.code != http.StatusSeeOther || cb.res.h.Get("Location") != "/internal/" {
		t.Fatalf("preview callback: %d %v", cb.res.code, cb.res.h)
	}
	noindexed(t, "callback", cb.res)
	session := b.cookie(previewHost, access.SessionCookieName)
	if session == "" || len(b.jar[host]) != 0 {
		t.Fatalf("jar after sign-in: preview %v, live %v", b.jar[previewHost], b.jar[host])
	}
	b.noCookiesOffSite(t)
	o.do(http.MethodGet, previewHost, "/internal/", session, nil).expect(t, 200, "Preview plans", "")
	sessions := o.sessions.take()
	if len(sessions) != 1 || sessions[0].SiteID != site.ID || sessions[0].PolicyID != pol.ID || sessions[0].Method != store.SessionOAuth {
		t.Fatalf("sessions %+v", sessions)
	}
	// A code made for the preview host is refused on the live host.
	_, cbURL := b.toCallback(previewHost, "github", "/")
	refused(t, b.request(http.MethodGet, strings.Replace(cbURL, previewHost, host, 1), nil))
	o.do(http.MethodGet, otherHost, "/", session, html).expect(t, 401, "Continue with", "Preview page")
	b.noSecretsLogged(t)
}
