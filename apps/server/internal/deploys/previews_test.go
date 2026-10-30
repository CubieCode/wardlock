// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// Preview deploys (C1.19).

func TestValidPreviewName(t *testing.T) {
	for name, want := range map[string]bool{
		"pr-42": true, "redesign": true, "a": true, "0": true, "a-b-c": true,
		strings.Repeat("a", 20): true,
		"":                      false, strings.Repeat("a", 21): false, "-pr": false, "pr-": false, "a--b": false,
		"PR": false, "pr_1": false, "pr.1": false, "prévu": false, "pr 1": false,
	} {
		if got := deploys.ValidPreviewName(name); got != want {
			t.Errorf("ValidPreviewName(%q) = %t, want %t", name, got, want)
		}
	}
}

// preview creates and activates a preview deploy of files to name.
func (w *pathWorld) preview(ownerID, site, name, path string, days *int, files map[string]string) store.Deploy {
	w.t.Helper()
	m := w.manifest(files)
	m.Path, m.Preview, m.PreviewDays = path, name, days
	d, _, err := w.create(ownerID, site, m)
	if err != nil {
		w.t.Fatalf("create preview %s of %s: %v", name, site, err)
	}
	if _, d, err = w.svc.Activate(w.t.Context(), ownerID, site, d.ID); err != nil {
		w.t.Fatalf("activate preview %s of %s: %v", name, site, err)
	}
	return d
}

func wantTarget(t *testing.T, what string, err, want error, target string) {
	t.Helper()
	var te *deploys.TargetError
	if !errors.Is(err, want) || !errors.As(err, &te) || te.Target != target || te.Message == "" {
		t.Errorf("%s: error %#v, want %v with target %s", what, err, want, target)
	}
}

func TestPreviewCreateRefusals(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	long := strings.Repeat("s", 50)
	w.site(w.owner.ID, long)
	// A site named before "--" was refused for new sites.
	w.site(w.owner.ID, "docs--pr-1")
	days := func(n int) *int { return &n }
	for _, tc := range []struct {
		name, site, preview string
		days                *int
		err                 error
		target              string
	}{
		{"bad name", "docs", "PR-1", nil, deploys.ErrInvalidPreview, "preview"},
		{"double dash", "docs", "a--b", nil, deploys.ErrInvalidPreview, "preview"},
		{"leading dash", "docs", "-a", nil, deploys.ErrInvalidPreview, "preview"},
		{"too long", "docs", strings.Repeat("p", 21), nil, deploys.ErrInvalidPreview, "preview"},
		{"label over 63", long, "twelve-chars", nil, deploys.ErrInvalidPreview, "preview"},
		{"label is a site's name", "docs", "pr-1", nil, deploys.ErrPreviewNameTaken, "preview"},
		{"lifetime without preview", "docs", "", days(14), deploys.ErrInvalidPreviewExpiry, "previewExpires"},
		{"lifetime over 90 days", "docs", "pr-2", days(91), deploys.ErrInvalidPreviewExpiry, "previewExpires"},
		{"negative lifetime", "docs", "pr-2", days(-1), deploys.ErrInvalidPreviewExpiry, "previewExpires"},
	} {
		m := w.manifest(map[string]string{"index.html": "x"})
		m.Preview, m.PreviewDays = tc.preview, tc.days
		_, _, err := w.svc.Create(t.Context(), w.owner.ID, tc.site, m)
		wantTarget(t, tc.name, err, tc.err, tc.target)
	}
	// The longest label that fits is accepted: 50 + 2 + 11.
	m := w.manifest(map[string]string{"index.html": "x"})
	m.Preview = "eleven-char"
	if _, _, err := w.svc.Create(t.Context(), w.owner.ID, long, m); err != nil {
		t.Errorf("63-character label refused: %v", err)
	}
}

func TestPreviewDeployLeavesTheSiteAlone(t *testing.T) {
	notified := map[string]int{}
	var mu sync.Mutex
	n := deploys.NewLocalNotifier()
	n.Subscribe(func(siteID string) { mu.Lock(); notified[siteID]++; mu.Unlock() })
	w := newPathWorld(t, func(c *deploys.Config) { c.Notifier = n })
	w.site(w.owner.ID, "docs")
	live := w.live("docs", "", map[string]string{"index.html": "<h1>Live</h1>"})
	clear(notified)
	site, err := w.db.Sites().GetByOwnerName(t.Context(), w.owner.ID, "docs")
	if err != nil {
		t.Fatal(err)
	}
	first := w.preview(w.owner.ID, "docs", "pr-1", "", nil, map[string]string{"index.html": "<h1>One</h1>"})
	if got, _ := w.served("docs"); got != live.ID {
		t.Fatalf("site serves %s after a preview deploy, want %s", got, live.ID)
	}
	if notified[site.ID] != 1 {
		t.Errorf("notifications after the preview deploy = %d, want 1", notified[site.ID])
	}
	if s := deploys.StatusOf(site, first); s != deploys.StatusPreview {
		t.Errorf("status = %s, want preview", s)
	}
	_, p, err := w.svc.Preview(t.Context(), w.owner.ID, "docs", "pr-1")
	if err != nil || p.DeployID != first.ID || p.Days != store.DefaultPreviewDays {
		t.Fatalf("Preview() = %+v, %v", p, err)
	}
	// Deploying to the same name replaces it, like a branch.
	second := w.preview(w.owner.ID, "docs", "pr-1", "", nil, map[string]string{"index.html": "<h1>Two</h1>"})
	if _, p, err = w.svc.Preview(t.Context(), w.owner.ID, "docs", "pr-1"); err != nil || p.DeployID != second.ID {
		t.Fatalf("Preview() after replacing = %+v, %v", p, err)
	}
	// Never promoted, neither is a rollback target.
	for _, id := range []string{first.ID, second.ID} {
		if _, _, err := w.svc.Rollback(t.Context(), w.owner.ID, "docs", id); !errors.Is(err, deploys.ErrNotActivated) {
			t.Errorf("Rollback(%s) = %v, want ErrNotActivated", id, err)
		}
	}
	if _, _, err := w.svc.Rollback(t.Context(), w.owner.ID, "docs", ""); !errors.Is(err, deploys.ErrNoPreviousDeploy) {
		t.Errorf("Rollback() = %v, want ErrNoPreviousDeploy", err)
	}
	// Deleting stops it at once.
	if err := w.svc.DeletePreview(t.Context(), w.owner.ID, "docs", "pr-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Preview(t.Context(), w.owner.ID, "docs", "pr-1"); !errors.Is(err, deploys.ErrNotFound) {
		t.Errorf("Preview() after delete = %v, want ErrNotFound", err)
	}
	if notified[site.ID] != 3 {
		t.Errorf("notifications = %d, want one per preview change", notified[site.ID])
	}
}

func TestPathPreviewMergesWithTheLiveSite(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	base := w.live("docs", "", basePages)
	d := w.preview(w.owner.ID, "docs", "mockup", "/mockup-x/", nil, map[string]string{"index.html": "<h1>New mockup</h1>"})
	if d.BaseDeployID != base.ID || d.PathPrefix != "/mockup-x/" {
		t.Fatalf("path preview = %+v, want /mockup-x/ on %s", d, base.ID)
	}
	got := w.files(d.ID)
	if _, ok := got["mockup-x/old.html"]; ok {
		t.Error("the prefix kept a file of the base")
	}
	if got["mockup-x/index.html"].SHA256 != hashOf("<h1>New mockup</h1>") || got["about.html"].SHA256 != hashOf("<h1>About</h1>") {
		t.Errorf("merged files = %+v", got)
	}
	if id, _ := w.served("docs"); id != base.ID {
		t.Errorf("site serves %s, want its base %s", id, base.ID)
	}
}

func TestPromote(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	live := w.live("docs", "", map[string]string{"index.html": "<h1>Live</h1>"})
	p := w.preview(w.owner.ID, "docs", "pr-1", "", nil, map[string]string{"index.html": "<h1>Next</h1>"})
	site, d, pv, err := w.svc.Promote(t.Context(), w.owner.ID, "docs", "pr-1")
	if err != nil || site.ActiveDeployID != p.ID || d.ID != p.ID || d.ActivatedAt.IsZero() || pv.DeployID != p.ID {
		t.Fatalf("Promote() = %+v, %+v, %+v, %v", site, d, pv, err)
	}
	if s := deploys.StatusOf(site, d); s != deploys.StatusActive {
		t.Errorf("status after promotion = %s, want active", s)
	}
	// An ordinary activation in history: rollback returns to the old live.
	if _, back, err := w.svc.Rollback(t.Context(), w.owner.ID, "docs", ""); err != nil || back.ID != live.ID {
		t.Fatalf("Rollback() = %+v, %v", back, err)
	}
	// Promoting again puts it back; promoting what is live changes nothing.
	for range 2 {
		if site, _, _, err := w.svc.Promote(t.Context(), w.owner.ID, "docs", "pr-1"); err != nil || site.ActiveDeployID != p.ID {
			t.Fatalf("Promote() again = %+v, %v", site, err)
		}
	}

	// A path preview on a base no longer live is stale, until deployed again.
	w.preview(w.owner.ID, "docs", "docs", "/docs/", nil, map[string]string{"index.html": "<h1>Docs</h1>"})
	w.live("docs", "", map[string]string{"index.html": "<h1>Newer</h1>"})
	if _, _, _, err := w.svc.Promote(t.Context(), w.owner.ID, "docs", "docs"); !errors.Is(err, deploys.ErrStaleBase) {
		t.Fatalf("Promote(stale path preview) = %v, want ErrStaleBase", err)
	}
	again := w.preview(w.owner.ID, "docs", "docs", "/docs/", nil, map[string]string{"index.html": "<h1>Docs</h1>"})
	if site, _, _, err := w.svc.Promote(t.Context(), w.owner.ID, "docs", "docs"); err != nil || site.ActiveDeployID != again.ID {
		t.Fatalf("Promote() after deploying again = %+v, %v", site, err)
	}
	if _, _, _, err := w.svc.Promote(t.Context(), w.owner.ID, "docs", "missing"); !errors.Is(err, deploys.ErrNotFound) {
		t.Errorf("Promote(missing) = %v, want ErrNotFound", err)
	}
}

func TestPreviewExpiry(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	days := 2
	w.preview(w.owner.ID, "docs", "short", "", &days, map[string]string{"index.html": "a"})
	never := 0
	w.preview(w.owner.ID, "docs", "kept", "", &never, map[string]string{"index.html": "b"})
	w.preview(w.owner.ID, "docs", "default", "", nil, map[string]string{"index.html": "c"})
	list := func() []string {
		t.Helper()
		_, ps, err := w.svc.Previews(t.Context(), w.owner.ID, "docs", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}
	if got := strings.Join(list(), ","); got != "default,kept,short" {
		t.Fatalf("previews = %s", got)
	}
	w.clock = w.clock.Add(2 * day)
	if got := strings.Join(list(), ","); got != "default,kept" {
		t.Fatalf("previews after 2 days = %s", got)
	}
	for _, op := range []func() error{
		func() error { _, _, err := w.svc.Preview(t.Context(), w.owner.ID, "docs", "short"); return err },
		func() error { return w.svc.DeletePreview(t.Context(), w.owner.ID, "docs", "short") },
		func() error { _, _, _, err := w.svc.Promote(t.Context(), w.owner.ID, "docs", "short"); return err },
	} {
		if err := op(); !errors.Is(err, deploys.ErrNotFound) {
			t.Errorf("expired preview: %v, want ErrNotFound", err)
		}
	}
	w.clock = w.clock.Add(12 * day)
	if got := strings.Join(list(), ","); got != "kept" {
		t.Fatalf("previews after 14 days = %s", got)
	}
}

func TestPreviewQuotas(t *testing.T) {
	w := newPathWorld(t, func(c *deploys.Config) { c.Quotas = store.Quotas{StorageBytes: 20, DeploysPerDay: 2} })
	w.site(w.owner.ID, "docs")
	w.live("docs", "", map[string]string{"index.html": "0123456789"})
	m := w.manifest(map[string]string{"index.html": "0123456789abcdefghijk"})
	m.Preview = "big"
	if _, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m); !errors.Is(err, deploys.ErrLimitExceeded) {
		t.Fatalf("preview over storage: %v, want the storage quota", err)
	}
	w.preview(w.owner.ID, "docs", "small", "", nil, map[string]string{"index.html": "0123456789"})
	m = w.manifest(map[string]string{"index.html": "0123456789"})
	m.Preview = "small"
	var te *deploys.TargetError
	if _, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m); !errors.As(err, &te) || te.Target != "deploys" {
		t.Fatalf("third deploy of the day, a preview: %v, want the daily quota", err)
	}
}

func TestPreviewsAreOwnerScoped(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	w.live("docs", "", map[string]string{"index.html": "<h1>Live</h1>"})
	d := w.preview(w.owner.ID, "docs", "pr-1", "", nil, map[string]string{"index.html": "<h1>One</h1>"})
	other := w.newOwner("other@example.test")
	m := w.manifest(map[string]string{"index.html": "x"})
	m.Preview = "pr-1"
	for name, op := range map[string]func() error{
		"Create":        func() error { _, _, err := w.svc.Create(t.Context(), other.ID, "docs", m); return err },
		"Activate":      func() error { _, _, err := w.svc.Activate(t.Context(), other.ID, "docs", d.ID); return err },
		"Preview":       func() error { _, _, err := w.svc.Preview(t.Context(), other.ID, "docs", "pr-1"); return err },
		"Previews":      func() error { _, _, err := w.svc.Previews(t.Context(), other.ID, "docs", "", 10); return err },
		"DeletePreview": func() error { return w.svc.DeletePreview(t.Context(), other.ID, "docs", "pr-1") },
		"Promote":       func() error { _, _, _, err := w.svc.Promote(t.Context(), other.ID, "docs", "pr-1"); return err },
	} {
		if err := op(); !errors.Is(err, deploys.ErrNotFound) {
			t.Errorf("%s by another owner: %v, want ErrNotFound", name, err)
		}
	}
	if _, p, err := w.svc.Preview(t.Context(), w.owner.ID, "docs", "pr-1"); err != nil || p.DeployID != d.ID {
		t.Fatalf("the owner's preview after another owner's attempts = %+v, %v", p, err)
	}
}

func TestPreviewsOnATakenDownSite(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	w.preview(w.owner.ID, "docs", "pr-1", "", nil, map[string]string{"index.html": "x"})
	site, err := w.db.Sites().GetByOwnerName(t.Context(), w.owner.ID, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.db.Sites().TakeDown(t.Context(), site.ID, gcEpoch); err != nil {
		t.Fatal(err)
	}
	m := w.manifest(map[string]string{"index.html": "y"})
	m.Preview = "pr-2"
	for name, op := range map[string]func() error{
		"Create":        func() error { _, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m); return err },
		"DeletePreview": func() error { return w.svc.DeletePreview(t.Context(), w.owner.ID, "docs", "pr-1") },
		"Promote":       func() error { _, _, _, err := w.svc.Promote(t.Context(), w.owner.ID, "docs", "pr-1"); return err },
	} {
		if err := op(); !errors.Is(err, deploys.ErrSiteUnavailable) {
			t.Errorf("%s: %v, want ErrSiteUnavailable", name, err)
		}
	}
	if _, ps, err := w.svc.Previews(t.Context(), w.owner.ID, "docs", "", 10); err != nil || len(ps) != 1 {
		t.Errorf("Previews() of a taken-down site = %+v, %v; want its preview", ps, err)
	}
}

func TestGCRemovesExpiredAndReplacedPreviews(t *testing.T) {
	w := newGCWorld(t)
	o := w.owner()
	w.site(o, "docs")
	w.mustDeploy(o, "docs", "live")
	preview := func(name, content string, days int) string {
		t.Helper()
		h := hashOf(content)
		m := deploys.Manifest{Files: []store.DeployFile{{Path: "index.html", SHA256: h, Size: int64(len(content))}}, Preview: name, PreviewDays: &days}
		d, missing, err := w.svc.Create(t.Context(), o, "docs", m)
		if err != nil {
			t.Fatal(err)
		}
		for range missing {
			if err := w.svc.Upload(t.Context(), o, "docs", d.ID, h, strings.NewReader(content)); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := w.svc.Activate(t.Context(), o, "docs", d.ID); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	replaced := preview("pr-1", "first", 14)
	serving := preview("pr-1", "second", 14)
	expiring := preview("short", "short-lived", 1)

	later := gcEpoch.Add(2 * day)
	res := w.run(later, 1)
	if res.PreviewsExpired != 1 || res.DeploysPruned != 2 {
		t.Fatalf("GC = %+v, want 1 preview expired and 2 deploys pruned", res)
	}
	for id, want := range map[string]bool{replaced: false, expiring: false, serving: true} {
		_, err := w.db.Deploys().Get(t.Context(), o, mustSiteID(t, w, o, "docs"), id)
		if (err == nil) != want {
			t.Errorf("deploy %s kept = %t, want %t", id, err == nil, want)
		}
	}
	if w.stored("first") || w.stored("short-lived") || !w.stored("second") || !w.stored("live") {
		t.Error("GC kept or removed the wrong blobs")
	}
	if !strings.Contains(w.logged(), "previews_expired=1") {
		t.Errorf("log = %s, want the expired preview count", w.logged())
	}
}

func mustSiteID(t *testing.T, w *gcWorld, ownerID, name string) string {
	t.Helper()
	s, err := w.db.Sites().GetByOwnerName(t.Context(), ownerID, name)
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

const day = 24 * time.Hour
