// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"

	"wardlock.nz/server/internal/store/storetest"
)

// TestMigration0004 checks owner_passkeys' constraints and that deleting an
// owner deletes their passkeys, with raw SQL since no repository method
// deletes owners.
func TestMigration0004(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	for _, q := range []string{
		`INSERT INTO owners (id, email, created_at) VALUES ('o1', 'one@example.test', 1), ('o2', 'two@example.test', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at) VALUES ('p1', 'o1', 'c1', '{}', 0, 'Laptop', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at, last_used_at) VALUES ('p2', 'o2', 'c2', '{}', 0, 'Phone', 1, 2)`,
	} {
		if err := db.ExecForTest(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, bad := range []string{
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at) VALUES ('p3', 'o1', 'c1', '{}', 0, 'Again', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at) VALUES ('p3', 'nobody', 'c3', '{}', 0, 'Laptop', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at) VALUES ('p3', 'o1', '', '{}', 0, 'Laptop', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at) VALUES ('p3', 'o1', 'c3', '', 0, 'Laptop', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at) VALUES ('p3', 'o1', 'c3', '{}', 0, '', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, name, created_at) VALUES ('p3', 'o1', 'c3', 'Laptop', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at) VALUES ('p3', 'o1', 'c3', '{}', -1, 'Laptop', 1)`,
		`INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, name, created_at) VALUES ('p3', 'o1', 'c3', '{}', 'Laptop', 1)`,
	} {
		if err := db.ExecForTest(ctx, bad); err == nil {
			t.Errorf("%s succeeded, want a constraint error", bad)
		}
	}
	if err := db.ExecForTest(ctx, `DELETE FROM owners WHERE id = 'o2'`); err != nil {
		t.Fatal(err)
	}
	got, err := db.CountForTest(ctx, `SELECT count(*) FROM owner_passkeys`)
	if err != nil || got != 1 {
		t.Fatalf("owner_passkeys rows after deleting o2 = %d, %v; want 1", got, err)
	}
}
