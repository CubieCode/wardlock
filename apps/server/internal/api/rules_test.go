// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"reflect"
	"testing"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
)

// TestDeployRules sends redirects and headers (C1.17) through the API:
// the Deploy object returns them with defaults filled in and canonical
// names, a deploy without any leaves both out, and bad rules answer
// invalid_config or limit_exceeded naming the rule.
func TestDeployRules(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	req := manifest(index)
	req.Redirects = &[]api.Redirect{{From: "/old", To: "/new"}, {From: "/blog/*", To: "/news/:splat", Status: new(api.RedirectStatusN308)}}
	req.Headers = &[]api.HeaderRule{{Path: "/*", Set: map[string]string{"x-frame-options": "DENY"}}}
	c := hs.createDeploy(a, "docs", req)
	wantRedirects := []api.Redirect{
		{From: "/old", To: "/new", Status: new(api.RedirectStatusN301)},
		{From: "/blog/*", To: "/news/:splat", Status: new(api.RedirectStatusN308)},
	}
	wantHeaders := []api.HeaderRule{{Path: "/*", Set: map[string]string{"X-Frame-Options": "DENY"}}}
	if c.Deploy.Redirects == nil || !reflect.DeepEqual(*c.Deploy.Redirects, wantRedirects) || c.Deploy.Headers == nil || !reflect.DeepEqual(*c.Deploy.Headers, wantHeaders) {
		t.Fatalf("created deploy rules %+v %+v", c.Deploy.Redirects, c.Deploy.Headers)
	}
	var page api.DeployPage
	hs.do(a, http.MethodGet, "/sites/docs/deploys", nil).decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Redirects == nil || !reflect.DeepEqual(*page.Items[0].Redirects, wantRedirects) {
		t.Fatalf("listed deploy %+v", page.Items)
	}

	plain := hs.createDeploy(a, "docs", manifest(index))
	if plain.Deploy.Redirects != nil || plain.Deploy.Headers != nil {
		t.Fatalf("deploy without rules returned %+v %+v", plain.Deploy.Redirects, plain.Deploy.Headers)
	}

	for name, tc := range map[string]struct {
		body   map[string]any
		status int
		code   string
		target string
	}{
		"bad status":       {map[string]any{"redirects": []any{map[string]any{"from": "/a", "to": "/", "status": 304}}}, 400, "invalid_config", "redirects[0]"},
		"refused header":   {map[string]any{"headers": []any{map[string]any{"path": "/*", "set": map[string]any{"Set-Cookie": "x=y"}}}}, 400, "invalid_config", "headers[0]"},
		"too many":         {map[string]any{"redirects": manyRedirects(deploys.MaxRedirects + 1)}, 413, "limit_exceeded", "redirects"},
		"status as string": {map[string]any{"redirects": []any{map[string]any{"from": "/a", "to": "/", "status": "301"}}}, 400, "invalid_request", ""},
	} {
		body := map[string]any{"files": []any{map[string]any{"path": index.path, "sha256": index.hash(), "size": len(index.content)}}}
		for k, v := range tc.body {
			body[k] = v
		}
		res := hs.do(a, http.MethodPost, "/sites/docs/deploys", body)
		res.want(t, tc.status, tc.code)
		if tc.target != "" {
			if e := res.err(t); e.Target == nil || *e.Target != tc.target {
				t.Errorf("%s: target %v, want %s", name, e.Target, tc.target)
			}
		}
	}
}

func manyRedirects(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"from": "/a", "to": "/"}
	}
	return out
}
