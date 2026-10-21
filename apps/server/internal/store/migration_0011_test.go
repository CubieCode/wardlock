// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0011 checks the path_prefix and base_deploy_id columns 0011
// adds to deploys with raw SQL: existing deploys become full deploys, a
// prefix must be a clean '/.../' other than '/', and deleting a base clears
// the reference rather than the path deploy.
func TestMigration0011(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at) VALUES ('d1', 's1', 0, 0, 1)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM deploys WHERE path_prefix IS NULL AND base_deploy_id IS NULL`); err != nil || got != 1 {
		t.Fatalf("existing full deploys = %d, %v; want 1", got, err)
	}
	for _, ok := range []string{
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix, base_deploy_id) VALUES ('d2', 's1', 0, 0, 2, '/mockup-x/', 'd1')`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix) VALUES ('d3', 's1', 0, 0, 3, '/a/b/')`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix) VALUES ('d4', 's1', 0, 0, 4, '/x/')`,
	} {
		if err := db.ExecForTest(ctx, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix) VALUES ('b1', 's1', 0, 0, 1, '/')`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix) VALUES ('b2', 's1', 0, 0, 1, '//')`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix) VALUES ('b3', 's1', 0, 0, 1, 'docs/')`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix) VALUES ('b4', 's1', 0, 0, 1, '/docs')`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix) VALUES ('b5', 's1', 0, 0, 1, '')`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, path_prefix, base_deploy_id) VALUES ('b6', 's1', 0, 0, 1, '/x/', 'nope')`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
	if err := db.ExecForTest(ctx, `DELETE FROM deploys WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM deploys WHERE id = 'd2' AND path_prefix = '/mockup-x/' AND base_deploy_id IS NULL`); err != nil || got != 1 {
		t.Fatalf("path deploy after its base was deleted = %d, %v; want 1 with no base", got, err)
	}
}
