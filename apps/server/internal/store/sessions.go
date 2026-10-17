// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
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

// Sessions stores dashboard (owner) and visitor (site) sessions.
type Sessions interface {
	CreateOwner(ctx context.Context, s OwnerSession) error
	CreateSite(ctx context.Context, s SiteSession) error
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
	}))
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
