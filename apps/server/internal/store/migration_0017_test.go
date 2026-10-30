// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0017 checks what 0017 adds for previews (C1.19) with raw
// SQL: existing deploys and access-log entries are not previews, preview
// names and lifetimes are checked, a site has one preview per name, and
// deleting a site or a deploy deletes its previews.
func TestMigration0017(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 16); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s2', 'o1', 'blog', 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at) VALUES ('d1', 's1', 0, 0, 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at) VALUES ('d2', 's2', 0, 0, 1)`,
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via) VALUES ('a1', 1, 's1', 'docs.example.test', '/', 200, 'public')`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM deploys WHERE preview IS NULL AND preview_lifetime_days IS NULL AND preview_activated_at IS NULL`: 2,
		`SELECT COUNT(*) FROM access_log WHERE preview IS NULL`:                                                                 1,
		`SELECT COUNT(*) FROM previews`: 0,
	} {
		if got, err := db.CountForTest(ctx, q); err != nil || got != want {
			t.Fatalf("%s = %d, %v; want %d", q, got, err, want)
		}
	}
	insert := func(site, name, deploy string) string {
		return `INSERT INTO previews (site_id, name, deploy_id, lifetime_days, created_at, deployed_at) VALUES ('` +
			site + `', '` + name + `', '` + deploy + `', 14, 1, 1)`
	}
	for _, bad := range []string{
		insert("s1", "", "d1"),
		insert("s1", "-pr", "d1"),
		insert("s1", "pr-", "d1"),
		insert("s1", "a--b", "d1"),
		insert("s1", "PR", "d1"),
		insert("s1", "abcdefghijklmnopqrstu", "d1"),
		insert("s1", "pr", "missing"),
		`INSERT INTO previews (site_id, name, deploy_id, lifetime_days, created_at, deployed_at) VALUES ('s1', 'pr', 'd1', 91, 1, 1)`,
		`UPDATE deploys SET preview_lifetime_days = 91 WHERE id = 'd1'`,
		`UPDATE deploys SET preview = '' WHERE id = 'd1'`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
	for _, ok := range []string{insert("s1", "pr-1", "d1"), insert("s2", "pr-1", "d2")} {
		if err := db.ExecForTest(ctx, ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
	if err := db.ExecForTest(ctx, insert("s1", "pr-1", "d1")); err == nil {
		t.Error("a second preview of the same name on one site was accepted")
	}
	for _, q := range []string{`DELETE FROM sites WHERE id = 's1'`, `DELETE FROM deploys WHERE id = 'd2'`} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM previews`); err != nil || got != 0 {
		t.Fatalf("previews after deleting their site and deploy = %d, %v; want 0", got, err)
	}
}
