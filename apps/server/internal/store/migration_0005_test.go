// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"strings"
	"testing"

	"wardlock.nz/server/internal/store/storetest"
)

// TestMigration0005 checks deploy_renditions' constraints and that deleting
// a manifest entry, or its deploy, deletes its renditions, with raw SQL
// since no repository method deletes single manifest entries.
func TestMigration0005(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	h := strings.Repeat("a", 64)
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'notes', 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at) VALUES ('d1', 's1', 2, 2, 1), ('d2', 's1', 1, 1, 1)`,
		`INSERT INTO deploy_files (deploy_id, path, sha256, size) VALUES ('d1', 'index.html', '` + h + `', 1), ('d1', 'a.html', '` + h + `', 1), ('d2', 'index.html', '` + h + `', 1)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d1', 'index.html', 'md', '` + h + `', 0), ('d1', 'index.html', 'txt', '` + h + `', 0), ('d1', 'a.html', 'md', '` + h + `', 0), ('d2', 'index.html', 'md', '` + h + `', 0)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, bad := range []string{
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d1', 'index.html', 'md', '` + h + `', 0)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d1', 'missing.html', 'md', '` + h + `', 0)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d9', 'index.html', 'md', '` + h + `', 0)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d2', 'index.html', 'html', '` + h + `', 0)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d2', 'index.html', 'txt', '` + strings.ToUpper(h) + `', 0)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d2', 'index.html', 'txt', 'abc', 0)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size) VALUES ('d2', 'index.html', 'txt', '` + h + `', -1)`,
		`INSERT INTO deploy_renditions (deploy_id, path, format, sha256) VALUES ('d2', 'index.html', 'txt', '` + h + `')`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
	for _, step := range []struct {
		q    string
		want int
	}{
		{`DELETE FROM deploy_files WHERE deploy_id = 'd1' AND path = 'a.html'`, 3},
		{`DELETE FROM deploys WHERE id = 'd1'`, 1},
	} {
		if err := db.ExecForTest(ctx, step.q); err != nil {
			t.Fatal(err)
		}
		got, err := db.CountForTest(ctx, `SELECT count(*) FROM deploy_renditions`)
		if err != nil || got != step.want {
			t.Fatalf("after %s: %d renditions, %v; want %d", step.q, got, err, step.want)
		}
	}
}
