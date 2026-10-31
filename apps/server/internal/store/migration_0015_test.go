// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0015 checks the redirects and headers columns 0015 adds to
// deploys with raw SQL: existing deploys have none, and a deploy written
// afterwards keeps its rules' JSON as given.
func TestMigration0015(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 14); err != nil {
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
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM deploys WHERE redirects IS NULL AND headers IS NULL`); err != nil || got != 1 {
		t.Fatalf("existing deploys without rules = %d, %v; want 1", got, err)
	}
	q := `INSERT INTO deploys (id, site_id, file_count, total_size, created_at, redirects, headers) VALUES ('d2', 's1', 0, 0, 2, '[{"from":"/a","to":"/b","status":301}]', '[{"path":"/*","set":{"X-Frame-Options":"DENY"}}]')`
	if err := db.ExecForTest(ctx, q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM deploys WHERE id = 'd2' AND redirects LIKE '[{"from":"/a"%' AND headers LIKE '[{"path":"/*"%'`); err != nil || got != 1 {
		t.Fatalf("deploy with rules = %d, %v; want 1", got, err)
	}
	d, err := db.Deploys().Get(ctx, store.ActorFor("o1"), "s1", "d2")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Redirects) != 1 || d.Redirects[0] != (store.Redirect{From: "/a", To: "/b", Status: 301}) || len(d.Headers) != 1 || d.Headers[0].Set["X-Frame-Options"] != "DENY" {
		t.Fatalf("Get() = %+v, want the rules written", d)
	}
}
