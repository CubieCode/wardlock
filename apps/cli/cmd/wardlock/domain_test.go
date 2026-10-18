// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// handleDomains adds the custom-domain operations to the fake, as the real
// server answers them. "taken.example.com" belongs to someone else.
func (f *fakeAPI) handleDomains(handle func(pattern, scope string, h func(w http.ResponseWriter, r *http.Request))) {
	f.domains = map[string][]*apiclient.Domain{}
	find := func(s *fakeSite, host string) *apiclient.Domain {
		for _, d := range f.domains[s.site.Name] {
			if d.Hostname == strings.TrimSuffix(strings.ToLower(host), ".") {
				return d
			}
		}
		return nil
	}
	handle("GET /api/v0/sites/{site}/domains", "", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		items := []apiclient.Domain{}
		for _, d := range f.domains[s.site.Name] {
			items = append(items, *d)
		}
		page, next := paginate(r, items)
		reply(w, http.StatusOK, apiclient.DomainPage{Items: page, NextCursor: next})
	}))
	handle("POST /api/v0/sites/{site}/domains", "manage", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		var body apiclient.AddDomainRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusBadRequest, apiErr("invalid_request", "The request is malformed."))
			return
		}
		host := strings.TrimSuffix(strings.ToLower(body.Hostname), ".")
		switch {
		case !strings.Contains(host, ".") || strings.Contains(host, "*"):
			reply(w, http.StatusBadRequest, apiErr("invalid_hostname", "Wildcard domains are not supported; add each hostname separately."))
			return
		case host == "taken.example.com" || find(s, host) != nil:
			reply(w, http.StatusConflict, apiErr("domain_taken", "This hostname is already attached to a site on this server."))
			return
		}
		f.nextID++
		reason, detail := apiclient.DomainFailureReasonNoRecord, "No CNAME or TXT record found for "+host+"."
		d := &apiclient.Domain{
			Id: f.id(), Hostname: host, Status: apiclient.DomainStatusPending,
			RoutingRecord:      apiclient.DnsRecord{Type: apiclient.DnsRecordTypeCNAME, Name: host, Value: s.site.Hostname},
			VerificationRecord: apiclient.DnsRecord{Type: apiclient.DnsRecordTypeTXT, Name: "_wardlock." + host, Value: "wardlock-verify=FAKETOKENFAKETOKENFAKETOKE"},
			CreatedAt:          1_800_000_000_000, LastCheckedAt: ptr(int64(1_800_000_000_000)), NextCheckAt: ptr(int64(1_800_000_060_000)),
			FailingSince: ptr(int64(1_800_000_000_000)), FailureReason: &reason, FailureDetail: &detail,
		}
		f.domains[s.site.Name] = append(f.domains[s.site.Name], d)
		reply(w, http.StatusCreated, d)
	}))
	withDomain := func(h func(w http.ResponseWriter, s *fakeSite, d *apiclient.Domain)) func(w http.ResponseWriter, r *http.Request) {
		return f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
			if d := find(s, r.PathValue("hostname")); d != nil {
				h(w, s, d)
				return
			}
			reply(w, http.StatusNotFound, notFound)
		})
	}
	handle("GET /api/v0/sites/{site}/domains/{hostname}", "", withDomain(func(w http.ResponseWriter, _ *fakeSite, d *apiclient.Domain) {
		reply(w, http.StatusOK, d)
	}))
	handle("POST /api/v0/sites/{site}/domains/{hostname}/verify", "manage", withDomain(func(w http.ResponseWriter, _ *fakeSite, d *apiclient.Domain) {
		f.verifyCalls++
		if f.throttle {
			f.throttle = false
			w.Header().Set("Retry-After", "7")
			reply(w, http.StatusTooManyRequests, apiErr("rate_limited", "This domain was checked moments ago. Try again shortly."))
			return
		}
		if f.verifyPasses > 0 {
			f.verifyPasses--
			if f.verifyPasses == 0 {
				d.Status, d.FailureReason, d.FailureDetail, d.FailingSince = apiclient.DomainStatusActive, nil, nil, nil
				d.VerifiedAt = ptr(int64(1_800_000_100_000))
			}
		}
		reply(w, http.StatusOK, d)
	}))
	handle("DELETE /api/v0/sites/{site}/domains/{hostname}", "manage", withDomain(func(w http.ResponseWriter, s *fakeSite, d *apiclient.Domain) {
		ds := f.domains[s.site.Name]
		for i := range ds {
			if ds[i] == d {
				f.domains[s.site.Name] = append(ds[:i:i], ds[i+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestDomainAdd(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	a, out, errOut := ciApp(t, srv)

	if err := run(t.Context(), a, []string{"domain", "add", "docs." + fakeBase, "WWW.Example.com."}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{
		"Added www.example.com to docs.",
		"  CNAME  www.example.com  docs." + fakeBase + "\n",
		"  TXT  _wardlock.www.example.com  wardlock-verify=FAKETOKEN",
		"apex domain",
		"www.example.com is pending: No CNAME or TXT record found for www.example.com.",
		"wardlock domain verify docs www.example.com",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if srv.verifyCalls != 0 {
		t.Fatal("add without --wait checked again")
	}

	// --json: one object, the site and the domain.
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "domain", "add", "docs", "a.example.com"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "domain")
	var d apiclient.Domain
	if err := json.Unmarshal(m["domain"], &d); err != nil || d.Hostname != "a.example.com" || d.VerificationRecord.Name != "_wardlock.a.example.com" {
		t.Fatalf("domain %s (%v)", m["domain"], err)
	}

	for _, tt := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"domain", "add", "docs", "taken.example.com"}, "already attached to a site", 1},
		{[]string{"domain", "add", "docs", "*.example.com"}, "Wildcard domains are not supported", 1},
		{[]string{"domain", "add", "missing", "b.example.com"}, "no site missing", 1},
		{[]string{"domain", "add", "docs"}, "Usage: wardlock domain add", 2},
		{[]string{"domain", "add", "docs", "a b.example.com"}, "is not a hostname", 2},
		{[]string{"domain", "add", "docs", "c.example.com", "--wiat"}, "flag provided but not defined", 2},
		{[]string{"domain", "frob", "docs"}, `unknown domain command "frob"`, 2},
		{[]string{"domain"}, "Usage: wardlock domain <add|list|verify|remove>", 2},
	} {
		out.Reset()
		errOut.Reset()
		err := run(t.Context(), a, tt.args)
		if code := exitCode(errOut, err); code != tt.code || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%v: exit %d, stderr %q; want %d and %q", tt.args, code, errOut, tt.code, tt.want)
		}
		if out.Len() != 0 {
			t.Errorf("%v: stdout %q on failure", tt.args, out)
		}
	}
}

func TestDomainAddWait(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	a, out, errOut := ciApp(t, srv)
	start := a.now()

	// Passes on the third check, after a 429 that asks for 7 seconds
	// (shorter than the interval, so the interval wins).
	srv.verifyPasses = 3
	srv.throttle = true
	if err := run(t.Context(), a, []string{"--json", "domain", "add", "docs", "www.example.com", "--wait"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "domain")
	var d apiclient.Domain
	if err := json.Unmarshal(m["domain"], &d); err != nil || d.Status != apiclient.DomainStatusActive {
		t.Fatalf("domain %s (%v)", m["domain"], err)
	}
	if srv.verifyCalls != 4 || a.now().Sub(start) != 4*waitInterval {
		t.Fatalf("%d checks over %v", srv.verifyCalls, a.now().Sub(start))
	}
	if !strings.Contains(errOut.String(), "Waiting for www.example.com: No CNAME or TXT record found") {
		t.Fatalf("progress %q", errOut)
	}

	// Never active: gives up after 10 minutes, exit 1, stdout empty with
	// --json, and says the domain stays attached.
	out.Reset()
	errOut.Reset()
	srv.verifyPasses, srv.verifyCalls = 0, 0
	start = a.now()
	err := run(t.Context(), a, []string{"--json", "domain", "add", "docs", "slow.example.com", "--wait"})
	if code := exitCode(errOut, err); code != 1 || out.Len() != 0 {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if !strings.Contains(errOut.String(), "still pending after 10 minutes") || !strings.Contains(errOut.String(), "stays attached") {
		t.Fatalf("stderr %q", errOut)
	}
	if waited := a.now().Sub(start); waited > waitTimeout || waited < waitTimeout-waitInterval {
		t.Fatalf("waited %v", waited)
	}

	// Text output with --wait ends with the active status.
	out.Reset()
	srv.verifyPasses = 1
	if err := run(t.Context(), a, []string{"domain", "add", "docs", "fast.example.com", "--wait"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fast.example.com is active: it serves docs.") {
		t.Fatalf("stdout %q", out)
	}
}

func TestDomainListVerifyRemove(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	a, out, errOut := ciApp(t, srv)
	ctx := t.Context()

	if err := run(ctx, a, []string{"domain", "list", "docs"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "docs has no custom domains") {
		t.Fatalf("empty list %q", out)
	}
	for _, h := range []string{"a.example.com", "b.example.com", "c.example.com"} {
		if err := run(ctx, a, []string{"--json", "domain", "add", "docs", h}); err != nil {
			t.Fatal(err)
		}
	}

	// list pages through every domain.
	out.Reset()
	if err := run(ctx, a, []string{"domain", "list", "docs"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "HOSTNAME") || !strings.Contains(lines[3], "c.example.com") ||
		!strings.Contains(lines[1], "pending") || !strings.Contains(lines[1], "No CNAME or TXT record found") {
		t.Fatalf("list:\n%s", out)
	}
	out.Reset()
	if err := run(ctx, a, []string{"domain", "list", "docs", "--json"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "domains")
	var ds []apiclient.Domain
	if err := json.Unmarshal(m["domains"], &ds); err != nil || len(ds) != 3 {
		t.Fatalf("domains %s (%v)", m["domains"], err)
	}

	// verify prints the result, whatever it is.
	out.Reset()
	srv.verifyPasses = 1
	if err := run(ctx, a, []string{"domain", "verify", "docs", "B.example.com"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "b.example.com is active") {
		t.Fatalf("verify %q", out)
	}
	out.Reset()
	if err := run(ctx, a, []string{"domain", "verify", "docs", "c.example.com", "--json"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "site", "domain")
	out.Reset()
	errOut.Reset()
	srv.throttle = true
	err := run(ctx, a, []string{"domain", "verify", "docs", "c.example.com"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "checked moments ago: try again in 7s") {
		t.Fatalf("throttled: exit %d %q", code, errOut)
	}
	errOut.Reset()
	err = run(ctx, a, []string{"domain", "verify", "docs", "z.example.com"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "docs has no custom domain z.example.com") {
		t.Fatalf("unknown: exit %d %q", code, errOut)
	}

	// remove: --yes is needed without a terminal; a terminal asks.
	errOut.Reset()
	err = run(ctx, a, []string{"domain", "remove", "docs", "a.example.com"})
	if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), "--yes") {
		t.Fatalf("no --yes: exit %d %q", code, errOut)
	}
	a.canPrompt = true
	a.stdin = strings.NewReader("a.example.org\n")
	errOut.Reset()
	err = run(ctx, a, []string{"domain", "remove", "docs", "a.example.com"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "nothing was removed") || len(srv.domains["docs"]) != 3 {
		t.Fatalf("wrong answer: exit %d %q", code, errOut)
	}
	a.stdin = strings.NewReader("a.example.com\n")
	out.Reset()
	if err := run(ctx, a, []string{"domain", "remove", "docs", "A.Example.com."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed a.example.com from docs.") || !strings.Contains(out.String(), "Delete the DNS records for a.example.com") || len(srv.domains["docs"]) != 2 {
		t.Fatalf("remove %q", out)
	}
	a.canPrompt = false
	out.Reset()
	errOut.Reset()
	if err := run(ctx, a, []string{"--json", "domain", "remove", "docs", "b.example.com", "--yes"}); err != nil {
		t.Fatal(err)
	}
	m = assertJSONKeys(t, out.Bytes(), "removed", "site", "hostname")
	if string(m["removed"]) != "true" || string(m["hostname"]) != `"b.example.com"` || !strings.Contains(errOut.String(), "Delete the DNS records") {
		t.Fatalf("remove --json %s / %q", out, errOut)
	}
	errOut.Reset()
	err = run(ctx, a, []string{"domain", "remove", "docs", "b.example.com", "--yes"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "no custom domain b.example.com") {
		t.Fatalf("removed twice: exit %d %q", code, errOut)
	}
}

func TestDomainNeedsManageScope(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	srv.scopes = []string{"deploy"}
	a, _, errOut := ciApp(t, srv)
	err := run(t.Context(), a, []string{"domain", "add", "docs", "www.example.com"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "scope") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}
