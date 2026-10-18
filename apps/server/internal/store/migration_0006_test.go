// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"

	"wardlock.nz/server/internal/store/storetest"
)

// TestMigration0006 checks policy_allowlist's and visitor_email_codes'
// constraints, and that both go with their policy and site, with raw SQL.
func TestMigration0006(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	policy := func(id, site string) string {
		return `INSERT INTO policies (id, site_id, path_prefix, visibility, links_enabled, password_enabled, email_enabled, oauth_enabled, version, created_at, updated_at) VALUES ('` +
			id + `', '` + site + `', '/` + id + `/', 'protected', 0, 0, 1, 0, 1, 1, 1)`
	}
	code := func(id, site, policy, address string) string {
		return `INSERT INTO visitor_email_codes (id, site_id, policy_id, policy_version, address_hash, code_hash, attempts, created_at, expires_at) VALUES ('` +
			id + `', '` + site + `', '` + policy + `', 1, '` + address + `', 'c', 0, 1, 2)`
	}
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1)`,
		`INSERT INTO sites (id, owner_id, name, created_at) VALUES ('s1', 'o1', 'notes', 1), ('s2', 'o1', 'other', 1)`,
		policy("p1", "s1"), policy("p2", "s1"), policy("p3", "s2"),
		`INSERT INTO policy_allowlist (policy_id, site_id, kind, value) VALUES ('p1', 's1', 'email', 'ana@example.test'), ('p1', 's1', 'domain', 'example.test'), ('p1', 's1', 'domain', '*.example.test'), ('p1', 's1', 'github_org', 'example-org'), ('p2', 's1', 'email', 'ana@example.test'), ('p3', 's2', 'domain', 'example.test')`,
		code("c1", "s1", "p1", "a1"), code("c2", "s1", "p1", "a2"), code("c3", "s1", "p2", "a1"), code("c4", "s2", "p3", "a1"),
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, bad := range []string{
		`INSERT INTO policy_allowlist (policy_id, site_id, kind, value) VALUES ('p1', 's1', 'email', 'ana@example.test')`,
		`INSERT INTO policy_allowlist (policy_id, site_id, kind, value) VALUES ('p1', 's1', 'email', 'Ana@example.test')`,
		`INSERT INTO policy_allowlist (policy_id, site_id, kind, value) VALUES ('p1', 's1', 'user', 'ana')`,
		`INSERT INTO policy_allowlist (policy_id, site_id, kind, value) VALUES ('p1', 's1', 'domain', '')`,
		`INSERT INTO policy_allowlist (policy_id, site_id, kind, value) VALUES ('p1', 's2', 'domain', 'example.org')`,
		`INSERT INTO policy_allowlist (policy_id, site_id, kind, value) VALUES ('p9', 's1', 'domain', 'example.org')`,
		`INSERT INTO policy_allowlist (policy_id, site_id, kind) VALUES ('p1', 's1', 'domain')`,
		code("c1", "s1", "p2", "a9"),
		code("c9", "s1", "p1", "a1"),
		code("c9", "s2", "p1", "a9"),
		code("c9", "s1", "p9", "a9"),
		`INSERT INTO visitor_email_codes (id, site_id, policy_id, policy_version, address_hash, code_hash, attempts, created_at, expires_at) VALUES ('c9', 's1', 'p1', 0, 'a9', 'c', 0, 1, 2)`,
		`INSERT INTO visitor_email_codes (id, site_id, policy_id, policy_version, address_hash, code_hash, attempts, created_at, expires_at) VALUES ('c9', 's1', 'p1', 1, 'a9', 'c', -1, 1, 2)`,
		`INSERT INTO visitor_email_codes (id, site_id, policy_id, policy_version, address_hash, code_hash, attempts, created_at, expires_at) VALUES ('c9', 's1', 'p1', 1, 'a9', 'c', 0, 2, 2)`,
		`INSERT INTO visitor_email_codes (id, site_id, policy_id, policy_version, address_hash, attempts, created_at, expires_at) VALUES ('c9', 's1', 'p1', 1, 'a9', 0, 1, 2)`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
	for _, step := range []struct {
		q                  string
		entries, codesLeft int
	}{
		{`DELETE FROM policies WHERE id = 'p1'`, 2, 2},
		{`DELETE FROM sites WHERE id = 's2'`, 1, 1},
	} {
		if err := db.ExecForTest(ctx, step.q); err != nil {
			t.Fatal(err)
		}
		entries, err := db.CountForTest(ctx, `SELECT count(*) FROM policy_allowlist`)
		if err != nil || entries != step.entries {
			t.Fatalf("after %s: %d allowlist entries, %v; want %d", step.q, entries, err, step.entries)
		}
		codes, err := db.CountForTest(ctx, `SELECT count(*) FROM visitor_email_codes`)
		if err != nil || codes != step.codesLeft {
			t.Fatalf("after %s: %d codes, %v; want %d", step.q, codes, err, step.codesLeft)
		}
	}
}
