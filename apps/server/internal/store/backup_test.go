// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestBackupDuringWrites backs up a database while writers on two handles
// (one standing in for the running server, one for another process) keep
// inserting, then opens the copy and checks it is a consistent snapshot.
func TestBackupDuringWrites(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	src := filepath.Join(dir, "wardlock.db")
	db, err := store.Open(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	// Each writer inserts sites in order, so a consistent snapshot holds a
	// prefix of each writer's sequence.
	var written [2]atomic.Int64
	// SQLite's write lock is not fair: on a loaded machine one handle can win
	// it for seconds while the other sits in busy_timeout. A writer that gets
	// this far ahead pauses, which frees the lock for the other, so both make
	// progress however the lock is handed out. Inside the window they still
	// contend for real.
	const maxLead = 8
	writeCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for w, h := range []*store.DB{db, other} {
		wg.Go(func() {
			for i := 0; writeCtx.Err() == nil; i++ {
				for written[w].Load()-written[1-w].Load() >= maxLead && writeCtx.Err() == nil {
					time.Sleep(time.Millisecond)
				}
				s := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: fmt.Sprintf("w%d-%06d", w, i), CreatedAt: time.Now()}
				if err := h.Sites().Create(writeCtx, s, store.Quotas{}); err != nil {
					// A slow machine can still run out one busy_timeout.
					// Lock fairness is not what this test checks, so try
					// the same site again.
					if strings.Contains(err.Error(), "SQLITE_BUSY") {
						i--
						continue
					}
					if writeCtx.Err() == nil {
						t.Errorf("writer %d: %v", w, err)
					}
					return
				}
				written[w].Add(1)
			}
		})
	}
	waitFor(t, func() bool { return written[0].Load() > 50 && written[1].Load() > 50 })

	dst := filepath.Join(dir, "backup.db")
	before := written[0].Load() + written[1].Load()
	if err := db.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	stop()
	wg.Wait()
	after := written[0].Load() + written[1].Load()

	// The backup is one file: no WAL or journal beside it, no leftovers.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		switch e.Name() {
		case "wardlock.db", "wardlock.db-wal", "wardlock.db-shm", "backup.db":
		default:
			t.Errorf("unexpected file %s after backup", e.Name())
		}
	}

	// Restore: the copy alone in a fresh data directory opens, migrates
	// (nothing to do) and answers queries.
	restoreDir := t.TempDir()
	restored := filepath.Join(restoreDir, "wardlock.db")
	if err := os.Rename(dst, restored); err != nil {
		t.Fatal(err)
	}
	rdb, err := store.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rdb.Close() }()
	applied, err := rdb.Migrate(ctx, time.Now)
	if err != nil || len(applied) != 0 {
		t.Fatalf("Migrate on restored copy = %v, %v; want nothing to apply", applied, err)
	}
	if err := rdb.Check(ctx); err != nil {
		t.Fatal(err)
	}
	var got int64
	seen := map[string]bool{}
	for after := ""; ; {
		page, err := rdb.Sites().List(ctx, owner.ID, after, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range page {
			seen[s.Name] = true
		}
		got += int64(len(page))
		if len(page) < 500 {
			break
		}
		after = page[len(page)-1].Name
	}
	if got < before || got > after {
		t.Errorf("backup holds %d sites; %d existed when it started and %d when it finished", got, before, after)
	}
	for w := range 2 {
		n := 0
		for seen[fmt.Sprintf("w%d-%06d", w, n)] {
			n++
		}
		for name := range seen {
			var ww, i int
			if _, err := fmt.Sscanf(name, "w%d-%06d", &ww, &i); err == nil && ww == w && i >= n {
				t.Errorf("writer %d: backup has %s but not w%d-%06d: not a consistent snapshot", w, name, w, n)
			}
		}
	}
	// The restored database takes writes.
	if err := rdb.Sites().Create(ctx, store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "after-restore", CreatedAt: time.Now()}, store.Quotas{}); err != nil {
		t.Fatalf("write to restored database: %v", err)
	}
}

func TestBackupNeverOverwrites(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	dst := filepath.Join(dir, "existing.db")
	if err := os.WriteFile(dst, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.Backup(ctx, dst); !errors.Is(err, store.ErrBackupExists) {
		t.Fatalf("Backup over an existing file = %v, want ErrBackupExists", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "keep me" {
		t.Errorf("existing file changed to %q", b)
	}
	if err := db.Backup(ctx, filepath.Join(dir, "missing", "b.db")); err == nil {
		t.Error("Backup into a missing directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if name := e.Name(); name != "wardlock.db" && name != "existing.db" && name != "wardlock.db-wal" && name != "wardlock.db-shm" {
			t.Errorf("unexpected file %s left behind", name)
		}
	}
}

// TestFailedBackupLeavesNothing fails a backup while its copy is open in
// WAL mode (so -wal and -shm exist beside it), and cancels one before it
// starts: neither leaves a file behind.
func TestFailedBackupLeavesNothing(t *testing.T) {
	ctx := t.Context()
	srcDir, dstDir := t.TempDir(), t.TempDir()
	db, err := store.Open(ctx, filepath.Join(srcDir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	var sidecars []string
	db.SetBackupFaultForTest(func(tmp string) error {
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(tmp + suffix); err == nil {
				sidecars = append(sidecars, suffix)
			}
		}
		return errors.New("injected failure")
	})
	dst := filepath.Join(dstDir, "backup.db")
	if err := db.Backup(ctx, dst); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("Backup = %v, want the injected failure", err)
	}
	if len(sidecars) != 2 {
		t.Errorf("copy had sidecars %v while open, want -wal and -shm (the test would prove nothing)", sidecars)
	}
	db.SetBackupFaultForTest(nil)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := db.Backup(cancelled, dst); err == nil {
		t.Fatal("Backup with a cancelled context succeeded")
	}
	if entries, _ := os.ReadDir(dstDir); len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("failed backups left %v", names)
	}
	// A later backup still works.
	if err := db.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup after failures: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	const timeout = 2 * time.Minute
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
