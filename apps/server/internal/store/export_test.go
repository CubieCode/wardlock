// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import "context"

// Test-only access to raw SQL; production code goes through sqlc queries.

func (db *DB) ExecForTest(ctx context.Context, query string) error {
	_, err := db.sql.ExecContext(ctx, query)
	return err
}

func (db *DB) PragmaForTest(ctx context.Context, name string) (string, error) {
	var v string
	err := db.sql.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v)
	return v, err
}

func (db *DB) CountForTest(ctx context.Context, query string) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, query).Scan(&n)
	return n, err
}
