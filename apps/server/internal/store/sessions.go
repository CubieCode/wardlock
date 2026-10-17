// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"math"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// OwnerSession is a dashboard session. Only the cookie token's hash is stored.
type OwnerSession struct {
	ID         string
	OwnerID    string
	TokenHash  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time // absolute expiry
	RevokedAt  time.Time
	UserAgent  string // as sent at sign-in; empty if none
}

// SessionMethod is the access method that created a site session.
type SessionMethod string

const (
	SessionLink     SessionMethod = "link"
	SessionPassword SessionMethod = "password"
	SessionEmail    SessionMethod = "email"
	SessionOAuth    SessionMethod = "oauth"
)

// SiteSession is a visitor's session on one site. Only the cookie token's
// hash is stored.
type SiteSession struct {
	ID            string
	SiteID        string
	TokenHash     string
	Method        SessionMethod
	ShareLinkID   string // set exactly when Method is SessionLink
	PolicyID      string
	PolicyVersion int
	Subject       string // verified email for email-code and OAuth sessions
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     time.Time
}

// OwnerSessionQuery selects one page of an owner's active dashboard
// sessions, most recently used first.
type OwnerSessionQuery struct {
	OwnerID string
	// Now excludes sessions past their absolute expiry.
	Now time.Time
	// IdleSince excludes sessions not seen after it (the idle expiry).
	IdleSince time.Time
	// AfterSeen and AfterID are the last row of the previous page; zero
	// values start at the first page.
	AfterSeen time.Time
	AfterID   string
	Limit     int
}

// Sessions stores dashboard (owner) and visitor (site) sessions.
type Sessions interface {
	CreateOwner(ctx context.Context, s OwnerSession) error
	CreateSite(ctx context.Context, s SiteSession) error
	// GetOwnerByTokenHash finds a dashboard session by its cookie token
	// hash, whatever its state; callers check expiry and revocation.
	GetOwnerByTokenHash(ctx context.Context, hash string) (OwnerSession, error)
	// TouchOwner moves the session's last use forward to at; an earlier at
	// changes nothing.
	TouchOwner(ctx context.Context, id string, at time.Time) error
	// RevokeOwner revokes ownerID's session id. Another owner's or a
	// missing session is ErrNotFound; revoking twice keeps the first time.
	RevokeOwner(ctx context.Context, ownerID, id string, at time.Time) error
	// ListOwner returns one page of active dashboard sessions.
	ListOwner(ctx context.Context, q OwnerSessionQuery) ([]OwnerSession, error)
	// GetSiteByTokenHash finds a visitor session by its cookie token hash,
	// whatever its state; callers check the site, expiry, revocation and
	// policy version. Instance-wide: the edge calls it for every site.
	GetSiteByTokenHash(ctx context.Context, hash string) (SiteSession, error)
	// RevokeSite revokes session id on siteID. A missing session or one on
	// another site is ErrNotFound; revoking twice keeps the first time.
	RevokeSite(ctx context.Context, siteID, id string, at time.Time) error
}

// Sessions returns the SQLite Sessions repository.
func (db *DB) Sessions() Sessions { return sessionsRepo{db} }

type sessionsRepo struct{ db *DB }

func (r sessionsRepo) CreateOwner(ctx context.Context, s OwnerSession) error {
	return mapErr("create owner session", r.db.q.CreateOwnerSession(ctx, sqlcgen.CreateOwnerSessionParams{
		ID:         s.ID,
		OwnerID:    s.OwnerID,
		TokenHash:  s.TokenHash,
		CreatedAt:  ms(s.CreatedAt),
		LastSeenAt: ms(s.LastSeenAt),
		ExpiresAt:  ms(s.ExpiresAt),
		RevokedAt:  nullMs(s.RevokedAt),
		UserAgent:  nullString(s.UserAgent),
	}))
}

func ownerSessionFromRow(row sqlcgen.OwnerSession) OwnerSession {
	return OwnerSession{
		ID:         row.ID,
		OwnerID:    row.OwnerID,
		TokenHash:  row.TokenHash,
		CreatedAt:  fromMs(row.CreatedAt),
		LastSeenAt: fromMs(row.LastSeenAt),
		ExpiresAt:  fromMs(row.ExpiresAt),
		RevokedAt:  fromNullMs(row.RevokedAt),
		UserAgent:  row.UserAgent.String,
	}
}

func (r sessionsRepo) GetOwnerByTokenHash(ctx context.Context, hash string) (OwnerSession, error) {
	row, err := r.db.q.GetOwnerSessionByTokenHash(ctx, hash)
	if err != nil {
		return OwnerSession{}, mapErr("get owner session", err)
	}
	return ownerSessionFromRow(row), nil
}

func (r sessionsRepo) TouchOwner(ctx context.Context, id string, at time.Time) error {
	return mapErr("touch owner session", r.db.q.TouchOwnerSession(ctx, sqlcgen.TouchOwnerSessionParams{ID: id, LastSeenAt: ms(at)}))
}

func (r sessionsRepo) RevokeOwner(ctx context.Context, ownerID, id string, at time.Time) error {
	n, err := r.db.q.RevokeOwnerSession(ctx, sqlcgen.RevokeOwnerSessionParams{ID: id, OwnerID: ownerID, RevokedAt: nullMs(at)})
	return rowsAffected("revoke owner session", n, err)
}

func (r sessionsRepo) ListOwner(ctx context.Context, q OwnerSessionQuery) ([]OwnerSession, error) {
	afterSeen := int64(math.MaxInt64)
	if q.AfterID != "" {
		afterSeen = ms(q.AfterSeen)
	}
	rows, err := r.db.q.ListOwnerSessions(ctx, sqlcgen.ListOwnerSessionsParams{
		OwnerID:   q.OwnerID,
		Now:       ms(q.Now),
		IdleSince: ms(q.IdleSince),
		AfterSeen: afterSeen,
		AfterID:   q.AfterID,
		RowLimit:  int64(q.Limit),
	})
	if err != nil {
		return nil, mapErr("list owner sessions", err)
	}
	out := make([]OwnerSession, len(rows))
	for i, row := range rows {
		out[i] = ownerSessionFromRow(row)
	}
	return out, nil
}

func (r sessionsRepo) CreateSite(ctx context.Context, s SiteSession) error {
	return mapErr("create site session", r.db.q.CreateSiteSession(ctx, sqlcgen.CreateSiteSessionParams{
		ID:            s.ID,
		SiteID:        s.SiteID,
		TokenHash:     s.TokenHash,
		Method:        string(s.Method),
		ShareLinkID:   nullString(s.ShareLinkID),
		PolicyID:      s.PolicyID,
		PolicyVersion: int64(s.PolicyVersion),
		Subject:       nullString(s.Subject),
		CreatedAt:     ms(s.CreatedAt),
		ExpiresAt:     ms(s.ExpiresAt),
		RevokedAt:     nullMs(s.RevokedAt),
	}))
}

func (r sessionsRepo) GetSiteByTokenHash(ctx context.Context, hash string) (SiteSession, error) {
	row, err := r.db.q.GetSiteSessionByTokenHash(ctx, hash)
	if err != nil {
		return SiteSession{}, mapErr("get site session", err)
	}
	return SiteSession{
		ID:            row.ID,
		SiteID:        row.SiteID,
		TokenHash:     row.TokenHash,
		Method:        SessionMethod(row.Method),
		ShareLinkID:   row.ShareLinkID.String,
		PolicyID:      row.PolicyID,
		PolicyVersion: int(row.PolicyVersion),
		Subject:       row.Subject.String,
		CreatedAt:     fromMs(row.CreatedAt),
		ExpiresAt:     fromMs(row.ExpiresAt),
		RevokedAt:     fromNullMs(row.RevokedAt),
	}, nil
}

func (r sessionsRepo) RevokeSite(ctx context.Context, siteID, id string, at time.Time) error {
	n, err := r.db.q.RevokeSiteSession(ctx, sqlcgen.RevokeSiteSessionParams{ID: id, SiteID: siteID, RevokedAt: nullMs(at)})
	return rowsAffected("revoke site session", n, err)
}
