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

// deployTimes deploys dir to site n times with different content and
// returns the deploy IDs, oldest first.
func deployTimes(t *testing.T, srv *fakeAPI, site string, n int) []string {
	t.Helper()
	a, _, errOut := ciApp(t, srv)
	for i := range n {
		writeFiles(t, workDir(t, a), map[string]string{"index.html": strings.Repeat("v", i+1)})
		if err := run(t.Context(), a, []string{"deploy", ".", site, "--message", "Version " + string(rune('A'+i))}); err != nil {
			t.Fatalf("deploy: %v\n%s", err, errOut)
		}
	}
	var ids []string
	for _, d := range srv.sites[site].deploys {
		ids = append(ids, d.Id)
	}
	return ids
}

func activeDeploy(srv *fakeAPI, site string) string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return *srv.sites[site].site.ActiveDeployId
}

func TestListSites(t *testing.T) {
	srv := newFakeAPI(t)
	deployTimes(t, srv, "docs", 1)
	srv.addSite("blog", apiclient.PolicyVisibilityProtected)
	srv.addSite("api-docs", "")
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"list"}); err != nil {
		t.Fatalf("list: %v\n%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "SITE") {
		t.Fatalf("output:\n%s", out)
	}
	for i, want := range [][]string{{"api-docs", "public", "not deployed yet"}, {"blog", "protected", "not deployed yet"}, {"docs", "public", "https://docs." + fakeBase + "/"}} {
		for _, w := range want {
			if !strings.Contains(lines[i+1], w) {
				t.Errorf("line %q lacks %q", lines[i+1], w)
			}
		}
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"list", "--json"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "sites")
	var sites []json.RawMessage
	if err := json.Unmarshal(m["sites"], &sites); err != nil || len(sites) != 3 {
		t.Fatalf("sites %s (%v)", m["sites"], err)
	}
	assertJSONKeys(t, sites[1], "id", "name", "hostname", "url", "createdAt", "unavailable", "tagIds", "pinned", "policy", "tags")
	assertJSONKeys(t, sites[2], "id", "name", "hostname", "url", "createdAt", "unavailable", "tagIds", "pinned", "activeDeployId", "activatedAt", "policy", "tags")
	var res siteList
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Sites[1].Policy != apiclient.PolicyVisibilityProtected {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestListWithoutSites(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, _ := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"list"}); err != nil || !strings.Contains(out.String(), "No sites yet") {
		t.Fatalf("%v: %s", err, out)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"list", "--json"}); err != nil || strings.TrimSpace(out.String()) != "{\n  \"sites\": []\n}" {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestListDeploys(t *testing.T) {
	srv := newFakeAPI(t)
	ids := deployTimes(t, srv, "docs", 2)
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"list", "docs." + fakeBase}); err != nil {
		t.Fatalf("list: %v\n%s", err, errOut)
	}
	text := out.String()
	newest, oldest := strings.Index(text, shortID(ids[1])), strings.Index(text, shortID(ids[0]))
	if newest < 0 || oldest < newest || !strings.Contains(text, "live") || !strings.Contains(text, "not live") || !strings.Contains(text, "Version B") {
		t.Fatalf("output:\n%s", text)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"list", "docs", "--json"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "deploys")
	var deploys []json.RawMessage
	if err := json.Unmarshal(m["deploys"], &deploys); err != nil || len(deploys) != 2 {
		t.Fatalf("deploys %s", m["deploys"])
	}
	assertJSONKeys(t, deploys[0], "id", "status", "createdAt", "activatedAt", "fileCount", "totalSize", "message", "spa", "immutableAssets")

	out.Reset()
	errOut.Reset()
	err := run(t.Context(), a, []string{"list", "nope"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "no site nope") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestRollback(t *testing.T) {
	srv := newFakeAPI(t)
	ids := deployTimes(t, srv, "docs", 3)
	a, out, errOut := ciApp(t, srv)

	// No DEPLOY_ID: the previous deploy.
	if err := run(t.Context(), a, []string{"rollback", "docs"}); err != nil {
		t.Fatalf("rollback: %v\n%s", err, errOut)
	}
	if activeDeploy(srv, "docs") != ids[1] || !strings.Contains(out.String(), "Rolled docs back to deploy "+shortID(ids[1])) ||
		!strings.Contains(out.String(), `"Version B"`) {
		t.Fatalf("active %s, output:\n%s", activeDeploy(srv, "docs"), out)
	}

	// A short ID, as list shows.
	out.Reset()
	if err := run(t.Context(), a, []string{"rollback", "docs", strings.ToUpper(shortID(ids[0])), "--json"}); err != nil {
		t.Fatalf("rollback: %v\n%s", err, errOut)
	}
	if activeDeploy(srv, "docs") != ids[0] {
		t.Fatalf("active %s, want %s", activeDeploy(srv, "docs"), ids[0])
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "deploy")
	var d apiclient.Deploy
	if err := json.Unmarshal(m["deploy"], &d); err != nil || d.Id != ids[0] || d.Status != apiclient.DeployStatusActive {
		t.Fatalf("deploy %+v %v", d, err)
	}

	// A full ID.
	if err := run(t.Context(), a, []string{"rollback", "docs", ids[2]}); err != nil || activeDeploy(srv, "docs") != ids[2] {
		t.Fatalf("rollback to %s: %v", ids[2], err)
	}

	tests := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"rollback", "docs", "ffffffff"}, 1, "site docs has no deploy ffffffff"},
		{[]string{"rollback", "docs", "not-an-id"}, 2, "is not a deploy ID"},
		{[]string{"rollback"}, 2, "Usage: wardlock rollback"},
		{[]string{"rollback", "nope"}, 1, "no site nope"},
	}
	for _, tt := range tests {
		errOut.Reset()
		err := run(t.Context(), a, tt.args)
		if code := exitCode(errOut, err); code != tt.code || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%q: exit %d, stderr %q", tt.args, code, errOut)
		}
	}
}

func TestRollbackWithNoEarlierDeploy(t *testing.T) {
	srv := newFakeAPI(t)
	deployTimes(t, srv, "docs", 1)
	a, _, errOut := ciApp(t, srv)
	err := run(t.Context(), a, []string{"rollback", "docs"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "There is no earlier deploy") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestTeardownConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		canPrompt bool
		stdin     string
		code      int
		deleted   bool
		want      string
	}{
		{"no terminal needs --yes", []string{"teardown", "docs"}, false, "docs\n", 2, false, "pass --yes"},
		{"--json needs --yes", []string{"teardown", "docs", "--json"}, true, "docs\n", 2, false, "pass --yes"},
		{"--yes", []string{"teardown", "docs", "--yes"}, false, "", 0, true, "Deleted docs."},
		{"typed name", []string{"teardown", "docs"}, true, "docs\n", 0, true, "Type the site name to confirm"},
		{"typed name by hostname", []string{"teardown", "docs." + fakeBase}, true, " docs \n", 0, true, "Deleted docs."},
		{"wrong name", []string{"teardown", "docs"}, true, "doc\n", 1, false, "nothing was deleted"},
		{"no answer", []string{"teardown", "docs"}, true, "", 1, false, "nothing was deleted"},
		{"missing site", []string{"teardown", "nope", "--yes"}, false, "", 1, true, "no site nope"},
		{"no domain", []string{"teardown", "--yes"}, false, "", 2, false, "Usage: wardlock teardown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			srv.addSite("docs", "")
			a, out, errOut := ciApp(t, srv)
			a.canPrompt = tt.canPrompt
			a.stdin = strings.NewReader(tt.stdin)
			err := run(t.Context(), a, tt.args)
			if code := exitCode(errOut, err); code != tt.code || !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			if _, kept := srv.sites["docs"]; kept == (tt.deleted && tt.name != "missing site") {
				t.Fatalf("docs kept = %v", kept)
			}
		})
	}
}

func TestTeardownJSONAndScope(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"teardown", "docs", "--yes", "--json"}); err != nil {
		t.Fatalf("teardown: %v\n%s", err, errOut)
	}
	m := assertJSONKeys(t, out.Bytes(), "deleted", "site")
	if string(m["deleted"]) != "true" {
		t.Fatalf("deleted %s", m["deleted"])
	}

	srv.addSite("blog", "")
	srv.scopes = []string{"deploy"}
	errOut.Reset()
	err := run(t.Context(), a, []string{"teardown", "blog", "--yes"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "lacks the manage scope") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestStoredLoginIsUsed(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	a, out, errOut := testApp(t)
	if _, err := a.creds.save(srv.URL, testToken); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), a, []string{"list"}); err != nil || !strings.Contains(out.String(), "docs") {
		t.Fatalf("list: %v\n%s%s", err, out, errOut)
	}
	// wardlock.json naming another server than the login is refused.
	writeFiles(t, workDir(t, a), map[string]string{"wardlock.json": `{"server": "https://other.example.test"}`})
	errOut.Reset()
	err := run(t.Context(), a, []string{"list"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "logged in to "+srv.URL) {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestGlobalFlagsBeforeTheCommand(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	tests := []struct {
		args []string
		keys []string
	}{
		{[]string{"--json", "list"}, []string{"sites"}},
		{[]string{"-json", "--server", "SERVER", "list", "docs"}, []string{"site", "deploys"}},
		{[]string{"--server=SERVER", "--json", "list"}, []string{"sites"}},
		{[]string{"--json", "version"}, []string{"version", "server"}},
		{[]string{"--json", "teardown", "docs", "--yes"}, []string{"deleted", "site"}},
	}
	for _, tt := range tests {
		a, out, errOut := testApp(t)
		a.getenv = env(map[string]string{"WARDLOCK_TOKEN": testToken})
		args := slices.Clone(tt.args)
		for i := range args {
			args[i] = strings.ReplaceAll(args[i], "SERVER", srv.URL)
		}
		if !slices.ContainsFunc(args, func(s string) bool { return strings.Contains(s, srv.URL) }) {
			a.getenv = env(map[string]string{"WARDLOCK_TOKEN": testToken, "WARDLOCK_SERVER": srv.URL})
		}
		if err := run(t.Context(), a, args); err != nil {
			t.Fatalf("%q: %v\n%s", args, err, errOut)
		}
		assertJSONKeys(t, out.Bytes(), tt.keys...)
	}

	for _, args := range [][]string{{"--server"}, {"--json"}, {"--json", "--nope", "list"}, {"--json", "teardown", "docs"}} {
		a, _, errOut := ciApp(t, srv)
		if code := exitCode(errOut, run(t.Context(), a, args)); code != 2 {
			t.Errorf("%q: exit %d, stderr %q", args, code, errOut)
		}
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	a, out, errOut := testApp(t)
	if code := exitCode(errOut, run(t.Context(), a, []string{"version", "extra"})); code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "Usage: wardlock version") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"version", "--json"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "version")
}

func TestPaginationAndShortIDsPastPageOne(t *testing.T) {
	srv := newFakeAPI(t)
	ids := deployTimes(t, srv, "docs", 2*fakePage+1)
	srv.mu.Lock()
	srv.sites["docs"].policy = apiclient.PolicyVisibilityProtected
	srv.sites["docs"].pathPolicies = 2 * fakePage // "/" is on page three
	srv.mu.Unlock()
	for _, name := range []string{"a", "b", "c", "d"} {
		srv.addSite(name, "")
	}
	a, out, errOut := ciApp(t, srv)

	if err := run(t.Context(), a, []string{"list", "--json"}); err != nil {
		t.Fatalf("list: %v\n%s", err, errOut)
	}
	var sites siteList
	if err := json.Unmarshal(out.Bytes(), &sites); err != nil || len(sites.Sites) != 5 || sites.Sites[4].Name != "docs" ||
		sites.Sites[4].Policy != apiclient.PolicyVisibilityProtected {
		t.Fatalf("sites %+v (%v)", sites, err)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"list", "docs", "--json"}); err != nil {
		t.Fatal(err)
	}
	var deploys deployList
	if err := json.Unmarshal(out.Bytes(), &deploys); err != nil || len(deploys.Deploys) != len(ids) || deploys.Deploys[len(ids)-1].Id != ids[0] {
		t.Fatalf("deploys %+v (%v)", deploys, err)
	}

	// The oldest deploy is on the last page.
	if err := run(t.Context(), a, []string{"rollback", "docs", shortID(ids[0])}); err != nil || activeDeploy(srv, "docs") != ids[0] {
		t.Fatalf("rollback: %v, active %s", err, activeDeploy(srv, "docs"))
	}
}

func TestTeardownNamesOnlyBuiltFeatures(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	a, out, errOut := ciApp(t, srv)
	a.canPrompt = true
	a.stdin = strings.NewReader("no\n")
	_ = run(t.Context(), a, []string{"teardown", "docs"})
	_ = run(t.Context(), a, []string{"teardown", "--help"})
	text := out.String() + errOut.String()
	if !strings.Contains(text, "every deploy and access setting") {
		t.Fatalf("output:\n%s", text)
	}
	for _, unbuilt := range []string{"share link", "custom domain"} {
		if strings.Contains(strings.ToLower(text), unbuilt) {
			t.Errorf("teardown mentions %s, which is not built:\n%s", unbuilt, text)
		}
	}
}

func TestLogoutRefusesAnotherServer(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := testApp(t)
	if _, err := a.creds.save(srv.URL, testToken); err != nil {
		t.Fatal(err)
	}
	err := run(t.Context(), a, []string{"--server", "https://other.example.test", "logout"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "logged in to "+srv.URL) {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if server, _, err := a.creds.load(); err != nil || server != srv.URL {
		t.Fatalf("login forgotten: %q %v", server, err)
	}
}

func TestLogoutRejectsMalformedServer(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, _, errOut := testApp(t)
	if _, err := a.creds.save(srv.URL, testToken); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"wardlock.example.test", "https://x.example.test/path", "ftp://x.example.test"} {
		errOut.Reset()
		err := run(t.Context(), a, []string{"logout", "--server", bad})
		if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), "is not a server address") {
			t.Fatalf("%q: exit %d, stderr %q", bad, code, errOut)
		}
	}
	if server, _, err := a.creds.load(); err != nil || server != srv.URL {
		t.Fatalf("login forgotten: %q %v", server, err)
	}
	// A matching server with a trailing slash still logs out.
	if err := run(t.Context(), a, []string{"logout", "--server", srv.URL + "/"}); err != nil || len(srv.signOuts) != 1 {
		t.Fatalf("logout: %v, sign-outs %d", err, len(srv.signOuts))
	}
}
