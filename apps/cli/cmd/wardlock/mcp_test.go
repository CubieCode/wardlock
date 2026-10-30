// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wardlock.nz/apiclient"
)

// mcpSession connects a client to an MCP server for a's working
// directory over an in-memory transport. The server logs to a's stderr.
func mcpSession(t *testing.T, a *app) *mcp.ClientSession {
	t.Helper()
	root, err := filepath.EvalSymlinks(workDir(t, a))
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := newMCPServer(a, root, "").newServer().Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})
	return cs
}

// callTool calls a tool and returns its result as JSON, or its error text
// with failed set.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (out []byte, failed bool) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		var text strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		return []byte(text.String()), true
	}
	out, err = json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return out, false
}

func mustCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, v any) {
	t.Helper()
	out, failed := callTool(t, cs, name, args)
	if failed {
		t.Fatalf("%s failed: %s", name, out)
	}
	if err := json.Unmarshal(out, v); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

func mustFail(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	out, failed := callTool(t, cs, name, args)
	if !failed || !strings.Contains(string(out), want) {
		t.Fatalf("%s(%v) = %s (failed %v), want an error containing %q", name, args, out, failed, want)
	}
}

func TestMCPListsTheToolsWithAnnotations(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, _ := ciApp(t, srv)
	cs := mcpSession(t, a)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type hints struct{ readOnly, destructive bool }
	want := map[string]hints{
		"list_sites": {readOnly: true}, "get_site": {readOnly: true}, "list_deploys": {readOnly: true},
		"deploy_directory": {destructive: true}, "pull_deploy": {}, "rollback": {destructive: true},
		"set_policy": {destructive: true}, "create_share_link": {}, "list_share_links": {readOnly: true},
		"revoke_share_link": {destructive: true},
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		w, ok := want[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %s", tool.Name)
			continue
		}
		ann := tool.Annotations
		// DestructiveHint defaults to true when absent, so it is always set.
		if ann == nil || ann.ReadOnlyHint != w.readOnly || ann.DestructiveHint == nil || *ann.DestructiveHint != w.destructive {
			t.Errorf("%s annotations %+v, want %+v", tool.Name, ann, w)
		}
		if tool.Description == "" || tool.InputSchema == nil {
			t.Errorf("%s has no description or input schema", tool.Name)
		}
	}
	if len(names) != len(want) {
		t.Errorf("tools %q, want the %d of %v", names, len(want), want)
	}
	for _, name := range names {
		if strings.Contains(name, "teardown") || strings.Contains(name, "domain") || strings.Contains(name, "token") || strings.Contains(name, "delete") {
			t.Errorf("tool %s must not be exposed", name)
		}
	}
}

func TestMCPReadTools(t *testing.T) {
	srv := newFakeAPI(t)
	docs := srv.addSite("docs", apiclient.PolicyVisibilityProtected)
	srv.addSite("blog", apiclient.PolicyVisibilityPublic)
	first := srv.publish(docs, map[string]string{"index.html": "one"})
	live := srv.publish(docs, map[string]string{"index.html": "two"})
	srv.domains["docs"] = []*apiclient.Domain{{Hostname: "docs.example.org", Status: apiclient.DomainStatusActive}}
	a, _, _ := ciApp(t, srv)
	cs := mcpSession(t, a)

	var sites siteList
	mustCall(t, cs, "list_sites", nil, &sites)
	if len(sites.Sites) != 2 || sites.Sites[0].Name != "blog" || sites.Sites[1].Policy != apiclient.PolicyVisibilityProtected {
		t.Fatalf("list_sites: %+v", sites)
	}
	mustFail(t, cs, "list_sites", map[string]any{"group": "nope"}, "nope")

	var details siteDetails
	mustCall(t, cs, "get_site", map[string]any{"site": "docs." + fakeBase}, &details)
	if details.Site.Name != "docs" || details.LiveDeploy == nil || details.LiveDeploy.Id != live.Id ||
		len(details.Domains) != 1 || details.Domains[0].Hostname != "docs.example.org" ||
		len(details.Policies) != 1 || details.Policies[0].Visibility != apiclient.PolicyVisibilityProtected {
		t.Fatalf("get_site: %+v", details)
	}
	var blog siteDetails
	mustCall(t, cs, "get_site", map[string]any{"site": "blog"}, &blog)
	if blog.LiveDeploy != nil || blog.Domains == nil || blog.Policies == nil {
		t.Fatalf("get_site blog: %+v", blog)
	}

	var deploys deployList
	mustCall(t, cs, "list_deploys", map[string]any{"site": "docs"}, &deploys)
	if len(deploys.Deploys) != 2 || deploys.Deploys[0].Id != live.Id || deploys.Deploys[1].Id != first.Id {
		t.Fatalf("list_deploys: %+v", deploys)
	}

	// Missing sites, bad names and missing arguments are tool errors.
	mustFail(t, cs, "get_site", map[string]any{"site": "nothere"}, "nothere")
	mustFail(t, cs, "list_deploys", map[string]any{"site": "Not A Name"}, "not a site name")
	mustFail(t, cs, "list_deploys", map[string]any{"site": "--json"}, "not a site name")
	mustFail(t, cs, "get_site", nil, "site")
}

func TestMCPDeployDirectory(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, _ := ciApp(t, srv)
	wd := workDir(t, a)
	writeFiles(t, wd, map[string]string{
		"dist/index.html":        "<h1>hi</h1>",
		"dist/css/site.css":      "body{}",
		"dist/.env":              "SECRET=1",
		"dist/.git/HEAD":         "ref",
		"dist/draft.md":          "draft",
		"dist/.wardlockignore":   "draft.md\n",
		"dist/node_modules/x.js": "x",
	})
	cs := mcpSession(t, a)

	var res deployResult
	mustCall(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "dist", "message": "from MCP"}, &res)
	if !res.SiteCreated || res.Site.Name != "docs" || res.Deploy.Status != apiclient.DeployStatusActive || res.Uploaded.Files != 2 {
		t.Fatalf("deploy_directory: %+v", res)
	}
	sent := srv.created[len(srv.created)-1]
	var paths []string
	for _, f := range sent.Files {
		paths = append(paths, f.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"css/site.css", "index.html"}) || sent.Message == nil || *sent.Message != "from MCP" {
		t.Fatalf("sent %q, message %v: want wardlock deploy's ignore rules", paths, sent.Message)
	}

	// A path deploy, and wardlock.json in the root naming the site and dir.
	writeFiles(t, wd, map[string]string{"mock/index.html": "mock", configFile: `{"site": "docs", "dir": "dist"}`})
	mustCall(t, cs, "deploy_directory", map[string]any{"directory": "mock", "path": "mockup-x"}, &res)
	if res.Path != "/mockup-x/" || res.SiteCreated {
		t.Fatalf("path deploy: %+v", res)
	}
	var fromConfig deployResult
	mustCall(t, cs, "deploy_directory", nil, &fromConfig)
	if fromConfig.Path != "" || fromConfig.Site.Name != "docs" || fromConfig.Deploy.FileCount != 2 {
		t.Fatalf("wardlock.json deploy: %+v", fromConfig)
	}

	// An empty directory with a path removes it, as wardlock deploy --path
	// --yes does; without a path there is nothing to deploy.
	if err := os.Mkdir(filepath.Join(wd, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustCall(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "empty", "path": "/mockup-x/"}, &res)
	if res.Path != "/mockup-x/" {
		t.Fatalf("removal: %+v", res)
	}
	mustFail(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "empty"}, "nothing to deploy")
	mustFail(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "missing"}, "missing")
}

func TestMCPPullDeploy(t *testing.T) {
	srv := newFakeAPI(t)
	docs := srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	srv.publish(docs, map[string]string{"index.html": "home", "a/b.txt": "bee"})
	a, _, _ := ciApp(t, srv)
	wd := workDir(t, a)
	cs := mcpSession(t, a)

	var res pullResult
	mustCall(t, cs, "pull_deploy", map[string]any{"site": "docs", "directory": "out/docs"}, &res)
	if res.Files != 2 || res.Dir != filepath.Join(wd, "out", "docs") {
		t.Fatalf("pull_deploy: %+v", res)
	}
	if b, err := os.ReadFile(filepath.Join(wd, "out", "docs", "a", "b.txt")); err != nil || string(b) != "bee" {
		t.Fatalf("pulled a/b.txt: %q, %v", b, err)
	}
	// Never into a directory with files, which only --force would replace.
	mustFail(t, cs, "pull_deploy", map[string]any{"site": "docs", "directory": "out/docs"}, "is not empty: pull into a new or empty directory")
	mustFail(t, cs, "pull_deploy", map[string]any{"site": "docs"}, "directory")
	mustFail(t, cs, "pull_deploy", map[string]any{"site": "docs", "directory": ""}, "which directory")
	mustFail(t, cs, "pull_deploy", map[string]any{"site": "docs", "directory": "x", "deploy": "zz"}, "not a deploy ID")
}

func TestMCPRootConfinement(t *testing.T) {
	srv := newFakeAPI(t)
	docs := srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	srv.publish(docs, map[string]string{"index.html": "home"})
	a, _, _ := ciApp(t, srv)
	wd := workDir(t, a)
	outside := filepath.Join(filepath.Dir(wd), "outside")
	writeFiles(t, outside, map[string]string{"site/index.html": "outside"})
	writeFiles(t, wd, map[string]string{"in/index.html": "inside"})
	for link, to := range map[string]string{"linked": outside, "linked-site": filepath.Join(outside, "site"), "dangling": filepath.Join(outside, "nothing")} {
		if err := os.Symlink(to, filepath.Join(wd, link)); err != nil {
			t.Fatal(err)
		}
	}
	cs := mcpSession(t, a)
	for _, dir := range []string{"..", "../outside/site", "in/../../outside/site", outside, filepath.Join(outside, "site"), "linked", "linked-site", "linked/site", "/"} {
		mustFail(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": dir}, "outside the root")
	}
	for _, dir := range []string{"..", "../outside/new", outside + "/new", "linked/new", "linked-site", "dangling", "dangling/x"} {
		mustFail(t, cs, "pull_deploy", map[string]any{"site": "docs", "directory": dir}, "outside the root")
	}
	// wardlock.json cannot point a deploy out of the root either.
	writeFiles(t, wd, map[string]string{configFile: `{"site": "docs", "dir": "../outside/site"}`})
	mustFail(t, cs, "deploy_directory", nil, "outside the root")
	if len(srv.created) != 0 {
		t.Fatalf("created %d deploys from outside the root", len(srv.created))
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Fatal("pulled outside the root")
	}

	// An absolute path inside the root, and a link that stays in it, work.
	if err := os.Symlink(filepath.Join(wd, "in"), filepath.Join(wd, "alias")); err != nil {
		t.Fatal(err)
	}
	var res deployResult
	mustCall(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": filepath.Join(wd, "in")}, &res)
	mustCall(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "alias"}, &res)
}

func TestMCPRollback(t *testing.T) {
	srv := newFakeAPI(t)
	docs := srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	first := srv.publish(docs, map[string]string{"index.html": "one"})
	srv.publish(docs, map[string]string{"index.html": "two"})
	third := srv.publish(docs, map[string]string{"index.html": "three"})
	a, _, _ := ciApp(t, srv)
	cs := mcpSession(t, a)

	var act apiclient.Activation
	mustCall(t, cs, "rollback", map[string]any{"site": "docs", "deploy": first.Id[len(first.Id)-8:]}, &act)
	if act.Deploy.Id != first.Id {
		t.Fatalf("rollback to %s: %+v", first.Id, act.Deploy)
	}
	mustCall(t, cs, "rollback", map[string]any{"site": "docs", "deploy": third.Id}, &act)
	if act.Deploy.Id != third.Id {
		t.Fatalf("rollback to %s: %+v", third.Id, act.Deploy)
	}
	mustFail(t, cs, "rollback", map[string]any{"site": "docs", "deploy": "not-an-id"}, "not a deploy ID")
}

func TestMCPSetPolicy(t *testing.T) {
	srv := newFakeAPI(t)
	docs := srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	docs.allowlist = apiclient.PolicyAllowlist{Emails: []string{"ana@example.com"}, Domains: []string{}}
	a, _, _ := ciApp(t, srv)
	cs := mcpSession(t, a)

	var p apiclient.Policy
	mustCall(t, cs, "set_policy", map[string]any{"site": "docs", "visibility": "protected", "methods": []string{"links", "email"}, "allow": []string{"example.com", "bo@example.org"}}, &p)
	if p.Visibility != apiclient.PolicyVisibilityProtected || len(p.Methods) != 2 || !slices.Equal(p.Allowlist.Domains, []string{"example.com"}) {
		t.Fatalf("set_policy: %+v", p)
	}
	mustCall(t, cs, "set_policy", map[string]any{"site": "docs", "path": "/docs/", "visibility": "public"}, &p)
	if p.Path != "/docs/" || p.Visibility != apiclient.PolicyVisibilityPublic {
		t.Fatalf("set_policy public: %+v", p)
	}
	// GitHub organisations alone keep the current addresses and domains.
	mustCall(t, cs, "set_policy", map[string]any{"site": "docs", "visibility": "protected", "methods": []string{"oauth"}, "githubOrgs": []string{"acme"}}, &p)
	last := docs.set[len(docs.set)-1]
	if al, _ := last["allowlist"].(map[string]any); al == nil || len(al["emails"].([]any)) != 1 || len(al["githubOrgs"].([]any)) != 1 {
		t.Fatalf("sent %v", last)
	}

	// The password method keeps the current password; with none yet the
	// error says where to set one, and no password is ever sent.
	mustFail(t, cs, "set_policy", map[string]any{"site": "docs", "visibility": "protected", "methods": []string{"password"}}, "wardlock policy set docs --path / --protected --password")
	for _, sent := range docs.set {
		if _, ok := sent["password"]; ok {
			t.Fatalf("sent a password: %v", sent)
		}
	}
	mustFail(t, cs, "set_policy", map[string]any{"site": "docs", "visibility": "secret"}, "public or protected")
	mustFail(t, cs, "set_policy", map[string]any{"site": "docs", "visibility": "public", "methods": []string{"links"}}, "public policy")
	mustFail(t, cs, "set_policy", map[string]any{"site": "docs", "visibility": "protected", "methods": []string{"magic"}}, `"magic" is not a method`)
	mustFail(t, cs, "set_policy", map[string]any{"site": "docs", "visibility": "protected", "allow": []string{"not an address"}}, "not an address")
	mustFail(t, cs, "set_policy", map[string]any{"site": "nothere", "visibility": "public"}, "nothere")
}

func TestMCPShareLinks(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityProtected)
	a, _, logs := ciApp(t, srv)
	cs := mcpSession(t, a)

	var created apiclient.CreatedShareLink
	mustCall(t, cs, "create_share_link", map[string]any{"site": "docs", "path": "/docs/", "label": "ENG-1", "expires": "7d", "uses": 3}, &created)
	if !strings.HasPrefix(created.Url, "https://docs."+fakeBase+"/docs/?t=wl_") || created.ShareLink.Label == nil || *created.ShareLink.MaxUses != 3 {
		t.Fatalf("create_share_link: %+v", created)
	}
	mustFail(t, cs, "create_share_link", map[string]any{"site": "docs", "expires": "soon"}, "--expires")

	var list shareListResult
	mustCall(t, cs, "list_share_links", map[string]any{"site": "docs"}, &list)
	if len(list.Links) != 1 || list.Links[0].PublicId != created.ShareLink.PublicId {
		t.Fatalf("list_share_links: %+v", list)
	}
	raw, _ := callTool(t, cs, "list_share_links", map[string]any{"site": "docs"})
	if bytes.Contains(raw, []byte(created.Token)) {
		t.Fatal("list_share_links returned the link's token")
	}

	var revoked shareRevokeResult
	mustCall(t, cs, "revoke_share_link", map[string]any{"site": "docs", "linkId": created.ShareLink.PublicId}, &revoked)
	if !revoked.Revoked || revoked.PublicID != created.ShareLink.PublicId {
		t.Fatalf("revoke_share_link: %+v", revoked)
	}
	mustFail(t, cs, "revoke_share_link", map[string]any{"site": "docs", "linkId": created.ShareLink.PublicId}, "no share link")
	mustFail(t, cs, "revoke_share_link", map[string]any{"site": "docs", "linkId": "x"}, "not a share link ID")

	// The logs name each tool, but never the URL or either token.
	for _, secret := range []string{created.Url, created.Token, testToken, "?t="} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs contain %q:\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs.String(), "tool=create_share_link") {
		t.Fatalf("logs do not name the tool:\n%s", logs)
	}
}

func TestMCPScopeRefusals(t *testing.T) {
	srv := newFakeAPI(t)
	docs := srv.addSite("docs", apiclient.PolicyVisibilityProtected)
	srv.publish(docs, map[string]string{"index.html": "one"})
	srv.publish(docs, map[string]string{"index.html": "two"})
	a, _, logs := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"dist/index.html": "hi"})
	cs := mcpSession(t, a)

	srv.scopes = []string{"deploy"}
	for name, args := range map[string]map[string]any{
		"set_policy":        {"site": "docs", "visibility": "public"},
		"create_share_link": {"site": "docs"},
		"revoke_share_link": {"site": "docs", "linkId": "abcdefghij"},
	} {
		mustFail(t, cs, name, args, name+" needs a token with the manage scope")
	}
	// Reads accept either scope.
	var sites siteList
	mustCall(t, cs, "list_sites", nil, &sites)

	srv.scopes = []string{"manage"}
	mustFail(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "dist"}, "deploy_directory needs a token with the deploy scope")
	mustFail(t, cs, "rollback", map[string]any{"site": "docs"}, "rollback needs a token with the deploy scope")
	if strings.Contains(logs.String(), testToken) {
		t.Fatalf("logs contain the token:\n%s", logs)
	}
}

// TestMCPCommand runs wardlock mcp itself over its stdin and stdout.
func TestMCPCommand(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityPublic)
	a, _, logs := ciApp(t, srv)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	a.stdin, a.stdout = inR, outW
	done := make(chan error, 1)
	go func() {
		done <- run(t.Context(), a, []string{"mcp", "--root", "."})
		_ = outW.Close()
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cs.InitializeResult().ServerInfo; got.Name != "wardlock" || got.Version != version {
		t.Fatalf("server info %+v", got)
	}
	var sites siteList
	mustCall(t, cs, "list_sites", nil, &sites)
	if len(sites.Sites) != 1 {
		t.Fatalf("list_sites: %+v", sites)
	}
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("wardlock mcp: %v\n%s", err, logs)
	}
	if !strings.Contains(logs.String(), "wardlock mcp started") || strings.Contains(logs.String(), testToken) {
		t.Fatalf("logs:\n%s", logs)
	}
}

func TestMCPCommandUsage(t *testing.T) {
	a, _, _ := testApp(t)
	if err := run(t.Context(), a, []string{"mcp", "extra"}); err == nil {
		t.Fatal("mcp with an argument: no error")
	}
	if err := run(t.Context(), a, []string{"mcp", "--root", "missing"}); err == nil {
		t.Fatal("mcp with a missing root: no error")
	}
	// Without a login the server says so before serving.
	if err := run(t.Context(), a, []string{"mcp"}); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("mcp without a login: %v", err)
	}
}
