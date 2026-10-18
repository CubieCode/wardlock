// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/sites"
)

func (hs *harness) domain(owner, method, path string, body any, status int) api.Domain {
	hs.t.Helper()
	res := hs.do(owner, method, path, body)
	res.want(hs.t, status, "")
	var d api.Domain
	res.decode(hs.t, &d)
	return d
}

func TestDomains(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	target := "docs." + baseDomain

	// Added, checked at once, and answered with the records to create.
	d := hs.domain(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]any{"hostname": "Docs.Example.COM."}, http.StatusCreated)
	if d.Hostname != "docs.example.com" || d.Status != api.DomainStatusPending || d.LastCheckedAt == nil || d.NextCheckAt == nil ||
		*d.NextCheckAt != *d.LastCheckedAt+time.Minute.Milliseconds() || d.FailingSince == nil || d.VerifiedAt != nil {
		t.Fatalf("added %+v", d)
	}
	if *d.FailureReason != api.DomainFailureReasonNoRecord || !strings.Contains(*d.FailureDetail, "No CNAME or TXT record") {
		t.Fatalf("failure %v %v", *d.FailureReason, *d.FailureDetail)
	}
	if d.RoutingRecord != (api.DnsRecord{Type: api.DnsRecordTypeCNAME, Name: "docs.example.com", Value: target}) {
		t.Fatalf("routing record %+v", d.RoutingRecord)
	}
	vr := d.VerificationRecord
	if vr.Type != api.DnsRecordTypeTXT || vr.Name != "_wardlock.docs.example.com" || !strings.HasPrefix(vr.Value, sites.TXTPrefix) || len(vr.Value) < len(sites.TXTPrefix)+16 {
		t.Fatalf("verification record %+v", vr)
	}

	// Checking again at once is refused with Retry-After.
	res := hs.do(hs.ownerA, http.MethodPost, "/sites/docs/domains/docs.example.com/verify", nil)
	res.want(t, http.StatusTooManyRequests, "rate_limited")
	if ra := res.header.Get("Retry-After"); ra == "" || ra > "10" {
		t.Fatalf("Retry-After %q", ra)
	}

	// The owner publishes the CNAME and checks again.
	hs.dns.Addr(target)
	hs.dns.CNAME("docs.example.com", target)
	hs.advance(time.Minute)
	v := hs.domain(hs.ownerA, http.MethodPost, "/sites/docs/domains/docs.example.com/verify", nil, http.StatusOK)
	if v.Status != api.DomainStatusActive || v.VerifiedAt == nil || v.FailureReason != nil || v.FailureDetail != nil || v.FailingSince != nil ||
		*v.NextCheckAt != *v.LastCheckedAt+(24*time.Hour).Milliseconds() || v.VerificationRecord != d.VerificationRecord {
		t.Fatalf("verified %+v", v)
	}
	if got := hs.domain(hs.ownerA, http.MethodGet, "/sites/docs/domains/docs.example.com", nil, http.StatusOK); !reflect.DeepEqual(got, v) {
		t.Fatalf("get %+v, want %+v", got, v)
	}

	// Listing by hostname, in pages.
	for _, h := range []string{"b.example.com", "a.example.com"} {
		hs.domain(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]any{"hostname": h}, http.StatusCreated)
	}
	var page api.DomainPage
	hs.do(hs.ownerA, http.MethodGet, "/sites/docs/domains?limit=2", nil).decode(t, &page)
	if len(page.Items) != 2 || page.Items[0].Hostname != "a.example.com" || page.Items[1].Hostname != "b.example.com" || page.NextCursor == nil {
		t.Fatalf("first page %+v", page)
	}
	var second api.DomainPage
	hs.do(hs.ownerA, http.MethodGet, "/sites/docs/domains?limit=2&cursor="+*page.NextCursor, nil).decode(t, &second)
	if len(second.Items) != 1 || !reflect.DeepEqual(second.Items[0], v) || second.NextCursor != nil {
		t.Fatalf("second page %+v", second)
	}
	hs.do(hs.ownerA, http.MethodGet, "/sites/docs/domains?cursor=bm90IGEgaG9zdA", nil).want(t, http.StatusBadRequest, "invalid_request")

	// Refused hostnames name the problem; the body never echoes anything
	// but the owner's own input.
	for _, h := range []string{"192.0.2.1", "*.example.com", target, "auth." + baseDomain, "console." + baseDomain, "localhost"} {
		res := hs.do(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]any{"hostname": h})
		res.want(t, http.StatusBadRequest, "invalid_hostname")
		if e := res.err(t); e.Target == nil || *e.Target != "hostname" || e.Message == "" {
			t.Fatalf("%s: %+v", h, e)
		}
	}
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]any{}).want(t, http.StatusBadRequest, "")

	// Removed: gone, and gone again is 404.
	notified := len(hs.notifiedSites())
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/domains/a.example.com", nil).want(t, http.StatusNoContent, "")
	if len(hs.notifiedSites()) == notified {
		t.Fatal("removal was not announced")
	}
	hs.do(hs.ownerA, http.MethodGet, "/sites/docs/domains/a.example.com", nil).want(t, http.StatusNotFound, "not_found")
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/domains/a.example.com", nil).want(t, http.StatusNotFound, "not_found")
}

func (hs *harness) notifiedSites() []string {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return slices.Clone(hs.notified)
}

// TestDomainIsolation: owner B gets the same 404 for owner A's domains as
// for ones that do not exist, and domain_taken says nothing about the
// holder.
func TestDomainIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	hs.createSite(hs.ownerB, "mine")
	hs.domain(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]any{"hostname": "www.example.com"}, http.StatusCreated)
	hs.advance(time.Minute)

	missing := hs.do(hs.ownerB, http.MethodGet, "/sites/mine/domains/nobody.example.com", nil)
	missing.want(t, http.StatusNotFound, "not_found")
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/sites/docs/domains"},
		{http.MethodGet, "/sites/docs/domains/www.example.com"},
		{http.MethodPost, "/sites/docs/domains/www.example.com/verify"},
		{http.MethodDelete, "/sites/docs/domains/www.example.com"},
		{http.MethodPost, "/sites/docs/domains"},
		{http.MethodGet, "/sites/mine/domains/www.example.com"},
		{http.MethodPost, "/sites/mine/domains/www.example.com/verify"},
		{http.MethodDelete, "/sites/mine/domains/www.example.com"},
	} {
		res := hs.do(hs.ownerB, req.method, req.path, map[string]any{"hostname": "x.example.com"})
		if res.status != http.StatusNotFound || !bytes.Equal(res.body, missing.body) {
			t.Errorf("owner B %s %s = %d %s; want the same 404 as a missing domain", req.method, req.path, res.status, res.body)
		}
	}
	var page api.DomainPage
	hs.do(hs.ownerB, http.MethodGet, "/sites/mine/domains", nil).decode(t, &page)
	if len(page.Items) != 0 {
		t.Fatalf("owner B lists %+v", page.Items)
	}

	// Taken, by another owner or by the same owner's other site: the same
	// answer, naming nobody.
	hs.createSite(hs.ownerA, "blog")
	taken := hs.do(hs.ownerB, http.MethodPost, "/sites/mine/domains", map[string]any{"hostname": "WWW.example.com"})
	taken.want(t, http.StatusConflict, "domain_taken")
	own := hs.do(hs.ownerA, http.MethodPost, "/sites/blog/domains", map[string]any{"hostname": "www.example.com"})
	if own.status != http.StatusConflict || !bytes.Equal(own.body, taken.body) {
		t.Fatalf("same-owner conflict %d %s, another owner's %s", own.status, own.body, taken.body)
	}
	for _, leak := range []string{"docs", hs.ownerA} {
		if strings.Contains(string(taken.body), leak) {
			t.Fatalf("domain_taken body %s names %q", taken.body, leak)
		}
	}
	// Owner A's domain is untouched.
	if d := hs.domain(hs.ownerA, http.MethodGet, "/sites/docs/domains/www.example.com", nil, http.StatusOK); d.Hostname != "www.example.com" {
		t.Fatalf("A's domain %+v", d)
	}
}
