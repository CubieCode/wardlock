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

// TestMigration0013 checks the traffic rollup 0013 adds with raw SQL: it
// starts empty and not backfilled over an existing access log; rows are
// keyed by site, hour and value with non-negative counts and only the
// values the writer uses; the backfill marker holds one row; and deleting
// a site deletes its rollup rows but not its access-log entries.
func TestMigration0013(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 12); err != nil {
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
		`INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via) VALUES ('a1', 3600000, 's1', 'docs.wardlock.localhost', '/', 200, 'public')`,
	)
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"traffic_hours", "traffic_links", "traffic_paths", "traffic_browsers", "traffic_backfill"} {
		count(`SELECT COUNT(*) FROM `+table, 0)
	}

	exec(
		`INSERT INTO traffic_hours (site_id, hour, via, allowed, refused) VALUES ('s1', 3600000, 'public', 1, 0), ('s1', 3600000, 'none', 0, 2)`,
		`INSERT INTO traffic_links (site_id, hour, link_public_id, allowed, refused) VALUES ('s1', 3600000, 'q4v7m2xkpa', 1, 0)`,
		`INSERT INTO traffic_paths (site_id, hour, path, allowed, refused) VALUES ('s1', 3600000, '/', 1, 0), ('s1', 3600000, '', 0, 2)`,
		`INSERT INTO traffic_browsers (site_id, hour, browser, allowed, refused) VALUES ('s1', 3600000, 'tool', 1, 2)`,
		`INSERT INTO traffic_backfill (id, done_at) VALUES ('access_log', 5)`,
	)
	for _, bad := range []string{
		`INSERT INTO traffic_hours (site_id, hour, via, allowed, refused) VALUES ('s1', 3600000, 'public', 1, 0)`,
		`INSERT INTO traffic_hours (site_id, hour, via, allowed, refused) VALUES ('s1', 0, 'cookie', 1, 0)`,
		`INSERT INTO traffic_hours (site_id, hour, via, allowed, refused) VALUES ('s1', 0, 'link', -1, 0)`,
		`INSERT INTO traffic_hours (site_id, hour, via, allowed, refused) VALUES ('nope', 0, 'link', 1, 0)`,
		`INSERT INTO traffic_links (site_id, hour, link_public_id, allowed, refused) VALUES ('s1', 0, '', 1, 0)`,
		`INSERT INTO traffic_links (site_id, hour, link_public_id, allowed, refused) VALUES ('s1', 0, 'x', 0, -1)`,
		`INSERT INTO traffic_paths (site_id, hour, path, allowed, refused) VALUES ('s1', 0, '/` + strings.Repeat("a", 256) + `', 1, 0)`,
		`INSERT INTO traffic_paths (site_id, hour, path, allowed, refused) VALUES ('nope', 0, '/', 1, 0)`,
		`INSERT INTO traffic_browsers (site_id, hour, browser, allowed, refused) VALUES ('s1', 0, 'opera', 1, 0)`,
		`INSERT INTO traffic_browsers (site_id, hour, browser, allowed, refused) VALUES ('s1', 0, 'chrome', 1, NULL)`,
		`INSERT INTO traffic_backfill (id, done_at) VALUES ('access_log', 6)`,
		`INSERT INTO traffic_backfill (id, done_at) VALUES ('other', 6)`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}

	// A torn-down or deleted site takes its rollup rows with it; its
	// access-log entries wait for the log's retention.
	exec(`DELETE FROM sites WHERE id = 's1'`)
	for _, table := range []string{"traffic_hours", "traffic_links", "traffic_paths", "traffic_browsers"} {
		count(`SELECT COUNT(*) FROM `+table, 0)
	}
	count(`SELECT COUNT(*) FROM access_log`, 1)
	count(`SELECT COUNT(*) FROM traffic_backfill`, 1)
}
