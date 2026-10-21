// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// globalPlacements puts the global flags g before the command, straight
// after it, after its subcommand (when it has one) and after its arguments
// (docs/SPEC.md "CLI": "--json and --server also before the command").
func globalPlacements(cmd, args, g []string) map[string][]string {
	join := func(parts ...[]string) []string { return slices.Concat(parts...) }
	p := map[string][]string{
		"before the command":  join(g, cmd, args),
		"after the command":   join(cmd[:1], g, cmd[1:], args),
		"after the arguments": join(cmd, args, g),
	}
	if len(cmd) > 1 {
		p["after the subcommand"] = join(cmd, g, args)
	}
	return p
}

// globalForms are the spellings of --json and --server the placements are
// tried with; SERVER stands for the server's URL.
var globalForms = [][]string{
	{"--json", "--server", "SERVER"},
	{"--server=SERVER", "-json"},
}

func withServer(args []string, url string) []string {
	out := slices.Clone(args)
	for i := range out {
		out[i] = strings.ReplaceAll(out[i], "SERVER", url)
	}
	return out
}

// seed runs a command against srv to set up state through the CLI itself.
func seed(t *testing.T, srv *fakeAPI, args ...string) {
	t.Helper()
	a, _, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, args); err != nil {
		t.Fatalf("%q: %v\n%s", args, err, errOut)
	}
}

func TestGlobalFlagsAnywhere(t *testing.T) {
	tests := []struct {
		cmd, args []string
		setup     func(t *testing.T, srv *fakeAPI)
	}{
		{cmd: []string{"deploy"}, args: []string{"site", "docs"}},
		{cmd: []string{"list"}},
		{cmd: []string{"list"}, args: []string{"docs"}},
		{cmd: []string{"rollback"}, args: []string{"docs"}, setup: func(t *testing.T, srv *fakeAPI) {
			srv.publish(srv.sites["docs"], map[string]string{"index.html": "one"})
			srv.publish(srv.sites["docs"], map[string]string{"index.html": "two"})
		}},
		{cmd: []string{"pull"}, args: []string{"docs"}, setup: func(t *testing.T, srv *fakeAPI) {
			srv.publish(srv.sites["docs"], map[string]string{"index.html": "one"})
		}},
		{cmd: []string{"teardown"}, args: []string{"docs", "--yes"}},
		{cmd: []string{"policy", "set"}, args: []string{"docs", "--protected", "--links"}},
		{cmd: []string{"share", "create"}, args: []string{"docs", "--label", "review"}},
		{cmd: []string{"share", "list"}, args: []string{"docs"}, setup: func(t *testing.T, srv *fakeAPI) {
			seed(t, srv, "share", "create", "docs")
		}},
		{cmd: []string{"share", "revoke"}, args: []string{"docs", "lnkaaaaaad"}, setup: func(t *testing.T, srv *fakeAPI) {
			seed(t, srv, "share", "create", "docs")
		}},
		{cmd: []string{"share", "rotate"}, args: []string{"docs", "lnkaaaaaad"}, setup: func(t *testing.T, srv *fakeAPI) {
			seed(t, srv, "share", "create", "docs")
		}},
		{cmd: []string{"domain", "add"}, args: []string{"docs", "a.example.com"}},
		{cmd: []string{"domain", "list"}, args: []string{"docs"}, setup: func(t *testing.T, srv *fakeAPI) {
			seed(t, srv, "domain", "add", "docs", "a.example.com")
		}},
		{cmd: []string{"domain", "verify"}, args: []string{"docs", "a.example.com"}, setup: func(t *testing.T, srv *fakeAPI) {
			seed(t, srv, "domain", "add", "docs", "a.example.com")
		}},
		{cmd: []string{"domain", "remove"}, args: []string{"docs", "a.example.com", "--yes"}, setup: func(t *testing.T, srv *fakeAPI) {
			seed(t, srv, "domain", "add", "docs", "a.example.com")
		}},
		{cmd: []string{"version"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(slices.Concat(tt.cmd, tt.args), " "), func(t *testing.T) {
			// want is the first placement's output; every other placement
			// and spelling must print the same.
			var want, wantFrom string
			for _, g := range globalForms {
				for where, args := range globalPlacements(tt.cmd, tt.args, g) {
					srv := newFakeAPI(t)
					srv.addSite("docs", apiclient.PolicyVisibilityProtected)
					if tt.setup != nil {
						tt.setup(t, srv)
					}
					a, out, errOut := testApp(t)
					// No WARDLOCK_SERVER: only --server names the server.
					a.getenv = env(map[string]string{"WARDLOCK_TOKEN": testToken})
					writeFiles(t, workDir(t, a), map[string]string{"site/index.html": "<h1>Docs</h1>"})
					args = withServer(args, srv.URL)
					if err := run(t.Context(), a, args); err != nil {
						t.Fatalf("%s %q: %v\n%s", where, args, err, errOut)
					}
					var v map[string]any
					if err := json.Unmarshal(out.Bytes(), &v); err != nil {
						t.Fatalf("%s %q: stdout is not one JSON object: %v\n%s", where, args, err, out)
					}
					got := strings.ReplaceAll(strings.ReplaceAll(out.String(), srv.URL, "SERVER"), workDir(t, a), "WORKDIR")
					if want == "" {
						want, wantFrom = got, where
					} else if got != want {
						t.Fatalf("%s %q prints\n%s\nbut %s prints\n%s", where, args, got, wantFrom, want)
					}
				}
			}
		})
	}
}

func TestGlobalFlagsAnywhereForLogin(t *testing.T) {
	for _, cmd := range []string{"login", "logout"} {
		t.Run(cmd, func(t *testing.T) {
			var want, wantFrom string
			for _, g := range globalForms {
				for where, args := range globalPlacements([]string{cmd}, nil, g) {
					srv := newFakeServer(t, http.StatusOK)
					a, out, errOut := testApp(t)
					if cmd == "logout" {
						if err := run(t.Context(), a, []string{"login", "--server", srv.URL}); err != nil {
							t.Fatal(err)
						}
						out.Reset()
					}
					args = withServer(args, srv.URL)
					if err := run(t.Context(), a, args); err != nil {
						t.Fatalf("%s %q: %v\n%s", where, args, err, errOut)
					}
					var v map[string]any
					if err := json.Unmarshal(out.Bytes(), &v); err != nil {
						t.Fatalf("%s %q: stdout is not one JSON object: %v\n%s", where, args, err, out)
					}
					got := strings.ReplaceAll(out.String(), srv.URL, "SERVER")
					if want == "" {
						want, wantFrom = got, where
					} else if got != want {
						t.Fatalf("%s %q prints\n%s\nbut %s prints\n%s", where, args, got, wantFrom, want)
					}
				}
			}
		})
	}
}

func TestGlobalFlagsWithSubcommandHelp(t *testing.T) {
	usages := map[string]string{"policy": "Usage: wardlock policy set", "share": "Usage: wardlock share", "domain": "Usage: wardlock domain"}
	for cmd, usage := range usages {
		for _, args := range [][]string{
			{"--json", cmd, "--help"},
			{"--server", "https://wardlock.example.test", cmd, "-h"},
			{cmd, "--json", "help"},
			{cmd, "--help", "--json"},
		} {
			a, out, errOut := testApp(t)
			if code := exitCode(errOut, run(t.Context(), a, args)); code != 0 || !strings.HasPrefix(out.String(), usage) || errOut.Len() > 0 {
				t.Errorf("%q: exit %d, stdout %q, stderr %q", args, code, out, errOut)
			}
		}
		// Global flags alone are still no subcommand.
		a, out, errOut := testApp(t)
		if code := exitCode(errOut, run(t.Context(), a, []string{"--json", cmd})); code != 2 || out.Len() > 0 || !strings.Contains(errOut.String(), usage) {
			t.Errorf("--json %s: exit %d, stdout %q, stderr %q", cmd, code, out, errOut)
		}
	}
}
