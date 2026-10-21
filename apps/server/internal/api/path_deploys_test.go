// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"net/http"
	"slices"
	"testing"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
)

// TestPathDeploys runs a path deploy (C1.13) through the API: the prefix
// in the Deploy object and the listing, an empty upload, the stale base
// 409 and a retry.
func TestPathDeploys(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	full := hs.deploy(a, "docs", index, app)
	if full.Deploy.Path != nil {
		t.Fatalf("full deploy path = %q, want absent", *full.Deploy.Path)
	}

	req := manifest(index)
	req.Path = new("/mockup-x/")
	c := hs.createDeploy(a, "docs", req)
	if c.Deploy.Path == nil || *c.Deploy.Path != "/mockup-x/" || c.Deploy.FileCount != 3 || len(c.Missing) != 0 {
		t.Fatalf("created %+v missing %v, want /mockup-x/ with 3 files and nothing to upload", c.Deploy, c.Missing)
	}
	// Another path deploy on the same base, then the first goes live.
	other := manifest(app)
	other.Path = new("/mockup-y/")
	stale := hs.createDeploy(a, "docs", other)
	act := hs.activate(a, "docs", c.Deploy.Id)
	if act.Deploy.Path == nil || *act.Deploy.Path != "/mockup-x/" {
		t.Fatalf("activation deploy %+v", act.Deploy)
	}
	hs.do(a, http.MethodPost, "/sites/docs/deploys/"+stale.Deploy.Id+"/activate", nil).want(t, http.StatusConflict, "stale_base")
	retry := hs.activate(a, "docs", hs.createDeploy(a, "docs", other).Deploy.Id)
	if retry.Deploy.FileCount != 4 {
		t.Fatalf("retried deploy has %d files, want 4 (both prefixes kept)", retry.Deploy.FileCount)
	}

	// An empty upload removes the prefix; files may be left out with a path.
	removed := hs.do(a, http.MethodPost, "/sites/docs/deploys", map[string]any{"path": "/mockup-x/"})
	removed.want(t, http.StatusCreated, "")
	var rc api.CreatedDeploy
	removed.decode(t, &rc)
	if rc.Deploy.FileCount != 3 {
		t.Fatalf("removal deploy has %d files, want 3", rc.Deploy.FileCount)
	}

	var page api.DeployPage
	hs.do(a, http.MethodGet, "/sites/docs/deploys", nil).decode(t, &page)
	var prefixes []string
	for _, d := range page.Items {
		p := ""
		if d.Path != nil {
			p = *d.Path
		}
		prefixes = append(prefixes, p)
	}
	if want := []string{"/mockup-x/", "/mockup-y/", "/mockup-y/", "/mockup-x/", ""}; !slices.Equal(prefixes, want) {
		t.Fatalf("listed prefixes %q, want %q", prefixes, want)
	}
}

func TestPathDeployRefusals(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	hs.createSite(a, "docs")
	hs.deploy(a, "docs", index)
	for _, path := range []string{"", "/", "docs/", "/docs", "/.wardlock/", "/a/../b/", "/a//b/"} {
		req := manifest(index)
		req.Path = new(path)
		res := hs.do(a, http.MethodPost, "/sites/docs/deploys", req)
		res.want(t, http.StatusBadRequest, "invalid_path")
		if e := res.err(t); e.Target == nil || *e.Target != "path" {
			t.Errorf("path %q: target %v, want path", path, e.Target)
		}
	}
	// Without a path, files are still required.
	hs.do(a, http.MethodPost, "/sites/docs/deploys", map[string]any{"files": []any{}}).want(t, http.StatusBadRequest, "invalid_manifest")

	// Another owner's site answers exactly as a missing one.
	req := manifest(index)
	req.Path = new("/x/")
	got := hs.do(b, http.MethodPost, "/sites/docs/deploys", req)
	got.want(t, http.StatusNotFound, "not_found")
	if want := hs.do(b, http.MethodPost, "/sites/nosuchsite/deploys", req); !bytes.Equal(got.body, want.body) {
		t.Fatalf("foreign site answered %s, missing site %s", got.body, want.body)
	}
}
