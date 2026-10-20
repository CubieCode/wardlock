// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	if *req.Message != "First" || *req.Spa || *req.ImmutableAssets {
		t.Fatalf("message %q spa %v immutableAssets %v", *req.Message, *req.Spa, *req.ImmutableAssets)
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
	assertJSONKeys(t, m["deploy"], "id", "status", "createdAt", "activatedAt", "fileCount", "totalSize", "spa", "immutableAssets")
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
		// POSIX classes.
		{"[[:alpha:]].txt", "a.txt", false, true},
		{"[[:alpha:]].txt", "1.txt", false, false},
		{"[[:digit:][:upper:]]x", "Bx", false, true},
		{"[![:digit:]]x", "1x", false, false},
		{"[![:digit:]]x", "ax", false, true},
		{"d/a[[:punct:]]b", "d/a.b", false, true},
		{"d/a[[:punct:]]b", "d/a/b", false, false},
		// No bracket expression matches "/".
		{"d/a[!x]b", "d/a/b", false, false},
		{"d/a[!x]b", "d/ayb", false, true},
		{"d/a[/]b", "d/a/b", false, false},
		{"d/a[.-0]b", "d/a/b", false, false},
		{"d/a[.-0]b", "d/a.b", false, true},
		{"d/a[.-0]b", "d/a0b", false, true},
		// Literal "-", "]" and "[" inside classes.
		{`[a\-z].txt`, "-.txt", false, true},
		{`[a\-z].txt`, "b.txt", false, false},
		{"[-a].txt", "-.txt", false, true},
		{"[]a].txt", "].txt", false, true},
		{"[āē].txt", "ē.txt", false, true},
		// An unclosed "[" is a literal, as in Git.
		{"a[b", "a[b", false, true},
		{"a[b", "ab", false, false},
		{"x[", "x[", false, true},
		// Patterns are normalised like paths: an NFD pattern matches an NFC path.
		{"re\u0301sume\u0301.pdf", "r\u00e9sum\u00e9.pdf", false, true},
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
	if _, err := newIgnorer("ok\n[[:nope:]]"); err == nil || !strings.Contains(err.Error(), "line 2") {
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
		"wardlock.json":     fmt.Sprintf(`{"server": %q, "site": "docs", "dir": "build", "spa": true, "immutableAssets": true}`, srv.URL+"/"),
		"build/index.html":  "app",
		"other/index.html":  "other",
		"top-level-file.md": "not deployed",
	})
	if err := run(t.Context(), a, []string{"deploy"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	req := srv.created[0]
	if got := manifestPaths(req); !slices.Equal(got, []string{"index.html"}) || !*req.Spa || !*req.ImmutableAssets || srv.sites["docs"] == nil {
		t.Fatalf("deployed %q spa %v immutableAssets %v to %v", got, *req.Spa, *req.ImmutableAssets, slices.Collect(maps.Keys(srv.sites)))
	}

	// Flags and arguments override every field.
	other := newFakeAPI(t)
	out.Reset()
	if err := run(t.Context(), a, []string{"deploy", "other", "blog", "--spa=false", "--immutable-assets=false", "--server", other.URL}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	req = other.created[0]
	if *req.Spa || *req.ImmutableAssets || other.sites["blog"] == nil || len(srv.created) != 1 {
		t.Fatalf("spa %v immutableAssets %v sites %v", *req.Spa, *req.ImmutableAssets, slices.Collect(maps.Keys(other.sites)))
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
		{"wrong type for immutableAssets", `{"immutableAssets": 1}`, "immutableAssets"},
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
	if err := run(t.Context(), a, []string{"deploy", "--help"}); err != nil || out.Len() != 0 || !strings.Contains(errOut.String(), "-spa") || !strings.Contains(errOut.String(), "-immutable-assets") {
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
	// Hold uploads until uploadWorkers are in flight together.
	srv.barrier, srv.reached = uploadWorkers, make(chan struct{})
	a, _, errOut := ciApp(t, srv)
	files := map[string]string{}
	for i := range 40 {
		files[fmt.Sprintf("f%02d.txt", i)] = strings.Repeat("x", 64<<10) + fmt.Sprint(i)
	}
	writeFiles(t, workDir(t, a), files)
	if err := run(t.Context(), a, []string{"deploy", ".", "docs"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if len(srv.uploads) != 40 || srv.maxInFlight != uploadWorkers {
		t.Fatalf("uploads %d, at most %d at once; want %d", len(srv.uploads), srv.maxInFlight, uploadWorkers)
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
	if !strings.Contains(errOut.String(), "\rHashing files: 1 of 2") || !strings.Contains(errOut.String(), "\rUploaded 2 files") ||
		strings.Contains(errOut.String(), "\033") {
		t.Fatalf("progress %q", errOut)
	}
	// A shorter line pads over a longer one.
	var b bytes.Buffer
	p := &progress{w: &b, live: true, now: time.Now}
	p.update("Uploading: 10 of 10")
	p.done("Done.")
	if b.String() != "\rUploading: 10 of 10\rDone.              \n" {
		t.Fatalf("redraw %q", b.String())
	}
}

func TestDeployMessageCountsCharacters(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
	// 300 characters, 600 bytes: within the API's 500-character limit.
	msg := strings.Repeat("ā", 300)
	if err := run(t.Context(), a, []string{"deploy", ".", "docs", "--message", msg}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if got := *srv.created[0].Message; got != msg {
		t.Fatalf("message %q", got)
	}
	errOut.Reset()
	err := run(t.Context(), a, []string{"deploy", ".", "docs", "--message", strings.Repeat("ā", 501)})
	if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), "--message is 501 characters; the limit is 500") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestDeployToNewHostnameUnderBaseDomain(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	a, out, errOut := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
	if err := run(t.Context(), a, []string{"deploy", ".", "Blog." + fakeBase, "--json"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	var res deployResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || !res.SiteCreated || res.Site.Name != "blog" || srv.sites["blog"] == nil {
		t.Fatalf("result %+v (%v)", res, err)
	}

	tests := []struct {
		name   string
		sites  []apiclient.Site
		domain string
		want   string
	}{
		{"no sites to infer from", nil, "blog." + fakeBase, "with no sites to go by"},
		{"not under the base domain", []apiclient.Site{{Name: "docs", Hostname: "docs." + fakeBase}}, "blog.example.com", "not a site address under " + fakeBase + ": to create a site, deploy to its name, such as blog"},
		{"two labels", []apiclient.Site{{Name: "docs", Hostname: "docs." + fakeBase}}, "a.b." + fakeBase, "not a site address under"},
		{"label not a site name", []apiclient.Site{{Name: "docs", Hostname: "docs." + fakeBase}}, "x_y." + fakeBase, "not a site address under"},
		{"sites disagree", []apiclient.Site{{Name: "docs", Hostname: "docs." + fakeBase}, {Name: "old", Hostname: "old.other.test"}}, "blog." + fakeBase, "your sites are on different base domains"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			for _, s := range tt.sites {
				srv.addSite(s.Name, "").site.Hostname = s.Hostname
			}
			a, _, errOut := ciApp(t, srv)
			writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
			err := run(t.Context(), a, []string{"deploy", ".", tt.domain})
			if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if len(srv.sites) != len(tt.sites) {
				t.Fatalf("created a site: %v", slices.Collect(maps.Keys(srv.sites)))
			}
		})
	}
}

func TestBaseDomain(t *testing.T) {
	tests := []struct {
		sites []apiclient.Site
		want  string
		ok    bool
	}{
		{nil, "", false},
		{[]apiclient.Site{{Name: "docs", Hostname: "docs.example.test"}}, "example.test", true},
		{[]apiclient.Site{{Name: "docs", Hostname: "DOCS.Example.test"}, {Name: "a", Hostname: "a.example.test"}}, "example.test", true},
		{[]apiclient.Site{{Name: "docs", Hostname: "docs.example.test"}, {Name: "a", Hostname: "a.other.test"}}, "", false},
		{[]apiclient.Site{{Name: "docs", Hostname: "elsewhere.test"}}, "", false},
	}
	for _, tt := range tests {
		if got, ok := baseDomain(tt.sites); got != tt.want || ok != tt.ok {
			t.Errorf("baseDomain(%v) = %q, %v; want %q, %v", tt.sites, got, ok, tt.want, tt.ok)
		}
	}
}

func TestUploadRetryPolicy(t *testing.T) {
	tests := []struct {
		name    string
		fail    []int
		ok      bool
		uploads int // PUTs answered after the failures
		sleeps  []time.Duration
		want    string
	}{
		{"dropped connection is retried", []int{dropConnection}, true, 1, []time.Duration{time.Second}, ""},
		{"429 honours Retry-After, capped", []int{http.StatusTooManyRequests}, true, 1, []time.Duration{maxRetryAfter}, ""},
		{"429 without Retry-After backs off", []int{rateLimitedBare, rateLimitedBare}, true, 1, []time.Duration{time.Second, 2 * time.Second}, ""},
		{"503 then 504 retried with backoff", []int{503, 504}, true, 1, []time.Duration{time.Second, 2 * time.Second}, ""},
		{"501 is not retried", []int{http.StatusNotImplemented}, false, 0, nil, "Internal server error."},
		{"file changed", []int{hashMismatch}, false, 0, nil, "index.html changed while deploying: run wardlock deploy again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			srv.failUploads = tt.fail
			a, _, errOut := ciApp(t, srv)
			var sleeps []time.Duration
			a.sleep = func(_ context.Context, d time.Duration) error {
				sleeps = append(sleeps, d)
				return nil
			}
			writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
			err := run(t.Context(), a, []string{"deploy", ".", "docs"})
			if (err == nil) != tt.ok || err != nil && !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, stderr %q", err, errOut)
			}
			if len(srv.uploads) != tt.uploads || !slices.Equal(sleeps, tt.sleeps) || len(srv.failUploads) != 0 {
				t.Fatalf("uploads %v, sleeps %v, unused failures %v", srv.uploads, sleeps, srv.failUploads)
			}
			if !tt.ok && srv.sites["docs"].site.ActiveDeployId != nil {
				t.Fatal("activated after a failed upload")
			}
		})
	}
}

func TestDeployDirIsASymlink(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	wd := workDir(t, a)
	writeFiles(t, filepath.Join(wd, "build-123"), map[string]string{"index.html": "x", "a/b.css": "y"})
	if err := os.Symlink(filepath.Join(wd, "build-123"), filepath.Join(wd, "dist")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := run(t.Context(), a, []string{"deploy", "dist", "docs"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if got := manifestPaths(srv.created[0]); !slices.Equal(got, []string{"a/b.css", "index.html"}) {
		t.Fatalf("deployed %q", got)
	}
}

// TestDeployImmutableAssetsFlag: --immutable-assets alone turns the option
// on, and the deploy JSON reports it.
func TestDeployImmutableAssetsFlag(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"index.html": "x", "assets/index-BdK3j2Lp.js": "y"})
	if err := run(t.Context(), a, []string{"deploy", ".", "docs", "--immutable-assets", "--json"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if req := srv.created[0]; !*req.ImmutableAssets || *req.Spa {
		t.Fatalf("immutableAssets %v spa %v", *req.ImmutableAssets, *req.Spa)
	}
	var res deployResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || !res.Deploy.ImmutableAssets {
		t.Fatalf("deploy %+v (%v)", res.Deploy, err)
	}
}
