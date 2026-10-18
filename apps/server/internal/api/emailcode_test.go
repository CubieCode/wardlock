// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"slices"
	"testing"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
)

// TestPolicyEmail covers the email method and the allowlist on setPolicy:
// set, normalised, kept, changed, cleared, listed and validated.
func TestPolicyEmail(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	put := func(body map[string]any) response {
		t.Helper()
		return hs.do(hs.ownerA, http.MethodPut, "/sites/docs/policies", body)
	}
	decode := func(res response) api.Policy {
		t.Helper()
		res.want(t, http.StatusOK, "")
		var p api.Policy
		res.decode(t, &p)
		return p
	}
	allow := func(emails, domains []string) map[string]any {
		return map[string]any{"emails": emails, "domains": domains}
	}

	// A new policy has an empty allowlist, never null.
	if def := hs.policies(hs.ownerA, "docs", "").Items[0]; def.Allowlist.Emails == nil || def.Allowlist.Domains == nil {
		t.Fatalf("default policy allowlist %+v", def.Allowlist)
	}
	p := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links", "email"},
		"allowlist": allow([]string{"Ana@Example.com", "ana@example.com"}, []string{"*.Example.com", "example.com."})}))
	if !slices.Equal(p.Methods, []api.PolicyMethod{api.PolicyMethodLinks, api.PolicyMethodEmail}) ||
		!slices.Equal(p.Allowlist.Emails, []string{"ana@example.com"}) || !slices.Equal(p.Allowlist.Domains, []string{"*.example.com", "example.com"}) {
		t.Fatalf("set email: %+v", p)
	}
	// Omitting the allowlist keeps it; the same entries change nothing.
	if kept := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links", "email"}})); kept.Version != p.Version || !slices.Equal(kept.Allowlist.Domains, p.Allowlist.Domains) {
		t.Fatalf("kept allowlist: %+v", kept)
	}
	if same := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links", "email"},
		"allowlist": allow([]string{"ana@example.com"}, []string{"example.com", "*.example.com"})})); same.Version != p.Version {
		t.Fatalf("same allowlist bumped the version: %+v", same)
	}
	// Any change to it increments the version.
	changed := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links", "email"},
		"allowlist": allow([]string{"ana@example.com"}, []string{"example.com"})}))
	if changed.Version != p.Version+1 {
		t.Fatalf("changed allowlist: %+v", changed)
	}
	// Turning email off keeps the allowlist, and is a change.
	off := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links"}}))
	if off.Version != changed.Version+1 || !slices.Equal(off.Allowlist.Domains, []string{"example.com"}) {
		t.Fatalf("email off: %+v", off)
	}
	if listed := hs.policies(hs.ownerA, "docs", "").Items[0]; !slices.Equal(listed.Allowlist.Emails, []string{"ana@example.com"}) {
		t.Fatalf("listed %+v", listed)
	}
	// Public clears it.
	if pub := decode(put(map[string]any{"path": "/", "visibility": "public"})); len(pub.Allowlist.Emails)+len(pub.Allowlist.Domains) != 0 {
		t.Fatalf("public: %+v", pub)
	}

	for _, tc := range []struct {
		name   string
		body   map[string]any
		target string
	}{
		{"email with nothing to keep", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"email"}}, "allowlist"},
		{"email with an empty allowlist", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"email"}, "allowlist": allow([]string{}, []string{})}, "allowlist"},
		{"bad address", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"email"}, "allowlist": allow([]string{"ana"}, nil)}, "allowlist"},
		{"bad domain", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"email"}, "allowlist": allow(nil, []string{"*.com"})}, "allowlist"},
		{"public with an allowlist", map[string]any{"path": "/", "visibility": "public", "allowlist": allow(nil, []string{"example.com"})}, "allowlist"},
		{"public with the method", map[string]any{"path": "/", "visibility": "public", "methods": []string{"email"}}, "methods"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := put(tc.body)
			res.want(t, http.StatusBadRequest, "invalid_policy")
			if e := res.err(t); e.Target == nil || *e.Target != tc.target {
				t.Fatalf("target %v, want %s: %s", e.Target, tc.target, res.body)
			}
		})
	}
}

// Owner B can neither set nor read owner A's allowlist: the same 404 as a
// missing site.
func TestPolicyEmailOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "alpha")
	hs.createSite(hs.ownerB, "beta")
	body := map[string]any{"path": "/", "visibility": "protected", "methods": []string{"email"},
		"allowlist": map[string]any{"emails": []string{"ana@example.com"}, "domains": []string{}}}
	before := hs.setPolicy(hs.ownerA, "alpha", body)
	missing := hs.do(hs.ownerB, http.MethodPut, "/sites/missing/policies", body)
	missing.want(t, http.StatusNotFound, "not_found")
	for _, req := range []struct{ method, path string }{{http.MethodPut, "/sites/alpha/policies"}, {http.MethodGet, "/sites/alpha/policies"}} {
		res := hs.do(hs.ownerB, req.method, req.path, map[string]any{"path": "/", "visibility": "protected", "methods": []string{"email"},
			"allowlist": map[string]any{"emails": []string{"eve@example.com"}, "domains": []string{}}})
		res.want(t, http.StatusNotFound, "not_found")
		if string(res.body) != string(missing.body) {
			t.Fatalf("%s: body %s differs from a missing site's %s", req.method, res.body, missing.body)
		}
	}
	if after := hs.policies(hs.ownerA, "alpha", "").Items[0]; after.Version != before.Version || !slices.Equal(after.Allowlist.Emails, []string{"ana@example.com"}) {
		t.Fatalf("owner B changed owner A's allowlist: %+v", after)
	}
}
