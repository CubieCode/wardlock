// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Owner is an account that can sign in to the dashboard and the API.
type Owner struct {
	ID    string
	Email string // lowercase
	// InvitedBy is the inviting owner's ID; empty for owners created by setup
	// or `wardlockd admin create`.
	InvitedBy string
	CreatedAt time.Time
	// AcceptedAt is the first successful sign-in; zero while the invite is
	// outstanding.
	AcceptedAt time.Time
}

// LoginCodePurpose says what a one-time owner credential is for.
type LoginCodePurpose string

const (
	LoginCodeSetup     LoginCodePurpose = "setup"      // first-start setup URL; no owner
	LoginCodeSignInURL LoginCodePurpose = "signin_url" // admin create or invite sign-in URL
	LoginCodeEmail     LoginCodePurpose = "email_code" // 6-digit dashboard sign-in code
)

// OwnerLoginCode is a single-use owner credential. Only its hash is stored.
type OwnerLoginCode struct {
	ID        string
	OwnerID   string // empty for LoginCodeSetup
	Purpose   LoginCodePurpose
	CodeHash  string
	Attempts  int
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

// Owners stores owner accounts and their one-time sign-in credentials.
type Owners interface {
	Create(ctx context.Context, o Owner) error
	Get(ctx context.Context, id string) (Owner, error)
	CreateLoginCode(ctx context.Context, c OwnerLoginCode) error
}

// Owners returns the SQLite Owners repository.
func (db *DB) Owners() Owners { return ownersRepo{db} }

type ownersRepo struct{ db *DB }

func (r ownersRepo) Create(ctx context.Context, o Owner) error {
	return mapErr("create owner", r.db.q.CreateOwner(ctx, sqlcgen.CreateOwnerParams{
		ID:         o.ID,
		Email:      o.Email,
		InvitedBy:  nullString(o.InvitedBy),
		CreatedAt:  ms(o.CreatedAt),
		AcceptedAt: nullMs(o.AcceptedAt),
	}))
}

func (r ownersRepo) Get(ctx context.Context, id string) (Owner, error) {
	row, err := r.db.q.GetOwner(ctx, id)
	if err != nil {
		return Owner{}, mapErr("get owner", err)
	}
	return Owner{
		ID:         row.ID,
		Email:      row.Email,
		InvitedBy:  row.InvitedBy.String,
		CreatedAt:  fromMs(row.CreatedAt),
		AcceptedAt: fromNullMs(row.AcceptedAt),
	}, nil
}

func (r ownersRepo) CreateLoginCode(ctx context.Context, c OwnerLoginCode) error {
	return mapErr("create owner login code", r.db.q.CreateOwnerLoginCode(ctx, sqlcgen.CreateOwnerLoginCodeParams{
		ID:        c.ID,
		OwnerID:   nullString(c.OwnerID),
		Purpose:   string(c.Purpose),
		CodeHash:  c.CodeHash,
		Attempts:  int64(c.Attempts),
		CreatedAt: ms(c.CreatedAt),
		ExpiresAt: ms(c.ExpiresAt),
		UsedAt:    nullMs(c.UsedAt),
	}))
}
