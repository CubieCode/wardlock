// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"

	"wardlock.nz/server/internal/store/storetest"
)

// TestMigration0008 checks the nullable suspension and takedown columns
// 0008 adds with raw SQL: existing and new rows start NULL, and a time can
// be set and cleared.
func TestMigration0008(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO owners (id, email, created_at, suspended_at) VALUES ('o2', 'two@example.test', 1, 5)`,
		`INSERT INTO sites (id, owner_id, name, created_at, taken_down_at) VALUES ('s2', 'o2', 'blog', 1, 7)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM owners WHERE id = 'o1' AND suspended_at IS NULL`: 1,
		`SELECT COUNT(*) FROM sites WHERE id = 's1' AND taken_down_at IS NULL`: 1,
		`SELECT suspended_at FROM owners WHERE id = 'o2'`:                      5,
		`SELECT taken_down_at FROM sites WHERE id = 's2'`:                      7,
	} {
		got, err := db.CountForTest(ctx, query)
		if err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", query, got, err, want)
		}
	}
	for _, q := range []string{
		`UPDATE owners SET suspended_at = NULL WHERE id = 'o2'`,
		`UPDATE sites SET taken_down_at = NULL WHERE id = 's2'`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}
