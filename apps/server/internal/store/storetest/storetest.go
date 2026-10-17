// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package storetest opens throwaway migrated databases for tests. Repository
// contract tests (one suite per repository interface) use it so a future
// backend can run the same suites.
package storetest

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// New returns a migrated database in t's temporary directory, closed when the
// test ends.
func New(t testing.TB) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(t.Context(), time.Now); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return db
}
