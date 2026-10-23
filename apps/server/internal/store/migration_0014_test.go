// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0014 checks what 0014 adds with raw SQL: existing access-log
// entries keep no country; access_log.country and traffic_countries take
// only two upper-case letters or 'unknown'; rollup rows are keyed by site,
// hour and country; and deleting a site deletes its country rows but not
// its access-log entries.
func TestMigration0014(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 13); err != nil {
		t.Fatal(err)
	}
	exec := func(qs ...string) {
		t.Helper()
		for _, q := range qs {
			if err := db.ExecForTest(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	count := func(q string, want int) {
		t.Helper()
		if got, err := db.CountForTest(ctx, q); err != nil || got != want {
			t.Fatalf("%s = %d, %v; want %d", q, got, err, want)
		}
	}
	exec(
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, ip) VALUES ('a1', 3600000, 's1', 'docs.wardlock.localhost', '/', 200, 'public', '192.0.2.0')`,
	)
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	count(`SELECT COUNT(*) FROM access_log WHERE country IS NULL`, 1)
	count(`SELECT COUNT(*) FROM traffic_countries`, 0)

	exec(
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, country) VALUES ('a2', 3600000, 's1', 'docs.wardlock.localhost', '/', 200, 'public', 'NZ')`,
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, country) VALUES ('a3', 3600000, 's1', 'docs.wardlock.localhost', '/', 200, 'public', 'unknown')`,
		`INSERT INTO traffic_countries (site_id, hour, country, allowed, refused) VALUES ('s1', 3600000, 'NZ', 1, 0), ('s1', 3600000, 'unknown', 0, 2)`,
	)
	for _, bad := range []string{
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, country) VALUES ('b1', 0, 's1', 'h', '/', 200, 'public', 'nz')`,
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, country) VALUES ('b2', 0, 's1', 'h', '/', 200, 'public', 'NZL')`,
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, country) VALUES ('b3', 0, 's1', 'h', '/', 200, 'public', 'N1')`,
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, country) VALUES ('b4', 0, 's1', 'h', '/', 200, 'public', '')`,
		`INSERT INTO traffic_countries (site_id, hour, country, allowed, refused) VALUES ('s1', 3600000, 'NZ', 1, 0)`,
		`INSERT INTO traffic_countries (site_id, hour, country, allowed, refused) VALUES ('s1', 0, 'Wellington', 1, 0)`,
		`INSERT INTO traffic_countries (site_id, hour, country, allowed, refused) VALUES ('s1', 0, NULL, 1, 0)`,
		`INSERT INTO traffic_countries (site_id, hour, country, allowed, refused) VALUES ('s1', 0, 'AU', -1, 0)`,
		`INSERT INTO traffic_countries (site_id, hour, country, allowed, refused) VALUES ('nope', 0, 'AU', 1, 0)`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}

	exec(`DELETE FROM sites WHERE id = 's1'`)
	count(`SELECT COUNT(*) FROM traffic_countries`, 0)
	count(`SELECT COUNT(*) FROM access_log`, 3)
}
