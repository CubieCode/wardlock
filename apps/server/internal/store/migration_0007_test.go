// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"

	"wardlock.nz/server/internal/store/storetest"
)

// TestMigration0007 checks the immutable_assets column 0007 adds to deploys
// with raw SQL: it defaults to 0 for existing and new rows and accepts only
// 0 and 1.
func TestMigration0007(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at) VALUES ('d1', 's1', 0, 0, 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, immutable_assets) VALUES ('d2', 's1', 0, 0, 1, 1)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for query, want := range map[string]int{
		`SELECT immutable_assets FROM deploys WHERE id = 'd1'`: 0,
		`SELECT immutable_assets FROM deploys WHERE id = 'd2'`: 1,
	} {
		got, err := db.CountForTest(ctx, query)
		if err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", query, got, err, want)
		}
	}
	for _, bad := range []string{
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, immutable_assets) VALUES ('d3', 's1', 0, 0, 1, 2)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, immutable_assets) VALUES ('d4', 's1', 0, 0, 1, -1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, immutable_assets) VALUES ('d5', 's1', 0, 0, 1, NULL)`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
}
