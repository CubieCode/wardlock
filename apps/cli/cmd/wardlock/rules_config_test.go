// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// TestDeployConfigRules sends "redirects" and "headers" from wardlock.json
// with the manifest (C1.17): as given when present, [] when empty, and
// left out when absent so a path deploy keeps its base's.
func TestDeployConfigRules(t *testing.T) {
	status := apiclient.RedirectStatusN308
	tests := []struct {
		name, config string
		redirects    *[]apiclient.Redirect
		headers      *[]apiclient.HeaderRule
	}{
		{
			name: "both",
			config: `{"site": "docs",
				"redirects": [{"from": "/old", "to": "/new"}, {"from": "/blog/*", "to": "https://blog.example.test/:splat", "status": 308}],
				"headers": [{"path": "/*", "set": {"X-Frame-Options": "DENY", "Content-Security-Policy": "default-src 'self'"}}]}`,
			redirects: &[]apiclient.Redirect{{From: "/old", To: "/new"}, {From: "/blog/*", To: "https://blog.example.test/:splat", Status: &status}},
			headers:   &[]apiclient.HeaderRule{{Path: "/*", Set: map[string]string{"X-Frame-Options": "DENY", "Content-Security-Policy": "default-src 'self'"}}},
		},
		{name: "empty lists", config: `{"site": "docs", "redirects": [], "headers": []}`, redirects: &[]apiclient.Redirect{}, headers: &[]apiclient.HeaderRule{}},
		{name: "absent", config: `{"site": "docs"}`},
		{name: "absent on a path deploy", config: `{"site": "docs", "path": "/mockup-x/"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			a, _, errOut := ciApp(t, srv)
			writeFiles(t, workDir(t, a), map[string]string{"wardlock.json": tt.config, "index.html": "x"})
			if err := run(t.Context(), a, []string{"deploy"}); err != nil {
				t.Fatalf("deploy: %v\n%s", err, errOut)
			}
			req := srv.created[0]
			if !reflect.DeepEqual(req.Redirects, tt.redirects) || !reflect.DeepEqual(req.Headers, tt.headers) {
				t.Fatalf("sent redirects %v headers %v, want %v %v", req.Redirects, req.Headers, tt.redirects, tt.headers)
			}
		})
	}
}

func TestConfigRuleErrors(t *testing.T) {
	tests := []struct {
		name, rules, want string
	}{
		{"redirects not a list", `"redirects": {"from": "/a"}`, "redirects"},
		{"redirect without from", `"redirects": [{"from": "/a", "to": "/b"}, {"to": "/b"}]`, `redirects[1] needs "from"`},
		{"redirect without to", `"redirects": [{"from": "/a"}]`, `redirects[0] needs "to"`},
		{"redirect status not allowed", `"redirects": [{"from": "/a", "to": "/b", "status": 303}]`, `redirects[0] has "status" 303`},
		{"redirect status a string", `"redirects": [{"from": "/a", "to": "/b", "status": "301"}]`, "status"},
		{"redirect unknown field", `"redirects": [{"from": "/a", "to": "/b", "force": true}]`, `unknown field "force"`},
		{"header rule without path", `"headers": [{"set": {"X-Frame-Options": "DENY"}}]`, `headers[0] needs "path"`},
		{"header rule without set", `"headers": [{"path": "/*"}]`, `headers[0] needs "set"`},
		{"header rule with empty set", `"headers": [{"path": "/*", "set": {}}]`, `headers[0] needs "set"`},
		{"header value not a string", `"headers": [{"path": "/*", "set": {"Access-Control-Max-Age": 600}}]`, "headers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			a, _, errOut := ciApp(t, srv)
			writeFiles(t, workDir(t, a), map[string]string{"wardlock.json": fmt.Sprintf(`{"site": "docs", %s}`, tt.rules), "index.html": "x"})
			err := run(t.Context(), a, []string{"deploy"})
			if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), tt.want) || !strings.Contains(errOut.String(), "wardlock.json") {
				t.Fatalf("exit %d, stderr %q, want %q", code, errOut, tt.want)
			}
			if len(srv.created) != 0 {
				t.Fatal("deployed with an invalid wardlock.json")
			}
		})
	}
}

// TestDeployRuleRefusals shows the server's refusal of a rule as it words
// it, naming the rule.
func TestDeployRuleRefusals(t *testing.T) {
	for _, r := range []*refusal{
		{400, apiclient.Error{Code: "invalid_config", Message: "Header rule headers[0] sets Set-Cookie, which only Wardlock sets.", Target: new("headers[0]")}},
		{413, apiclient.Error{Code: "limit_exceeded", Message: "The deploy has 1001 redirects; the limit is 1000.", Target: new("redirects")}},
	} {
		srv := newFakeAPI(t)
		srv.refuseDeploy = r
		a, _, errOut := ciApp(t, srv)
		writeFiles(t, workDir(t, a), map[string]string{"wardlock.json": `{"site": "docs", "headers": [{"path": "/*", "set": {"Set-Cookie": "x=y"}}]}`, "index.html": "x"})
		err := run(t.Context(), a, []string{"deploy"})
		if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), r.body.Message) {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		if len(srv.uploads) != 0 {
			t.Fatalf("uploaded %v after a refusal", srv.uploads)
		}
	}
}
