// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package store owns the SQLite connection, migrations and sqlc queries.
// Domain packages never import it directly: they depend on the per-area
// repository interfaces declared here (F0.3 onwards), so another backend can
// be added later without touching domain code. See docs/ARCHITECTURE.md.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	"wardlock.nz/server/internal/store/sqlcgen"

	_ "modernc.org/sqlite" // Registers the pure-Go "sqlite" driver.
)

// DB is an open, migrated database.
type DB struct {
	sql *sql.DB
	q   *sqlcgen.Queries
}

// Open opens (creating if needed) the SQLite database at path in WAL mode
// with foreign keys enforced. Call Migrate before use.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := (&url.URL{Scheme: "file", Opaque: path, RawQuery: url.Values{
		"_pragma": {"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(5000)", "synchronous(NORMAL)"},
		// Take the write lock at BEGIN so concurrent writers wait on
		// busy_timeout instead of failing mid-transaction.
		"_txlock": {"immediate"},
	}.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return &DB{sql: db, q: sqlcgen.New(db)}, nil
}

// Close closes the database.
func (db *DB) Close() error { return db.sql.Close() }

// Ping checks the database is reachable.
func (db *DB) Ping(ctx context.Context) error { return db.sql.PingContext(ctx) }

// withTx runs fn in a transaction, committing only if fn succeeds.
func (db *DB) withTx(ctx context.Context, fn func(*sql.Tx, *sqlcgen.Queries) error) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx, db.q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
