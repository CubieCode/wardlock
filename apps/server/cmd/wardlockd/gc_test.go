// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

func TestGCCommand(t *testing.T) {
	env := map[string]string{"WARDLOCK_DATA_DIR": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	cfg, err := platform.LoadConfig(getenv)
	if err != nil {
		t.Fatal(err)
	}
	// An owner's upload whose deploy is long gone.
	db, err := openStore(t.Context(), cfg, platform.NewLogger(io.Discard, cfg))
	if err != nil {
		t.Fatal(err)
	}
	body := "orphaned page"
	sum := sha256.Sum256([]byte(body))
	h := hex.EncodeToString(sum[:])
	blobs := deploys.NewFSBlobStore(cfg.BlobDir)
	if err := blobs.Put(t.Context(), h, int64(len(body)), strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	owner := store.Owner{ID: store.NewID(), Email: "gc@example.test", CreatedAt: time.Now()}
	if err := db.Owners().Create(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := db.Deploys().RecordUpload(t.Context(), store.ActorFor(owner.ID), owner.ID, store.Blob{SHA256: h, Size: int64(len(body)), CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	var out, logs bytes.Buffer
	if err := run(t.Context(), []string{"gc"}, getenv, &out, &logs); err != nil {
		t.Fatalf("gc: %v\n%s", err, logs.String())
	}
	if want := "deleted 1 blobs and 0 unrecorded files, freed 13 bytes"; !strings.Contains(out.String(), want) {
		t.Fatalf("output %q lacks %q", out.String(), want)
	}
	if !strings.Contains(logs.String(), `"bytes_freed":13`) {
		t.Fatalf("log lacks bytes freed:\n%s", logs.String())
	}
	if ok, err := blobs.Has(t.Context(), h); err != nil || ok {
		t.Fatalf("blob still stored: %v, %v", ok, err)
	}

	out.Reset()
	if err := run(t.Context(), []string{"gc", "--help"}, getenv, &out, io.Discard); err != nil || !strings.Contains(out.String(), "WARDLOCK_DEPLOY_HISTORY") {
		t.Fatalf("gc --help: %v, %q", err, out.String())
	}
	if err := run(t.Context(), []string{"gc", "now"}, getenv, io.Discard, io.Discard); !errors.Is(err, errUsage) {
		t.Fatalf("gc now: %v, want usage error", err)
	}
}
