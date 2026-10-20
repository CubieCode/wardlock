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

// TestMigration0010 checks that 0010 leaves existing owners following the
// instance default (not exempt) and that moderation_results enforces its
// rules.
func TestMigration0010(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if err := db.ExecForTest(ctx, `INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	if got, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM owners WHERE moderation_exempt = 0`); err != nil || got != 1 {
		t.Fatalf("owners not exempt = %d, %v; want 1", got, err)
	}
	a := strings.Repeat("a", 64)
	for _, ok := range []string{
		`UPDATE owners SET moderation_exempt = 1 WHERE id = 'o1'`,
		`INSERT INTO moderation_results (sha256, kind, status, queued_at, next_attempt_at) VALUES ('` + a + `', 'image', 'pending', 1, 1)`,
		`UPDATE moderation_results SET status = 'quarantined', categories = 'sexual', next_attempt_at = NULL, checked_at = 2 WHERE sha256 = '` + a + `'`,
		`UPDATE moderation_results SET kind = 'image,text', reason = 'unscreenable', decoded_sha256 = '` + a + `' WHERE sha256 = '` + a + `'`,
		// kind defaults to '' (nothing covered yet).
		`INSERT INTO moderation_results (sha256, status, queued_at) VALUES ('` + strings.Repeat("c", 64) + `', 'pending', 1)`,
	} {
		if err := db.ExecForTest(ctx, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`UPDATE owners SET moderation_exempt = 2 WHERE id = 'o1'`,
		`UPDATE owners SET moderation_exempt = NULL WHERE id = 'o1'`,
		`INSERT INTO moderation_results (sha256, kind, status, queued_at) VALUES ('` + a + `', 'image', 'pending', 1)`,
		`INSERT INTO moderation_results (sha256, kind, status, queued_at) VALUES ('` + strings.Repeat("A", 64) + `', 'image', 'pending', 1)`,
		`INSERT INTO moderation_results (sha256, kind, status, queued_at) VALUES ('abc', 'image', 'pending', 1)`,
		`INSERT INTO moderation_results (sha256, kind, status, queued_at) VALUES ('` + strings.Repeat("b", 64) + `', 'video', 'pending', 1)`,
		`INSERT INTO moderation_results (sha256, kind, status, queued_at) VALUES ('` + strings.Repeat("b", 64) + `', 'image', 'banned', 1)`,
		`INSERT INTO moderation_results (sha256, kind, status, attempts, queued_at) VALUES ('` + strings.Repeat("b", 64) + `', 'image', 'pending', -1, 1)`,
		`INSERT INTO moderation_results (sha256, kind, status) VALUES ('` + strings.Repeat("b", 64) + `', 'image', 'pending')`,
		`UPDATE moderation_results SET kind = 'text,image' WHERE sha256 = '` + a + `'`,
		`UPDATE moderation_results SET decoded_sha256 = 'abc' WHERE sha256 = '` + a + `'`,
		`UPDATE moderation_results SET decoded_sha256 = '` + strings.Repeat("A", 64) + `' WHERE sha256 = '` + a + `'`,
		`UPDATE moderation_results SET reason = NULL WHERE sha256 = '` + a + `'`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
}
