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
)

// TestDeployFilesSite: deploy --group and --tag, and wardlock.json's group
// and tags, file the site once live, with a deploy-scoped token as with a
// manage-scoped one. Tags are added to the site's, never replacing them.
func TestDeployFilesSite(t *testing.T) {
	for _, scopes := range [][]string{{"deploy"}, {"deploy", "manage"}} {
		t.Run(strings.Join(scopes, "+"), func(t *testing.T) {
			srv := newFakeAPI(t)
			srv.scopes = scopes
			board := srv.addGroup("Board")
			preview, internal := srv.addTag("preview", 1), srv.addTag("internal", 2)
			a, out, errOut := ciApp(t, srv)
			wd := workDir(t, a)
			writeFiles(t, wd, map[string]string{"index.html": "one"})

			err := run(t.Context(), a, []string{"deploy", ".", "docs", "--group", "board", "--tag", "preview", "--json"})
			if err != nil {
				t.Fatalf("deploy: %v\n%s", err, errOut)
			}
			var res deployResult
			if err := json.Unmarshal(out.Bytes(), &res); err != nil || !res.SiteCreated || res.Site.GroupId == nil || *res.Site.GroupId != board || !slices.Equal(res.Site.TagIds, []string{preview}) {
				t.Fatalf("output %s (%v)", out, err)
			}
			assertJSONKeys(t, out.Bytes(), "site", "deploy", "siteCreated", "uploaded", "skipped")

			// wardlock.json's tags are added to the site's; its group stays
			// when none is named.
			writeFiles(t, wd, map[string]string{"index.html": "two", configFile: `{"site": "docs", "tags": ["internal"]}`})
			out.Reset()
			if err := run(t.Context(), a, []string{"deploy"}); err != nil {
				t.Fatalf("deploy: %v\n%s", err, errOut)
			}
			if g, tags, _ := srv.organisationOf("docs"); g != board || !slices.Equal(tags, []string{internal, preview}) {
				t.Fatalf("docs: group %q tags %q", g, tags)
			}
			if !strings.Contains(out.String(), "is live") {
				t.Fatalf("output %q", out)
			}

			// Flags override wardlock.json; nothing to change sends nothing.
			filed := len(srv.filed)
			writeFiles(t, wd, map[string]string{"index.html": "three"})
			if err := run(t.Context(), a, []string{"deploy", "--tag", "preview"}); err != nil {
				t.Fatalf("deploy: %v\n%s", err, errOut)
			}
			if len(srv.filed) != filed {
				t.Fatalf("filed %+v", srv.filed[filed:])
			}
			writeFiles(t, wd, map[string]string{configFile: `{"site": "docs", "group": "Nope"}`})
			if err := run(t.Context(), a, []string{"deploy", "--group", ""}); err != nil {
				t.Fatalf("--group '' should ignore wardlock.json's group: %v\n%s", err, errOut)
			}
		})
	}
}

// TestDeployUnknownGroupOrTag: an unknown name fails before anything is
// created or uploaded.
func TestDeployUnknownGroupOrTag(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		config string
		want   string
	}{
		{"flag tag", []string{"--tag", "preview", "--tag", "nope"}, "", `no tag named "nope"`},
		{"flag tags", []string{"--tag", "nope", "--tag", "gone"}, "", `no tags named "nope", "gone"`},
		{"flag group", []string{"--group", "Nope"}, "", `no group named "Nope"`},
		{"config tags", nil, `{"tags": ["nope"]}`, `no tag named "nope"`},
		{"config group", nil, `{"group": "Nope"}`, `no group named "Nope"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeAPI(t)
			srv.scopes = []string{"deploy"}
			srv.addTag("preview", 1)
			a, out, errOut := ciApp(t, srv)
			wd := workDir(t, a)
			writeFiles(t, wd, map[string]string{"dist/index.html": "hello"})
			if tc.config != "" {
				writeFiles(t, wd, map[string]string{configFile: tc.config})
			}
			err := run(t.Context(), a, append([]string{"deploy", "dist", "docs", "--json"}, tc.args...))
			if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), tc.want) || out.Len() != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if len(srv.sites) != 0 || len(srv.created) != 0 || len(srv.uploads) != 0 {
				t.Fatalf("changed something: %d sites, %d deploys, %d uploads", len(srv.sites), len(srv.created), len(srv.uploads))
			}
		})
	}
}

// TestDeployFilingFailsAfterActivation: when filing fails, the deploy is
// still live and the error says so.
func TestDeployFilingFailsAfterActivation(t *testing.T) {
	srv := newFakeAPI(t)
	s := srv.addSite("docs", "")
	var ids []string
	for i := range 21 {
		ids = append(ids, srv.addTag("t"+strings.Repeat("x", i), 0))
	}
	s.site.TagIds = ids[:20]
	a, out, errOut := ciApp(t, srv)
	wd := workDir(t, a)
	writeFiles(t, wd, map[string]string{"index.html": "hello"})
	err := run(t.Context(), a, []string{"deploy", ".", "docs", "--tag", "t" + strings.Repeat("x", 20), "--json"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "is live, but its group and tags were not set") || out.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if s.site.ActiveDeployId == nil {
		t.Fatal("not activated")
	}
}

func TestConfigGroupAndTags(t *testing.T) {
	cfg, err := parseConfig([]byte(`{"group": "Client work", "tags": ["preview", "internal"]}`))
	if err != nil || cfg.Group != "Client work" || !slices.Equal(cfg.Tags, []string{"preview", "internal"}) {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, bad := range []string{`{"group": 1}`, `{"tags": "preview"}`, `{"tag": ["preview"]}`} {
		if _, err := parseConfig([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// An invalid name in wardlock.json is a usage error before any request.
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	wd := workDir(t, a)
	if err := os.WriteFile(filepath.Join(wd, configFile), []byte(`{"site": "docs", "tags": ["Not Valid"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, wd, map[string]string{"index.html": "x"})
	srv.Close()
	err = run(t.Context(), a, []string{"deploy"})
	if code := exitCode(errOut, err); code != 2 || strings.Contains(errOut.String(), "could not reach") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
