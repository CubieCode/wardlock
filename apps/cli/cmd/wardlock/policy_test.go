// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// setPolicy stores nothing but answers as the server does: methods default
// to [links] on a protected policy and must be empty on a public one.
func (f *fakeAPI) setPolicy(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	raw, _ := io.ReadAll(r.Body)
	var sent map[string]any
	var body apiclient.SetPolicyRequest
	if json.Unmarshal(raw, &sent) != nil || json.Unmarshal(raw, &body) != nil {
		reply(w, http.StatusBadRequest, apiErr("invalid_request", "Bad JSON."))
		return
	}
	s.set = append(s.set, sent)
	methods := []apiclient.PolicyMethod{}
	if body.Methods != nil {
		methods = *body.Methods
	}
	if body.Visibility == apiclient.PolicyVisibilityProtected && body.Methods == nil {
		methods = []apiclient.PolicyMethod{apiclient.PolicyMethodLinks}
	}
	if body.Visibility == apiclient.PolicyVisibilityPublic && len(methods) > 0 {
		reply(w, http.StatusBadRequest, apiErr("invalid_policy", "A public policy takes no methods."))
		return
	}
	allow := apiclient.PolicyAllowlist{Emails: []string{}, Domains: []string{}}
	if body.Allowlist != nil {
		allow = *body.Allowlist
	}
	reply(w, http.StatusOK, apiclient.Policy{Id: f.id(), Path: body.Path, Visibility: body.Visibility, Methods: methods, Allowlist: allow, Version: int32(len(s.set))})
}

func TestPolicySet(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		want string
		sent string // the JSON body the server received, or "" for none
	}{
		{"protected default", []string{"policy", "set", "docs", "--protected"}, 0, "docs / is now protected by share links.", `{"path":"/","visibility":"protected"}`},
		{"protected with links", []string{"policy", "set", "docs", "--protected", "--links", "--path", "/internal/"}, 0, "docs /internal/ is now protected by share links.", `{"methods":["links"],"path":"/internal/","visibility":"protected"}`},
		{"public", []string{"policy", "set", "docs." + fakeBase, "--public"}, 0, "docs / is now public.", `{"path":"/","visibility":"public"}`},
		{"public with a method", []string{"policy", "set", "docs", "--public", "--links"}, 2, "a public policy takes no methods", ""},
		{"neither", []string{"policy", "set", "docs"}, 2, "exactly one of --public or --protected", ""},
		{"both", []string{"policy", "set", "docs", "--public", "--protected"}, 2, "exactly one of --public or --protected", ""},
		{"no domain", []string{"policy", "set", "--public"}, 2, "Usage: wardlock policy set", ""},
		{"no subcommand", []string{"policy"}, 2, "Usage: wardlock policy set", ""},
		{"missing site", []string{"policy", "set", "nope", "--public"}, 1, "no site nope", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			site := srv.addSite("docs", "")
			a, out, errOut := ciApp(t, srv)
			err := run(t.Context(), a, tt.args)
			if code := exitCode(errOut, err); code != tt.code || !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			var got string
			if len(site.set) == 1 {
				b, _ := json.Marshal(site.set[0])
				got = string(b)
			}
			if got != tt.sent {
				t.Fatalf("sent %s, want %s", got, tt.sent)
			}
		})
	}
}

func TestPolicySetJSONAndScope(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"policy", "set", "docs", "--protected", "--json"}); err != nil {
		t.Fatalf("policy set: %v\n%s", err, errOut)
	}
	assertJSONKeys(t, out.Bytes(), "allowlist", "createdAt", "id", "methods", "path", "updatedAt", "version", "visibility")

	srv.scopes = []string{"deploy"}
	a, _, errOut = ciApp(t, srv)
	if code := exitCode(errOut, run(t.Context(), a, []string{"policy", "set", "docs", "--public"})); code != 1 || !strings.Contains(errOut.String(), "scope") {
		t.Fatalf("without manage: exit %d, stderr %q", code, errOut)
	}
}

// cliPassword is a synthetic password for tests only.
const cliPassword = "harbour-lights-7"

func TestPolicySetPassword(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		terminal bool // stdin is a terminal
		stdin    string
		typed    []string // what the no-echo prompt returns, in order
		code     int
		want     string
		sent     string
	}{
		{"from stdin", []string{"policy", "set", "docs", "--protected", "--password"}, false, cliPassword + "\n", nil, 0, "docs / is now protected by a password.",
			`{"methods":["password"],"password":"` + cliPassword + `","path":"/","visibility":"protected"}`},
		{"from stdin with CRLF and links", []string{"policy", "set", "docs", "--protected", "--links", "--password"}, false, cliPassword + "\r\nignored\n", nil, 0, "protected by share links or a password.",
			`{"methods":["links","password"],"password":"` + cliPassword + `","path":"/","visibility":"protected"}`},
		{"--json without a terminal reads stdin", []string{"policy", "set", "docs", "--protected", "--password", "--json"}, false, cliPassword, nil, 0, `"visibility": "protected"`,
			`{"methods":["password"],"password":"` + cliPassword + `","path":"/","visibility":"protected"}`},
		{"--json on a terminal still reads without echo", []string{"policy", "set", "docs", "--protected", "--password", "--json"}, true, "", []string{cliPassword, cliPassword}, 0, `"visibility": "protected"`,
			`{"methods":["password"],"password":"` + cliPassword + `","path":"/","visibility":"protected"}`},
		{"password typed as the domain", []string{"policy", "set", "--protected", "--password", cliPassword}, false, cliPassword + "\n", nil, 1, "no site the site given", ""},
		{"password typed as a bad domain", []string{"policy", "set", "--protected", "--password", cliPassword + "!"}, false, cliPassword + "\n", nil, 2, "DOMAIN is not a site name", ""},
		{"password typed before the domain", []string{"policy", "set", "--protected", "--password", cliPassword, "docs"}, false, cliPassword + "\n", nil, 2, "Usage: wardlock policy set", ""},
		{"prompted twice", []string{"policy", "set", "docs", "--protected", "--password"}, true, "", []string{cliPassword, cliPassword}, 0, "New password: \nAgain: \n",
			`{"methods":["password"],"password":"` + cliPassword + `","path":"/","visibility":"protected"}`},
		{"prompted, no match", []string{"policy", "set", "docs", "--protected", "--password"}, true, "", []string{cliPassword, cliPassword + "8"}, 1, "do not match", ""},
		{"empty stdin", []string{"policy", "set", "docs", "--protected", "--password"}, false, "", nil, 2, "--password needs a password", ""},
		{"too short", []string{"policy", "set", "docs", "--protected", "--password"}, false, "short\n", nil, 2, "at least 8 characters", ""},
		{"public with a password", []string{"policy", "set", "docs", "--public", "--password"}, false, cliPassword + "\n", nil, 2, "a public policy takes no methods", ""},
		{"no password value flag", []string{"policy", "set", "docs", "--protected", "--password=" + cliPassword}, false, "", nil, 2, "--password takes no value", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			site := srv.addSite("docs", "")
			a, out, errOut := ciApp(t, srv)
			a.stdinTerminal = tt.terminal
			a.stdin = strings.NewReader(tt.stdin)
			typed := tt.typed
			a.readPassword = func() (string, error) {
				if len(typed) == 0 {
					t.Fatal("prompted more often than expected")
				}
				pw := typed[0]
				typed = typed[1:]
				return pw, nil
			}
			err := run(t.Context(), a, tt.args)
			if code := exitCode(errOut, err); code != tt.code || !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			if strings.Contains(out.String()+errOut.String(), cliPassword) {
				t.Fatalf("output shows the password: stdout %q, stderr %q", out, errOut)
			}
			var got string
			if len(site.set) == 1 {
				b, _ := json.Marshal(site.set[0])
				got = string(b)
			}
			if got != tt.sent {
				t.Fatalf("sent %s, want %s", got, tt.sent)
			}
		})
	}
}

func TestPolicySetEmail(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		want string
		sent string
	}{
		{"email with an allowlist", []string{"policy", "set", "docs", "--protected", "--email", "--allow", "Ana@Example.com, example.com", "--allow", "*.example.com"}, 0,
			"docs / is now protected by email codes.\nAllowed: Ana@Example.com, example.com, *.example.com\n",
			`{"allowlist":{"domains":["example.com","*.example.com"],"emails":["Ana@Example.com"]},"methods":["email"],"path":"/","visibility":"protected"}`},
		{"email keeping the allowlist", []string{"policy", "set", "docs", "--protected", "--links", "--email"}, 0,
			"docs / is now protected by share links or email codes.",
			`{"methods":["links","email"],"path":"/","visibility":"protected"}`},
		{"emptying the allowlist", []string{"policy", "set", "docs", "--protected", "--links", "--allow", ""}, 0,
			"docs / is now protected by share links.",
			`{"allowlist":{"domains":[],"emails":[]},"methods":["links"],"path":"/","visibility":"protected"}`},
		{"public with email", []string{"policy", "set", "docs", "--public", "--email"}, 2, "a public policy takes no methods", ""},
		{"public with an allowlist", []string{"policy", "set", "docs", "--public", "--allow", "example.com"}, 2, "a public policy has no allowlist", ""},
		{"bad address", []string{"policy", "set", "docs", "--protected", "--email", "--allow", "Ana <ana@example.com>"}, 2, "is not an email address", ""},
		{"bad domain", []string{"policy", "set", "docs", "--protected", "--email", "--allow", "localhost"}, 2, "is not an address or a domain", ""},
		{"wildcard alone", []string{"policy", "set", "docs", "--protected", "--email", "--allow", "*.com"}, 2, "is not an address or a domain", ""},
		{"long wildcard", []string{"policy", "set", "docs", "--protected", "--email", "--allow", "*." + strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 56) + ".test"}, 2, "is not an address or a domain", ""},
		{"Unicode domain", []string{"policy", "set", "docs", "--protected", "--email", "--allow", "bücher.example"}, 2, "xn-- form", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			site := srv.addSite("docs", "")
			a, out, errOut := ciApp(t, srv)
			err := run(t.Context(), a, tt.args)
			if code := exitCode(errOut, err); code != tt.code || !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			var got string
			if len(site.set) == 1 {
				b, _ := json.Marshal(site.set[0])
				got = string(b)
			}
			if got != tt.sent {
				t.Fatalf("sent %s, want %s", got, tt.sent)
			}
		})
	}
}

func TestPolicySetOAuth(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		want string
		sent string
	}{
		{"oauth with organisations", []string{"policy", "set", "docs", "--protected", "--oauth", "--allow", "example.com", "--github-org", "Example-Org, ops", "--github-org", "crew"}, 0,
			"docs / is now protected by OAuth sign-in.",
			`{"allowlist":{"domains":["example.com"],"emails":[],"githubOrgs":["Example-Org","ops","crew"]},"methods":["oauth"],"path":"/","visibility":"protected"}`},
		{"--allow alone keeps the organisations", []string{"policy", "set", "docs", "--protected", "--oauth", "--allow", "ana@example.com"}, 0,
			"protected by OAuth sign-in",
			`{"allowlist":{"domains":[],"emails":["ana@example.com"]},"methods":["oauth"],"path":"/","visibility":"protected"}`},
		{"--github-org alone keeps the addresses and domains", []string{"policy", "set", "docs", "--protected", "--oauth", "--github-org", "ops"}, 0,
			"protected by OAuth sign-in",
			`{"allowlist":{"domains":["kept.example"],"emails":["kept@example.com"],"githubOrgs":["ops"]},"methods":["oauth"],"path":"/","visibility":"protected"}`},
		{"emptying the organisations", []string{"policy", "set", "docs", "--protected", "--oauth", "--github-org", ""}, 0,
			"protected by OAuth sign-in",
			`{"allowlist":{"domains":["kept.example"],"emails":["kept@example.com"],"githubOrgs":[]},"methods":["oauth"],"path":"/","visibility":"protected"}`},
		{"oauth keeping everything", []string{"policy", "set", "docs", "--protected", "--links", "--oauth"}, 0,
			"protected by share links or OAuth sign-in",
			`{"methods":["links","oauth"],"path":"/","visibility":"protected"}`},
		{"public with oauth", []string{"policy", "set", "docs", "--public", "--oauth"}, 2, "a public policy takes no methods", ""},
		{"public with organisations", []string{"policy", "set", "docs", "--public", "--github-org", "ops"}, 2, "a public policy has no allowlist", ""},
		{"bad organisation", []string{"policy", "set", "docs", "--protected", "--oauth", "--github-org", "not an org"}, 2, "is not a GitHub organisation name", ""},
		{"organisation with a double hyphen", []string{"policy", "set", "docs", "--protected", "--oauth", "--github-org", "a--b"}, 2, "is not a GitHub organisation name", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			site := srv.addSite("docs", "")
			site.policy = apiclient.PolicyVisibilityProtected
			site.allowlist = apiclient.PolicyAllowlist{Emails: []string{"kept@example.com"}, Domains: []string{"kept.example"}, GithubOrgs: &[]string{"old-org"}}
			a, out, errOut := ciApp(t, srv)
			err := run(t.Context(), a, tt.args)
			if code := exitCode(errOut, err); code != tt.code || !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			var got string
			if len(site.set) == 1 {
				b, _ := json.Marshal(site.set[0])
				got = string(b)
			}
			if got != tt.sent {
				t.Fatalf("sent %s, want %s", got, tt.sent)
			}
		})
	}
}
