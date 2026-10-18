// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// PasswordSessions creates the site sessions a policy password earns
// (A2.3); Policies.Set ends them when the password changes.
type PasswordSessions interface {
	// CreateSession stores s, a password session, only while its policy
	// (s.SiteID, s.PolicyID) is still protected with the password method,
	// passwordHash and version s.PolicyVersion, in one statement. Otherwise
	// it stores nothing and returns ErrNotFound, so a password change that
	// commits first cannot race a sign-in into a live session.
	CreateSession(ctx context.Context, s SiteSession, passwordHash string) error
}

// PasswordSessions returns the SQLite PasswordSessions repository.
func (db *DB) PasswordSessions() PasswordSessions { return passwordSessionsRepo{db} }

type passwordSessionsRepo struct{ db *DB }

func (r passwordSessionsRepo) CreateSession(ctx context.Context, s SiteSession, passwordHash string) error {
	n, err := r.db.q.CreatePasswordSession(ctx, sqlcgen.CreatePasswordSessionParams{
		ID:               s.ID,
		SessionTokenHash: s.TokenHash,
		CreatedAt:        ms(s.CreatedAt),
		ExpiresAt:        ms(s.ExpiresAt),
		PolicyID:         s.PolicyID,
		SiteID:           s.SiteID,
		PolicyVersion:    int64(s.PolicyVersion),
		PasswordHash:     nullString(passwordHash),
	})
	return rowsAffected("create password session", n, err)
}
