// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/deploys/s3test"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// gcWorld is a deploy service and GC over one real database and blob
// directory. Uploads happen at the service clock; each GC run chooses its
// own time, and with the default grace anything the service did less than
// a day before it is young.
type gcWorld struct {
	t     *testing.T
	db    *store.DB
	dir   string
	blobs gcStore
	svc   *deploys.Service
	log   bytes.Buffer
	logMu sync.Mutex
	now   atomic.Int64 // service clock, Unix ms
}

var gcEpoch = time.UnixMilli(1_800_000_000_000).UTC()

// gcStore is a blob store GC can also sweep.
type gcStore interface {
	deploys.BlobStore
	deploys.BlobFiles
}

func newGCWorld(t *testing.T) *gcWorld {
	t.Helper()
	dir := t.TempDir()
	w := newGCWorldOver(t, deploys.NewFSBlobStore(dir))
	w.dir = dir
	return w
}

// newGCWorldOver is a gcWorld storing blobs in blobs.
func newGCWorldOver(t *testing.T, blobs gcStore) *gcWorld {
	t.Helper()
	w := &gcWorld{t: t, db: storetest.New(t), blobs: blobs}
	w.now.Store(gcEpoch.UnixMilli())
	w.svc = deploys.NewService(deploys.Config{
		Sites:    w.db.Sites(),
		Deploys:  w.db.Deploys(),
		Blobs:    w.blobs,
		Notifier: deploys.NewLocalNotifier(),
		Limits:   deploys.DefaultLimits(),
		Now:      w.clock,
	})
	return w
}

func (w *gcWorld) clock() time.Time { return time.UnixMilli(w.now.Load()).UTC() }

// advance moves the service clock on by d.
func (w *gcWorld) advance(d time.Duration) { w.now.Add(d.Milliseconds()) }

func (w *gcWorld) Write(p []byte) (int, error) {
	w.logMu.Lock()
	defer w.logMu.Unlock()
	return w.log.Write(p)
}

func (w *gcWorld) logged() string {
	w.logMu.Lock()
	defer w.logMu.Unlock()
	return w.log.String()
}

// gc returns a GC running at at, keeping history deploys per site.
func (w *gcWorld) gc(at time.Time, history int) *deploys.GC {
	return w.gcFiles(at, history, w.blobs)
}

// gcFiles is gc with files (nil to skip unrecorded files).
func (w *gcWorld) gcFiles(at time.Time, history int, files deploys.BlobFiles) *deploys.GC {
	return deploys.NewGC(deploys.GCConfig{
		Repo:    w.db.GC(),
		Blobs:   w.blobs,
		Files:   files,
		History: history,
		Log:     slog.New(slog.NewTextHandler(w, nil)),
		Now:     func() time.Time { return at },
	})
}

func (w *gcWorld) run(at time.Time, history int) deploys.GCResult {
	w.t.Helper()
	res, err := w.gc(at, history).Run(w.t.Context())
	if err != nil {
		w.t.Fatalf("gc: %v", err)
	}
	return res
}

func (w *gcWorld) owner() string {
	w.t.Helper()
	id := store.NewID()
	err := w.db.Owners().Create(w.t.Context(), store.Owner{ID: id, Email: id + "@example.test", CreatedAt: gcEpoch})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *gcWorld) site(ownerID, name string) {
	w.t.Helper()
	err := w.db.Sites().Create(w.t.Context(), store.Site{ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: gcEpoch})
	if err != nil {
		w.t.Fatal(err)
	}
}

// deploy creates, uploads and activates a deploy of contents, returning its
// ID. It retries the way a client does when activation reports blobs
// missing.
func (w *gcWorld) deploy(ctx context.Context, ownerID, site string, contents ...string) (string, error) {
	m := deploys.Manifest{}
	body := map[string]string{}
	for i, c := range contents {
		h := hashOf(c)
		body[h] = c
		m.Files = append(m.Files, store.DeployFile{Path: fmt.Sprintf("f%d.txt", i), SHA256: h, Size: int64(len(c))})
	}
	d, missing, err := w.svc.Create(ctx, ownerID, site, m)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	for range 5 {
		for _, h := range missing {
			if err := w.svc.Upload(ctx, ownerID, site, d.ID, h, strings.NewReader(body[h])); err != nil {
				return "", fmt.Errorf("upload: %w", err)
			}
		}
		_, _, err = w.svc.Activate(ctx, ownerID, site, d.ID)
		var te *deploys.TargetError
		if !errors.As(err, &te) || !errors.Is(err, deploys.ErrBlobsMissing) {
			break
		}
		// The error names only one hash, so upload them all again.
		missing = slices.Collect(maps.Keys(body))
	}
	if err != nil {
		return "", fmt.Errorf("activate: %w", err)
	}
	return d.ID, nil
}

func (w *gcWorld) mustDeploy(ownerID, site string, contents ...string) string {
	w.t.Helper()
	id, err := w.deploy(w.t.Context(), ownerID, site, contents...)
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// stored reports whether content's blob file exists.
func (w *gcWorld) stored(content string) bool {
	w.t.Helper()
	ok, err := w.blobs.Has(w.t.Context(), hashOf(content))
	if err != nil {
		w.t.Fatal(err)
	}
	return ok
}

// serves checks every file of the site's active deploy can be read with
// the right content.
func (w *gcWorld) serves(ctx context.Context, ownerID, site string) error {
	s, err := w.db.Sites().GetByOwnerName(ctx, ownerID, site)
	if err != nil {
		return err
	}
	files, err := w.db.Deploys().Files(ctx, s.ActiveDeployID)
	if err != nil {
		return err
	}
	for _, f := range files {
		r, err := w.blobs.Open(ctx, f.SHA256, f.Size)
		if err != nil {
			return fmt.Errorf("%s of active deploy %s: %w", f.Path, s.ActiveDeployID, err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			return fmt.Errorf("read %s of active deploy: %w", f.Path, err)
		}
		if hashOf(string(b)) != f.SHA256 {
			return fmt.Errorf("%s of active deploy: wrong content", f.Path)
		}
	}
	return nil
}

func TestGCKeepsReferencedAndYoungBlobs(t *testing.T) {
	w := newGCWorld(t)
	o := w.owner()
	w.site(o, "docs")
	w.mustDeploy(o, "docs", "old shared", "old only")
	w.mustDeploy(o, "docs", "old shared", "current")
	// A pending deploy, and a blob recorded for it, an hour before GC.
	gcAt := gcEpoch.Add(25 * time.Hour)
	w.advance(24 * time.Hour)
	d, missing, err := w.svc.Create(t.Context(), o, "docs", deploys.Manifest{Files: []store.DeployFile{
		{Path: "a.txt", SHA256: hashOf("in flight"), Size: int64(len("in flight"))},
	}})
	if err != nil || len(missing) != 1 {
		t.Fatalf("create pending: %v, missing %v", err, missing)
	}
	if err := w.svc.Upload(t.Context(), o, "docs", d.ID, missing[0], strings.NewReader("in flight")); err != nil {
		t.Fatal(err)
	}
	// History 1 prunes the first deploy (older than the pending one by
	// more than the limit), leaving "old only" unreferenced.
	res := w.run(gcAt, 1)
	if res.DeploysPruned != 1 || res.BlobsDeleted != 1 || res.BytesFreed != int64(len("old only")) {
		t.Fatalf("result = %+v", res)
	}
	for c, want := range map[string]bool{"old shared": true, "current": true, "in flight": true, "old only": false} {
		if got := w.stored(c); got != want {
			t.Errorf("%q stored = %v, want %v", c, got, want)
		}
	}
	if err := w.serves(t.Context(), o, "docs"); err != nil {
		t.Fatal(err)
	}
	// Nothing left to do: a second run frees nothing.
	if res := w.run(gcAt, 1); res != (deploys.GCResult{}) {
		t.Fatalf("second run = %+v", res)
	}
	// The pending deploy is still there and activates without re-upload.
	if _, _, err := w.svc.Activate(t.Context(), o, "docs", d.ID); err != nil {
		t.Fatalf("activate pending after gc: %v", err)
	}
	log := w.logged()
	for _, want := range []string{"garbage collection finished", "deploys_pruned=1", "blobs_deleted=1", fmt.Sprintf("bytes_freed=%d", len("old only"))} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestGCGracePeriod(t *testing.T) {
	w := newGCWorld(t)
	o := w.owner()
	w.site(o, "docs")
	w.mustDeploy(o, "docs", "first")
	w.mustDeploy(o, "docs", "second")
	w.mustDeploy(o, "docs", "third")
	// Within the grace period the replaced deploys' blobs survive even
	// though their deploys are pruned.
	if res := w.run(gcEpoch.Add(time.Hour), 1); res.DeploysPruned != 2 || res.BlobsDeleted != 0 {
		t.Fatalf("result inside grace = %+v", res)
	}
	if !w.stored("first") || !w.stored("second") {
		t.Fatal("young blobs were deleted")
	}
	res := w.run(gcEpoch.Add(deploys.DefaultGCGrace+time.Millisecond), 1)
	if res.BlobsDeleted != 2 || w.stored("first") || w.stored("second") || !w.stored("third") {
		t.Fatalf("result after grace = %+v", res)
	}
}

func TestGCHistoryKeepsActiveDeploy(t *testing.T) {
	w := newGCWorld(t)
	o := w.owner()
	w.site(o, "docs")
	var ids []string
	for i := range 5 {
		w.advance(time.Second)
		ids = append(ids, w.mustDeploy(o, "docs", fmt.Sprintf("v%d", i)))
	}
	// Roll back to the oldest: it is beyond the limit but active.
	if _, _, err := w.svc.Rollback(t.Context(), o, "docs", ids[0]); err != nil {
		t.Fatal(err)
	}
	res := w.run(gcEpoch.Add(48*time.Hour), 2)
	if res.DeploysPruned != 2 || res.BlobsDeleted != 2 {
		t.Fatalf("result = %+v", res)
	}
	_, kept, err := w.svc.List(t.Context(), o, "docs", store.DeployCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range kept {
		got = append(got, d.ID)
	}
	if want := []string{ids[4], ids[3], ids[0]}; !slices.Equal(got, want) {
		t.Fatalf("kept deploys = %v, want %v", got, want)
	}
	if err := w.serves(t.Context(), o, "docs"); err != nil {
		t.Fatal(err)
	}
	for c, want := range map[string]bool{"v0": true, "v1": false, "v2": false, "v3": true, "v4": true} {
		if w.stored(c) != want {
			t.Errorf("%s stored = %v, want %v", c, !want, want)
		}
	}
	// The kept history still rolls back.
	if _, _, err := w.svc.Rollback(t.Context(), o, "docs", ids[3]); err != nil {
		t.Fatalf("rollback to kept deploy: %v", err)
	}
	if err := w.serves(t.Context(), o, "docs"); err != nil {
		t.Fatal(err)
	}
}

func TestGCOwnerIsolation(t *testing.T) {
	w := newGCWorld(t)
	a, b := w.owner(), w.owner()
	w.site(a, "alpha")
	w.site(b, "beta")
	w.mustDeploy(a, "alpha", "shared", "a only")
	w.mustDeploy(b, "beta", "shared", "b first")
	w.mustDeploy(b, "beta", "b second")
	gcAt := gcEpoch.Add(48 * time.Hour)

	// B's history moves on, dropping B's reference to "shared"; A's
	// deploy still lists it, so it stays, and A still need not upload it.
	res := w.run(gcAt, 1)
	if res.DeploysPruned != 1 || res.BlobsDeleted != 1 || !w.stored("shared") || w.stored("b first") {
		t.Fatalf("result = %+v", res)
	}
	if err := w.serves(t.Context(), a, "alpha"); err != nil {
		t.Fatal(err)
	}
	_, missing, err := w.svc.Create(t.Context(), a, "alpha", deploys.Manifest{Files: []store.DeployFile{
		{Path: "s.txt", SHA256: hashOf("shared"), Size: int64(len("shared"))},
	}})
	if err != nil || len(missing) != 0 {
		t.Fatalf("A's claim to a kept blob: %v, missing %v", err, missing)
	}

	// Once no deploy lists it, it goes with both owners' claims: neither
	// is told they hold it.
	w.mustDeploy(a, "alpha", "a later")
	w.advance(time.Second)
	w.mustDeploy(a, "alpha", "a latest")
	if res := w.run(gcAt.Add(time.Hour), 1); !w.stored("a latest") || w.stored("shared") {
		t.Fatalf("result = %+v", res)
	}
	for _, o := range []struct{ id, site string }{{a, "alpha"}, {b, "beta"}} {
		_, missing, err := w.svc.Create(t.Context(), o.id, o.site, deploys.Manifest{Files: []store.DeployFile{
			{Path: "s.txt", SHA256: hashOf("shared"), Size: int64(len("shared"))},
		}})
		if err != nil || !slices.Equal(missing, []string{hashOf("shared")}) {
			t.Fatalf("after collection: %v, missing %v", err, missing)
		}
	}
}

func TestGCRemovesUnrecordedFiles(t *testing.T) {
	w := newGCWorld(t)
	o := w.owner()
	w.site(o, "docs")
	w.mustDeploy(o, "docs", "recorded")
	// A blob stored but never recorded (a crash before RecordUpload), an
	// abandoned temporary upload, and a fresh unrecorded blob.
	for _, c := range []string{"crashed", "fresh"} {
		if err := w.blobs.Put(t.Context(), hashOf(c), int64(len(c)), strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	tmp := filepath.Join(w.dir, "sha256", "ab", ".upload-123")
	if err := os.MkdirAll(filepath.Dir(tmp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	stranger := filepath.Join(w.dir, "sha256", "ab", "not-a-blob")
	if err := os.WriteFile(stranger, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{tmp, stranger, blobPath(w.dir, hashOf("crashed")), blobPath(w.dir, hashOf("recorded"))} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	res := w.run(time.Now(), 50)
	if res.FilesDeleted != 2 || res.BytesFreed != int64(len("crashed")+len("partial")) {
		t.Fatalf("result = %+v", res)
	}
	if w.stored("crashed") || !w.stored("fresh") || !w.stored("recorded") {
		t.Fatal("wrong files removed")
	}
	for p, want := range map[string]bool{tmp: false, stranger: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}

// TestGCS3 runs GC over an S3 blob store: pruned deploys' blobs leave the
// bucket, and the sweep removes unrecorded blob objects and stale
// temporary uploads, leaving young ones and unrelated keys.
func TestGCS3(t *testing.T) {
	srv := s3test.New(t, "wardlock-gc", "AKIATEST")
	srv.PageSize = 2 // list in several pages
	w := newGCWorldOver(t, srv.Store(t, "wl/"))
	// Objects are stamped by the service clock; GC runs two days later.
	srv.SetNow(w.clock)
	o := w.owner()
	w.site(o, "docs")
	w.mustDeploy(o, "docs", "pruned")
	w.mustDeploy(o, "docs", "recorded")
	for _, c := range []string{"crashed", "fresh"} {
		if err := w.blobs.Put(t.Context(), hashOf(c), int64(len(c)), strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	young := gcEpoch.Add(47 * time.Hour)
	h := hashOf("fresh")
	srv.SetModTime("wl/sha256/"+h[:2]+"/"+h[2:], young)
	srv.PutObject("wl/uploads/stale", []byte("partial"), gcEpoch)
	srv.PutObject("wl/uploads/young", []byte("in progress"), young)
	srv.PutObject("wl/sha256/ab/not-a-blob", []byte("x"), gcEpoch)
	srv.PutObject("other/uploads/stale", []byte("not ours"), gcEpoch)

	res := w.run(gcEpoch.Add(48*time.Hour), 1)
	if res.DeploysPruned != 1 || res.BlobsDeleted != 1 || res.FilesDeleted != 2 ||
		res.BytesFreed != int64(len("pruned")+len("crashed")+len("partial")) {
		t.Fatalf("result = %+v", res)
	}
	if w.stored("pruned") || w.stored("crashed") || !w.stored("fresh") || !w.stored("recorded") {
		t.Fatal("wrong blobs removed")
	}
	for key, want := range map[string]bool{
		"wl/uploads/stale": false, "wl/uploads/young": true,
		"wl/sha256/ab/not-a-blob": true, "other/uploads/stale": true,
	} {
		if _, ok := srv.Object(key); ok != want {
			t.Errorf("%s exists = %v, want %v", key, ok, want)
		}
	}
	if err := w.serves(t.Context(), o, "docs"); err != nil {
		t.Fatal(err)
	}
}

func blobPath(dir, h string) string { return filepath.Join(dir, "sha256", h[:2], h[2:]) }

// TestGCConcurrentWithUploads runs GC continuously while two owners deploy
// overlapping content, so deploys keep re-referencing blobs GC is about to
// collect. Every activation must leave the active deploy fully servable.
// Run it under -race.
func TestGCConcurrentWithUploads(t *testing.T) {
	w := newGCWorld(t)
	owners := []struct{ id, site string }{{w.owner(), "alpha"}, {w.owner(), "beta"}}
	for _, o := range owners {
		w.site(o.id, o.site)
	}
	pool := []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6"}
	// Every blob is past the grace period as soon as it is recorded; only
	// the history limit and references protect anything. Unrecorded files
	// are left out: GC would rightly take uploads in progress for
	// abandoned ones when the grace period is already over.
	gcAt := func() time.Time { return w.clock().Add(deploys.DefaultGCGrace + time.Hour) }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var gcRuns atomic.Int64
	var gcWG, wg sync.WaitGroup
	errs := make(chan error, len(owners)+1)
	gcWG.Go(func() {
		for ctx.Err() == nil {
			if _, err := w.gcFiles(gcAt(), 1, nil).Run(ctx); err != nil && ctx.Err() == nil {
				errs <- fmt.Errorf("gc: %w", err)
				return
			}
			gcRuns.Add(1)
		}
	})
	const rounds = 25
	for n, o := range owners {
		wg.Go(func() {
			for i := range rounds {
				// Three files: a rotating window over the pool, so each
				// deploy drops one blob and picks up one a previous
				// deploy dropped.
				k := i + n*3
				contents := []string{pool[k%len(pool)], pool[(k+1)%len(pool)], pool[(k+2)%len(pool)]}
				if _, err := w.deploy(ctx, o.id, o.site, contents...); err != nil {
					errs <- fmt.Errorf("%s round %d: %w", o.site, i, err)
					return
				}
				if err := w.serves(ctx, o.id, o.site); err != nil {
					errs <- fmt.Errorf("%s round %d: %w", o.site, i, err)
					return
				}
			}
		})
	}
	wg.Wait()
	cancel()
	gcWG.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if gcRuns.Load() == 0 {
		t.Fatal("GC never completed a run during the deploys")
	}
	// A final collection keeps both active deploys whole.
	if _, err := w.gcFiles(gcAt(), 1, nil).Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, o := range owners {
		if err := w.serves(t.Context(), o.id, o.site); err != nil {
			t.Error(err)
		}
	}
}

// listHook runs after GC lists unreferenced blobs, before it deletes any.
type listHook struct {
	store.GC
	after func([]store.Blob)
}

func (h listHook) UnreferencedBlobs(ctx context.Context, before time.Time, after string, limit int) ([]store.Blob, error) {
	bs, err := h.GC.UnreferencedBlobs(ctx, before, after, limit)
	if err == nil && len(bs) > 0 && h.after != nil {
		h.after(bs)
	}
	return bs, err
}

// deleteHook runs before each blob file is deleted.
type deleteHook struct {
	deploys.BlobStore
	before func(sha256 string)
}

func (h deleteHook) Delete(ctx context.Context, sha256 string) error {
	h.before(sha256)
	return h.BlobStore.Delete(ctx, sha256)
}

// TestGCInterleavings forces the orderings TestGCConcurrentWithUploads can
// only hit by chance: a deploy that re-references a blob after GC listed it
// as garbage, and one that runs while GC is removing the file.
func TestGCInterleavings(t *testing.T) {
	setup := func(t *testing.T) (*gcWorld, string) {
		w := newGCWorld(t)
		o := w.owner()
		w.site(o, "docs")
		w.mustDeploy(o, "docs", "reused")
		w.mustDeploy(o, "docs", "other")
		return w, o
	}
	gcAt := gcEpoch.Add(48 * time.Hour)
	newGC := func(w *gcWorld, repo store.GC, blobs deploys.BlobStore) *deploys.GC {
		return deploys.NewGC(deploys.GCConfig{
			Repo: repo, Blobs: blobs, History: 1,
			Log: slog.New(slog.DiscardHandler),
			Now: func() time.Time { return gcAt },
		})
	}

	t.Run("re-referenced after listing", func(t *testing.T) {
		w, o := setup(t)
		hooked := false
		g := newGC(w, listHook{GC: w.db.GC(), after: func(bs []store.Blob) {
			if hooked || !slices.ContainsFunc(bs, func(b store.Blob) bool { return b.SHA256 == hashOf("reused") }) {
				return
			}
			hooked = true
			// The owner still holds "reused", so this deploy activates
			// without uploading it.
			if _, err := w.deploy(t.Context(), o, "docs", "reused"); err != nil {
				t.Errorf("deploy during gc: %v", err)
			}
		}}, w.blobs)
		if _, err := g.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !hooked {
			t.Fatal("GC never listed the blob")
		}
		if err := w.serves(t.Context(), o, "docs"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("deploy while the file is removed", func(t *testing.T) {
		w, o := setup(t)
		var deployed chan error
		g := newGC(w, w.db.GC(), deleteHook{BlobStore: w.blobs, before: func(h string) {
			if h != hashOf("reused") || deployed != nil {
				return
			}
			deployed = make(chan error, 1)
			go func() {
				_, err := w.deploy(context.WithoutCancel(t.Context()), o, "docs", "reused")
				deployed <- err
			}()
			// GC holds the store transaction, so the deploy must wait
			// for it; give a wrong implementation time to race ahead.
			select {
			case err := <-deployed:
				deployed <- err
			case <-time.After(200 * time.Millisecond):
			}
		}})
		if _, err := g.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
		if deployed == nil {
			t.Fatal("GC never removed the blob")
		}
		if err := <-deployed; err != nil {
			t.Fatalf("deploy during gc: %v", err)
		}
		if err := w.serves(t.Context(), o, "docs"); err != nil {
			t.Fatal(err)
		}
	})
}
