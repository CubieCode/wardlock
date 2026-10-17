// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"strings"
	"testing"

	"wardlock.nz/server/internal/store/storetest"
)

// TestMigration0003 checks the columns and table 0003 adds and drops with raw
// SQL, including the optional columns no repository method writes yet.
func TestMigration0003(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	hex := strings.Repeat("ab", 32)
	setup := []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1), ('o2', 'two@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'docs', 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at) VALUES ('d1', 's1', 0, 0, 1)`,
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, spa) VALUES ('d2', 's1', 0, 0, 1, 1)`,
		`INSERT INTO owner_sessions (id, owner_id, token_hash, created_at, last_seen_at, expires_at, user_agent) VALUES ('os1', 'o1', 'h1', 1, 1, 2, 'Mozilla/5.0 (example)')`,
		`INSERT INTO domains (id, site_id, hostname, status, verification_token, created_at, verified_at, failure_detail) VALUES ('dm1', 's1', 'docs.example.test', 'active', 'verify-token-000001', 1, 2, 'SERVFAIL')`,
		`INSERT INTO cli_login_requests (id, device_code_hash, user_code, status, scope_deploy, scope_manage, created_at, expires_at, client_name) VALUES ('c1', 'dh1', 'BDFH-KMPR', 'pending', 1, 0, 1, 2, 'wardlock on laptop')`,
		`INSERT INTO owner_blobs (owner_id, sha256, created_at) VALUES ('o1', '` + hex + `', 1), ('o2', '` + hex + `', 1)`,
	}
	for _, q := range setup {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for query, want := range map[string]int{
		`SELECT spa FROM deploys WHERE id = 'd1'`:                                            0,
		`SELECT spa FROM deploys WHERE id = 'd2'`:                                            1,
		`SELECT count(*) FROM owner_sessions WHERE user_agent = 'Mozilla/5.0 (example)'`:     1,
		`SELECT count(*) FROM domains WHERE verified_at = 2 AND failure_detail = 'SERVFAIL'`: 1,
		`SELECT count(*) FROM cli_login_requests WHERE client_name = 'wardlock on laptop'`:   1,
	} {
		got, err := db.CountForTest(ctx, query)
		if err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", query, got, err, want)
		}
	}

	for _, bad := range []string{
		`INSERT INTO deploys (id, site_id, file_count, total_size, created_at, spa) VALUES ('d3', 's1', 0, 0, 1, 2)`,
		`INSERT INTO sites (id, owner_id, name, spa_fallback, created_at) VALUES ('s2', 'o1', 'blog', 0, 1)`,
		`INSERT INTO owner_blobs (owner_id, sha256, created_at) VALUES ('o1', '` + hex + `', 2)`,
		`INSERT INTO owner_blobs (owner_id, sha256, created_at) VALUES ('o1', '` + strings.Repeat("g", 64) + `', 1)`,
		`INSERT INTO owner_blobs (owner_id, sha256, created_at) VALUES ('o1', '` + strings.Repeat("AB", 32) + `', 1)`,
		`INSERT INTO owner_blobs (owner_id, sha256, created_at) VALUES ('o1', '` + hex[:63] + `', 1)`,
		`INSERT INTO owner_blobs (owner_id, sha256, created_at) VALUES ('nobody', '` + strings.Repeat("cd", 32) + `', 1)`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}

	// Deleting an owner deletes their blob claims and nobody else's.
	if err := db.ExecForTest(ctx, `DELETE FROM owners WHERE id = 'o2'`); err != nil {
		t.Fatal(err)
	}
	got, err := db.CountForTest(ctx, `SELECT count(*) FROM owner_blobs`)
	if err != nil || got != 1 {
		t.Fatalf("owner_blobs rows after deleting o2 = %d, %v; want 1", got, err)
	}
}
