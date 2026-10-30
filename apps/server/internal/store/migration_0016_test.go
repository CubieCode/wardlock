// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0016 checks the primary-hostname columns 0016 adds to sites
// with raw SQL: existing sites have no primary, the flag takes only 0 and
// 1, a chosen domain must exist, and removing it clears the choice.
func TestMigration0016(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 15); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO domains (id, site_id, hostname, status, verification_token, created_at) VALUES ('m1', 's1', 'docs.example.test', 'active', 'verify-token-000001', 1)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM sites WHERE primary_base = 0 AND primary_domain_id IS NULL`); err != nil || got != 1 {
		t.Fatalf("existing sites without a primary = %d, %v; want 1", got, err)
	}
	for _, bad := range []string{
		`UPDATE sites SET primary_base = 2 WHERE id = 's1'`,
		`UPDATE sites SET primary_domain_id = 'missing' WHERE id = 's1'`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
	if err := db.ExecForTest(ctx, `UPDATE sites SET primary_domain_id = 'm1' WHERE id = 's1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.ExecForTest(ctx, `DELETE FROM domains WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM sites WHERE primary_domain_id IS NULL`); err != nil || got != 1 {
		t.Fatalf("sites without a primary after removing its domain = %d, %v; want 1", got, err)
	}
}
