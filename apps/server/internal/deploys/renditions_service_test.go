// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// vanishingBlobs, once vanish is set, loses the first copy of every blob
// Put stores, as if garbage collection had removed an old unreferenced
// copy just before the rendition was recorded.
type vanishingBlobs struct {
	*deploys.FSBlobStore
	mu     sync.Mutex
	vanish map[string]bool
}

func (b *vanishingBlobs) Put(ctx context.Context, h string, size int64, r io.Reader) error {
	if err := b.FSBlobStore.Put(ctx, h, size, r); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.vanish != nil && !b.vanish[h] {
		b.vanish[h] = true
		return b.Delete(ctx, h)
	}
	return nil
}

type renditionWorld struct {
	t     *testing.T
	db    *store.DB
	blobs *vanishingBlobs
	svc   *deploys.Service
	owner store.Owner
}

func newRenditionWorld(t *testing.T, renditions bool) *renditionWorld {
	t.Helper()
	w := &renditionWorld{t: t, db: storetest.New(t), blobs: &vanishingBlobs{FSBlobStore: deploys.NewFSBlobStore(t.TempDir())}}
	c := deploys.Config{
		Sites: w.db.Sites(), Deploys: w.db.Deploys(), Blobs: w.blobs, Notifier: deploys.NewLocalNotifier(),
		Limits: deploys.DefaultLimits(), Now: func() time.Time { return gcEpoch },
		SiteOrigin: func(name string) string { return "https://" + name + ".wardlock.test" },
	}
	if renditions {
		c.Renditions = w.db.Renditions()
	}
	w.svc = deploys.NewService(c)
	w.owner = store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: gcEpoch}
	if err := w.db.Owners().Create(t.Context(), w.owner); err != nil {
		t.Fatal(err)
	}
	return w
}

// deploy creates the site if needed and uploads files, without activating.
func (w *renditionWorld) deploy(site string, files map[string]string) store.Deploy {
	t, ctx := w.t, w.t.Context()
	t.Helper()
	if _, err := w.db.Sites().GetByOwnerName(ctx, w.owner.ID, site); err != nil {
		if err := w.db.Sites().Create(ctx, store.Site{ID: store.NewID(), OwnerID: w.owner.ID, Name: site, CreatedAt: gcEpoch}); err != nil {
			t.Fatal(err)
		}
	}
	var m deploys.Manifest
	for p, body := range files {
		m.Files = append(m.Files, store.DeployFile{Path: p, SHA256: hashOf(body), Size: int64(len(body))})
	}
	d, _, err := w.svc.Create(ctx, w.owner.ID, site, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if err := w.svc.Upload(ctx, w.owner.ID, site, d.ID, f.SHA256, strings.NewReader(files[f.Path])); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func (w *renditionWorld) read(h string) string {
	w.t.Helper()
	b, err := w.blobs.Open(w.t.Context(), h)
	if err != nil {
		w.t.Fatalf("open %s: %v", h, err)
	}
	defer func() { _ = b.Close() }()
	body, err := io.ReadAll(b)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(body)
}

var renditionPages = map[string]string{
	"index.html":     `<h1>Notes</h1><p><a href="docs/">Docs</a></p>`,
	"docs/index.htm": `<h1>Docs</h1>`,
	"site.css":       "body { color: teal }",
}

func TestActivateRenders(t *testing.T) {
	w := newRenditionWorld(t, true)
	d := w.deploy("notes", renditionPages)
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "notes", d.ID); err != nil {
		t.Fatal(err)
	}
	rs, err := w.db.Renditions().List(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rs {
		body := w.read(r.SHA256)
		if r.SHA256 != hashOf(body) || r.Size != int64(len(body)) {
			t.Fatalf("rendition %+v does not match its blob", r)
		}
		got[r.Path+" "+r.Format] = body
	}
	want := map[string]string{
		"index.html md":      "# Notes\n\n[Docs](https://notes.wardlock.test/docs/)\n",
		"index.html txt":     "Notes\n\nDocs (https://notes.wardlock.test/docs/)\n",
		"docs/index.htm md":  "# Docs\n",
		"docs/index.htm txt": "Docs\n",
	}
	if len(got) != len(want) {
		t.Fatalf("renditions = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// TestActivateRestoresVanishedRenditions: a rendition blob that garbage
// collection removed before the rendition referenced it is stored again.
func TestActivateRestoresVanishedRenditions(t *testing.T) {
	w := newRenditionWorld(t, true)
	d := w.deploy("notes", renditionPages)
	w.blobs.vanish = map[string]bool{}
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "notes", d.ID); err != nil {
		t.Fatal(err)
	}
	rs, err := w.db.Renditions().List(t.Context(), d.ID)
	if err != nil || len(rs) != 4 {
		t.Fatalf("renditions = %v, %v", rs, err)
	}
	for _, r := range rs {
		if ok, err := w.blobs.Has(t.Context(), r.SHA256); !ok || err != nil {
			t.Fatalf("rendition blob %s missing after activation: %v", r.SHA256, err)
		}
	}
}

func TestActivateWithoutRenditions(t *testing.T) {
	w := newRenditionWorld(t, false)
	d := w.deploy("notes", renditionPages)
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "notes", d.ID); err != nil {
		t.Fatal(err)
	}
	if rs, err := w.db.Renditions().List(t.Context(), d.ID); err != nil || len(rs) != 0 {
		t.Fatalf("renditions = %v, %v; want none", rs, err)
	}
}
