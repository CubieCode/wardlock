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

// TestPolicyOAuth covers the oauth method and GitHub organisations on
// setPolicy: set and normalised, kept when omitted (also while emails and
// domains change), replaced, cleared, and validated.
func TestPolicyOAuth(t *testing.T) {
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
	orgs := func(p api.Policy) []string {
		if p.Allowlist.GithubOrgs == nil {
			t.Fatalf("githubOrgs missing from %+v", p.Allowlist)
		}
		return *p.Allowlist.GithubOrgs
	}
	oauth := []string{"oauth"}

	if def := hs.policies(hs.ownerA, "docs", "").Items[0]; orgs(def) == nil || len(orgs(def)) != 0 {
		t.Fatalf("default policy githubOrgs %+v", def.Allowlist.GithubOrgs)
	}
	// Organisations alone are an allowlist for OAuth.
	p := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": oauth,
		"allowlist": map[string]any{"emails": []string{}, "domains": []string{}, "githubOrgs": []string{"Harbour-Crew", "harbour-crew", "ops"}}}))
	if !slices.Equal(p.Methods, []api.PolicyMethod{api.PolicyMethodOauth}) || !slices.Equal(orgs(p), []string{"harbour-crew", "ops"}) {
		t.Fatalf("set oauth: %+v %v", p.Methods, orgs(p))
	}
	// Replacing emails and domains without githubOrgs keeps the
	// organisations.
	p2 := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"oauth", "email"},
		"allowlist": map[string]any{"emails": []string{"ana@example.com"}, "domains": []string{"example.com"}}}))
	if !slices.Equal(orgs(p2), []string{"harbour-crew", "ops"}) || !slices.Equal(p2.Allowlist.Emails, []string{"ana@example.com"}) || p2.Version != p.Version+1 {
		t.Fatalf("emails and domains only: %+v %v", p2, orgs(p2))
	}
	// Omitting the allowlist keeps all of it.
	if kept := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"oauth", "email"}})); kept.Version != p2.Version || !slices.Equal(orgs(kept), orgs(p2)) {
		t.Fatalf("kept: %+v", kept)
	}
	// An explicit list replaces them; [] clears them.
	p3 := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": oauth,
		"allowlist": map[string]any{"emails": []string{"ana@example.com"}, "domains": []string{}, "githubOrgs": []string{}}}))
	if len(orgs(p3)) != 0 || len(p3.Allowlist.Domains) != 0 {
		t.Fatalf("cleared organisations: %v %+v", orgs(p3), p3.Allowlist)
	}
	if listed := hs.policies(hs.ownerA, "docs", "").Items[0]; !slices.Equal(listed.Methods, []api.PolicyMethod{api.PolicyMethodOauth}) {
		t.Fatalf("listed %+v", listed)
	}
	// Public clears everything.
	if pub := decode(put(map[string]any{"path": "/", "visibility": "public"})); len(orgs(pub))+len(pub.Allowlist.Emails) != 0 {
		t.Fatalf("public: %+v", pub)
	}

	for _, tc := range []struct {
		name   string
		body   map[string]any
		target string
	}{
		{"oauth with nothing to keep", map[string]any{"path": "/", "visibility": "protected", "methods": oauth}, "allowlist"},
		{"oauth with an empty allowlist", map[string]any{"path": "/", "visibility": "protected", "methods": oauth,
			"allowlist": map[string]any{"emails": []string{}, "domains": []string{}, "githubOrgs": []string{}}}, "allowlist"},
		{"bad organisation", map[string]any{"path": "/", "visibility": "protected", "methods": oauth,
			"allowlist": map[string]any{"emails": []string{}, "domains": []string{}, "githubOrgs": []string{"not an org"}}}, "allowlist"},
		{"public with organisations", map[string]any{"path": "/", "visibility": "public",
			"allowlist": map[string]any{"emails": []string{}, "domains": []string{}, "githubOrgs": []string{"ops"}}}, "allowlist"},
		{"public with the method", map[string]any{"path": "/", "visibility": "public", "methods": oauth}, "methods"},
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
