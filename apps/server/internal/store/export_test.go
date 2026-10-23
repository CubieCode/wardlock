// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

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

// SetBackupFaultForTest makes Backup call fault, with the temporary copy's
// path, while the copy is open for checking, and fail if it does.
func (db *DB) SetBackupFaultForTest(fault func(tmp string) error) { db.backupFault = fault }

// MigrateToForTest applies migrations up to version upTo only, so a test
// can write rows a later migration must carry over.
func (db *DB) MigrateToForTest(ctx context.Context, upTo int64) error {
	_, err := db.migrate(ctx, time.Now, upTo)
	return err
}

// CountQueriesForTest makes every later sqlc query outside a transaction
// add one to the returned counter.
func (db *DB) CountQueriesForTest() *atomic.Int64 {
	c := &countingDB{DBTX: db.sql, n: &atomic.Int64{}}
	db.q = sqlcgen.New(c)
	return c.n
}

type countingDB struct {
	sqlcgen.DBTX
	n *atomic.Int64
}

func (c *countingDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	c.n.Add(1)
	return c.DBTX.ExecContext(ctx, q, args...)
}

func (c *countingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.n.Add(1)
	return c.DBTX.QueryContext(ctx, q, args...)
}

func (c *countingDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	c.n.Add(1)
	return c.DBTX.QueryRowContext(ctx, q, args...)
}
