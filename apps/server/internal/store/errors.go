// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Repository errors. Every backend returns these (wrapped) so domain code can
// check them with errors.Is; only internal/api and internal/edge map them to
// HTTP responses.
var (
	// ErrNotFound means the row does not exist or belongs to another owner;
	// callers must not be able to tell the two apart.
	ErrNotFound = errors.New("not found")
	// ErrConflict means a unique value (site name, hostname, token public ID,
	// …) is already taken.
	ErrConflict = errors.New("conflict")
	// ErrConstraint means a value failed a schema rule: a CHECK, NOT NULL or
	// a reference to a row that does not exist (or is still referenced).
	// Domain packages validate first, so this usually signals a bug.
	ErrConstraint = errors.New("constraint violated")
)

// mapErr translates driver errors into the repository errors above, keeping
// the original for logs. what names the operation for the message.
func mapErr(what string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%s: %w: %w", what, ErrConflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY, sqlite3.SQLITE_CONSTRAINT_NOTNULL:
			return fmt.Errorf("%s: %w: %w", what, ErrConstraint, err)
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

// rowsAffected turns "no row matched" from an owner-scoped write into
// ErrNotFound.
func rowsAffected(what string, n int64, err error) error {
	if err != nil {
		return mapErr(what, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return nil
}

// Column conversions. Timestamps are UTC Unix milliseconds; the zero
// time.Time, empty string and zero count stand for NULL in nullable columns.

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMs(v int64) time.Time { return time.UnixMilli(v).UTC() }

func nullMs(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func fromNullMs(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return fromMs(v.Int64)
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
