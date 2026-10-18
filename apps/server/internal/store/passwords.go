// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// PasswordSessions ends the site sessions a policy password created (A2.3).
type PasswordSessions interface {
	// RevokeForPolicy revokes, at at, every unrevoked password session of
	// siteID bound to policyID at any version, and returns how many it
	// revoked. Other methods' sessions are untouched. Instance-wide: the
	// caller has already checked the policy belongs to the owner.
	RevokeForPolicy(ctx context.Context, siteID, policyID string, at time.Time) (int, error)
}

// PasswordSessions returns the SQLite PasswordSessions repository.
func (db *DB) PasswordSessions() PasswordSessions { return passwordSessionsRepo{db} }

type passwordSessionsRepo struct{ db *DB }

func (r passwordSessionsRepo) RevokeForPolicy(ctx context.Context, siteID, policyID string, at time.Time) (int, error) {
	n, err := r.db.q.RevokePolicyPasswordSessions(ctx, sqlcgen.RevokePolicyPasswordSessionsParams{
		SiteID: siteID, PolicyID: policyID, RevokedAt: nullMs(at),
	})
	if err != nil {
		return 0, mapErr("revoke password sessions", err)
	}
	return int(n), nil
}
