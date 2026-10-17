// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Visibility says whether a policy lets anyone in.
type Visibility string

const (
	Public    Visibility = "public"
	Protected Visibility = "protected"
)

// Policy decides who may read a site or a path prefix. PathPrefix "/" is the
// site default. Version increases on every change.
type Policy struct {
	ID              string
	SiteID          string
	PathPrefix      string
	Visibility      Visibility
	LinksEnabled    bool
	PasswordEnabled bool
	EmailEnabled    bool
	OAuthEnabled    bool
	PasswordHash    string // Argon2id
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Policies stores site and path policies, one per site and prefix.
type Policies interface {
	// Create adds p to one of ownerID's sites.
	Create(ctx context.Context, ownerID string, p Policy) error
}

// Policies returns the SQLite Policies repository.
func (db *DB) Policies() Policies { return policiesRepo{db} }

type policiesRepo struct{ db *DB }

func (r policiesRepo) Create(ctx context.Context, ownerID string, p Policy) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, p.SiteID); err != nil {
			return err
		}
		return mapErr("create policy", q.CreatePolicy(ctx, sqlcgen.CreatePolicyParams{
			ID:              p.ID,
			SiteID:          p.SiteID,
			PathPrefix:      p.PathPrefix,
			Visibility:      string(p.Visibility),
			LinksEnabled:    boolInt(p.LinksEnabled),
			PasswordEnabled: boolInt(p.PasswordEnabled),
			EmailEnabled:    boolInt(p.EmailEnabled),
			OauthEnabled:    boolInt(p.OAuthEnabled),
			PasswordHash:    nullString(p.PasswordHash),
			Version:         int64(p.Version),
			CreatedAt:       ms(p.CreatedAt),
			UpdatedAt:       ms(p.UpdatedAt),
		}))
	})
}
