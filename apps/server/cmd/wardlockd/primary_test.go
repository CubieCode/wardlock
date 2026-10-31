// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// Primary hostname (C1.18) through the real store, sites service and edge.

const (
	custom      = "www.example.com"
	pendingHost = "pending.example.com"
	lapsedHost  = "lapsed.example.com"
)

// addDomain attaches hostname to site with status, straight in the store.
func (p *policyEdge) addDomain(site, hostname string, status store.DomainStatus) store.Domain {
	p.t.Helper()
	d := store.Domain{
		ID: store.NewID(), SiteID: p.siteID(site), Hostname: hostname, Status: status,
		VerificationToken: "verify-" + store.NewID(), CreatedAt: p.now(),
	}
	if status != store.DomainPending {
		d.LastCheckedAt, d.VerifiedAt = p.now(), p.now()
	}
	if err := p.db.Domains().Create(p.t.Context(), store.ActorFor(p.owner.ID), d); err != nil {
		p.t.Fatal(err)
	}
	return d
}

// recheck records a check moving d to status and tells the edge, as the
// sites service does on a status change.
func (p *policyEdge) recheck(d store.Domain, status store.DomainStatus) store.Domain {
	p.t.Helper()
	p.advance(time.Minute)
	next := d
	next.Status, next.LastCheckedAt = status, p.now()
	if status == store.DomainActive {
		next.VerifiedAt, next.FailingSince, next.LastFailureReason = p.now(), time.Time{}, ""
	} else {
		next.FailingSince, next.LastFailureReason = p.now(), store.DomainNoRecord
	}
	if err := p.db.Domains().RecordCheck(p.t.Context(), d, next); err != nil {
		p.t.Fatal(err)
	}
	p.notifier.Notify(p.t.Context(), d.SiteID)
	return next
}

// setPrimary makes primary the site's primary hostname through the sites
// service ("" clears it).
func (p *policyEdge) setPrimary(site, primary string) {
	p.t.Helper()
	svc := p.operatorSites(nil)
	var err error
	if primary == "" {
		_, err = svc.ClearPrimaryHost(p.t.Context(), store.ActorFor(p.owner.ID), site)
	} else {
		_, err = svc.SetPrimaryHost(p.t.Context(), store.ActorFor(p.owner.ID), site, primary)
	}
	if err != nil {
		p.t.Fatalf("primary %q: %v", primary, err)
	}
}

// toPrimary fails t unless r is the bare 308 to location: no body, never
// stored, no cookie set or cleared, and nothing else said about the site.
func (r result) toPrimary(t *testing.T, location string) {
	t.Helper()
	if r.code != http.StatusPermanentRedirect || r.h.Get("Location") != location || r.body != "" {
		t.Fatalf("%d Location %q body %q, want a bare 308 to %q", r.code, r.h.Get("Location"), r.body, location)
	}
	want := []string{"Cache-Control", "Location", "X-Content-Type-Options"}
	if got := slices.Sorted(maps.Keys(r.h)); !slices.Equal(got, want) || r.h.Get("Cache-Control") != "no-store" {
		t.Fatalf("308 headers %v, want only %v with no-store", r.h, want)
	}
}

// TestPrimaryHostThroughTheEdge: with a primary, every other served
// hostname answers 308 to the same path and query there, for every method;
// /.wardlock/ never redirects; pending and lapsed hostnames are never
// served or redirected to; and the redirect follows every change of the
// primary, its domain's status and the site on the next request.
func TestPrimaryHostThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	active := p.addDomain("docs", custom, store.DomainActive)
	p.addDomain("docs", pendingHost, store.DomainPending)
	p.addDomain("docs", lapsedHost, store.DomainLapsed)
	unknown := p.visit(visit{host: "unknown.example.org", target: "/"})
	notServed := func(h string) {
		t.Helper()
		for _, target := range []string{"/", "/.wardlock/health", "/missing?x=1"} {
			r := p.visit(visit{host: h, target: target})
			if r.code != unknown.code || r.body != unknown.body || !maps.EqualFunc(r.h, unknown.h, slices.Equal) {
				t.Fatalf("%s%s = %d %v %q; want exactly the unknown-host answer", h, target, r.code, r.h, r.body)
			}
		}
	}
	serves := func(h string) {
		t.Helper()
		p.visit(visit{host: h, target: "/", header: html}).expect(t, 200, "Home page", "")
	}

	// Off by default: every served hostname serves.
	serves(host)
	serves(custom)
	notServed(pendingHost)
	notServed(lapsedHost)

	methods := []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	// The path and query exactly as received.
	targets := []string{"/", "/missing", "/internal/", "/a%20b/c%2Fd.html", "/x/../y", "//twice//over", "/?q=1&r=%2F&q=2", "/assets/site.css?v=3"}
	// Every redirect: a bare 308, and no access record (it comes before
	// the access decision).
	redirects := func(from, to string) {
		t.Helper()
		p.accessRecords() // drains what came before
		defer func() {
			t.Helper()
			if got := p.accessRecords(); len(got) != 0 {
				t.Fatalf("redirects reached the access log: %+v", got)
			}
		}()
		for _, m := range methods {
			for _, target := range targets {
				p.visit(visit{method: m, host: from, target: target, header: html}).toPrimary(t, "http://"+to+target)
			}
		}
		// OPTIONS * has no path of its own.
		r := p.visit(visit{method: http.MethodOptions, host: from, target: "*"})
		r.toPrimary(t, "http://"+to+"/")
	}
	reservedServed := func(h string) {
		t.Helper()
		for _, target := range []string{"/.wardlock/health", "/x/../.wardlock/health"} {
			p.visit(visit{host: h, target: target}).expect(t, 200, `"ok"`, "")
		}
		// Reserved in any letter case: Wardlock's 404, never a redirect.
		if r := p.visit(visit{host: h, target: "/.WARDLOCK/health"}); r.code != 404 || r.h.Get("Location") != "" {
			t.Fatalf("%s/.WARDLOCK/health = %d to %q", h, r.code, r.h.Get("Location"))
		}
		p.visit(visit{host: h, target: "/.wardlock/login", header: html}).expect(t, 200, "", "")
		p.visit(visit{host: h, target: "/.wardlock/view"}).expect(t, 405, "", "")
	}

	// The active custom domain as primary.
	p.setPrimary("docs", custom)
	redirects(host, custom)
	reservedServed(host)
	serves(custom)
	reservedServed(custom)
	notServed(pendingHost)
	notServed(lapsedHost)
	serves(otherHost) // another site is untouched

	// The base-domain hostname as primary.
	p.setPrimary("docs", host)
	redirects(custom, host)
	reservedServed(custom)
	serves(host)

	// A lapsed primary falls back to no primary; it resumes on recovery.
	p.setPrimary("docs", custom)
	lapsed := p.recheck(active, store.DomainLapsed)
	serves(host)
	notServed(custom)
	recovered := p.recheck(lapsed, store.DomainActive)
	redirects(host, custom)
	serves(custom)

	// Removed: no primary, for good.
	if err := p.operatorSites(nil).RemoveDomain(t.Context(), store.ActorFor(p.owner.ID), "docs", recovered.Hostname); err != nil {
		t.Fatal(err)
	}
	serves(host)
	notServed(custom)
	p.addDomain("docs", custom, store.DomainActive)
	serves(host)
	serves(custom)

	// Cleared: every hostname serves.
	p.setPrimary("docs", custom)
	redirects(host, custom)
	p.setPrimary("docs", "")
	serves(host)
	serves(custom)
}

// TestPrimaryHostOnProtectedSites: the redirect comes before the access
// decision and reveals nothing beyond the hostname (the same bare 308 as
// a public site's, whatever credentials the request carries); a share-link
// token in the query goes along and is checked again on the primary, where
// the access rules apply unchanged.
func TestPrimaryHostOnProtectedSites(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.publish("other")
	p.addDomain("docs", custom, store.DomainActive)
	p.addDomain("other", "www.other.example", store.DomainActive)
	p.set("docs", "/", store.Protected, true)
	p.setPrimary("docs", custom)
	p.setPrimary("other", "www.other.example") // public
	l, token := p.link("docs", access.NewLink{})
	_, once := p.link("docs", access.NewLink{MaxUses: 1})
	navigate := http.Header{"Accept": {"text/html"}, "Sec-Fetch-Mode": {"navigate"}}

	// The same answer as a public site's, with or without credentials.
	public := p.visit(visit{host: otherHost, target: "/internal/?x=1", header: html})
	public.toPrimary(t, "http://www.other.example/internal/?x=1")
	for name, v := range map[string]visit{
		"browser":    {target: "/internal/?x=1", header: html},
		"fetcher":    {target: "/internal/?x=1"},
		"bad cookie": {target: "/internal/?x=1", cookie: "djE.AAAA"},
		"bearer":     {target: "/internal/?x=1", header: bearer(token)},
		"bad bearer": {target: "/internal/?x=1", header: bearer("wl_aaaaaaaaaa_" + strings.Repeat("B", 43))},
		"POST login": {method: http.MethodPost, target: "/internal/?x=1", header: sameOrigin, form: url.Values{"password": {"x"}}},
	} {
		r := p.visit(v)
		r.toPrimary(t, "http://"+custom+"/internal/?x=1")
		if !maps.EqualFunc(r.h, public.h, func(a, b []string) bool { return len(a) == len(b) }) {
			t.Fatalf("%s: headers %v differ from a public site's %v", name, r.h, public.h)
		}
	}
	if recs := p.accessRecords(); len(recs) != 0 {
		t.Fatalf("redirects reached the access log: %+v", recs)
	}

	// A share link to the base-domain hostname is followed to the primary
	// with its token, where it is checked again and earns its session.
	r := p.visit(visit{target: withT("/", token) + "&keep=1", header: navigate})
	r.toPrimary(t, "http://"+custom+withT("/", token)+"&keep=1")
	loc, err := url.Parse(r.h.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	r = p.visit(visit{host: loc.Host, target: loc.RequestURI(), header: navigate})
	r.expect(t, 200, "Home page", "")
	if r.sessionCookie() == "" || !strings.Contains(r.body, stripMarker) {
		t.Fatalf("followed share link: cookie %q, stripped %v", r.sessionCookie(), strings.Contains(r.body, stripMarker))
	}
	recs := p.accessRecords()
	if len(recs) != 1 || recs[0].Host != custom || recs[0].Via != access.ViaLink || recs[0].LinkPublicID != l.PublicID {
		t.Fatalf("access records %+v, want one on the primary via %s", recs, l.PublicID)
	}

	// Without the token the primary refuses as before; Bearer works there.
	p.visit(visit{host: custom, target: "/", header: html}).expect(t, 401, "This page is private", "Home page")
	p.visit(visit{host: custom, target: "/", header: bearer(token)}).expect(t, 200, "Home page", "")

	// A use-limited link is not spent by the redirect, nor by the GET on
	// the primary; only the interstitial's POST spends it.
	r = p.visit(visit{target: withT("/", once), header: html})
	r.toPrimary(t, "http://"+custom+withT("/", once))
	view := p.visit(visit{host: custom, target: withT("/", once), header: html})
	view.expect(t, 200, "Click to view", "Home page")
	got, err := p.db.Links().GetByPublicID(t.Context(), once[3:13])
	if err != nil || got.UseCount != 0 {
		t.Fatalf("use count %d, %v after the redirect and the GET; want 0", got.UseCount, err)
	}

	// Token guesses on the redirecting host never reach the limiter: the
	// redirect does not look at them.
	limit := access.DefaultLimits().TokenFailuresPerIP.N
	for range limit + 1 {
		p.visit(visit{target: withT("/", "junk"), ip: "198.51.100.40"}).toPrimary(t, "http://"+custom+withT("/", "junk"))
	}
	p.visit(visit{host: custom, target: withT("/", token), ip: "198.51.100.40"}).expect(t, 200, "Home page", "")
	p.noRawTokens(t, p.accessRecords())
}

// TestPrimaryHostUnavailableAndUnpublished: a taken-down site answers its
// unavailable page on every hostname and an unpublished one the unknown-
// host answer; neither ever redirects.
func TestPrimaryHostUnavailableAndUnpublished(t *testing.T) {
	p := newPolicyEdge(t)
	p.publish("docs")
	p.addDomain("docs", custom, store.DomainActive)
	p.setPrimary("docs", custom)
	if err := p.db.Sites().TakeDown(t.Context(), p.siteID("docs"), p.now()); err != nil {
		t.Fatal(err)
	}
	p.notifier.Notify(t.Context(), p.siteID("docs"))
	for _, h := range []string{host, custom} {
		p.visit(visit{host: h, target: "/"}).unavailable(t)
		p.visit(visit{host: h, target: "/.wardlock/health"}).expect(t, 200, `"ok"`, "")
	}

	unknown := p.visit(visit{host: "unknown.example.org", target: "/"})
	empty := store.Site{ID: store.NewID(), OwnerID: p.owner.ID, Name: "empty", CreatedAt: p.now()}
	if err := p.db.Sites().Create(t.Context(), store.ActorFor(empty.OwnerID), empty, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	p.addDomain("empty", "empty.example.com", store.DomainActive)
	p.setPrimary("empty", "empty.example.com")
	for _, h := range []string{"empty.wardlock.localhost", "empty.example.com"} {
		if r := p.visit(visit{host: h, target: "/"}); r.code != unknown.code || r.body != unknown.body {
			t.Fatalf("%s = %d %q, want the unknown-host answer", h, r.code, r.body)
		}
	}
}
