// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"wardlock.nz/apiclient"
	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
)

func (hs *harness) policies(owner, site, query string) api.PolicyPage {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/sites/"+site+"/policies"+query, nil)
	res.want(hs.t, http.StatusOK, "")
	var page api.PolicyPage
	res.decode(hs.t, &page)
	return page
}

func (hs *harness) setPolicy(owner, site string, body any) api.Policy {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPut, "/sites/"+site+"/policies", body)
	res.want(hs.t, http.StatusOK, "")
	var p api.Policy
	res.decode(hs.t, &p)
	return p
}

func TestPolicies(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")

	page := hs.policies(hs.ownerA, "docs", "")
	if len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("new site policies %+v", page)
	}
	def := page.Items[0]
	if def.Path != "/" || def.Visibility != api.PolicyVisibilityProtected || !slices.Equal(def.Methods, []api.PolicyMethod{api.PolicyMethodLinks}) || def.Version != 1 {
		t.Fatalf("default policy %+v", def)
	}

	// Protected without methods means share links.
	docs := hs.setPolicy(hs.ownerA, "docs", map[string]any{"path": "/docs/", "visibility": "protected"})
	if docs.Path != "/docs/" || !slices.Equal(docs.Methods, []api.PolicyMethod{api.PolicyMethodLinks}) || docs.Version != 1 {
		t.Fatalf("set /docs/ = %+v", docs)
	}
	assets := hs.setPolicy(hs.ownerA, "docs", api.SetPolicyRequest{Path: "/assets/", Visibility: api.PolicyVisibilityPublic})
	if len(assets.Methods) != 0 || assets.Version != 1 {
		t.Fatalf("set /assets/ = %+v", assets)
	}
	before := len(hs.notifications())
	pub := hs.setPolicy(hs.ownerA, "docs", api.SetPolicyRequest{Path: "/", Visibility: api.PolicyVisibilityPublic, Methods: &[]api.PolicyMethod{}})
	if pub.Id != def.Id || pub.Version != 2 || pub.Visibility != api.PolicyVisibilityPublic || len(pub.Methods) != 0 {
		t.Fatalf("set / public = %+v", pub)
	}
	if len(hs.notifications()) == before {
		t.Fatal("setting a policy notified nobody")
	}
	if again := hs.setPolicy(hs.ownerA, "docs", api.SetPolicyRequest{Path: "/", Visibility: api.PolicyVisibilityPublic}); again.Version != 2 {
		t.Fatalf("identical set bumped the version: %+v", again)
	}

	// Pages ordered by path.
	first := hs.policies(hs.ownerA, "docs", "?limit=2")
	if len(first.Items) != 2 || first.Items[0].Path != "/" || first.Items[1].Path != "/assets/" || first.NextCursor == nil {
		t.Fatalf("first page %+v", first)
	}
	second := hs.policies(hs.ownerA, "docs", "?limit=2&cursor="+*first.NextCursor)
	if len(second.Items) != 1 || second.Items[0].Id != docs.Id || second.NextCursor != nil {
		t.Fatalf("second page %+v", second)
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"relative path", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "docs/", "visibility": "public"}, 400, "invalid_policy_path"},
		{"no trailing slash", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/docs", "visibility": "public"}, 400, "invalid_policy_path"},
		{"dot segment", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/a/../", "visibility": "public"}, 400, "invalid_policy_path"},
		{"reserved path", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/.wardlock/", "visibility": "public"}, 400, "invalid_policy_path"},
		{"public with methods", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/", "visibility": "public", "methods": []string{"links"}}, 400, "invalid_policy"},
		{"protected with no methods", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/", "visibility": "protected", "methods": []string{}}, 400, "invalid_policy"},
		{"unknown method", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"password"}}, 400, "invalid_request"},
		{"duplicate method", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links", "links"}}, 400, "invalid_request"},
		{"unknown visibility", http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/", "visibility": "secret"}, 400, "invalid_request"},
		{"bad cursor", http.MethodGet, "/sites/docs/policies?cursor=bm90LWEtcHJlZml4", nil, 400, "invalid_request"},
		{"bad limit", http.MethodGet, "/sites/docs/policies?limit=0", nil, 400, "invalid_request"},
		{"delete the default", http.MethodDelete, "/sites/docs/policies/" + def.Id, nil, 409, "default_policy"},
		{"delete a missing policy", http.MethodDelete, "/sites/docs/policies/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e41", nil, 404, "not_found"},
		{"missing site", http.MethodGet, "/sites/nope/policies", nil, 404, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs.do(hs.ownerA, tc.method, tc.path, tc.body).want(t, tc.status, tc.code)
		})
	}

	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/policies/"+assets.Id, nil).want(t, http.StatusNoContent, "")
	if page := hs.policies(hs.ownerA, "docs", ""); len(page.Items) != 2 {
		t.Fatalf("after delete %+v", page)
	}
	hs.do("", http.MethodGet, "/sites/docs/policies", nil).want(t, http.StatusUnauthorized, "unauthenticated")
}

func TestPoliciesOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "alpha")
	hs.createSite(hs.ownerB, "beta")
	docs := hs.setPolicy(hs.ownerA, "alpha", api.SetPolicyRequest{Path: "/docs/", Visibility: api.PolicyVisibilityPublic})
	missing := hs.do(hs.ownerB, http.MethodGet, "/sites/missing/policies", nil)
	missing.want(t, http.StatusNotFound, "not_found")

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"list", http.MethodGet, "/sites/alpha/policies", nil},
		{"set", http.MethodPut, "/sites/alpha/policies", api.SetPolicyRequest{Path: "/", Visibility: api.PolicyVisibilityPublic}},
		{"delete", http.MethodDelete, "/sites/alpha/policies/" + docs.Id, nil},
		{"delete through own site", http.MethodDelete, "/sites/beta/policies/" + docs.Id, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := hs.do(hs.ownerB, tc.method, tc.path, tc.body)
			res.want(t, http.StatusNotFound, "not_found")
			if string(res.body) != string(missing.body) {
				t.Fatalf("body %s differs from a missing site's %s", res.body, missing.body)
			}
		})
	}
	page := hs.policies(hs.ownerA, "alpha", "")
	if len(page.Items) != 2 || page.Items[0].Version != 1 || page.Items[0].Visibility != api.PolicyVisibilityProtected || page.Items[1].Id != docs.Id {
		t.Fatalf("owner B changed owner A's policies: %+v", page)
	}
}

// TestPoliciesThroughGoClient proves the generated Go client (which the CLI
// will use for `wardlock policy set`) covers every policy operation.
func TestPoliciesThroughGoClient(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	srv := httptest.NewServer(hs.h)
	t.Cleanup(srv.Close)
	c, err := apiclient.NewClientWithResponses(srv.URL, apiclient.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set(ownerHeader, hs.ownerA)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	set, err := c.SetPolicyWithResponse(ctx, "docs", apiclient.SetPolicyRequest{
		Path: "/docs/", Visibility: apiclient.PolicyVisibilityProtected, Methods: &[]apiclient.PolicyMethod{apiclient.PolicyMethodLinks},
	})
	if err != nil || set.JSON200 == nil {
		t.Fatalf("set: %v %s", err, set.Body)
	}
	list, err := c.ListPoliciesWithResponse(ctx, "docs", &apiclient.ListPoliciesParams{})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Items) != 2 || list.JSON200.Items[1].Id != set.JSON200.Id {
		t.Fatalf("list: %v %s", err, list.Body)
	}
	del, err := c.DeletePolicyWithResponse(ctx, "docs", set.JSON200.Id)
	if err != nil || del.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete: %v %d", err, del.StatusCode())
	}
	del, err = c.DeletePolicyWithResponse(ctx, "docs", list.JSON200.Items[0].Id)
	if err != nil || del.JSON409 == nil || del.JSON409.Code != "default_policy" {
		t.Fatalf("delete default: %v %s", err, del.Body)
	}
}
