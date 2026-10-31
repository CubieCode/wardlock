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

// TestMigration0012 checks the groups, tags and pins 0012 adds with raw
// SQL: existing sites start ungrouped, untagged and unpinned; group name
// keys and tag names are unique per owner only; colours are slots 0-8; deleting a group ungroups
// its sites, and deleting a tag or a site deletes its site_tags rows.
func TestMigration0012(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 11); err != nil {
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
		`INSERT INTO owners (id, email, created_at) VALUES ('o2', 'two@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
	)
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	count(`SELECT COUNT(*) FROM sites WHERE id = 's1' AND group_id IS NULL`, 1)

	exec(
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s2', 'o1', 'blog', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, note, created_at) VALUES ('g1', 'o1', 'Client work', 'client work', 'Previews', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, created_at) VALUES ('g2', 'o2', 'Client work', 'client work', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, created_at) VALUES ('g3', 'o1', 'Kōwhai Studio and friends, forty in all!', 'kōwhai studio and friends, forty in all!', 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('t1', 'o1', 'preview', 1, 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('t2', 'o1', 'internal', 0, 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('t3', 'o2', 'preview', 8, 1)`,
		`UPDATE sites SET group_id = 'g1' WHERE id IN ('s1', 's2')`,
		`INSERT INTO site_pins (owner_id, site_id, pinned_at) VALUES ('o1', 's1', 5), ('o1', 's2', 5)`,
		`INSERT INTO site_tags (site_id, tag_id) VALUES ('s1', 't1'), ('s1', 't2'), ('s2', 't1')`,
	)
	for _, bad := range []string{
		`INSERT INTO site_groups (id, owner_id, name, name_key, created_at) VALUES ('b1', 'o1', 'CLIENT WORK', 'client work', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, created_at) VALUES ('b1n', 'o1', 'No key', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, created_at) VALUES ('b1e', 'o1', 'Empty key', '', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, created_at) VALUES ('b2', 'o1', '', 'x', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, created_at) VALUES ('b3', 'o1', '` + strings.Repeat("a", 41) + `', 'long', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, note, created_at) VALUES ('b4', 'o1', 'Notes', 'notes', '', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, note, created_at) VALUES ('b5', 'o1', 'Notes', 'notes', '` + strings.Repeat("a", 201) + `', 1)`,
		`INSERT INTO site_groups (id, owner_id, name, name_key, created_at) VALUES ('b6', 'nobody', 'Orphan', 'orphan', 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('b7', 'o1', 'preview', 2, 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('b8', 'o1', 'Upper', 2, 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('b9', 'o1', '` + strings.Repeat("a", 31) + `', 2, 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('b10', 'o1', 'nine', 9, 1)`,
		`INSERT INTO tags (id, owner_id, name, colour, created_at) VALUES ('b11', 'o1', 'minus', -1, 1)`,
		`INSERT INTO site_tags (site_id, tag_id) VALUES ('s1', 't1')`,
		`INSERT INTO site_tags (site_id, tag_id) VALUES ('s1', 'nope')`,
		`INSERT INTO site_tags (site_id, tag_id) VALUES ('nope', 't1')`,
		`UPDATE sites SET group_id = 'nope' WHERE id = 's1'`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}

	exec(`DELETE FROM site_groups WHERE id = 'g1'`)
	count(`SELECT COUNT(*) FROM sites JOIN site_pins ON site_pins.site_id = sites.id WHERE sites.group_id IS NULL AND site_pins.pinned_at = 5`, 2)
	exec(`DELETE FROM tags WHERE id = 't1'`)
	count(`SELECT COUNT(*) FROM site_tags`, 1)
	exec(`DELETE FROM sites WHERE id = 's1'`)
	count(`SELECT COUNT(*) FROM site_tags`, 0)
	count(`SELECT COUNT(*) FROM tags WHERE id = 't2'`, 1)
}
