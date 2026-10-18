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

// ShareLinkQuery selects one page of a site's unrevoked links, newest first.
type ShareLinkQuery struct {
	OwnerID string
	SiteID  string
	// BeforeCreated and BeforeID are the last row of the previous page;
	// zero values start at the first page.
	BeforeCreated time.Time
	BeforeID      string
	Limit         int
}

// Links stores share links.
type Links interface {
	// Create adds l to one of ownerID's sites.
	Create(ctx context.Context, ownerID string, l ShareLink) error
	// GetByPublicID finds a link by its public ID, whatever its state;
	// callers compare the hash and check scope, expiry, uses and
	// revocation. Instance-wide: the edge calls it for every site.
	GetByPublicID(ctx context.Context, publicID string) (ShareLink, error)
	// Get finds siteID's link id, whatever its state. Instance-wide: the
	// edge checks the link behind every link session.
	Get(ctx context.Context, siteID, id string) (ShareLink, error)
	// List returns one page of a site's unrevoked links. Another owner's
	// site lists nothing.
	List(ctx context.Context, q ShareLinkQuery) ([]ShareLink, error)
	// Revoke revokes the link publicID on ownerID's site siteID and every
	// session it created. A missing, revoked or another owner's link is
	// ErrNotFound.
	Revoke(ctx context.Context, ownerID, siteID, publicID string, at time.Time) error
	// Rotate replaces the token hash of the unrevoked link publicID on
	// ownerID's site siteID, records at as its rotation time and revokes
	// every session it created, in one transaction. A missing, revoked or
	// another owner's link is ErrNotFound.
	Rotate(ctx context.Context, ownerID, siteID, publicID, tokenHash string, at time.Time) (ShareLink, error)
	// Use spends one use of link id and records the access, provided its
	// hash is still tokenHash and it is unrevoked, unexpired at at and has
	// a use left. Otherwise it is ErrNotFound and nothing changes.
	Use(ctx context.Context, id, tokenHash string, at time.Time) error
	// Touch moves the link's last access forward to at; an earlier at
	// changes nothing.
	Touch(ctx context.Context, id string, at time.Time) error
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

func shareLinkFromRow(row sqlcgen.ShareLink) ShareLink {
	return ShareLink{
		ID:             row.ID,
		SiteID:         row.SiteID,
		PublicID:       row.PublicID,
		TokenHash:      row.TokenHash,
		Label:          row.Label.String,
		PathPrefix:     row.PathPrefix,
		CreatedAt:      fromMs(row.CreatedAt),
		ExpiresAt:      fromNullMs(row.ExpiresAt),
		MaxUses:        int(row.MaxUses.Int64),
		UseCount:       int(row.UseCount),
		LastAccessedAt: fromNullMs(row.LastAccessedAt),
		RotatedAt:      fromNullMs(row.RotatedAt),
		RevokedAt:      fromNullMs(row.RevokedAt),
	}
}

func (r linksRepo) GetByPublicID(ctx context.Context, publicID string) (ShareLink, error) {
	row, err := r.db.q.GetShareLinkByPublicID(ctx, publicID)
	if err != nil {
		return ShareLink{}, mapErr("get share link", err)
	}
	return shareLinkFromRow(row), nil
}

func (r linksRepo) Get(ctx context.Context, siteID, id string) (ShareLink, error) {
	row, err := r.db.q.GetShareLink(ctx, sqlcgen.GetShareLinkParams{ID: id, SiteID: siteID})
	if err != nil {
		return ShareLink{}, mapErr("get share link", err)
	}
	return shareLinkFromRow(row), nil
}

func (r linksRepo) List(ctx context.Context, q ShareLinkQuery) ([]ShareLink, error) {
	before := int64(math.MaxInt64)
	if q.BeforeID != "" {
		before = ms(q.BeforeCreated)
	}
	rows, err := r.db.q.ListShareLinks(ctx, sqlcgen.ListShareLinksParams{
		SiteID:        q.SiteID,
		OwnerID:       q.OwnerID,
		BeforeCreated: before,
		BeforeID:      q.BeforeID,
		RowLimit:      int64(q.Limit),
	})
	if err != nil {
		return nil, mapErr("list share links", err)
	}
	out := make([]ShareLink, len(rows))
	for i, row := range rows {
		out[i] = shareLinkFromRow(row)
	}
	return out, nil
}

func (r linksRepo) Revoke(ctx context.Context, ownerID, siteID, publicID string, at time.Time) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		id, err := q.RevokeShareLink(ctx, sqlcgen.RevokeShareLinkParams{
			RevokedAt: nullMs(at), SiteID: siteID, PublicID: publicID, OwnerID: ownerID,
		})
		if err != nil {
			return mapErr("revoke share link", err)
		}
		return mapErr("revoke share link sessions", q.RevokeShareLinkSessions(ctx, sqlcgen.RevokeShareLinkSessionsParams{
			RevokedAt: nullMs(at), SiteID: siteID, ShareLinkID: sql.NullString{String: id, Valid: true},
		}))
	})
}

func (r linksRepo) Rotate(ctx context.Context, ownerID, siteID, publicID, tokenHash string, at time.Time) (ShareLink, error) {
	var l ShareLink
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		row, err := q.RotateShareLink(ctx, sqlcgen.RotateShareLinkParams{
			TokenHash: tokenHash, RotatedAt: nullMs(at), SiteID: siteID, PublicID: publicID, OwnerID: ownerID,
		})
		if err != nil {
			return mapErr("rotate share link", err)
		}
		l = shareLinkFromRow(row)
		return mapErr("revoke share link sessions", q.RevokeShareLinkSessions(ctx, sqlcgen.RevokeShareLinkSessionsParams{
			RevokedAt: nullMs(at), SiteID: siteID, ShareLinkID: sql.NullString{String: l.ID, Valid: true},
		}))
	})
	if err != nil {
		return ShareLink{}, err
	}
	return l, nil
}

func (r linksRepo) Use(ctx context.Context, id, tokenHash string, at time.Time) error {
	n, err := r.db.q.UseShareLink(ctx, sqlcgen.UseShareLinkParams{At: nullMs(at), ID: id, TokenHash: tokenHash})
	return rowsAffected("use share link", n, err)
}

func (r linksRepo) Touch(ctx context.Context, id string, at time.Time) error {
	return mapErr("touch share link", r.db.q.TouchShareLink(ctx, sqlcgen.TouchShareLinkParams{ID: id, LastAccessedAt: nullMs(at)}))
}
