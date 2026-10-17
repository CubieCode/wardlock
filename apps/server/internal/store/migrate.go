// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
	"wardlock.nz/server/migrations"
)

// ErrDatabaseNewer means the database has migrations this binary does not
// know, usually after downgrading the server. Restore a backup or upgrade.
var ErrDatabaseNewer = errors.New("database is newer than this server")

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

type migration struct {
	version int64
	name    string
	sql     string
}

// Migrate applies pending migrations in order, each in its own transaction,
// and returns the versions it applied.
func (db *DB) Migrate(ctx context.Context, now func() time.Time) ([]int64, error) {
	all, err := loadMigrations(migrations.FS)
	if err != nil {
		return nil, err
	}
	// 0001 creates the bookkeeping table idempotently, so it can run before
	// the applied list is read. It is still recorded like any other.
	if _, err := db.sql.ExecContext(ctx, all[0].sql); err != nil {
		return nil, fmt.Errorf("bootstrap migrations: %w", err)
	}
	applied, err := db.q.ListAppliedMigrations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	if n := len(applied); n > 0 && applied[n-1] > all[len(all)-1].version {
		return nil, fmt.Errorf("%w: at version %d, server knows up to %d", ErrDatabaseNewer, applied[n-1], all[len(all)-1].version)
	}

	var ran []int64
	for _, m := range all {
		if slices.Contains(applied, m.version) {
			continue
		}
		err := db.withTx(ctx, func(tx *sql.Tx, q *sqlcgen.Queries) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return err
			}
			return q.RecordMigration(ctx, sqlcgen.RecordMigrationParams{
				Version:   m.version,
				Name:      m.name,
				AppliedAt: now().UTC().UnixMilli(),
			})
		})
		if err != nil {
			return ran, fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
		ran = append(ran, m.version)
	}
	return ran, nil
}

// loadMigrations reads and orders migration files, rejecting misnamed,
// duplicate or gapped versions.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var out []migration
	for _, file := range entries {
		match := migrationName.FindStringSubmatch(file)
		if match == nil {
			return nil, fmt.Errorf("migration %q: want NNNN_snake_case.sql", file)
		}
		version, _ := strconv.ParseInt(match[1], 10, 64)
		body, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", file, err)
		}
		out = append(out, migration{version: version, name: match[2], sql: string(body)})
	}
	slices.SortFunc(out, func(a, b migration) int { return int(a.version - b.version) })
	for i, m := range out {
		if m.version != int64(i+1) {
			return nil, fmt.Errorf("migration versions must run 0001, 0002, … without gaps; found %04d at position %d", m.version, i+1)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no migrations embedded")
	}
	return out, nil
}
