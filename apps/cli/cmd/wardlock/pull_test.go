// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// tree reads every regular file under dir, by slash path.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func wantTree(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	got := tree(t, dir)
	if len(got) != len(want) {
		t.Fatalf("%s holds %d files, want %d: %v", dir, len(got), len(want), keys(got))
	}
	for p, body := range want {
		if got[p] != body {
			t.Fatalf("%s/%s = %q, want %q", dir, p, got[p], body)
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDeployThenPullRoundTrip deploys a fixture site and pulls it back:
// the tree is byte for byte the one deployed, binary files included.
func TestDeployThenPullRoundTrip(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	fixture, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "fixtures", "sites", "basic"))
	if err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), a, []string{"deploy", fixture, "docs"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	out.Reset()
	errOut.Reset()
	if err := run(t.Context(), a, []string{"pull", "docs", "--json"}); err != nil {
		t.Fatalf("pull: %v\n%s", err, errOut)
	}
	dir := filepath.Join(workDir(t, a), "docs")
	want := tree(t, fixture)
	wantTree(t, dir, want)

	m := assertJSONKeys(t, out.Bytes(), "site", "deploy", "dir", "files", "bytes")
	var res pullResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, body := range want {
		total += int64(len(body))
	}
	if res.Site.Name != "docs" || res.Deploy.Id != *srv.sites["docs"].site.ActiveDeployId || res.Dir != dir ||
		res.Files != len(want) || res.Bytes != total {
		t.Fatalf("--json = %s", out)
	}
	if _, ok := m["deploy"]; !ok || !strings.Contains(errOut.String(), fmt.Sprintf("Downloaded %d files", len(want))) {
		t.Fatalf("progress:\n%s", errOut)
	}
	assertNoToken(t, out.String(), errOut.String())
}

func TestPullText(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	srv.publish(s, map[string]string{"index.html": "<h1>Hi</h1>", "a/b/c.txt": "deep", "empty.txt": ""})
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"pull", "docs.wardlock.example.test", "site-copy"}); err != nil {
		t.Fatalf("pull: %v\n%s", err, errOut)
	}
	if !strings.HasPrefix(out.String(), "Pulled 3 files (15 B) of deploy ") || !strings.Contains(out.String(), " of docs into site-copy in ") {
		t.Fatalf("output:\n%s", out)
	}
	wantTree(t, filepath.Join(workDir(t, a), "site-copy"), map[string]string{"index.html": "<h1>Hi</h1>", "a/b/c.txt": "deep", "empty.txt": ""})
}

func TestPullNonEmptyDir(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	srv.publish(s, map[string]string{"index.html": "new", "assets/app.js": "js"})
	a, out, errOut := ciApp(t, srv)
	dir := filepath.Join(workDir(t, a), "docs")
	writeFiles(t, dir, map[string]string{"index.html": "old", "notes.txt": "mine"})

	err := run(t.Context(), a, []string{"pull", "docs"})
	if err == nil || exitCode(&bytes.Buffer{}, err) != 1 || !strings.Contains(err.Error(), "docs is not empty") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("pull into a non-empty dir: %v", err)
	}
	if len(srv.downloads) != 0 || out.Len() != 0 {
		t.Fatalf("downloaded %v before refusing", srv.downloads)
	}
	wantTree(t, dir, map[string]string{"index.html": "old", "notes.txt": "mine"})

	if err := run(t.Context(), a, []string{"pull", "docs", "--force"}); err != nil {
		t.Fatalf("pull --force: %v\n%s", err, errOut)
	}
	// Files with the same names are replaced; others are left alone.
	wantTree(t, dir, map[string]string{"index.html": "new", "assets/app.js": "js", "notes.txt": "mine"})

	// A file where DIR should be.
	writeFiles(t, workDir(t, a), map[string]string{"plain": "x"})
	if err := run(t.Context(), a, []string{"pull", "docs", "plain"}); err == nil {
		t.Fatal("pulled into a file")
	}
}

// A site name from the server names the default DIR, so one that is not
// a plain site name is refused before anything is written.
func TestPullRefusesInvalidSiteName(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	srv.publish(s, map[string]string{"index.html": "x"})
	s.site.Name = "../escape"
	a, _, _ := ciApp(t, srv)

	err := run(t.Context(), a, []string{"pull", "docs"})
	if err == nil || !strings.Contains(err.Error(), "invalid site name") {
		t.Fatalf("pull with a bad site name: %v", err)
	}
	if len(srv.downloads) != 0 {
		t.Fatalf("downloaded %v", srv.downloads)
	}
	if _, err := os.Stat(filepath.Join(workDir(t, a), "..", "escape")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("created a directory outside the working directory: %v", err)
	}
}

func TestPullDeployAndPath(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	first := srv.publish(s, map[string]string{"index.html": "one", "mockup-x/index.html": "mock one", "mockup-xy/a.txt": "not under"})
	srv.publish(s, map[string]string{"index.html": "two"})
	a, out, errOut := ciApp(t, srv)
	wd := workDir(t, a)

	short := strings.ToUpper(first.Id[len(first.Id)-10:])
	if err := run(t.Context(), a, []string{"pull", "docs", "old", "--deploy", short}); err != nil {
		t.Fatalf("pull --deploy: %v\n%s", err, errOut)
	}
	wantTree(t, filepath.Join(wd, "old"), map[string]string{"index.html": "one", "mockup-x/index.html": "mock one", "mockup-xy/a.txt": "not under"})

	// --path takes the prefix with or without slashes and writes the
	// files relative to it, as deploy --path reads them.
	for i, p := range []string{"mockup-x", "/mockup-x/"} {
		out.Reset()
		d := filepath.Join(wd, "mock", string(rune('a'+i)))
		if err := run(t.Context(), a, []string{"pull", "docs", d, "--deploy", first.Id, "--path", p, "--json"}); err != nil {
			t.Fatalf("pull --path %s: %v\n%s", p, err, errOut)
		}
		wantTree(t, d, map[string]string{"index.html": "mock one"})
		var res pullResult
		if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Files != 1 || res.Bytes != 8 || res.Deploy.Id != first.Id {
			t.Fatalf("--json = %s", out)
		}
	}
	err := run(t.Context(), a, []string{"pull", "docs", "none", "--path", "/nothing/"})
	if err == nil || !strings.Contains(err.Error(), "no files under /nothing/") {
		t.Fatalf("empty prefix: %v", err)
	}

	for args, want := range map[string]string{
		"--deploy 0000000000": "has no deploy 0000000000",
		"--deploy zz":         "not a deploy ID",
	} {
		err := run(t.Context(), a, append([]string{"pull", "docs", "x"}, strings.Fields(args)...))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
	// A short ID matching two deploys is refused.
	srv.mu.Lock()
	s.deploys[1].Id = s.deploys[1].Id[:24] + first.Id[24:]
	srv.mu.Unlock()
	err = run(t.Context(), a, []string{"pull", "docs", "y", "--deploy", first.Id[len(first.Id)-8:]})
	if err == nil || !strings.Contains(err.Error(), "matches 2 deploys") {
		t.Fatalf("ambiguous short ID: %v", err)
	}
}

func TestPullRefusesUnsafePaths(t *testing.T) {
	for _, bad := range []string{"../escape.txt", "a/../../escape.txt", "/etc/passwd", "a//b", "./a", `a\b`, "a/\x01b"} {
		t.Run(bad, func(t *testing.T) {
			srv := newFakeAPI(t)
			s := srv.addSite("docs", "")
			srv.publish(s, map[string]string{"index.html": "ok", bad: "evil"})
			a, out, _ := ciApp(t, srv)
			err := run(t.Context(), a, []string{"pull", "docs"})
			if err == nil || !strings.Contains(err.Error(), "cannot pull "+bad) {
				t.Fatalf("pull = %v, want the path named", err)
			}
			if out.Len() != 0 || len(srv.downloads) != 0 {
				t.Fatalf("downloaded %v", srv.downloads)
			}
			// Nothing was written, inside or outside DIR.
			parent := filepath.Dir(workDir(t, a))
			if _, err := os.Stat(filepath.Join(workDir(t, a), "docs")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("created DIR: %v", err)
			}
			if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("wrote outside DIR")
			}
		})
	}
}

func TestPlanPullLocalNames(t *testing.T) {
	files := func(paths ...string) []apiclient.DeployFile {
		var out []apiclient.DeployFile
		for _, p := range paths {
			out = append(out, apiclient.DeployFile{Path: p, Sha256: strings.Repeat("a", 64)})
		}
		return out
	}
	tests := []struct {
		goos  string
		paths []string
		want  string // "" for no error
	}{
		{"linux", []string{"index.html", "a:b.txt", "CON.txt", "trailing.", "Index.html"}, ""},
		{"windows", []string{"index.html", "docs/guide.html"}, ""},
		{"windows", []string{"a:b.txt"}, `cannot pull a:b.txt: a name with ":" cannot be held on Windows`},
		{"windows", []string{"what?.html"}, `cannot pull what?.html`},
		{"windows", []string{`say"hi".txt`}, `cannot pull say"hi".txt`},
		{"windows", []string{"docs/CON.txt"}, "cannot pull docs/CON.txt: CON.txt is a device name on Windows"},
		{"windows", []string{"lpt1"}, "device name"},
		{"windows", []string{"com¹.log"}, "device name"},
		{"windows", []string{"dir./a.txt"}, "ending with a dot or space cannot be held"},
		{"windows", []string{"a /b.txt"}, "ending with a dot or space cannot be held"},
		{"windows", []string{"Index.html", "index.html"}, "cannot pull index.html: it would be the same file as Index.html"},
		{"darwin", []string{"café.html", "café.html"}, "would be the same file"},
		{"linux", []string{"page", "page/index.html"}, "cannot pull page/index.html: page is a file in this deploy"},
		{"linux", []string{strings.Repeat("n", 256)}, "longer than file systems allow"},
		{"linux", []string{"a\x7fb"}, "control character"},
		{"linux", []string{"\xff.html"}, "not valid UTF-8"},
	}
	for _, tt := range tests {
		_, err := planPull(files(tt.paths...), "", tt.goos)
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%s %q: %v, want %q", tt.goos, tt.paths, err, tt.want)
		}
	}
	// Under a prefix, names are relative to it and checked from there.
	plan, err := planPull(files("docs/a.html", "docs/b/c.html", "other.html"), "docs/", "linux")
	if err != nil || len(plan) != 2 || plan[1].name != filepath.FromSlash("b/c.html") {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}

func TestPullHashMismatch(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	srv.publish(s, map[string]string{"index.html": "the real page"})
	srv.failDownloads = []int{corruptBody}
	a, out, _ := ciApp(t, srv)
	err := run(t.Context(), a, []string{"pull", "docs", "--json"})
	if err == nil || !strings.Contains(err.Error(), "index.html did not match its SHA-256") {
		t.Fatalf("pull = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout on failure: %s", out)
	}
	// Neither the bad file nor its temporary file is left.
	wantTree(t, filepath.Join(workDir(t, a), "docs"), map[string]string{})
}

func TestPullRetries(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	srv.publish(s, map[string]string{"index.html": "page"})
	srv.failDownloads = []int{http.StatusServiceUnavailable, truncateBody}
	a, _, errOut := ciApp(t, srv)
	start := a.now()
	if err := run(t.Context(), a, []string{"pull", "docs"}); err != nil {
		t.Fatalf("pull: %v\n%s", err, errOut)
	}
	if got := a.now().Sub(start).Seconds(); got != 3 {
		t.Fatalf("waited %vs, want 1 + 2", got)
	}
	wantTree(t, filepath.Join(workDir(t, a), "docs"), map[string]string{"index.html": "page"})

	// 429 waits for Retry-After, capped at 30 seconds.
	srv.failDownloads = []int{http.StatusTooManyRequests}
	start = a.now()
	if err := run(t.Context(), a, []string{"pull", "docs", "again"}); err != nil {
		t.Fatalf("pull after 429: %v\n%s", err, errOut)
	}
	if got := a.now().Sub(start).Seconds(); got != 30 {
		t.Fatalf("waited %vs after 429, want 30", got)
	}

	// Three failures in a row stop the pull; a 404 is not retried.
	for _, fails := range [][]int{{502, 502, 502}, {http.StatusNotFound}} {
		srv.failDownloads = fails
		srv.downloads = map[string]int{}
		err := run(t.Context(), a, []string{"pull", "docs", "failed", "--force"})
		if err == nil || !strings.Contains(err.Error(), "download index.html") {
			t.Fatalf("pull with %v = %v", fails, err)
		}
		if total := srv.downloads[s.deploys[0].hashes[0]]; total != len(fails) {
			t.Fatalf("with %v tried %d times", fails, total)
		}
	}
}

func TestPullDownloadsEightAtATime(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	files := map[string]string{}
	for i := range 20 {
		files[strings.Repeat("f", i+1)+".txt"] = strings.Repeat("x", i)
	}
	srv.publish(s, files)
	srv.barrier, srv.reached = downloadWorkers, make(chan struct{})
	a, _, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"pull", "docs"}); err != nil {
		t.Fatalf("pull: %v\n%s", err, errOut)
	}
	if srv.maxDlInFlight != downloadWorkers {
		t.Fatalf("at most %d downloads at once, want %d", srv.maxDlInFlight, downloadWorkers)
	}
	wantTree(t, filepath.Join(workDir(t, a), "docs"), files)
}

func TestPullWithheldAndUnavailable(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	d := srv.publish(s, map[string]string{"index.html": "page", "bad.png": "withheld bytes"})
	for _, f := range d.files {
		if f.Path == "bad.png" {
			srv.withheld[f.Sha256] = true
		}
	}
	a, out, errOut := ciApp(t, srv)
	err := run(t.Context(), a, []string{"pull", "docs", "--json"})
	if err == nil || exitCode(&bytes.Buffer{}, err) != 1 || !strings.Contains(err.Error(), "1 file withheld") {
		t.Fatalf("pull = %v", err)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "has withheld bad.png") {
		t.Fatalf("stdout %q, stderr:\n%s", out, errOut)
	}
	wantTree(t, filepath.Join(workDir(t, a), "docs"), map[string]string{"index.html": "page"})

	srv.takenDown["docs"] = true
	err = run(t.Context(), a, []string{"pull", "docs", "down"})
	if err == nil || !strings.Contains(err.Error(), "taken this site down") {
		t.Fatalf("taken-down pull = %v", err)
	}

	empty := srv.addSite("empty", "")
	_ = empty
	err = run(t.Context(), a, []string{"pull", "empty"})
	if err == nil || !strings.Contains(err.Error(), "nothing is live on empty") {
		t.Fatalf("pull of an empty site = %v", err)
	}
	err = run(t.Context(), a, []string{"pull", "missing"})
	if err == nil || !strings.Contains(err.Error(), "no site missing") {
		t.Fatalf("pull of a missing site = %v", err)
	}
	if err := run(t.Context(), a, []string{"pull"}); !errors.Is(err, errUsage) {
		t.Fatalf("pull without DOMAIN = %v", err)
	}
}
