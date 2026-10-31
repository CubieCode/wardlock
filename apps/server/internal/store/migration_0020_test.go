// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestMigration0020 checks what 0020 does for teams (T7.1) with raw SQL:
// every site keeps its owner, pins move without changing, whatever was made
// before is credited to the site's owner, and the new tables reject what they
// should.
func TestMigration0020(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateToForTest(ctx, 19); err != nil {
		t.Fatal(err)
	}
	exec := func(queries ...string) {
		t.Helper()
		for _, q := range queries {
			if err := db.ExecForTest(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	refused := func(queries ...string) {
		t.Helper()
		for _, q := range queries {
			if err := db.ExecForTest(ctx, q); err == nil {
				t.Errorf("accepted: %s", q)
			}
		}
	}
	count := func(q string, want int) {
		t.Helper()
		if n, err := db.CountForTest(ctx, q); err != nil || n != want {
			t.Fatalf("%s = %d, %v; want %d", q, n, err, want)
		}
	}
	exec(
		`INSERT INTO owners (id, email, created_at, max_sites, stored_bytes) VALUES ('o1', 'one@example.test', 1, 5, 40)`,
		`INSERT INTO owners (id, email, created_at) VALUES ('o2', 'two@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at, pinned_at) VALUES ('s1', 'o1', 'docs', 1, 7)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s2', 'o2', 'blog', 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, spa, immutable_assets, created_at) VALUES ('d1', 's1', 0, 0, 0, 0, 1)`,
		`INSERT INTO policies (id, site_id, path_prefix, visibility, links_enabled, password_enabled, email_enabled, oauth_enabled, version, created_at, updated_at)
		 VALUES ('p1', 's2', '/', 'public', 0, 0, 0, 0, 1, 1, 1)`,
		`INSERT INTO share_links (id, site_id, public_id, token_hash, path_prefix, created_at, use_count) VALUES ('l1', 's1', 'aaaaaaaaaa', 'h', '/', 1, 0)`,
		`INSERT INTO domains (id, site_id, hostname, status, verification_token, created_at) VALUES ('c1', 's2', 'a.example.test', 'pending', 'token-token-token-1', 1)`,
		`INSERT INTO api_tokens (id, owner_id, public_id, token_hash, name, scope_deploy, scope_manage, created_at) VALUES ('t1', 'o1', 'bbbbbbbbbb', 'h', 'n', 1, 0, 1)`,
	)
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}

	// Existing rows keep everything, and every owner stays a person.
	count(`SELECT COUNT(*) FROM owners WHERE kind = 'owner'`, 2)
	count(`SELECT COUNT(*) FROM owners WHERE id = 'o1' AND max_sites = 5 AND stored_bytes = 40 AND max_teams IS NULL`, 1)
	count(`SELECT COUNT(*) FROM sites WHERE id = 's1' AND owner_id = 'o1'`, 1)
	count(`SELECT COUNT(*) FROM sites WHERE id = 's2' AND owner_id = 'o2'`, 1)
	count(`SELECT COUNT(*) FROM teams`, 0)
	count(`SELECT COUNT(*) FROM team_members`, 0)

	// The pin moved to whoever owned the site, at the same time.
	count(`SELECT COUNT(*) FROM site_pins`, 1)
	count(`SELECT COUNT(*) FROM site_pins WHERE owner_id = 'o1' AND site_id = 's1' AND pinned_at = 7`, 1)

	// Everything made so far is credited to the site's only owner.
	count(`SELECT COUNT(*) FROM deploys WHERE id = 'd1' AND created_by = 'o1' AND created_by_token IS NULL`, 1)
	count(`SELECT COUNT(*) FROM policies WHERE id = 'p1' AND created_by = 'o2'`, 1)
	count(`SELECT COUNT(*) FROM share_links WHERE id = 'l1' AND created_by = 'o1'`, 1)
	count(`SELECT COUNT(*) FROM domains WHERE id = 'c1' AND created_by = 'o2'`, 1)
	count(`SELECT COUNT(*) FROM api_tokens WHERE id = 't1' AND created_by IS NULL`, 1)

	exec(
		`INSERT INTO owners (id, email, kind, created_at) VALUES ('t', 't@team.wardlock.invalid', 'team', 1)`,
		`INSERT INTO teams (id, name, created_by, created_at) VALUES ('t', 'Web', 'o1', 1)`,
		`INSERT INTO team_members (team_id, owner_id, role, created_at) VALUES ('t', 'o1', 'admin', 1)`,
		`INSERT INTO team_invitations (id, team_id, email, role, created_at, expires_at) VALUES ('i1', 't', 'x@example.test', 'viewer', 1, 2)`,
		`INSERT INTO site_pins (owner_id, site_id, pinned_at) VALUES ('o2', 's1', 9)`,
	)
	refused(
		`INSERT INTO owners (id, email, kind, created_at) VALUES ('x', 'x@example.test', 'group', 1)`,
		`INSERT INTO teams (id, name, created_at) VALUES ('nobody', 'Web', 1)`,
		`INSERT INTO teams (id, name, created_at) VALUES ('o2', '', 1)`,
		`INSERT INTO team_members (team_id, owner_id, role, created_at) VALUES ('t', 'o2', 'owner', 1)`,
		`INSERT INTO team_members (team_id, owner_id, role, created_at) VALUES ('t', 'nobody', 'viewer', 1)`,
		`INSERT INTO team_members (team_id, owner_id, role, created_at) VALUES ('t', 'o1', 'viewer', 1)`,
		`INSERT INTO team_invitations (id, team_id, email, role, created_at, expires_at) VALUES ('i2', 't', 'X@example.test', 'viewer', 1, 2)`,
		`INSERT INTO team_invitations (id, team_id, email, role, created_at, expires_at) VALUES ('i2', 't', 'x@example.test', 'viewer', 1, 2)`,
		`INSERT INTO team_invitations (id, team_id, email, role, created_at, expires_at) VALUES ('i2', 't', 'y@example.test', 'viewer', 2, 1)`,
		`INSERT INTO site_pins (owner_id, site_id, pinned_at) VALUES ('o1', 's1', 8)`,
		`INSERT INTO owners (id, email, created_at, max_teams) VALUES ('x', 'x@example.test', 1, -1)`,
	)

	// A team's site ties the team down; deleting the account takes the rest.
	exec(`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s3', 't', 'team-site', 1)`)
	refused(`DELETE FROM owners WHERE id = 't'`)
	exec(`DELETE FROM sites WHERE id = 's3'`, `DELETE FROM owners WHERE id = 't'`)
	count(`SELECT COUNT(*) FROM teams`, 0)
	count(`SELECT COUNT(*) FROM team_members`, 0)
	count(`SELECT COUNT(*) FROM team_invitations`, 0)
	// A team's creator can go without taking the team's history with them.
	count(`SELECT COUNT(*) FROM owners WHERE id = 'o1'`, 1)
}
