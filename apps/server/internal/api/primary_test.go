// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/owners"
)

func (hs *harness) site(owner, method, path string, body any, status int) api.Site {
	hs.t.Helper()
	res := hs.do(owner, method, path, body)
	res.want(hs.t, status, "")
	var s api.Site
	res.decode(hs.t, &s)
	return s
}

// activeDomain adds host to site with its CNAME in place, so it is active.
func (hs *harness) activeDomain(owner, site, host string) {
	hs.t.Helper()
	target := site + "." + baseDomain
	hs.dns.Addr(target)
	hs.dns.CNAME(host, target)
	if d := hs.domain(owner, http.MethodPost, "/sites/"+site+"/domains", map[string]any{"hostname": host}, http.StatusCreated); d.Status != api.DomainStatusActive {
		hs.t.Fatalf("%s is %s, want active", host, d.Status)
	}
}

func TestPrimaryHost(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	hs.activeDomain(hs.ownerA, "docs", "www.example.com")
	hs.domain(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]any{"hostname": "pending.example.com"}, http.StatusCreated)

	if s := hs.site(hs.ownerA, http.MethodGet, "/sites/docs", nil, http.StatusOK); s.PrimaryHost != nil {
		t.Fatalf("a new site has primaryHost %q", *s.PrimaryHost)
	}

	// The base-domain hostname, in any spelling.
	notified := len(hs.notifiedSites())
	s := hs.site(hs.ownerA, http.MethodPut, "/sites/docs/primary-host", map[string]any{"host": "DOCS." + baseDomain + "."}, http.StatusOK)
	if s.PrimaryHost == nil || *s.PrimaryHost != "docs."+baseDomain || s.Name != "docs" || s.TagIds == nil {
		t.Fatalf("set base primary = %+v", s)
	}
	if len(hs.notifiedSites()) == notified {
		t.Fatal("the change was not announced to the edge")
	}

	// An active custom domain; the domain says it is the primary.
	s = hs.site(hs.ownerA, http.MethodPut, "/sites/docs/primary-host", map[string]any{"host": "www.example.com"}, http.StatusOK)
	if s.PrimaryHost == nil || *s.PrimaryHost != "www.example.com" {
		t.Fatalf("set custom primary = %+v", s)
	}
	if got := hs.site(hs.ownerA, http.MethodGet, "/sites/docs", nil, http.StatusOK); got.PrimaryHost == nil || *got.PrimaryHost != "www.example.com" {
		t.Fatalf("getSite = %+v", got)
	}
	if d := hs.domain(hs.ownerA, http.MethodGet, "/sites/docs/domains/www.example.com", nil, http.StatusOK); d.Primary == nil || !*d.Primary {
		t.Fatalf("the primary domain = %+v, want primary", d)
	}
	var page api.DomainPage
	hs.do(hs.ownerA, http.MethodGet, "/sites/docs/domains", nil).decode(t, &page)
	for _, d := range page.Items {
		if (d.Primary != nil) != (d.Hostname == "www.example.com") {
			t.Fatalf("listed %s with primary %v", d.Hostname, d.Primary)
		}
	}

	// A pending domain is refused with its own code; nothing changes.
	res := hs.do(hs.ownerA, http.MethodPut, "/sites/docs/primary-host", map[string]any{"host": "pending.example.com"})
	res.want(t, http.StatusConflict, "domain_not_active")
	if e := res.err(t); e.Target == nil || *e.Target != "host" {
		t.Fatalf("domain_not_active = %+v", e)
	}
	hs.do(hs.ownerA, http.MethodPut, "/sites/docs/primary-host", map[string]any{}).want(t, http.StatusBadRequest, "")
	if got := hs.site(hs.ownerA, http.MethodGet, "/sites/docs", nil, http.StatusOK); *got.PrimaryHost != "www.example.com" {
		t.Fatalf("after refusals primaryHost = %q", *got.PrimaryHost)
	}

	// Removing the primary custom domain clears it: primaryHost is absent.
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/domains/www.example.com", nil).want(t, http.StatusNoContent, "")
	if got := hs.site(hs.ownerA, http.MethodGet, "/sites/docs", nil, http.StatusOK); got.PrimaryHost != nil {
		t.Fatalf("after removing the domain primaryHost = %q", *got.PrimaryHost)
	}

	// Cleared, twice.
	hs.site(hs.ownerA, http.MethodPut, "/sites/docs/primary-host", map[string]any{"host": "docs." + baseDomain}, http.StatusOK)
	for range 2 {
		if s := hs.site(hs.ownerA, http.MethodDelete, "/sites/docs/primary-host", nil, http.StatusOK); s.PrimaryHost != nil {
			t.Fatalf("cleared = %+v", s)
		}
	}
}

// TestPrimaryHostIsolation: a hostname that is not one of the site's,
// another owner's included, answers exactly like a missing site, and owner
// B cannot change owner A's primary.
func TestPrimaryHostIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	hs.createSite(hs.ownerA, "blog")
	hs.createSite(hs.ownerB, "mine")
	hs.activeDomain(hs.ownerA, "docs", "www.example.com")
	hs.activeDomain(hs.ownerB, "mine", "mine.example.org")
	hs.advance(time.Minute)

	missing := hs.do(hs.ownerA, http.MethodPut, "/sites/nosuch/primary-host", map[string]any{"host": "nosuch." + baseDomain})
	missing.want(t, http.StatusNotFound, "not_found")
	for _, c := range []struct{ owner, path, host string }{
		{hs.ownerA, "/sites/docs/primary-host", "mine.example.org"},     // another owner's domain
		{hs.ownerA, "/sites/docs/primary-host", "mine." + baseDomain},   // another owner's site
		{hs.ownerA, "/sites/docs/primary-host", "blog." + baseDomain},   // the owner's other site
		{hs.ownerA, "/sites/docs/primary-host", "unknown.example.net"},  // no one's
		{hs.ownerA, "/sites/docs/primary-host", "https://www.example/"}, // not a hostname
		{hs.ownerB, "/sites/docs/primary-host", "docs." + baseDomain},   // owner A's site
		{hs.ownerB, "/sites/docs/primary-host", "www.example.com"},
		{hs.ownerB, "/sites/mine/primary-host", "www.example.com"}, // owner A's domain
	} {
		res := hs.do(c.owner, http.MethodPut, c.path, map[string]any{"host": c.host})
		if res.status != http.StatusNotFound || !bytes.Equal(res.body, missing.body) {
			t.Errorf("PUT %s %s = %d %s; want the same 404 as a missing site", c.path, c.host, res.status, res.body)
		}
	}
	hs.site(hs.ownerA, http.MethodPut, "/sites/docs/primary-host", map[string]any{"host": "www.example.com"}, http.StatusOK)
	res := hs.do(hs.ownerB, http.MethodDelete, "/sites/docs/primary-host", nil)
	if res.status != http.StatusNotFound || !bytes.Equal(res.body, missing.body) {
		t.Errorf("owner B DELETE = %d %s; want the same 404 as a missing site", res.status, res.body)
	}
	if got := hs.site(hs.ownerA, http.MethodGet, "/sites/docs", nil, http.StatusOK); got.PrimaryHost == nil || *got.PrimaryHost != "www.example.com" {
		t.Fatalf("owner A's primary = %+v", got)
	}
}

// TestPrimaryHostNeedsManage: choosing the primary needs the manage scope.
func TestPrimaryHostNeedsManage(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	deploy := createToken(t, h, session, "CI deploys", api.ScopeDeploy).Token
	manage := createToken(t, h, session, "Admin", api.ScopeManage).Token
	wantStatus(t, h.bearer(deploy, req{method: http.MethodPost, path: "/api/v0/sites", body: api.CreateSiteRequest{Name: "docs"}}), http.StatusCreated)
	body := api.SetPrimaryHostRequest{Host: "docs.sites.example.test"} // the token harness's base domain
	wantError(t, h.bearer(deploy, req{method: http.MethodPut, path: "/api/v0/sites/docs/primary-host", body: body}), http.StatusForbidden, "forbidden")
	wantError(t, h.bearer(deploy, req{method: http.MethodDelete, path: "/api/v0/sites/docs/primary-host"}), http.StatusForbidden, "forbidden")
	wantStatus(t, h.bearer(manage, req{method: http.MethodPut, path: "/api/v0/sites/docs/primary-host", body: body}), http.StatusOK)
	wantStatus(t, h.bearer(manage, req{method: http.MethodDelete, path: "/api/v0/sites/docs/primary-host"}), http.StatusOK)
}
