// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
)

// Synthetic passwords for tests only.
const (
	apiPassword      = "harbour-lights-7"
	apiOtherPassword = "tide-chart-42"
)

// TestPolicyPassword covers the write-only password on setPolicy: it is
// set, kept, replaced and removed, never returned, and validated.
func TestPolicyPassword(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	put := func(body map[string]any) response {
		t.Helper()
		res := hs.do(hs.ownerA, http.MethodPut, "/sites/docs/policies", body)
		for _, secret := range []string{apiPassword, apiOtherPassword, "argon2"} {
			if strings.Contains(string(res.body), secret) {
				t.Fatalf("response %s contains a password or its hash", res.body)
			}
		}
		return res
	}
	decode := func(res response) api.Policy {
		t.Helper()
		res.want(t, http.StatusOK, "")
		var p api.Policy
		res.decode(t, &p)
		return p
	}

	p := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links", "password"}, "password": apiPassword}))
	if !slices.Equal(p.Methods, []api.PolicyMethod{api.PolicyMethodLinks, api.PolicyMethodPassword}) || p.Version != 2 {
		t.Fatalf("set password: %+v", p)
	}
	// Omitting the password keeps it: nothing changed.
	if kept := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links", "password"}})); kept.Version != p.Version {
		t.Fatalf("kept password bumped the version: %+v", kept)
	}
	// A new one is a change.
	if changed := decode(put(map[string]any{"path": "/", "visibility": "protected", "methods": []string{"password"}, "password": apiOtherPassword})); changed.Version != p.Version+1 ||
		!slices.Equal(changed.Methods, []api.PolicyMethod{api.PolicyMethodPassword}) {
		t.Fatalf("changed password: %+v", changed)
	}
	page := hs.policies(hs.ownerA, "docs", "")
	if !slices.Equal(page.Items[0].Methods, []api.PolicyMethod{api.PolicyMethodPassword}) {
		t.Fatalf("listed %+v", page.Items[0])
	}

	for _, tc := range []struct {
		name   string
		body   map[string]any
		code   string
		target string
	}{
		{"too short", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"password"}, "password": "short"}, "invalid_policy", "password"},
		{"empty", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"password"}, "password": ""}, "invalid_policy", "password"},
		{"without the method", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links"}, "password": apiPassword}, "invalid_policy", "password"},
		{"on a public policy", map[string]any{"path": "/", "visibility": "public", "password": apiPassword}, "invalid_policy", "password"},
		{"method with nothing to keep", map[string]any{"path": "/docs/", "visibility": "protected", "methods": []string{"password"}}, "invalid_policy", "password"},
		{"public with the method", map[string]any{"path": "/", "visibility": "public", "methods": []string{"password"}}, "invalid_policy", "methods"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := put(tc.body)
			res.want(t, http.StatusBadRequest, tc.code)
			if e := res.err(t); e.Target == nil || *e.Target != tc.target {
				t.Fatalf("target %v, want %s: %s", e.Target, tc.target, res.body)
			}
		})
	}
}

// Owner B can neither set nor learn anything about owner A's password.
func TestPolicyPasswordOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "alpha")
	hs.createSite(hs.ownerB, "beta")
	before := hs.setPolicy(hs.ownerA, "alpha", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"password"}, "password": apiPassword})
	missing := hs.do(hs.ownerB, http.MethodPut, "/sites/missing/policies", map[string]any{"path": "/", "visibility": "public"})
	missing.want(t, http.StatusNotFound, "not_found")
	res := hs.do(hs.ownerB, http.MethodPut, "/sites/alpha/policies", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"password"}, "password": apiOtherPassword})
	res.want(t, http.StatusNotFound, "not_found")
	if string(res.body) != string(missing.body) {
		t.Fatalf("body %s differs from a missing site's %s", res.body, missing.body)
	}
	if after := hs.policies(hs.ownerA, "alpha", "").Items[0]; after.Version != before.Version {
		t.Fatalf("owner B changed owner A's password: %+v", after)
	}
}
