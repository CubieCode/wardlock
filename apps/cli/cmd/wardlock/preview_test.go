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

// Preview deploys (C1.19): deploy --preview and --expires, and wardlock
// preview list, delete and promote.

const previewURLOf = "https://docs--pr-42." + fakeBase + "/"

func TestDeployPreview(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	live := deref(srv.sites["docs"].site.ActiveDeployId)
	writeFiles(t, filepath.Join(workDir(t, a), "next"), map[string]string{"index.html": "next"})
	out.Reset()
	if err := run(t.Context(), a, []string{"deploy", "next", "docs", "--preview", "pr-42"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	req := srv.created[0]
	if deref(req.Preview) != "pr-42" || req.PreviewExpires != nil {
		t.Fatalf("request preview %v expires %v", req.Preview, req.PreviewExpires)
	}
	if got := deref(srv.sites["docs"].site.ActiveDeployId); got != live {
		t.Fatalf("live deploy moved to %s", got)
	}
	if text := out.String(); !strings.Contains(text, "Deployed 1 file (4 B) to preview pr-42 of docs") ||
		!strings.Contains(text, "expires ") || !strings.HasSuffix(text, previewURLOf+"\n") {
		t.Fatalf("output:\n%s", text)
	}

	// --expires is sent as given; --json adds preview and url.
	for _, c := range []struct{ expires, want string }{{"never", "never expires"}, {"30d", "expires "}} {
		out.Reset()
		if err := run(t.Context(), a, []string{"deploy", "next", "docs", "--preview", "pr-42", "--expires", c.expires}); err != nil {
			t.Fatal(err)
		}
		if got := deref(srv.created[len(srv.created)-1].PreviewExpires); got != c.expires || !strings.Contains(out.String(), c.want) {
			t.Fatalf("--expires %s: sent %q, output %q", c.expires, got, out)
		}
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "deploy", "next", "docs", "--preview", "pr-42"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "deploy", "preview", "url", "siteCreated", "uploaded", "skipped")
	if string(m["preview"]) != `"pr-42"` || string(m["url"]) != `"`+previewURLOf+`"` {
		t.Fatalf("preview %s url %s", m["preview"], m["url"])
	}
	d := assertJSONKeys(t, m["deploy"], "id", "status", "createdAt", "fileCount", "totalSize", "spa", "immutableAssets", "preview")
	if string(d["status"]) != `"preview"` {
		t.Fatalf("deploy %s", m["deploy"])
	}
	// A live deploy's --json has neither.
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "deploy", "next", "docs"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "site", "deploy", "siteCreated", "uploaded", "skipped")
}

func TestDeployPathPreview(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "mock"), map[string]string{"index.html": "new"})
	out.Reset()
	if err := run(t.Context(), a, []string{"deploy", "mock", "docs", "--path", "/mockup-x/", "--preview", "pr-42"}); err != nil {
		t.Fatalf("deploy: %v\n%s", err, errOut)
	}
	if text := out.String(); !strings.Contains(text, "to /mockup-x/ of preview pr-42 of docs") || !strings.HasSuffix(text, previewURLOf+"mockup-x/\n") {
		t.Fatalf("output:\n%s", text)
	}
	if want := []string{"index.html", "mockup-x/old.html"}; !slices.Equal(livePaths(t, srv, "docs"), want) {
		t.Fatalf("live %q, want it unchanged", livePaths(t, srv, "docs"))
	}
	// Emptying the prefix of a preview changes nothing live: no --yes needed.
	if err := os.Mkdir(filepath.Join(workDir(t, a), "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), a, []string{"deploy", "empty", "docs", "--path", "/mockup-x/", "--preview", "pr-42"}); err != nil {
		t.Fatalf("emptying a preview's path: %v\n%s", err, errOut)
	}
}

func TestDeployPreviewUsage(t *testing.T) {
	srv := newFakeAPI(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--expires", "14d"}, "--expires needs --preview"},
		{[]string{"--preview", "pr-42", "--expires", "91d"}, "1d to 90d"},
		{[]string{"--preview", "pr-42", "--expires", "0d"}, "1d to 90d"},
		{[]string{"--preview", "pr-42", "--expires", "2w"}, "1d to 90d"},
		{[]string{"--preview", "PR-42"}, "preview name"},
		{[]string{"--preview", "a--b"}, "preview name"},
		{[]string{"--preview", strings.Repeat("p", 21)}, "preview name"},
	} {
		a, _, errOut := ciApp(t, srv)
		writeFiles(t, workDir(t, a), map[string]string{"index.html": "x"})
		err := run(t.Context(), a, append([]string{"deploy", ".", "docs"}, c.args...))
		if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), c.want) {
			t.Errorf("%q: exit %d %q", c.args, code, errOut)
		}
	}
	if len(srv.created) != 0 {
		t.Fatalf("a usage error reached the server: %+v", srv.created)
	}
}

func TestDeployPreviewRefusesServerWithoutPreviews(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "next"), map[string]string{"index.html": "next"})
	srv.ignorePreview = true
	srv.uploads = map[string]int{}
	err := run(t.Context(), a, []string{"deploy", "next", "docs", "--preview", "pr-42"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "does not support preview deploys") {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if len(srv.uploads) != 0 {
		t.Fatalf("uploaded %v", srv.uploads)
	}
	if want := []string{"index.html", "mockup-x/old.html"}; !slices.Equal(livePaths(t, srv, "docs"), want) {
		t.Fatalf("live %q, want it unchanged", livePaths(t, srv, "docs"))
	}
}

// previewSite deploys docs and its preview pr-42.
func previewSite(t *testing.T, srv *fakeAPI, a *app) {
	t.Helper()
	baseSite(t, srv, a)
	writeFiles(t, filepath.Join(workDir(t, a), "next"), map[string]string{"index.html": "next"})
	if err := run(t.Context(), a, []string{"deploy", "next", "docs", "--preview", "pr-42", "--message", "Try the new nav"}); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewList(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	srv.addSite("blog", "")
	out.Reset()
	if err := run(t.Context(), a, []string{"preview", "list", "blog"}); err != nil || !strings.Contains(out.String(), "blog has no previews") {
		t.Fatalf("empty list: %v %q", err, out)
	}
	previewSite(t, srv, a)
	// Three previews page past the fake's page size.
	for _, n := range []string{"alpha", "zeta"} {
		if err := run(t.Context(), a, []string{"deploy", "next", "docs", "--preview", n, "--expires", "never"}); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"preview", "list", "docs"}); err != nil {
		t.Fatalf("list: %v\n%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "NAME") || !strings.HasPrefix(lines[1], "alpha") ||
		!strings.Contains(lines[2], previewURLOf) || !strings.Contains(lines[2], "Try the new nav") || !strings.Contains(lines[1], "never") {
		t.Fatalf("list:\n%s", out)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "preview", "list", "docs"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "site", "previews")
	var ps []apiclient.Preview
	if err := json.Unmarshal(m["previews"], &ps); err != nil || len(ps) != 3 || ps[1].Name != "pr-42" {
		t.Fatalf("previews %s", m["previews"])
	}
	// The deploy history marks preview deploys.
	out.Reset()
	if err := run(t.Context(), a, []string{"list", "docs"}); err != nil || !strings.Contains(out.String(), "preview pr-42") {
		t.Fatalf("list docs: %v\n%s", err, out)
	}
}

func TestPreviewDelete(t *testing.T) {
	for _, c := range []struct {
		name      string
		args      []string
		canPrompt bool
		stdin     string
		code      int
		deleted   bool
		want      string
	}{
		{"no terminal needs --yes", []string{"preview", "delete", "docs", "pr-42"}, false, "", 2, false, "pass --yes"},
		{"--json needs --yes", []string{"--json", "preview", "delete", "docs", "pr-42"}, true, "pr-42\n", 2, false, "pass --yes"},
		{"--yes", []string{"preview", "delete", "docs", "pr-42", "--yes"}, false, "", 0, true, "Deleted the preview pr-42 of docs"},
		{"typed name", []string{"preview", "delete", "docs", "pr-42"}, true, "pr-42\n", 0, true, "Type the preview name to confirm"},
		{"wrong name", []string{"preview", "delete", "docs", "pr-42"}, true, "pr-4\n", 1, false, "nothing was deleted"},
		{"missing preview", []string{"preview", "delete", "docs", "nope", "--yes"}, false, "", 1, false, "docs has no preview nope"},
		{"bad name", []string{"preview", "delete", "docs", "Nope", "--yes"}, false, "", 2, false, "preview name"},
		{"no name", []string{"preview", "delete", "docs", "--yes"}, false, "", 2, false, "Usage: wardlock preview delete"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			a, out, errOut := ciApp(t, srv)
			previewSite(t, srv, a)
			out.Reset()
			a.canPrompt = c.canPrompt
			a.stdin = strings.NewReader(c.stdin)
			err := run(t.Context(), a, c.args)
			if code := exitCode(errOut, err); code != c.code || !strings.Contains(out.String()+errOut.String(), c.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			if _, kept := srv.sites["docs"].previews["pr-42"]; kept == c.deleted {
				t.Fatalf("pr-42 kept = %v", kept)
			}
		})
	}
	srv := newFakeAPI(t)
	a, out, _ := ciApp(t, srv)
	previewSite(t, srv, a)
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "preview", "delete", "docs", "pr-42", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if m := assertJSONKeys(t, out.Bytes(), "deleted", "site", "preview"); string(m["preview"]) != `"pr-42"` {
		t.Fatalf("json %s", out)
	}
}

func TestPreviewPromote(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	previewSite(t, srv, a)
	p := srv.sites["docs"].previews["pr-42"]
	out.Reset()
	if err := run(t.Context(), a, []string{"preview", "promote", "docs", "pr-42"}); err != nil {
		t.Fatalf("promote: %v\n%s", err, errOut)
	}
	if deref(srv.sites["docs"].site.ActiveDeployId) != p.Deploy.Id {
		t.Fatal("the preview's deploy is not live")
	}
	if text := out.String(); !strings.Contains(text, "Promoted the preview pr-42. Deploy "+shortID(p.Deploy.Id)+" is live") || !strings.HasSuffix(text, "https://docs."+fakeBase+"/\n") {
		t.Fatalf("output:\n%s", text)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "preview", "promote", "docs", "pr-42"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "site", "deploy", "preview")

	// A path preview made before another deploy went live is stale.
	writeFiles(t, filepath.Join(workDir(t, a), "mock"), map[string]string{"index.html": "mock"})
	if err := run(t.Context(), a, []string{"deploy", "mock", "docs", "--path", "/mockup-x/", "--preview", "mock"}); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), a, []string{"deploy", "next", "docs"}); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	err := run(t.Context(), a, []string{"preview", "promote", "docs", "mock"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "deploy the preview again, then promote it") {
		t.Fatalf("stale promote: exit %d %q", code, errOut)
	}
	errOut.Reset()
	err = run(t.Context(), a, []string{"preview", "promote", "docs", "nope"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "docs has no preview nope") {
		t.Fatalf("missing: exit %d %q", code, errOut)
	}
}

func TestPreviewUsage(t *testing.T) {
	srv := newFakeAPI(t)
	for _, args := range [][]string{{"preview"}, {"preview", "rename", "docs"}, {"preview", "list"}, {"preview", "promote", "docs"}} {
		a, _, errOut := ciApp(t, srv)
		if code := exitCode(errOut, run(t.Context(), a, args)); code != 2 {
			t.Errorf("%q: exit %d %q", args, code, errOut)
		}
	}
	a, out, _ := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"preview", "help"}); err != nil || !strings.Contains(out.String(), "deploy DIR DOMAIN --preview NAME") {
		t.Fatalf("help: %v %q", err, out)
	}
}
