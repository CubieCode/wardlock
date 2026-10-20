// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/deploys/s3test"
)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// files lists every regular file under dir.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// blobStore is one BlobStore implementation under the contract tests.
type blobStore struct {
	name string
	// open returns an empty store and a function listing everything it
	// holds, as slash-separated paths relative to its root.
	open func(t *testing.T) (deploys.BlobStore, func() []string)
}

func blobStores() []blobStore {
	s3Keys := func(srv *s3test.Server) []string {
		var out []string
		for _, k := range srv.Keys() {
			if rel, ok := strings.CutPrefix(k, "wl/"); ok {
				out = append(out, rel)
			}
		}
		return out
	}
	return []blobStore{
		{"fs", func(t *testing.T) (deploys.BlobStore, func() []string) {
			dir := filepath.Join(t.TempDir(), "blobs")
			return deploys.NewFSBlobStore(dir), func() []string { return files(t, dir) }
		}},
		{"s3", func(t *testing.T) (deploys.BlobStore, func() []string) {
			srv := s3test.New(t, "wardlock-test", "AKIATEST")
			// A key outside the prefix is never touched or listed.
			srv.PutObject("other/sha256/ab/cd", []byte("not ours"), time.Now())
			return srv.Store(t, "wl/"), func() []string { return s3Keys(srv) }
		}},
		{"cached s3", func(t *testing.T) (deploys.BlobStore, func() []string) {
			srv := s3test.New(t, "wardlock-test", "AKIATEST")
			bs, err := deploys.NewCachingBlobStore(deploys.BlobCacheConfig{
				Blobs: srv.Store(t, "wl/"), Dir: t.TempDir(), MaxBytes: 1 << 20,
			})
			if err != nil {
				t.Fatal(err)
			}
			return bs, func() []string { return s3Keys(srv) }
		}},
	}
}

// eachBlobStore runs fn as a subtest against every implementation.
func eachBlobStore(t *testing.T, fn func(t *testing.T, bs deploys.BlobStore, stored func() []string)) {
	for _, impl := range blobStores() {
		t.Run(impl.name, func(t *testing.T) {
			bs, stored := impl.open(t)
			fn(t, bs, stored)
		})
	}
}

func TestBlobStore(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, bs deploys.BlobStore, stored func() []string) {
		ctx := t.Context()
		content := "hello, blob"
		h := hashOf(content)

		if ok, err := bs.Has(ctx, h); err != nil || ok {
			t.Fatalf("Has(new) = %v, %v", ok, err)
		}
		if err := openErr(t, bs, h, int64(len(content))); !errors.Is(err, deploys.ErrBlobNotFound) {
			t.Fatalf("Open(new) error = %v, want ErrBlobNotFound", err)
		}
		if err := bs.Put(ctx, h, int64(len(content)), strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		if got, want := stored(), []string{"sha256/" + h[:2] + "/" + h[2:]}; !slices.Equal(got, want) {
			t.Fatalf("stored = %v, want %v", got, want)
		}
		if ok, err := bs.Has(ctx, h); err != nil || !ok {
			t.Fatalf("Has(stored) = %v, %v", ok, err)
		}
		rc, err := bs.Open(ctx, h, int64(len(content)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rc.Seek(7, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		rest, _ := io.ReadAll(rc)
		_ = rc.Close()
		if string(rest) != "blob" {
			t.Fatalf("read after Seek = %q", rest)
		}

		// Putting an existing blob verifies it and keeps the blob.
		if err := bs.Put(ctx, h, int64(len(content)), strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		if err := bs.Put(ctx, h, int64(len(content)), strings.NewReader("hello, blub")); !errors.Is(err, deploys.ErrHashMismatch) {
			t.Fatalf("Put(existing, wrong body) error = %v, want ErrHashMismatch", err)
		}
		if got := read(t, bs, h, len(content)); got != content {
			t.Fatalf("after puts, blob = %q", got)
		}
		if got := stored(); len(got) != 1 {
			t.Fatalf("stored = %v, want only the blob", got)
		}

		if err := bs.Delete(ctx, h); err != nil {
			t.Fatal(err)
		}
		if ok, _ := bs.Has(ctx, h); ok {
			t.Fatal("Has after Delete = true")
		}
		if err := openErr(t, bs, h, int64(len(content))); !errors.Is(err, deploys.ErrBlobNotFound) {
			t.Fatalf("Open(deleted) error = %v, want ErrBlobNotFound", err)
		}
		if err := bs.Delete(ctx, h); err != nil {
			t.Fatalf("Delete(missing) = %v, want nil", err)
		}
	})
}

// read reads blob h, of size bytes.
func read(t *testing.T, bs deploys.BlobStore, h string, size int) string {
	t.Helper()
	rc, err := bs.Open(t.Context(), h, int64(size))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// openErr is the error opening blob h and reading its first byte gives:
// a store may report a missing blob from either.
func openErr(t *testing.T, bs deploys.BlobStore, h string, size int64) error {
	t.Helper()
	rc, err := bs.Open(t.Context(), h, size)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	_, err = rc.Read(make([]byte, 1))
	return err
}

func TestBlobStoreEmpty(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, bs deploys.BlobStore, _ func() []string) {
		h := hashOf("")
		if err := bs.Put(t.Context(), h, 0, strings.NewReader("")); err != nil {
			t.Fatal(err)
		}
		if got := read(t, bs, h, 0); got != "" {
			t.Fatalf("empty blob = %q", got)
		}
	})
}

// TestBlobStoreSeek covers the reads http.ServeContent makes for ranges:
// the size from SeekEnd, then a seek to each range's start.
func TestBlobStoreSeek(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, bs deploys.BlobStore, _ func() []string) {
		content := "0123456789abcdefghijklmnopqrstuvwxyz"
		h := hashOf(content)
		if err := bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		rc, err := bs.Open(t.Context(), h, int64(len(content)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rc.Close() }()
		readN := func(n int) string {
			t.Helper()
			buf := make([]byte, n)
			got, err := io.ReadFull(rc, buf)
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			return string(buf[:got])
		}
		seek := func(off int64, whence int, want int64) {
			t.Helper()
			if got, err := rc.Seek(off, whence); err != nil || got != want {
				t.Fatalf("Seek(%d, %d) = %d, %v; want %d", off, whence, got, err, want)
			}
		}
		seek(0, io.SeekEnd, int64(len(content)))
		seek(0, io.SeekStart, 0)
		if got := readN(4); got != "0123" {
			t.Fatalf("first read = %q", got)
		}
		if got := readN(4); got != "4567" {
			t.Fatalf("sequential read = %q", got)
		}
		seek(2, io.SeekCurrent, 10)
		if got := readN(3); got != "abc" {
			t.Fatalf("read after SeekCurrent = %q", got)
		}
		seek(-3, io.SeekEnd, int64(len(content))-3)
		if got := readN(10); got != "xyz" {
			t.Fatalf("read of the tail = %q", got)
		}
		if n, err := rc.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("read at end = %d, %v; want EOF", n, err)
		}
		seek(1, io.SeekStart, 1)
		if got := readN(2); got != "12" {
			t.Fatalf("read after seeking back = %q", got)
		}
		seek(100, io.SeekStart, 100)
		if n, err := rc.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("read past the end = %d, %v; want EOF", n, err)
		}
		if _, err := rc.Seek(-1, io.SeekStart); err == nil {
			t.Fatal("Seek to a negative position succeeded")
		}
	})
}

// TestBlobStoreServeContent serves a blob through http.ServeContent, as the
// edge does, with and without Range.
func TestBlobStoreServeContent(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, bs deploys.BlobStore, _ func() []string) {
		content := strings.Repeat("range me ", 100)
		h := hashOf(content)
		if err := bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			rng, want string
			status    int
		}{
			{"", content, http.StatusOK},
			{"bytes=9-16", "range me", http.StatusPartialContent},
			{"bytes=-3", "me ", http.StatusPartialContent},
			{"bytes=891-", "range me ", http.StatusPartialContent},
		} {
			blob, err := bs.Open(t.Context(), h, int64(len(content)))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/f.txt", nil)
			if tc.rng != "" {
				req.Header.Set("Range", tc.rng)
			}
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "text/plain")
			http.ServeContent(rec, req, "", time.Time{}, blob)
			_ = blob.Close()
			if rec.Code != tc.status || rec.Body.String() != tc.want {
				t.Errorf("Range %q: %d %q, want %d %q", tc.rng, rec.Code, rec.Body.String(), tc.status, tc.want)
			}
		}
	})
}

func TestBlobStoreRejectsMismatch(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, bs deploys.BlobStore, stored func() []string) {
		content := "exact content"
		h := hashOf(content)
		for name, tc := range map[string]struct {
			size int64
			body string
		}{
			"other content":  {int64(len(content)), "exact contenT"},
			"short body":     {int64(len(content)), content[:5]},
			"long body":      {int64(len(content)), content + "x"},
			"declared short": {int64(len(content)) - 1, content},
			"declared long":  {int64(len(content)) + 1, content},
			"negative size":  {-1, content},
			"empty":          {0, ""},
		} {
			t.Run(name, func(t *testing.T) {
				err := bs.Put(t.Context(), h, tc.size, strings.NewReader(tc.body))
				if !errors.Is(err, deploys.ErrHashMismatch) {
					t.Fatalf("Put error = %v, want ErrHashMismatch", err)
				}
			})
		}
		if got := stored(); len(got) != 0 {
			t.Fatalf("mismatched puts left %v", got)
		}
		if ok, err := bs.Has(t.Context(), h); err != nil || ok {
			t.Fatalf("Has after mismatches = %v, %v", ok, err)
		}
	})
}

func TestBlobStoreRejectsInvalidHash(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, bs deploys.BlobStore, stored func() []string) {
		for _, h := range []string{"", "../../etc/passwd", strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("g", 64), "ab/" + strings.Repeat("a", 61)} {
			if _, err := bs.Has(t.Context(), h); !errors.Is(err, deploys.ErrInvalidHash) {
				t.Errorf("Has(%q) error = %v", h, err)
			}
			if err := bs.Put(t.Context(), h, 1, strings.NewReader("x")); !errors.Is(err, deploys.ErrInvalidHash) {
				t.Errorf("Put(%q) error = %v", h, err)
			}
			if _, err := bs.Open(t.Context(), h, 1); !errors.Is(err, deploys.ErrInvalidHash) {
				t.Errorf("Open(%q) error = %v", h, err)
			}
			if err := bs.Delete(t.Context(), h); !errors.Is(err, deploys.ErrInvalidHash) {
				t.Errorf("Delete(%q) error = %v", h, err)
			}
		}
		if got := stored(); len(got) != 0 {
			t.Fatalf("invalid hashes created %v", got)
		}
	})
}

func TestBlobStoreConcurrentPut(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, bs deploys.BlobStore, stored func() []string) {
		content := strings.Repeat("concurrent ", 1000)
		h := hashOf(content)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Go(func() { errs[i] = bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)) })
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := stored(); len(got) != 1 {
			t.Fatalf("stored = %v, want the one blob and no temporary uploads", got)
		}
		if got := read(t, bs, h, len(content)); got != content {
			t.Fatal("concurrent puts stored the wrong content")
		}
	})
}

// TestFSBlobStoreFiles: blobs are private files, and putting an existing
// blob keeps the file rather than replacing it.
func TestFSBlobStoreFiles(t *testing.T) {
	dir := t.TempDir()
	bs := deploys.NewFSBlobStore(dir)
	content := "hello, blob"
	h := hashOf(content)
	if err := bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "sha256", h[:2], h[2:]))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("blob mode %v is readable by others", perm)
	}
	if err := bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(filepath.Join(dir, "sha256", h[:2], h[2:]))
	if err != nil || !os.SameFile(info, again) {
		t.Fatalf("existing blob was replaced (%v)", err)
	}
}

func TestLocalNotifier(t *testing.T) {
	n := deploys.NewLocalNotifier()
	var mu sync.Mutex
	got := map[string][]string{}
	sub := func(name string) func() {
		return n.Subscribe(func(siteID string) {
			mu.Lock()
			defer mu.Unlock()
			got[name] = append(got[name], siteID)
		})
	}
	unsubA := sub("a")
	sub("b")
	n.Notify(t.Context(), "s1")
	unsubA()
	unsubA() // idempotent
	n.Notify(t.Context(), "s2")

	if strings.Join(got["a"], ",") != "s1" || strings.Join(got["b"], ",") != "s1,s2" {
		t.Fatalf("deliveries = %v", got)
	}

	// Concurrent notifies and subscriptions are safe under -race.
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { n.Notify(t.Context(), "s3") })
		wg.Go(func() { sub("c")() })
	}
	wg.Wait()
}

// TestFSBlobStoreExistingBlobDoesTheSameWork: putting a blob that exists
// writes and fsyncs a temp file and the directory exactly as a new blob
// does, so upload timing does not reveal that someone stored it before.
func TestFSBlobStoreExistingBlobDoesTheSameWork(t *testing.T) {
	dir := t.TempDir()
	bs := deploys.NewFSBlobStore(dir)
	var synced []string
	bs.SetSyncForTest(func(f *os.File) error {
		synced = append(synced, f.Name())
		return f.Sync()
	})
	content := "stored once"
	h := hashOf(content)
	shard := filepath.Join(dir, "sha256", h[:2])
	put := func() []string {
		t.Helper()
		synced = nil
		if err := bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		return synced
	}
	// What each sync touched: a temp upload file in the shard, then the shard.
	shape := func(names []string) string {
		var out []string
		for _, n := range names {
			switch {
			case n == shard:
				out = append(out, "dir")
			case filepath.Dir(n) == shard && strings.HasPrefix(filepath.Base(n), ".upload-"):
				out = append(out, "temp")
			default:
				out = append(out, n)
			}
		}
		return strings.Join(out, ",")
	}
	first := shape(put())
	before, err := os.Stat(filepath.Join(shard, h[2:]))
	if err != nil {
		t.Fatal(err)
	}
	second := shape(put())
	if first != "temp,dir" || second != first {
		t.Fatalf("syncs for a new blob %q, for an existing one %q; want temp,dir both times", first, second)
	}
	after, err := os.Stat(filepath.Join(shard, h[2:]))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("existing blob was replaced (%v)", err)
	}
	if got := files(t, dir); len(got) != 1 {
		t.Fatalf("files = %v, want only the blob", got)
	}
}

func TestLocalNotifierReentrant(t *testing.T) {
	n := deploys.NewLocalNotifier()
	var mu sync.Mutex
	var calls []string
	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, s)
	}
	// A subscriber that unsubscribes itself and subscribes another from
	// inside its callback must not deadlock.
	var unsub func()
	unsub = n.Subscribe(func(siteID string) {
		record("once:" + siteID)
		unsub()
		n.Subscribe(func(siteID string) { record("later:" + siteID) })
	})
	done := make(chan struct{})
	go func() {
		n.Notify(t.Context(), "s1")
		n.Notify(t.Context(), "s2")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Notify deadlocked on a subscriber that (un)subscribes")
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(calls, ","); got != "once:s1,later:s2" {
		t.Fatalf("calls = %s", got)
	}
}

func TestLocalNotifierConcurrent(t *testing.T) {
	n := deploys.NewLocalNotifier()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { n.Notify(t.Context(), "s") })
		wg.Go(func() {
			unsub := n.Subscribe(func(string) {})
			n.Notify(t.Context(), "s")
			unsub()
		})
	}
	wg.Wait()
}
