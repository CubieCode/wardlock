// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
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
)

// cacheWorld is a CachingBlobStore over the S3 fake.
type cacheWorld struct {
	t     *testing.T
	srv   *s3test.Server
	s3    *deploys.S3BlobStore
	dir   string
	cache *deploys.CachingBlobStore
	clock atomic.Int64
}

func newCacheWorld(t *testing.T, maxBytes int64) *cacheWorld {
	t.Helper()
	w := &cacheWorld{t: t, srv: s3test.New(t, "wardlock-test", "AKIATEST"), dir: filepath.Join(t.TempDir(), "cache")}
	w.s3 = w.srv.Store(t, "")
	w.clock.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
	w.cache = w.open(maxBytes)
	return w
}

// open starts a cache over the world's bucket and directory, as a new
// process would.
func (w *cacheWorld) open(maxBytes int64) *deploys.CachingBlobStore {
	w.t.Helper()
	c, err := deploys.NewCachingBlobStore(deploys.BlobCacheConfig{
		Blobs: w.s3, Dir: w.dir, MaxBytes: maxBytes,
		// Each use is a second later than the last.
		Now: func() time.Time { return time.Unix(w.clock.Add(1), 0) },
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

// put stores content in the bucket and returns its hash; the request
// counts start afresh.
func (w *cacheWorld) put(content string) string {
	w.t.Helper()
	h := hashOf(content)
	if err := w.cache.Put(w.t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
		w.t.Fatal(err)
	}
	w.srv.Requests()
	return h
}

// read reads all of a blob through the cache and returns the bucket
// requests it made.
func (w *cacheWorld) read(content string) map[string]int {
	w.t.Helper()
	if got := read(w.t, w.cache, hashOf(content), len(content)); got != content {
		w.t.Fatalf("read %q, want %q", got, content)
	}
	return s3test.Count(w.srv.Requests())
}

// cached lists the hashes with a copy in the cache, and fails if a fill
// was left behind.
func (w *cacheWorld) cached() []string {
	w.t.Helper()
	var out []string
	for _, rel := range files(w.t, w.dir) {
		if strings.HasPrefix(rel, "fill/") {
			w.t.Fatalf("fill left behind: %s", rel)
		}
		out = append(out, strings.ReplaceAll(strings.TrimPrefix(rel, "sha256/"), "/", ""))
	}
	slices.Sort(out)
	return out
}

var oneGet = map[string]int{s3test.GetObject: 1}

func TestBlobCacheReadThrough(t *testing.T) {
	w := newCacheWorld(t, 1<<20)
	content := strings.Repeat("cache me ", 100)
	h := w.put(content)

	if got := w.read(content); !maps.Equal(got, oneGet) {
		t.Fatalf("cold read made %v, want one GetObject", got)
	}
	if got := w.cached(); !slices.Equal(got, []string{h}) {
		t.Fatalf("cached = %v, want the blob", got)
	}
	for range 3 {
		if got := w.read(content); len(got) != 0 {
			t.Fatalf("warm read made %v, want none", got)
		}
	}
	// Has and Put still reach the bucket.
	if ok, err := w.cache.Has(t.Context(), h); err != nil || !ok {
		t.Fatalf("Has = %v, %v", ok, err)
	}
	if got := s3test.Count(w.srv.Requests()); !maps.Equal(got, map[string]int{s3test.HeadObject: 1}) {
		t.Fatalf("Has made %v", got)
	}
}

// TestBlobCacheRange: a Range on a cold cache is one ranged GetObject and
// fills nothing, so it never downloads the whole object; a full read
// after it fills.
func TestBlobCacheRange(t *testing.T) {
	w := newCacheWorld(t, 1<<20)
	content := strings.Repeat("0123456789", 100)
	h := w.put(content)
	serve := func(rng string) *httptest.ResponseRecorder {
		blob, err := w.cache.Open(t.Context(), h, int64(len(content)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blob.Close() }()
		req := httptest.NewRequest(http.MethodGet, "/f.txt", nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "text/plain")
		http.ServeContent(rec, req, "", time.Time{}, blob)
		return rec
	}

	rec := serve("bytes=500-509")
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "0123456789" {
		t.Fatalf("range = %d %q", rec.Code, rec.Body.String())
	}
	reqs := w.srv.Requests()
	if len(reqs) != 1 || reqs[0].Op != s3test.GetObject || reqs[0].Range != "bytes=500-" {
		t.Fatalf("range made %v, want one GetObject from byte 500", s3Calls(reqs))
	}
	if got := w.cached(); len(got) != 0 {
		t.Fatalf("a range filled the cache: %v", got)
	}
	// A range from the start that stops short fills nothing either.
	if rec := serve("bytes=0-9"); rec.Body.String() != "0123456789" {
		t.Fatalf("first bytes = %q", rec.Body.String())
	}
	w.srv.Requests()
	if got := w.cached(); len(got) != 0 {
		t.Fatalf("a short range filled the cache: %v", got)
	}

	if rec := serve(""); rec.Body.String() != content {
		t.Fatal("full read served the wrong content")
	}
	if got := s3test.Count(w.srv.Requests()); !maps.Equal(got, oneGet) {
		t.Fatalf("full read made %v", got)
	}
	// A warm range costs nothing.
	if rec := serve("bytes=500-509"); rec.Body.String() != "0123456789" {
		t.Fatalf("warm range = %q", rec.Body.String())
	}
	if got := w.srv.Requests(); len(got) != 0 {
		t.Fatalf("warm range made %v", s3Calls(got))
	}
}

// TestBlobCacheVerifiesHash: bytes in the bucket that do not hash to the
// blob's name are served as they are, as without a cache, but never kept.
func TestBlobCacheVerifiesHash(t *testing.T) {
	w := newCacheWorld(t, 1<<20)
	content := "the content that was uploaded"
	h := hashOf(content)
	w.srv.PutObject("sha256/"+h[:2]+"/"+h[2:], []byte("the content that was replaced"), time.Now())
	for range 2 {
		rc, err := w.cache.Open(t.Context(), h, int64(len(content)))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(rc)
		_ = rc.Close()
		if got := s3test.Count(w.srv.Requests()); !maps.Equal(got, oneGet) {
			t.Fatalf("read made %v, want one GetObject each time", got)
		}
		if got := w.cached(); len(got) != 0 {
			t.Fatalf("kept a copy that does not match its hash: %v", got)
		}
	}
}

// TestBlobCacheEvictsLeastRecentlyUsed keeps the cache within MaxBytes by
// dropping the blob used longest ago.
func TestBlobCacheEvictsLeastRecentlyUsed(t *testing.T) {
	a, b, c := strings.Repeat("a", 100), strings.Repeat("b", 100), strings.Repeat("c", 100)
	w := newCacheWorld(t, 250)
	ha, hb, hc := w.put(a), w.put(b), w.put(c)
	w.read(a)
	w.read(b)
	if got := w.read(a); len(got) != 0 {
		t.Fatalf("warm a made %v", got)
	}
	w.read(c) // evicts b, used before a
	if got, want := w.cached(), sorted(ha, hc); !slices.Equal(got, want) {
		t.Fatalf("cached = %v, want a and c", got)
	}
	if got := w.read(b); !maps.Equal(got, oneGet) {
		t.Fatalf("evicted b made %v", got)
	}
	// b in evicts a, used before c.
	if got, want := w.cached(), sorted(hb, hc); !slices.Equal(got, want) {
		t.Fatalf("cached = %v, want b and c", got)
	}
	// A blob bigger than the cache reads through without filling.
	big := strings.Repeat("z", 300)
	w.put(big)
	w.read(big)
	if got := w.read(big); !maps.Equal(got, oneGet) {
		t.Fatalf("big blob made %v, want a GetObject each read", got)
	}
}

func sorted(s ...string) []string {
	slices.Sort(s)
	return s
}

// TestBlobCacheRebuild: a new process indexes the copies already on disk in
// order of last use, evicting beyond its limit, and removes abandoned
// fills.
func TestBlobCacheRebuild(t *testing.T) {
	a, b := strings.Repeat("a", 100), strings.Repeat("b", 100)
	w := newCacheWorld(t, 1<<20)
	ha := w.put(a)
	w.put(b)
	w.read(a)
	w.read(b)
	w.read(a) // a is now the most recent
	if err := os.WriteFile(filepath.Join(w.dir, "fill", "blob-abandoned"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	w.cache = w.open(150)
	if got := w.cached(); !slices.Equal(got, []string{ha}) {
		t.Fatalf("after restart cached = %v, want a alone (b was used before it)", got)
	}
	if got := w.read(a); len(got) != 0 {
		t.Fatalf("a after restart made %v, want none", got)
	}
	if got := w.read(b); !maps.Equal(got, oneGet) {
		t.Fatalf("b after restart made %v", got)
	}
}

// TestBlobCacheWiped: a cache directory removed while running is filled
// again from the bucket, and a truncated copy is a miss.
func TestBlobCacheWiped(t *testing.T) {
	w := newCacheWorld(t, 1<<20)
	content := strings.Repeat("wipe me ", 50)
	h := w.put(content)
	w.read(content)
	if err := os.RemoveAll(w.dir); err != nil {
		t.Fatal(err)
	}
	if got := w.read(content); !maps.Equal(got, oneGet) {
		t.Fatalf("read after wipe made %v", got)
	}
	if got := w.cached(); !slices.Equal(got, []string{h}) {
		t.Fatalf("not refilled: %v", got)
	}
	if err := os.Truncate(filepath.Join(w.dir, "sha256", h[:2], h[2:]), 10); err != nil {
		t.Fatal(err)
	}
	if got := w.read(content); !maps.Equal(got, oneGet) {
		t.Fatalf("read of a truncated copy made %v", got)
	}
	if got := w.read(content); len(got) != 0 {
		t.Fatalf("refilled copy made %v", got)
	}
}

// TestBlobCacheConcurrentFills: many cold readers of one blob all get it,
// one of them fills, and the cache ends with one complete copy.
func TestBlobCacheConcurrentFills(t *testing.T) {
	w := newCacheWorld(t, 1<<20)
	content := strings.Repeat("concurrent ", 1000)
	h := w.put(content)
	var wg sync.WaitGroup
	got := make([]string, 16)
	start := make(chan struct{})
	for i := range got {
		wg.Go(func() {
			<-start
			rc, err := w.cache.Open(t.Context(), h, int64(len(content)))
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = rc.Close() }()
			b, _ := io.ReadAll(rc)
			got[i] = string(b)
		})
	}
	close(start)
	wg.Wait()
	for i, g := range got {
		if g != content {
			t.Fatalf("reader %d got %d bytes", i, len(g))
		}
	}
	if gets := s3test.Count(w.srv.Requests())[s3test.GetObject]; gets < 1 || gets > len(got) {
		t.Fatalf("%d GetObjects for %d readers", gets, len(got))
	}
	if c := w.cached(); !slices.Equal(c, []string{h}) {
		t.Fatalf("cached = %v", c)
	}
	if got := w.read(content); len(got) != 0 {
		t.Fatalf("warm read made %v", got)
	}
}

// TestBlobCacheGCRemoval: deleting a blob, as GC does for recorded and
// unrecorded blobs, removes its cached copy, including one being filled.
func TestBlobCacheGCRemoval(t *testing.T) {
	w := newCacheWorld(t, 1<<20)
	a, b, c := strings.Repeat("a", 100), strings.Repeat("b", 100), strings.Repeat("c", 100)
	ha, hb, hc := w.put(a), w.put(b), w.put(c)
	w.read(a)
	w.read(b)

	if err := w.cache.Delete(t.Context(), ha); err != nil {
		t.Fatal(err)
	}
	if got := w.cached(); !slices.Equal(got, []string{hb}) {
		t.Fatalf("after Delete cached = %v", got)
	}
	// RemoveFile, for a blob with no record.
	err := w.cache.WalkFiles(t.Context(), time.Now().Add(time.Hour), func(f deploys.BlobFile) error {
		if f.SHA256 == hb {
			return w.cache.RemoveFile(t.Context(), f)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.cached(); len(got) != 0 {
		t.Fatalf("after RemoveFile cached = %v", got)
	}
	if ok, _ := w.s3.Has(t.Context(), hb); ok {
		t.Fatal("RemoveFile left the object")
	}

	// A fill under way when its blob is deleted is not kept.
	rc, err := w.cache.Open(t.Context(), hc, int64(len(c)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rc, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	if err := w.cache.Delete(t.Context(), hc); err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(rc)
	_ = rc.Close()
	if got := w.cached(); len(got) != 0 {
		t.Fatalf("a fill outlived its blob: %v", got)
	}
}
