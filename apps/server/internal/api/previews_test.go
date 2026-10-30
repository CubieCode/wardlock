// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// Preview deploys (C1.19) through the owner API.

// previewDeploy runs the deploy protocol to preview name with req's other
// settings and returns the activation.
func (hs *harness) previewDeploy(owner, site, name string, req api.CreateDeployRequest, files ...file) api.Activation {
	hs.t.Helper()
	m := manifest(files...)
	m.Preview, m.PreviewExpires, m.Path = &name, req.PreviewExpires, req.Path
	c := hs.createDeploy(owner, site, m)
	for _, f := range files {
		if slices.Contains(c.Missing, f.hash()) {
			hs.upload(owner, site, c.Deploy.Id, f).want(hs.t, http.StatusNoContent, "")
		}
	}
	return hs.activate(owner, site, c.Deploy.Id)
}

func (hs *harness) previews(owner, site, query string) api.PreviewPage {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/sites/"+site+"/previews"+query, nil)
	res.want(hs.t, http.StatusOK, "")
	var page api.PreviewPage
	res.decode(hs.t, &page)
	return page
}

func TestPreviewDeploys(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	live := hs.deploy(hs.ownerA, "docs", file{"index.html", "live"})

	a := hs.previewDeploy(hs.ownerA, "docs", "pr-42", api.CreateDeployRequest{}, file{"index.html", "preview"})
	if a.Preview == nil || a.Preview.Name != "pr-42" || a.Preview.Hostname != "docs--pr-42."+baseDomain ||
		a.Preview.Url != "http://docs--pr-42."+baseDomain+":8080/" || a.Preview.Deploy.Id != a.Deploy.Id || a.Preview.ExpiresAt == nil {
		t.Fatalf("activation preview = %+v", a.Preview)
	}
	if got := time.UnixMilli(*a.Preview.ExpiresAt).Sub(time.UnixMilli(a.Preview.DeployedAt)); got != 14*24*time.Hour {
		t.Fatalf("default lifetime = %s, want 14 days", got)
	}
	if a.Deploy.Status != api.DeployStatusPreview || a.Deploy.Preview == nil || *a.Deploy.Preview != "pr-42" || a.Deploy.ActivatedAt != nil {
		t.Fatalf("preview deploy = %+v", a.Deploy)
	}
	if a.Site.ActiveDeployId == nil || *a.Site.ActiveDeployId != live.Deploy.Id {
		t.Fatalf("the live site moved to %v", a.Site.ActiveDeployId)
	}

	// Replaced like a branch; "never" from the API.
	never := "never"
	b := hs.previewDeploy(hs.ownerA, "docs", "pr-42", api.CreateDeployRequest{PreviewExpires: &never}, file{"index.html", "preview 2"})
	if b.Preview.Deploy.Id == a.Deploy.Id || b.Preview.ExpiresAt != nil || b.Preview.CreatedAt != a.Preview.CreatedAt {
		t.Fatalf("replaced preview = %+v", b.Preview)
	}
	hs.previewDeploy(hs.ownerA, "docs", "alpha", api.CreateDeployRequest{}, file{"index.html", "alpha"})

	page := hs.previews(hs.ownerA, "docs", "?limit=1")
	if len(page.Items) != 1 || page.Items[0].Name != "alpha" || page.NextCursor == nil {
		t.Fatalf("first page = %+v", page)
	}
	page = hs.previews(hs.ownerA, "docs", "?limit=1&cursor="+*page.NextCursor)
	if len(page.Items) != 1 || page.Items[0].Name != "pr-42" || page.NextCursor != nil || page.Items[0].Deploy.Id != b.Deploy.Id {
		t.Fatalf("second page = %+v", page)
	}

	// History lists preview deploys, marked; rollback refuses them.
	var history api.DeployPage
	hs.do(hs.ownerA, http.MethodGet, "/sites/docs/deploys", nil).decode(t, &history)
	previews := 0
	for _, d := range history.Items {
		if d.Preview != nil {
			previews++
			if d.Status != api.DeployStatusPreview {
				t.Fatalf("history entry %+v", d)
			}
		}
	}
	if previews != 3 {
		t.Fatalf("history has %d preview deploys, want 3", previews)
	}
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/rollback", api.RollbackRequest{DeployId: &b.Deploy.Id}).want(t, http.StatusConflict, "deploy_not_activated")
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/rollback", nil).want(t, http.StatusConflict, "no_previous_deploy")

	// Promote puts it live, in history; the preview stays.
	res := hs.do(hs.ownerA, http.MethodPost, "/sites/docs/previews/pr-42/promote", nil)
	res.want(t, http.StatusOK, "")
	var promoted api.Activation
	res.decode(t, &promoted)
	if *promoted.Site.ActiveDeployId != b.Deploy.Id || promoted.Deploy.Status != api.DeployStatusActive || promoted.Deploy.ActivatedAt == nil ||
		promoted.Preview == nil || promoted.Preview.Deploy.Id != b.Deploy.Id {
		t.Fatalf("promotion = %+v", promoted)
	}
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/rollback", nil).want(t, http.StatusOK, "")

	// Delete; then it is gone.
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/previews/pr-42", nil).want(t, http.StatusNoContent, "")
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/previews/pr-42", nil).want(t, http.StatusNotFound, "not_found")
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/previews/pr-42/promote", nil).want(t, http.StatusNotFound, "not_found")
	if page := hs.previews(hs.ownerA, "docs", ""); len(page.Items) != 1 || page.Items[0].Name != "alpha" {
		t.Fatalf("previews after delete = %+v", page)
	}
}

func TestPreviewDeployRefusals(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	// A site named with "--" before the rule, straight in the store.
	if err := hs.db.Sites().Create(t.Context(), store.Site{ID: store.NewID(), OwnerID: hs.ownerB, Name: "docs--taken", CreatedAt: time.Now()}, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	for _, c := range []struct {
		name            string
		preview, expiry *string
		status          int
		code, target    string
	}{
		{"bad name", str("PR_1"), nil, http.StatusBadRequest, "invalid_preview_name", "preview"},
		{"double dash", str("a--b"), nil, http.StatusBadRequest, "invalid_preview_name", "preview"},
		{"label is a site's name, another owner's", str("taken"), nil, http.StatusConflict, "site_name_taken", "preview"},
		{"expiry without preview", nil, str("14d"), http.StatusBadRequest, "invalid_request", "previewExpires"},
		{"expiry over 90 days", str("pr-1"), str("91d"), http.StatusBadRequest, "invalid_request", "previewExpires"},
		{"zero days", str("pr-1"), str("0d"), http.StatusBadRequest, "invalid_request", ""},
		{"expiry in hours", str("pr-1"), str("12h"), http.StatusBadRequest, "invalid_request", ""},
	} {
		m := manifest(file{"index.html", "x"})
		m.Preview, m.PreviewExpires = c.preview, c.expiry
		res := hs.do(hs.ownerA, http.MethodPost, "/sites/docs/deploys", m)
		res.want(t, c.status, c.code)
		if e := res.err(t); c.target != "" && (e.Target == nil || *e.Target != c.target) {
			t.Errorf("%s: target %v, want %s", c.name, e.Target, c.target)
		}
	}
	// New site names may not contain "--".
	res := hs.do(hs.ownerA, http.MethodPost, "/sites", api.CreateSiteRequest{Name: "docs--pr-1"})
	res.want(t, http.StatusBadRequest, "invalid_site_name")
	if e := res.err(t); !strings.Contains(e.Message, "'--'") {
		t.Fatalf("invalid_site_name message %q does not mention --", e.Message)
	}
	// The longest valid lifetime.
	m := manifest(file{"index.html", "x"})
	m.Preview, m.PreviewExpires = str("pr-1"), str("90d")
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/deploys", m).want(t, http.StatusCreated, "")
}

// TestPreviewIsolation: owner B cannot read, list, deploy to, delete or
// promote owner A's previews; every attempt answers exactly like a missing
// site or preview.
func TestPreviewIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	hs.deploy(hs.ownerA, "docs", file{"index.html", "live"})
	a := hs.previewDeploy(hs.ownerA, "docs", "pr-1", api.CreateDeployRequest{}, file{"index.html", "preview"})
	hs.createSite(hs.ownerB, "mine")

	missing := hs.do(hs.ownerA, http.MethodGet, "/sites/nosuch/previews", nil)
	missing.want(t, http.StatusNotFound, "not_found")
	name := "pr-2"
	m := manifest(file{"index.html", "theirs"})
	m.Preview = &name
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/sites/docs/previews", nil},
		{http.MethodDelete, "/sites/docs/previews/pr-1", nil},
		{http.MethodPost, "/sites/docs/previews/pr-1/promote", nil},
		{http.MethodPost, "/sites/docs/deploys", m},
		{http.MethodPost, "/sites/docs/deploys/" + a.Deploy.Id + "/activate", nil},
		{http.MethodDelete, "/sites/mine/previews/pr-1", nil}, // B's own site has no such preview
		{http.MethodPost, "/sites/mine/previews/pr-1/promote", nil},
	} {
		res := hs.do(hs.ownerB, c.method, c.path, c.body)
		if res.status != missing.status || string(res.body) != string(missing.body) {
			t.Errorf("%s %s by owner B = %d %s, want the missing-site answer", c.method, c.path, res.status, res.body)
		}
	}
	if page := hs.previews(hs.ownerB, "mine", ""); len(page.Items) != 0 {
		t.Fatalf("owner B's previews = %+v", page)
	}
	if page := hs.previews(hs.ownerA, "docs", ""); len(page.Items) != 1 || page.Items[0].Deploy.Id != a.Deploy.Id {
		t.Fatalf("owner A's previews after B's attempts = %+v", page)
	}
}

func TestPreviewAccessLog(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	docs := hs.createSite(hs.ownerA, "docs")
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond).UTC()
	preview := logEntry(base, 0, docs.Id, store.ViaPublic, "", "/", http.StatusOK)
	preview.Preview, preview.Host = "pr-1", "docs--pr-1."+baseDomain
	liveEntry := logEntry(base, time.Minute, docs.Id, store.ViaPublic, "", "/", http.StatusOK)
	if err := hs.db.AccessLog().Append(t.Context(), []store.AccessEntry{preview, liveEntry}); err != nil {
		t.Fatal(err)
	}
	page := hs.siteAccessLog(hs.ownerA, "docs", "")
	if len(page.Items) != 2 || page.Items[0].Preview != nil || page.Items[1].Preview == nil || *page.Items[1].Preview != "pr-1" {
		t.Fatalf("access log = %+v", page.Items)
	}
}
