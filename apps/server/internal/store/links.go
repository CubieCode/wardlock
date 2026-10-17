// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// ShareLink is a site's share link (wl_<public id>_<secret>). Only the HMAC of
// the whole token is stored.
type ShareLink struct {
	ID             string
	SiteID         string
	PublicID       string
	TokenHash      string
	Label          string
	PathPrefix     string
	CreatedAt      time.Time
	ExpiresAt      time.Time // zero: never
	MaxUses        int       // zero: unlimited
	UseCount       int
	LastAccessedAt time.Time
	RotatedAt      time.Time
	RevokedAt      time.Time
}

// Links stores share links.
type Links interface {
	// Create adds l to one of ownerID's sites.
	Create(ctx context.Context, ownerID string, l ShareLink) error
}

// Links returns the SQLite Links repository.
func (db *DB) Links() Links { return linksRepo{db} }

type linksRepo struct{ db *DB }

func (r linksRepo) Create(ctx context.Context, ownerID string, l ShareLink) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, l.SiteID); err != nil {
			return err
		}
		return mapErr("create share link", q.CreateShareLink(ctx, sqlcgen.CreateShareLinkParams{
			ID:             l.ID,
			SiteID:         l.SiteID,
			PublicID:       l.PublicID,
			TokenHash:      l.TokenHash,
			Label:          nullString(l.Label),
			PathPrefix:     l.PathPrefix,
			CreatedAt:      ms(l.CreatedAt),
			ExpiresAt:      nullMs(l.ExpiresAt),
			MaxUses:        nullInt(int64(l.MaxUses)),
			UseCount:       int64(l.UseCount),
			LastAccessedAt: nullMs(l.LastAccessedAt),
			RotatedAt:      nullMs(l.RotatedAt),
			RevokedAt:      nullMs(l.RevokedAt),
		}))
	})
}
