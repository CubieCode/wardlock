// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// Path deploys (C1.13): deploy --path, "path" in wardlock.json, removing a
// path and the retry on a stale base.

// livePaths returns the paths the site's active deploy lists.
func livePaths(t *testing.T, srv *fakeAPI, site string) []string {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	s := srv.sites[site]
	var paths []string
	for _, d := range s.deploys {
		if s.site.ActiveDeployId != nil && d.Id == *s.site.ActiveDeployId {
			for _, f := range d.files {
				paths = append(paths, f.Path)
			}
		}
	}
	slices.Sort(paths)
	return paths
}

// baseSite deploys index.html and mockup-x/old.html to docs as a full
// deploy, then forgets that request.
func baseSite(t *testing.T, srv *fakeAPI, a *app) {
	t.Helper()
	dir := filepath.Join(workDir(t, a), "site")
	writeFiles(t, dir, map[string]string{"index.html": "home", "mockup-x/old.html": "old"})
	if err := run(t.Context(), a, []string{"deploy", dir, "docs", "--spa"}); err != nil {
		t.Fatal(err)
	}
	srv.created = nil
}

func TestDeployPath(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "mock"), map[string]string{"index.html": "new", "img/a.png": "png"})
	out.Reset()
	// The slashes are added when left out.
	if err := run(t.Context(), a, []string{"deploy", "mock", "docs", "--path", "mockup-x"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	req := srv.created[0]
	if req.Path == nil || *req.Path != "/mockup-x/" {
		t.Fatalf("path %v, want /mockup-x/", req.Path)
	}
	if got := manifestPaths(req); !slices.Equal(got, []string{"img/a.png", "index.html"}) {
		t.Fatalf("manifest %q, want paths relative to the prefix", got)
	}
	// Unset flags are left to follow the deploy the path goes into.
	if req.Spa != nil || req.ImmutableAssets != nil {
		t.Fatalf("spa %v immutableAssets %v, want both left out", req.Spa, req.ImmutableAssets)
	}
	if want := []string{"index.html", "mockup-x/img/a.png", "mockup-x/index.html"}; !slices.Equal(livePaths(t, srv, "docs"), want) {
		t.Fatalf("live %q, want %q", livePaths(t, srv, "docs"), want)
	}
	if !strings.Contains(out.String(), "Deployed 2 files (6 B) to /mockup-x/") || !strings.HasSuffix(out.String(), "https://docs."+fakeBase+"/mockup-x/\n") {
		t.Fatalf("output:\n%s", out)
	}

	// Given flags are sent.
	if err := run(t.Context(), a, []string{"deploy", "mock", "docs", "--path", "/mockup-x/", "--spa=false"}); err != nil {
		t.Fatal(err)
	}
	if req := srv.created[1]; req.Spa == nil || *req.Spa || req.ImmutableAssets != nil {
		t.Fatalf("spa %v immutableAssets %v, want false and left out", req.Spa, req.ImmutableAssets)
	}
}

func TestDeployPathJSON(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "mock"), map[string]string{"index.html": "new"})
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "deploy", "mock", "docs", "--path", "/mockup-x/"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "deploy", "path", "siteCreated", "uploaded", "skipped")
	if string(m["path"]) != `"/mockup-x/"` {
		t.Fatalf("path %s", m["path"])
	}
	d := assertJSONKeys(t, m["deploy"], "id", "status", "createdAt", "activatedAt", "fileCount", "totalSize", "spa", "immutableAssets", "path")
	if string(d["path"]) != `"/mockup-x/"` || string(d["fileCount"]) != "2" {
		t.Fatalf("deploy %s", m["deploy"])
	}
	// A full deploy has no path.
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "deploy", "mock", "docs"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "site", "deploy", "siteCreated", "uploaded", "skipped")
}

func TestDeployPathFromConfig(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	wd := workDir(t, a)
	writeFiles(t, filepath.Join(wd, "out"), map[string]string{"report.html": "q3"})
	if err := os.WriteFile(filepath.Join(wd, configFile), []byte(`{"site": "docs", "dir": "out", "path": "/reports/q3"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), a, []string{"deploy"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if req := srv.created[0]; req.Path == nil || *req.Path != "/reports/q3/" {
		t.Fatalf("path %v, want /reports/q3/ from wardlock.json", req.Path)
	}
	// --path '' deploys the whole site after all.
	if err := run(t.Context(), a, []string{"deploy", "--path", ""}); err != nil {
		t.Fatal(err)
	}
	if req := srv.created[1]; req.Path != nil || req.Spa == nil {
		t.Fatalf("path %v spa %v, want a full deploy", req.Path, req.Spa)
	}
	if got := livePaths(t, srv, "docs"); !slices.Equal(got, []string{"report.html"}) {
		t.Fatalf("live %q", got)
	}
	if err := os.WriteFile(filepath.Join(wd, configFile), []byte(`{"path": 3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := exitCode(errOut, run(t.Context(), a, []string{"deploy"})); code != 2 {
		t.Fatalf("path of the wrong type: exit %d %q", code, errOut)
	}
}

func TestDeployPathUsage(t *testing.T) {
	srv := newFakeAPI(t)
	for _, path := range []string{"/", "//", ""} {
		a, _, errOut := ciApp(t, srv)
		writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
		if path == "" {
			// An empty --path with no wardlock.json is a plain full deploy.
			if err := run(t.Context(), a, []string{"deploy", ".", "docs", "--path", path}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		err := run(t.Context(), a, []string{"deploy", ".", "docs", "--path", path})
		if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), "whole site") {
			t.Errorf("--path %q: exit %d %q", path, code, errOut)
		}
	}
	a, _, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"deploy", "--help"}); err != nil || !strings.Contains(errOut.String(), "-path") || !strings.Contains(errOut.String(), "-yes") {
		t.Fatalf("--help: %v %q", err, errOut)
	}
}

func TestDeployEmptyDirRemovesPath(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	empty := filepath.Join(workDir(t, a), "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	// Without a terminal, or with --json, --yes is required.
	for _, args := range [][]string{
		{"deploy", empty, "docs", "--path", "/mockup-x/"},
		{"--json", "deploy", empty, "docs", "--path", "/mockup-x/"},
	} {
		errOut.Reset()
		if code := exitCode(errOut, run(ctx, a, args)); code != 2 || !strings.Contains(errOut.String(), "--yes") {
			t.Fatalf("%q: exit %d %q", args, code, errOut)
		}
	}
	// Without --path an empty directory is still an error.
	errOut.Reset()
	if code := exitCode(errOut, run(ctx, a, []string{"deploy", empty, "docs", "--yes"})); code != 1 || !strings.Contains(errOut.String(), "nothing to deploy") {
		t.Fatalf("no path: exit %d %q", code, errOut)
	}
	// A terminal asks for the path; anything else changes nothing.
	a.canPrompt = true
	a.stdin = strings.NewReader("mockup-x\n")
	errOut.Reset()
	if code := exitCode(errOut, run(ctx, a, []string{"deploy", empty, "docs", "--path", "/mockup-x/"})); code != 1 || !strings.Contains(errOut.String(), "nothing was removed") {
		t.Fatalf("wrong answer: exit %d %q", code, errOut)
	}
	if len(srv.created) != 0 {
		t.Fatalf("created %d deploys before confirmation", len(srv.created))
	}
	a.stdin = strings.NewReader("/mockup-x/\n")
	out.Reset()
	if err := run(ctx, a, []string{"deploy", empty, "docs", "--path", "/mockup-x/"}); err != nil {
		t.Fatal(err)
	}
	if req := srv.created[0]; len(req.Files) != 0 || req.Path == nil || *req.Path != "/mockup-x/" {
		t.Fatalf("request %+v, want an empty manifest for /mockup-x/", req)
	}
	if got := livePaths(t, srv, "docs"); !slices.Equal(got, []string{"index.html"}) {
		t.Fatalf("live %q, want the prefix gone", got)
	}
	if !strings.Contains(out.String(), "Removed /mockup-x/ from docs") {
		t.Fatalf("output %q", out)
	}

	// --yes with --json, and a site that does not exist is never created.
	a.canPrompt = false
	out.Reset()
	if err := run(ctx, a, []string{"--json", "deploy", empty, "docs", "--path", "/other/", "--yes"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "deploy", "path", "siteCreated", "uploaded", "skipped")
	if string(m["path"]) != `"/other/"` || string(m["siteCreated"]) != "false" {
		t.Fatalf("output %s", out)
	}
	errOut.Reset()
	if code := exitCode(errOut, run(ctx, a, []string{"deploy", empty, "newsite", "--path", "/x/", "--yes"})); code != 1 || srv.sites["newsite"] != nil {
		t.Fatalf("missing site: exit %d %q, created %v", code, errOut, srv.sites["newsite"] != nil)
	}
}

// goLiveElsewhere makes another deploy of the site go live, as a second
// publisher's path deploy would between this one's creation and activation.
func goLiveElsewhere(srv *fakeAPI) func(s *fakeSite) {
	return func(s *fakeSite) {
		other := &fakeDeploy{Deploy: apiclient.Deploy{Id: srv.id(), Status: apiclient.DeployStatusPending, CreatedAt: 1_800_000_000_000, FileCount: 1},
			files: []apiclient.ManifestFile{{Path: "mockup-y/index.html", Sha256: strings.Repeat("e", 64), Size: 1}}}
		if id := s.site.ActiveDeployId; id != nil {
			for _, d := range s.deploys {
				if d.Id == *id {
					for _, f := range d.files {
						if !strings.HasPrefix(f.Path, "mockup-y/") {
							other.files = append(other.files, f)
						}
					}
				}
			}
		}
		srv.blobs[strings.Repeat("e", 64)] = true // the other publisher uploaded it
		s.deploys = append(s.deploys, other)
		srv.setActive(s, other)
	}
}

func TestDeployPathRetriesStaleBaseOnce(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "mock"), map[string]string{"index.html": "new", "b.css": "b"})
	srv.beforeActivate = goLiveElsewhere(srv)
	out.Reset()
	errOut.Reset()
	if err := run(t.Context(), a, []string{"--json", "deploy", "mock", "docs", "--path", "/mockup-x/"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if len(srv.created) != 2 || !sameRequest(srv.created[0], srv.created[1]) {
		t.Fatalf("created %d deploys, want the same request twice", len(srv.created))
	}
	// Both blobs went up once, on the first attempt only.
	for h, n := range srv.uploads {
		if n != 1 {
			t.Fatalf("%s uploaded %d times", h, n)
		}
	}
	var res deployResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Uploaded.Files != 2 || res.Path != "/mockup-x/" || res.Deploy.Status != apiclient.DeployStatusActive {
		t.Fatalf("result %+v", res)
	}
	// The other publisher's files survive.
	if want := []string{"index.html", "mockup-x/b.css", "mockup-x/index.html", "mockup-y/index.html"}; !slices.Equal(livePaths(t, srv, "docs"), want) {
		t.Fatalf("live %q, want %q", livePaths(t, srv, "docs"), want)
	}
	if !strings.Contains(errOut.String(), "site changed while deploying") {
		t.Fatalf("stderr %q", errOut)
	}

	// Stale twice in a row: stop and say so; nothing more is retried.
	hook := goLiveElsewhere(srv)
	srv.beforeActivate = func(s *fakeSite) {
		hook(s)
		srv.beforeActivate = hook
	}
	srv.created = nil
	errOut.Reset()
	err := run(t.Context(), a, []string{"deploy", "mock", "docs", "--path", "/mockup-x/"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "changed again") || len(srv.created) != 2 {
		t.Fatalf("exit %d %q after %d creates", code, errOut, len(srv.created))
	}
}

func sameRequest(a, b apiclient.CreateDeployRequest) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestDeployPathRefusalNamesTheFile(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "mock"), map[string]string{"setup.exe": "mz"})
	srv.refuseDeploy = &refusal{422, apiclient.Error{Code: "file_type_blocked", Message: "mockup-x/setup.exe is a .exe file, a type this server does not accept.", Target: ptr("mockup-x/setup.exe")}}
	err := run(t.Context(), a, []string{"deploy", "mock", "docs", "--path", "/mockup-x/"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "list setup.exe in .wardlockignore") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestListShowsDeployPaths(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "mock"), map[string]string{"index.html": "new"})
	if err := run(t.Context(), a, []string{"deploy", "mock", "docs", "--path", "/mockup-x/"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"list", "docs"}); err != nil {
		t.Fatalf("list: %v\n%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 || !strings.Contains(lines[2], "PATH") {
		t.Fatalf("output:\n%s", out)
	}
	if fields := strings.Fields(lines[3]); !slices.Contains(fields, "/mockup-x/") {
		t.Errorf("path deploy line %q lacks its prefix", lines[3])
	}
	if fields := strings.Fields(lines[4]); !slices.Contains(fields, "/") {
		t.Errorf("full deploy line %q lacks /", lines[4])
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"list", "docs", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"path": "/mockup-x/"`) {
		t.Fatalf("--json:\n%s", out)
	}
}
