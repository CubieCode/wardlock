// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"math"
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
	// Country is the ISO 3166-1 alpha-2 code (upper case) the writer
	// looked IP up as, CountryUnknown when the database had no match, or ""
	// when the lookup is off or no IP is stored (V5.13).
	Country string
	// Preview is the preview the request reached (C1.19), "" for the live
	// site. Preview entries are logged but never counted in the traffic
	// rollup.
	Preview string
	// Browser is the family the writer classified UserAgent as, for the
	// traffic rollup (V5.4); the log row does not store it. Append counts
	// "" as BrowserOther. Lists leave it "".
	Browser Browser
}

// AccessCursor positions an access-log listing: lists return entries
// strictly older than it. The zero value starts at the newest.
type AccessCursor struct {
	OccurredAt time.Time
	ID         string
}

// Bounds on how many rows one AccessLog call reads or deletes; larger
// requests are cut to these, smaller than 1 raised to 1.
const (
	MaxAccessListLimit  = 1000
	MaxAccessPruneLimit = 10_000
)

// AccessLog stores access-log entries, written in batches off the request
// path.
type AccessLog interface {
	// Append writes entries in one transaction, all or none, and adds them
	// to the traffic rollup in the same transaction once its backfill has
	// run (see Traffic). Entries of a site that no longer exists, and
	// preview entries, are logged but not counted.
	Append(ctx context.Context, entries []AccessEntry) error
	// ListBySite returns up to limit (1 to MaxAccessListLimit) of
	// ownerID's site's entries, newest first, before the cursor. A missing
	// or foreign site lists nothing.
	ListBySite(ctx context.Context, ownerID, siteID string, before AccessCursor, limit int) ([]AccessEntry, error)
	// ListByLink is ListBySite narrowed to one share link's public ID.
	ListByLink(ctx context.Context, ownerID, siteID, linkPublicID string, before AccessCursor, limit int) ([]AccessEntry, error)
	// Prune deletes up to limit (1 to MaxAccessPruneLimit) entries, on any
	// site, that occurred before cutoff and returns how many it deleted;
	// fewer than limit means none are left. It is the retention job, not an
	// owner operation.
	Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error)
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
				Country:      nullString(e.Country),
				Preview:      nullString(e.Preview),
			})
			if err != nil {
				return mapErr("append access log", err)
			}
		}
		return addTraffic(ctx, q, entries)
	})
}

func (r accessLogRepo) ListBySite(ctx context.Context, ownerID, siteID string, before AccessCursor, limit int) ([]AccessEntry, error) {
	beforeAt, beforeID := before.bounds()
	rows, err := r.db.q.ListAccessLogBySite(ctx, sqlcgen.ListAccessLogBySiteParams{
		SiteID:           siteID,
		OwnerID:          ownerID,
		BeforeOccurredAt: beforeAt,
		BeforeID:         beforeID,
		RowLimit:         clamp(limit, MaxAccessListLimit),
	})
	if err != nil {
		return nil, mapErr("list access log", err)
	}
	return accessEntriesFrom(rows), nil
}

func (r accessLogRepo) ListByLink(ctx context.Context, ownerID, siteID, linkPublicID string, before AccessCursor, limit int) ([]AccessEntry, error) {
	beforeAt, beforeID := before.bounds()
	rows, err := r.db.q.ListAccessLogByLink(ctx, sqlcgen.ListAccessLogByLinkParams{
		LinkPublicID:     sql.NullString{String: linkPublicID, Valid: true},
		SiteID:           siteID,
		OwnerID:          ownerID,
		BeforeOccurredAt: beforeAt,
		BeforeID:         beforeID,
		RowLimit:         clamp(limit, MaxAccessListLimit),
	})
	if err != nil {
		return nil, mapErr("list access log by link", err)
	}
	return accessEntriesFrom(rows), nil
}

func (r accessLogRepo) Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	n, err := r.db.q.PruneAccessLog(ctx, sqlcgen.PruneAccessLogParams{Before: ms(cutoff), RowLimit: clamp(limit, MaxAccessPruneLimit)})
	if err != nil {
		return 0, mapErr("prune access log", err)
	}
	return n, nil
}

// clamp bounds a row limit to 1..maxLimit.
func clamp(limit, maxLimit int) int64 {
	return int64(min(max(limit, 1), maxLimit))
}

// bounds returns the cursor as query arguments; the zero cursor is after
// every entry.
func (c AccessCursor) bounds() (int64, string) {
	if c.OccurredAt.IsZero() {
		return math.MaxInt64, ""
	}
	return ms(c.OccurredAt), c.ID
}

func accessEntriesFrom(rows []sqlcgen.AccessLog) []AccessEntry {
	entries := make([]AccessEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, AccessEntry{
			ID:           row.ID,
			OccurredAt:   fromMs(row.OccurredAt),
			SiteID:       row.SiteID,
			Host:         row.Host,
			Path:         row.Path,
			Status:       int(row.Status),
			Via:          AccessVia(row.Via),
			LinkPublicID: row.LinkPublicID.String,
			IP:           row.Ip.String,
			UserAgent:    row.UserAgent.String,
			Country:      row.Country.String,
			Preview:      row.Preview.String,
		})
	}
	return entries
}
