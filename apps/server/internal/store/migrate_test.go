// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wardlock.db")
	for run, wantApplied := range []bool{true, false} {
		db, err := store.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		applied, err := db.Migrate(t.Context(), time.Now)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if got := len(applied) > 0; got != wantApplied {
			t.Fatalf("run %d: applied %v, want any applied = %v", run, applied, wantApplied)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateRejectsNewerDatabase(t *testing.T) {
	db := storetest.New(t)
	if err := db.ExecForTest(t.Context(), `INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, 'future', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(t.Context(), time.Now); !errors.Is(err, store.ErrDatabaseNewer) {
		t.Fatalf("Migrate() error = %v, want ErrDatabaseNewer", err)
	}
}

func TestOpenEnablesWALAndForeignKeys(t *testing.T) {
	db := storetest.New(t)
	for pragma, want := range map[string]string{"journal_mode": "wal", "foreign_keys": "1"} {
		got, err := db.PragmaForTest(t.Context(), pragma)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
}
