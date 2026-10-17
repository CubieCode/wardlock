// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
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

func TestFSBlobStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "blobs")
	bs := deploys.NewFSBlobStore(dir)
	ctx := t.Context()
	content := "hello, blob"
	h := hashOf(content)

	if ok, err := bs.Has(ctx, h); err != nil || ok {
		t.Fatalf("Has(new) = %v, %v", ok, err)
	}
	if _, err := bs.Open(ctx, h); !errors.Is(err, deploys.ErrBlobNotFound) {
		t.Fatalf("Open(new) error = %v, want ErrBlobNotFound", err)
	}
	if err := bs.Put(ctx, h, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if got, want := files(t, dir), []string{"sha256/" + h[:2] + "/" + h[2:]}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if ok, err := bs.Has(ctx, h); err != nil || !ok {
		t.Fatalf("Has(stored) = %v, %v", ok, err)
	}
	rc, err := bs.Open(ctx, h)
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
	info, err := os.Stat(filepath.Join(dir, "sha256", h[:2], h[2:]))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("blob mode %v is readable by others", perm)
	}

	// Putting an existing blob verifies it and keeps the file.
	if err := bs.Put(ctx, h, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := bs.Put(ctx, h, int64(len(content)), strings.NewReader("hello, blub")); !errors.Is(err, deploys.ErrHashMismatch) {
		t.Fatalf("Put(existing, wrong body) error = %v, want ErrHashMismatch", err)
	}
	again, err := os.Stat(filepath.Join(dir, "sha256", h[:2], h[2:]))
	if err != nil || !os.SameFile(info, again) {
		t.Fatalf("existing blob was replaced (%v)", err)
	}

	if err := bs.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	if ok, _ := bs.Has(ctx, h); ok {
		t.Fatal("Has after Delete = true")
	}
	if err := bs.Delete(ctx, h); err != nil {
		t.Fatalf("Delete(missing) = %v, want nil", err)
	}
}

func TestFSBlobStoreRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	bs := deploys.NewFSBlobStore(dir)
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
	if got := files(t, dir); len(got) != 0 {
		t.Fatalf("mismatched puts left %v", got)
	}
}

func TestFSBlobStoreRejectsInvalidHash(t *testing.T) {
	dir := t.TempDir()
	bs := deploys.NewFSBlobStore(dir)
	for _, h := range []string{"", "../../etc/passwd", strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("g", 64), "ab/" + strings.Repeat("a", 61)} {
		if _, err := bs.Has(t.Context(), h); !errors.Is(err, deploys.ErrInvalidHash) {
			t.Errorf("Has(%q) error = %v", h, err)
		}
		if err := bs.Put(t.Context(), h, 1, strings.NewReader("x")); !errors.Is(err, deploys.ErrInvalidHash) {
			t.Errorf("Put(%q) error = %v", h, err)
		}
		if _, err := bs.Open(t.Context(), h); !errors.Is(err, deploys.ErrInvalidHash) {
			t.Errorf("Open(%q) error = %v", h, err)
		}
		if err := bs.Delete(t.Context(), h); !errors.Is(err, deploys.ErrInvalidHash) {
			t.Errorf("Delete(%q) error = %v", h, err)
		}
	}
	if got := files(t, dir); len(got) != 0 {
		t.Fatalf("invalid hashes created %v", got)
	}
}

func TestFSBlobStoreConcurrentPut(t *testing.T) {
	dir := t.TempDir()
	bs := deploys.NewFSBlobStore(dir)
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
	if got := files(t, dir); len(got) != 1 {
		t.Fatalf("files = %v, want the one blob and no temp files", got)
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
