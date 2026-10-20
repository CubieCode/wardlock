// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0009 writes deploys at version 8 and checks that 0009
// counts them: each owner's distinct hashes once with the number of
// deploys listing them, stored_bytes as their sum, every deploy in the
// deploy log, and no overrides. Then it checks the new columns' rules.
func TestMigration0009(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 8); err != nil {
		t.Fatal(err)
	}
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1), ('o2', 'two@example.test', 1), ('o3', 'three@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1), ('s2', 'o1', 'blog', 1), ('s3', 'o2', 'shop', 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at) VALUES ('d1', 's1', 3, 50, 10), ('d2', 's2', 1, 10, 20), ('d3', 's3', 1, 10, 30)`,
		// o1 lists a twice in d1 and again in d2 (on another site), b once.
		`INSERT INTO deploy_files (deploy_id, path, sha256, size) VALUES
		  ('d1', 'index.html', '` + a + `', 10), ('d1', 'copy.html', '` + a + `', 10), ('d1', 'b.bin', '` + b + `', 30),
		  ('d2', 'index.html', '` + a + `', 10),
		  ('d3', 'index.html', '` + a + `', 10)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]int{
		`SELECT stored_bytes FROM owners WHERE id = 'o1'`:                                                                   40,
		`SELECT stored_bytes FROM owners WHERE id = 'o2'`:                                                                   10,
		`SELECT stored_bytes FROM owners WHERE id = 'o3'`:                                                                   0,
		`SELECT deploys FROM owner_blob_refs WHERE owner_id = 'o1' AND sha256 = '` + a + `'`:                                2,
		`SELECT deploys FROM owner_blob_refs WHERE owner_id = 'o1' AND sha256 = '` + b + `'`:                                1,
		`SELECT size FROM owner_blob_refs WHERE owner_id = 'o1' AND sha256 = '` + b + `'`:                                   30,
		`SELECT COUNT(*) FROM owner_blob_refs`:                                                                              3,
		`SELECT COUNT(*) FROM owner_deploy_log WHERE owner_id = 'o1'`:                                                       2,
		`SELECT created_at FROM owner_deploy_log WHERE deploy_id = 'd3' AND owner_id = 'o2'`:                                30,
		`SELECT COUNT(*) FROM owners WHERE max_storage_bytes IS NULL AND max_sites IS NULL AND max_deploys_per_day IS NULL`: 3,
	} {
		got, err := db.CountForTest(ctx, query)
		if err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", query, got, err, want)
		}
	}
	for _, ok := range []string{
		`UPDATE owners SET max_storage_bytes = 0, max_sites = 5, max_deploys_per_day = 100 WHERE id = 'o3'`,
		`UPDATE owners SET max_storage_bytes = NULL WHERE id = 'o3'`,
	} {
		if err := db.ExecForTest(ctx, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`UPDATE owners SET max_storage_bytes = -1 WHERE id = 'o3'`,
		`UPDATE owners SET max_sites = -1 WHERE id = 'o3'`,
		`UPDATE owners SET max_deploys_per_day = -1 WHERE id = 'o3'`,
		`UPDATE owners SET stored_bytes = -1 WHERE id = 'o3'`,
		`UPDATE owners SET stored_bytes = NULL WHERE id = 'o3'`,
		`INSERT INTO owner_blob_refs (owner_id, sha256, size, deploys) VALUES ('o1', '` + a + `', 1, 1)`,
		`INSERT INTO owner_blob_refs (owner_id, sha256, size, deploys) VALUES ('o3', '` + strings.Repeat("A", 64) + `', 1, 1)`,
		`INSERT INTO owner_blob_refs (owner_id, sha256, size, deploys) VALUES ('o3', '` + a + `', -1, 1)`,
		`INSERT INTO owner_blob_refs (owner_id, sha256, size, deploys) VALUES ('o3', '` + a + `', 1, -1)`,
		`INSERT INTO owner_blob_refs (owner_id, sha256, size, deploys) VALUES ('nobody', '` + a + `', 1, 1)`,
		`INSERT INTO owner_deploy_log (owner_id, deploy_id, created_at) VALUES ('o1', 'd1', 99)`,
		`INSERT INTO owner_deploy_log (owner_id, deploy_id, created_at) VALUES ('nobody', 'd9', 1)`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
	// Deleting an owner deletes their counts and nobody else's.
	for _, q := range []string{`DELETE FROM sites WHERE id = 's3'`, `DELETE FROM owners WHERE id = 'o2'`} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM owner_blob_refs`:  2,
		`SELECT COUNT(*) FROM owner_deploy_log`: 2,
	} {
		got, err := db.CountForTest(ctx, query)
		if err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", query, got, err, want)
		}
	}
}
