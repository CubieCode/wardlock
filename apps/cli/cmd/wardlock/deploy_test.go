// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// ciApp is testApp authenticated the CI way, with WARDLOCK_TOKEN and
// WARDLOCK_SERVER pointing at srv.
func ciApp(t *testing.T, srv *fakeAPI) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	a, out, errOut := testApp(t)
	a.getenv = env(map[string]string{"WARDLOCK_TOKEN": testToken, "WARDLOCK_SERVER": srv.URL})
	return a, out, errOut
}

func workDir(t *testing.T, a *app) string {
	t.Helper()
	wd, err := a.getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// writeFiles creates files (slash paths) under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertJSONKeys checks out is one JSON object with exactly keys at the top
// level, as docs/SPEC.md "--json output" documents.
func assertJSONKeys(t *testing.T, out []byte, keys ...string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(out))
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, out)
	}
	if dec.More() {
		t.Fatalf("more than one JSON value:\n%s", out)
	}
	got := slices.Sorted(maps.Keys(m))
	slices.Sort(keys)
	if !slices.Equal(got, keys) {
		t.Fatalf("JSON keys %q, want %q\n%s", got, keys, out)
	}
	return m
}

func manifestPaths(req apiclient.CreateDeployRequest) []string {
	var paths []string
	for _, f := range req.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestFirstDeployCreatesSiteAndUploadsEverything(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	writeFiles(t, filepath.Join(workDir(t, a), "dist"), map[string]string{
		"index.html": "<h1>Hi</h1>", "assets/app.js": "console.log(1)", "copy.html": "<h1>Hi</h1>", "empty.txt": "",
	})
	if err := run(t.Context(), a, []string{"deploy", "dist", "docs", "--message", "First"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if !strings.Contains(out.String(), "Created docs and deployed 4 files") || !strings.HasSuffix(out.String(), "https://docs."+fakeBase+"/\n") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.Contains(errOut.String(), "Hashed 4 files") || !strings.Contains(errOut.String(), "Uploaded 3 files") {
		t.Fatalf("progress:\n%s", errOut)
	}
	s := srv.sites["docs"]
	if s == nil || s.site.ActiveDeployId == nil || *s.site.ActiveDeployId != s.deploys[0].Id {
		t.Fatal("docs was not created and activated")
	}
	req := srv.created[0]
	if got, want := manifestPaths(req), []string{"assets/app.js", "copy.html", "empty.txt", "index.html"}; !slices.Equal(got, want) {
		t.Fatalf("manifest %q, want %q", got, want)
	}
	if *req.Message != "First" || *req.Spa {
		t.Fatalf("message %q spa %v", *req.Message, *req.Spa)
	}
	// Three distinct bodies, each uploaded once (copy.html shares index.html's).
	if len(srv.uploads) != 3 {
		t.Fatalf("uploads %v", srv.uploads)
	}
	for h, n := range srv.uploads {
		if n != 1 {
			t.Fatalf("%s uploaded %d times", h, n)
		}
	}
	assertNoToken(t, out.String(), errOut.String())
}

func TestNoChangeDeployUploadsNothing(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	dir := filepath.Join(workDir(t, a), "site")
	writeFiles(t, dir, map[string]string{"index.html": "same", "a.css": "body{}"})
	for range 2 {
		out.Reset()
		errOut.Reset()
		if err := run(t.Context(), a, []string{"deploy", dir, "docs", "--json"}); err != nil {
			t.Fatalf("deploy: %v\n%s", err, errOut)
		}
	}
	var res deployResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.SiteCreated || res.Uploaded.Files != 0 || res.Uploaded.Bytes != 0 || res.Deploy.Status != apiclient.DeployStatusActive {
		t.Fatalf("second deploy %+v", res)
	}
	if !strings.Contains(errOut.String(), "Nothing to upload") {
		t.Fatalf("progress:\n%s", errOut)
	}
	if len(srv.uploads) != 2 || len(srv.sites["docs"].deploys) != 2 {
		t.Fatalf("uploads %v, deploys %d", srv.uploads, len(srv.sites["docs"].deploys))
	}
}

func TestDeployJSONSchema(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, _ := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
	if err := run(t.Context(), a, []string{"deploy", "--json", ".", "docs"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "deploy", "siteCreated", "uploaded", "skipped")
	assertJSONKeys(t, m["uploaded"], "files", "bytes")
	assertJSONKeys(t, m["site"], "id", "name", "hostname", "url", "createdAt", "activeDeployId", "activatedAt")
	assertJSONKeys(t, m["deploy"], "id", "status", "createdAt", "activatedAt", "fileCount", "totalSize", "spa")
	if string(m["skipped"]) != "[]" || string(m["siteCreated"]) != "true" {
		t.Fatalf("skipped %s siteCreated %s", m["skipped"], m["siteCreated"])
	}
}

func TestDeployIgnoreRules(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	wd := workDir(t, a)
	writeFiles(t, wd, map[string]string{
		"index.html":                  "1",
		"wardlock.json":               `{"site":"docs"}`,
		".wardlockignore":             "# drafts stay local\ndrafts/\n*.map\n!keep.map\n/root-only.txt\n!.htaccess\n!.env.local\nsub/**/secret.*\n",
		".env":                        "SECRET=1",
		".env.local":                  "SECRET=1",
		".git/config":                 "x",
		"node_modules/lib/index.js":   "x",
		".DS_Store":                   "x",
		"assets/.DS_Store":            "x",
		".hidden":                     "x",
		".config/settings":            "x",
		".htaccess":                   "x",
		".well-known/security.txt":    "x",
		"drafts/post.html":            "x",
		"app.js.map":                  "x",
		"keep.map":                    "x",
		"root-only.txt":               "x",
		"nested/root-only.txt":        "x",
		"nested/wardlock.json":        "x",
		"sub/a/b/secret.txt":          "x",
		"sub/secret.txt":              "x",
		"node_modules.txt":            "x",
		"assets/drafts":               "a file, not a directory",
		"assets/style.css":            "x",
		"nested/node_modules/x/y.js":  "x",
		".well-known/.hidden-in-well": "x",
	})
	if err := run(t.Context(), a, []string{"deploy"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	want := []string{
		".htaccess", ".well-known/security.txt", "assets/drafts", "assets/style.css", "index.html",
		"keep.map", "nested/root-only.txt", "node_modules.txt",
	}
	if got := manifestPaths(srv.created[0]); !slices.Equal(got, want) {
		t.Fatalf("deployed %q\nwant %q", got, want)
	}
}

func TestIgnorePatterns(t *testing.T) {
	tests := []struct {
		rules string
		path  string
		isDir bool
		want  bool
	}{
		{"", "index.html", false, false},
		{"", ".gitignore", false, true},
		{"", ".well-known", true, false},
		{"", ".well-known", false, true},
		{"", "a/.git", true, true},
		{"", "a/.env.production", false, true},
		{"!.env", ".env", false, true}, // always ignored, even when re-included
		{"!.git/", ".git", true, true},
		{"!wardlock.json", "wardlock.json", false, true},
		{"*.log", "a/b/c.log", false, true},
		{"/a.log", "b/a.log", false, false},
		{"/a.log", "a.log", false, true},
		{"a/*.log", "a/b/c.log", false, false},
		{"a/**/c.log", "a/b/x/c.log", false, true},
		{"a/**/c.log", "a/c.log", false, true},
		{"**/build", "x/y/build", true, true},
		{"logs/**", "logs/a/b", false, true},
		{"build/", "build", false, false},
		{"build/", "x/build", true, true},
		{"file?.txt", "file1.txt", false, true},
		{"file?.txt", "file10.txt", false, false},
		{"[abc].txt", "b.txt", false, true},
		{"[!abc].txt", "b.txt", false, false},
		{"[!abc].txt", "d.txt", false, true},
		{`\#hash`, "#hash", false, true},
		{`\!bang`, "!bang", false, true},
		{"trailing   ", "trailing", false, true},
		{"*.html\n!keep.html", "keep.html", false, false},
		{"*.html\n!keep.html\nkeep.html", "keep.html", false, true},
		{"résumé.pdf", "résumé.pdf", false, true},
		{"*.txt\r\n", "a.txt", false, true},
	}
	for _, tt := range tests {
		ig, err := newIgnorer(tt.rules)
		if err != nil {
			t.Fatalf("%q: %v", tt.rules, err)
		}
		if got := ig.ignored(tt.path, tt.isDir); got != tt.want {
			t.Errorf("rules %q: ignored(%q, dir=%v) = %v, want %v", tt.rules, tt.path, tt.isDir, got, tt.want)
		}
	}
	if _, err := newIgnorer("ok\n[unclosed"); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("bad pattern error = %v", err)
	}
}

func TestManifestPathSeparators(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		sep  byte
		want string
		err  string
	}{
		{"linux", "assets/css/site.css", '/', "assets/css/site.css", ""},
		{"macos", "assets/css/site.css", '/', "assets/css/site.css", ""},
		{"windows", `assets\css\site.css`, '\\', "assets/css/site.css", ""},
		{"windows top level", "index.html", '\\', "index.html", ""},
		{"macos decomposed name", "café/ménu.html", '/', "café/ménu.html", ""},
		{"windows decomposed name", "café\\index.html", '\\', "café/index.html", ""},
		{"backslash in a unix name", `a\b.html`, '/', "", "cannot be deployed"},
		{"invalid utf-8", "a/\xff.html", '/', "", "not valid UTF-8"},
	}
	for _, tt := range tests {
		got, err := manifestPath(tt.rel, tt.sep)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%s: error %v, want %q", tt.name, err, tt.err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%s: manifestPath(%q) = %q, %v; want %q", tt.name, tt.rel, got, err, tt.want)
		}
	}
}

func TestDeploySkipsSymlinks(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	wd := workDir(t, a)
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"secret.txt": "outside"})
	writeFiles(t, wd, map[string]string{"index.html": "x"})
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(wd, "link.txt")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := run(t.Context(), a, []string{"deploy", ".", "docs", "--json"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if got := manifestPaths(srv.created[0]); !slices.Equal(got, []string{"index.html"}) {
		t.Fatalf("deployed %q", got)
	}
	var res deployResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || !slices.Equal(res.Skipped, []string{"link.txt"}) {
		t.Fatalf("skipped %q (%v)", res.Skipped, err)
	}
}

func TestDeployConfigFields(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := testApp(t)
	// No WARDLOCK_SERVER: the server comes from wardlock.json.
	a.getenv = env(map[string]string{"WARDLOCK_TOKEN": testToken})
	wd := workDir(t, a)
	writeFiles(t, wd, map[string]string{
		"wardlock.json":     fmt.Sprintf(`{"server": %q, "site": "docs", "dir": "build", "spa": true}`, srv.URL+"/"),
		"build/index.html":  "app",
		"other/index.html":  "other",
		"top-level-file.md": "not deployed",
	})
	if err := run(t.Context(), a, []string{"deploy"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	req := srv.created[0]
	if got := manifestPaths(req); !slices.Equal(got, []string{"index.html"}) || !*req.Spa || srv.sites["docs"] == nil {
		t.Fatalf("deployed %q spa %v to %v", got, *req.Spa, slices.Collect(maps.Keys(srv.sites)))
	}

	// Flags and arguments override every field.
	other := newFakeAPI(t)
	out.Reset()
	if err := run(t.Context(), a, []string{"deploy", "other", "blog", "--spa=false", "--server", other.URL}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	req = other.created[0]
	if *req.Spa || other.sites["blog"] == nil || len(srv.created) != 1 {
		t.Fatalf("spa %v sites %v", *req.Spa, slices.Collect(maps.Keys(other.sites)))
	}
	if !strings.Contains(out.String(), "https://blog."+fakeBase+"/") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestConfigRejectsUnknownAndInvalid(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"unknown field", `{"site": "docs", "sever": "https://x.example"}`, `unknown field "sever"`},
		{"wrong type", `{"spa": "yes"}`, "spa"},
		{"not JSON", `site: docs`, "invalid character"},
		{"two values", `{} {}`, "unexpected data"},
		{"empty", ``, "empty file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			for _, cmd := range [][]string{{"deploy", ".", "docs"}, {"list"}, {"rollback", "docs"}, {"teardown", "docs", "--yes"}} {
				a, _, errOut := ciApp(t, srv)
				writeFiles(t, workDir(t, a), map[string]string{"wardlock.json": tt.body, "index.html": "x"})
				err := run(t.Context(), a, cmd)
				if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), tt.want) || !strings.Contains(errOut.String(), "wardlock.json") {
					t.Fatalf("%s: exit %d, stderr %q", cmd[0], code, errOut)
				}
			}
			if len(srv.created) != 0 {
				t.Fatal("deployed with an invalid wardlock.json")
			}
		})
	}
	if cfg, err := parseConfig([]byte(`{}`)); err != nil || cfg != (projectConfig{}) {
		t.Fatalf("{} = %+v, %v", cfg, err)
	}
}

func TestDeployUsageErrors(t *testing.T) {
	srv := newFakeAPI(t)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"deploy"}, "which site?"},
		{[]string{"deploy", ".", "Not_A_Name"}, "not a site name"},
		{[]string{"deploy", ".", "docs", "extra"}, "Usage: wardlock deploy"},
		{[]string{"deploy", "--nope"}, "flag provided but not defined"},
		{[]string{"deploy", ".", "docs", "--message", strings.Repeat("m", 501)}, "limit is 500"},
	}
	for _, tt := range tests {
		a, _, errOut := ciApp(t, srv)
		err := run(t.Context(), a, tt.args)
		if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%q: exit %d, stderr %q", tt.args, code, errOut)
		}
	}
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"deploy", "--help"}); err != nil || out.Len() != 0 || !strings.Contains(errOut.String(), "-spa") {
		t.Fatalf("--help: %v, stderr %q", err, errOut)
	}
}

func TestDeployFailures(t *testing.T) {
	srv := newFakeAPI(t)
	srv.taken["taken"] = true
	tests := []struct {
		name  string
		setup func(a *app, wd string)
		args  []string
		want  string
	}{
		{"nothing to deploy", func(_ *app, wd string) { writeFiles(t, wd, map[string]string{".env": "x"}) }, []string{"deploy", ".", "docs"}, "nothing to deploy"},
		{"missing dir", nil, []string{"deploy", "nope", "docs"}, "deploy nope"},
		{"file as dir", func(_ *app, wd string) { writeFiles(t, wd, map[string]string{"f": "x"}) }, []string{"deploy", "f", "docs"}, "not a directory"},
		{"name taken", func(_ *app, wd string) { writeFiles(t, wd, map[string]string{"i.html": "x"}) }, []string{"deploy", ".", "taken"}, "That site name is taken."},
		{"unknown hostname", func(_ *app, wd string) { writeFiles(t, wd, map[string]string{"i.html": "x"}) }, []string{"deploy", ".", "new.example.com"}, "deploy to its name, such as new"},
		{"not logged in", func(a *app, wd string) {
			a.getenv = noEnv
			writeFiles(t, wd, map[string]string{"i.html": "x"})
		}, []string{"deploy", ".", "docs"}, "not logged in"},
		{"rejected token", func(a *app, wd string) {
			a.getenv = env(map[string]string{"WARDLOCK_TOKEN": "wl_aaaaaaaaaa_wrong", "WARDLOCK_SERVER": srv.URL})
			writeFiles(t, wd, map[string]string{"i.html": "x"})
		}, []string{"deploy", ".", "docs"}, "run wardlock login again"},
		{"server rejects a path", func(_ *app, wd string) {
			writeFiles(t, wd, map[string]string{".wardlockignore": "!.wardlock/", ".wardlock/x.html": "x"})
		}, []string{"deploy", ".", "docs"}, "Invalid path."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, errOut := ciApp(t, srv)
			if tt.setup != nil {
				tt.setup(a, workDir(t, a))
			}
			err := run(t.Context(), a, tt.args)
			if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			assertNoToken(t, errOut.String())
		})
	}
}

func TestDeployToHostname(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	a, out, errOut := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
	if err := run(t.Context(), a, []string{"deploy", ".", "DOCS." + strings.ToUpper(fakeBase) + "."}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if len(srv.sites["docs"].deploys) != 1 || strings.Contains(out.String(), "Created") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestDeployNetworkFailures(t *testing.T) {
	t.Run("server down", func(t *testing.T) {
		srv := newFakeAPI(t)
		a, _, errOut := ciApp(t, srv)
		srv.Close()
		writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
		err := run(t.Context(), a, []string{"deploy", ".", "docs"})
		if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "could not reach "+srv.URL+": ") ||
			!strings.Contains(errOut.String(), "connection refused") {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
	})
	t.Run("upload retried after 503", func(t *testing.T) {
		srv := newFakeAPI(t)
		srv.failUploads = []int{503, 502}
		a, _, errOut := ciApp(t, srv)
		writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
		if err := run(t.Context(), a, []string{"deploy", ".", "docs"}); err != nil {
			t.Fatalf("deploy: %v\n%s", err, errOut)
		}
	})
	t.Run("upload keeps failing", func(t *testing.T) {
		srv := newFakeAPI(t)
		srv.failUploads = []int{500, 500, 500}
		a, _, errOut := ciApp(t, srv)
		writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
		err := run(t.Context(), a, []string{"deploy", ".", "docs"})
		if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "upload index.html: server "+srv.URL+": Internal server error.") {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		if srv.sites["docs"].site.ActiveDeployId != nil {
			t.Fatal("activated after a failed upload")
		}
	})
	t.Run("client errors are not retried", func(t *testing.T) {
		srv := newFakeAPI(t)
		srv.failUploads = []int{413}
		a, _, errOut := ciApp(t, srv)
		writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
		if err := run(t.Context(), a, []string{"deploy", ".", "docs"}); err == nil || len(srv.failUploads) != 0 || len(srv.uploads) != 0 {
			t.Fatalf("err %v, uploads %v\n%s", err, srv.uploads, errOut)
		}
	})
}

func TestUploadsAreParallelAndBounded(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	files := map[string]string{}
	for i := range 40 {
		files[fmt.Sprintf("f%02d.txt", i)] = strings.Repeat("x", 64<<10) + fmt.Sprint(i)
	}
	writeFiles(t, workDir(t, a), files)
	if err := run(t.Context(), a, []string{"deploy", ".", "docs"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if len(srv.uploads) != 40 || srv.maxInFlight > uploadWorkers {
		t.Fatalf("uploads %d, at most %d at once", len(srv.uploads), srv.maxInFlight)
	}
}

func TestLiveProgressRedrawsOneLine(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	a.liveProgress = true
	writeFiles(t, workDir(t, a), map[string]string{"index.html": "x", "b.html": "y"})
	if err := run(t.Context(), a, []string{"deploy", ".", "docs"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "\r\033[KHashing files: 1 of 2") || !strings.Contains(errOut.String(), "\r\033[KUploaded 2 files") {
		t.Fatalf("progress %q", errOut)
	}
}
