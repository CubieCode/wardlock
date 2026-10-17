// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// AccessVia is how a request was let in (or "none" when it was not).
type AccessVia string

const (
	ViaPublic   AccessVia = "public"
	ViaLink     AccessVia = "link"
	ViaPassword AccessVia = "password"
	ViaEmail    AccessVia = "email"
	ViaOAuth    AccessVia = "oauth"
	ViaNone     AccessVia = "none"
)

// AccessEntry is one access-log row. It never holds a raw token; links are
// named by public ID. SiteID is not a foreign key, so entries outlive sites.
type AccessEntry struct {
	ID           string
	OccurredAt   time.Time
	SiteID       string
	Host         string
	Path         string
	Status       int
	Via          AccessVia
	LinkPublicID string // set exactly when Via is ViaLink
	IP           string // already truncated per configuration
	UserAgent    string
}

// AccessLog stores access-log entries, written in batches off the request
// path.
type AccessLog interface {
	// Append writes entries in one transaction: all or none.
	Append(ctx context.Context, entries []AccessEntry) error
}

// AccessLog returns the SQLite AccessLog repository.
func (db *DB) AccessLog() AccessLog { return accessLogRepo{db} }

type accessLogRepo struct{ db *DB }

func (r accessLogRepo) Append(ctx context.Context, entries []AccessEntry) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		for _, e := range entries {
			err := q.InsertAccessLogEntry(ctx, sqlcgen.InsertAccessLogEntryParams{
				ID:           e.ID,
				OccurredAt:   ms(e.OccurredAt),
				SiteID:       e.SiteID,
				Host:         e.Host,
				Path:         e.Path,
				Status:       int64(e.Status),
				Via:          string(e.Via),
				LinkPublicID: nullString(e.LinkPublicID),
				Ip:           nullString(e.IP),
				UserAgent:    nullString(e.UserAgent),
			})
			if err != nil {
				return mapErr("append access log", err)
			}
		}
		return nil
	})
}
