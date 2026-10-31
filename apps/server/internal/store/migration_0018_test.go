// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0018 checks what 0018 adds for notifications (I6.2) with raw
// SQL: a link already used up is not marked, so upgrading sends nothing
// about it; the outbox holds one row per channel and key, knows its
// statuses and goes with its owner, as do opt-outs.
func TestMigration0018(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 17); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO share_links (id, site_id, public_id, token_hash, path_prefix, created_at, max_uses, use_count)
		 VALUES ('l1', 's1', 'p000000001', 'h', '/', 1, 1, 1)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	n, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM share_links WHERE used_up_at IS NOT NULL`)
	if err != nil || n != 0 {
		t.Fatalf("used-up links marked by the migration: %d, %v", n, err)
	}
	row := func(id, channel, key, status string) string {
		return `INSERT INTO notification_outbox (id, channel, event_key, owner_id, event, payload, status, created_at)
			VALUES ('` + id + `', '` + channel + `', '` + key + `', 'o1', 'e', '{}', '` + status + `', 1)`
	}
	for _, q := range []string{
		row("n1", "email", "k", "pending"),
		row("n2", "webhook", "k", "sent"),
		`INSERT INTO notification_opt_outs (owner_id, event) VALUES ('o1', 'e')`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"repeated key":   row("n3", "email", "k", "pending"),
		"unknown status": row("n4", "email", "other", "lost"),
		"unknown owner": `INSERT INTO notification_outbox (id, channel, event_key, owner_id, event, payload, status, created_at)
			VALUES ('n5', 'email', 'k5', 'nobody', 'e', '{}', 'pending', 1)`,
		"repeated opt-out": `INSERT INTO notification_opt_outs (owner_id, event) VALUES ('o1', 'e')`,
	} {
		if err := db.ExecForTest(ctx, q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, q := range []string{`DELETE FROM sites WHERE id = 's1'`, `DELETE FROM owners WHERE id = 'o1'`} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, table := range []string{"notification_outbox", "notification_opt_outs"} {
		if n, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM `+table); err != nil || n != 0 {
			t.Fatalf("%s after deleting the owner: %d, %v", table, n, err)
		}
	}
}
