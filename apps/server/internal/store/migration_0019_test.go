// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0019 checks what 0019 adds for webhooks (I6.3) with raw SQL:
// it leaves the outbox's rows alone, the columns reject what they should, a
// listed site may outlive its row in sites, and everything goes with its
// owner.
func TestMigration0019(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 18); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO notification_outbox (id, channel, event_key, owner_id, event, payload, status, created_at)
		 VALUES ('n1', 'email', 'k', 'o1', 'e', '{}', 'pending', 1)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	if n, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM notification_outbox`); err != nil || n != 1 {
		t.Fatalf("outbox after the migration: %d, %v", n, err)
	}
	hook := func(id, url, disabled string) string {
		return `INSERT INTO webhooks (id, owner_id, url, all_events, all_sites, secret_salt, created_at, disabled_reason)
			VALUES ('` + id + `', 'o1', '` + url + `', 1, 1, 's', 1, ` + disabled + `)`
	}
	delivery := func(id, webhook, status string) string {
		return `INSERT INTO webhook_deliveries (id, webhook_id, event_id, event, body, status, created_at)
			VALUES ('` + id + `', '` + webhook + `', 'e', 'ping', '{}', '` + status + `', 1)`
	}
	for _, q := range []string{
		hook("w1", "https://a.example.test/", "NULL"),
		`INSERT INTO webhook_events (webhook_id, event) VALUES ('w1', 'ping')`,
		`INSERT INTO webhook_sites (webhook_id, site_id) VALUES ('w1', 'a-site-long-gone')`,
		delivery("d1", "w1", "pending"),
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"empty URL":       hook("w2", "", "NULL"),
		"unknown reason":  hook("w3", "https://a.example.test/", "'bored'"),
		"unknown status":  delivery("d2", "w1", "lost"),
		"unknown webhook": delivery("d3", "nothing", "pending"),
		"repeated event":  `INSERT INTO webhook_events (webhook_id, event) VALUES ('w1', 'ping')`,
	} {
		if err := db.ExecForTest(ctx, q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := db.ExecForTest(ctx, `DELETE FROM owners WHERE id = 'o1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"webhooks", "webhook_events", "webhook_sites", "webhook_deliveries"} {
		if n, err := db.CountForTest(ctx, `SELECT COUNT(*) FROM `+table); err != nil || n != 0 {
			t.Fatalf("%s after deleting the owner: %d, %v", table, n, err)
		}
	}
}
