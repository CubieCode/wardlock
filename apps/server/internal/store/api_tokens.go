// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// APIToken is an owner's API token (wl_<public id>_<secret>). Only the HMAC of
// the whole token is stored.
type APIToken struct {
	ID          string
	OwnerID     string
	PublicID    string
	TokenHash   string
	Name        string
	ScopeDeploy bool
	ScopeManage bool
	CreatedAt   time.Time
	ExpiresAt   time.Time // zero: never
	LastUsedAt  time.Time
	RevokedAt   time.Time
}

// CLILoginStatus is the state of a CLI login request.
type CLILoginStatus string

const (
	CLILoginPending  CLILoginStatus = "pending"
	CLILoginApproved CLILoginStatus = "approved"
	CLILoginDenied   CLILoginStatus = "denied"
	CLILoginRedeemed CLILoginStatus = "redeemed"
)

// CLILoginRequest is a device-code style CLI login. The device code is stored
// hashed; the API token is minted only when an approved request is redeemed.
type CLILoginRequest struct {
	ID             string
	DeviceCodeHash string
	UserCode       string
	Status         CLILoginStatus
	OwnerID        string // set once decided
	ScopeDeploy    bool
	ScopeManage    bool
	APITokenID     string // set once redeemed
	CreatedAt      time.Time
	ExpiresAt      time.Time
	DecidedAt      time.Time
}

// APITokens stores API tokens and the CLI login requests that mint them.
type APITokens interface {
	Create(ctx context.Context, t APIToken) error
	CreateCLILogin(ctx context.Context, r CLILoginRequest) error
}

// APITokens returns the SQLite APITokens repository.
func (db *DB) APITokens() APITokens { return apiTokensRepo{db} }

type apiTokensRepo struct{ db *DB }

func (r apiTokensRepo) Create(ctx context.Context, t APIToken) error {
	return mapErr("create API token", r.db.q.CreateAPIToken(ctx, sqlcgen.CreateAPITokenParams{
		ID:          t.ID,
		OwnerID:     t.OwnerID,
		PublicID:    t.PublicID,
		TokenHash:   t.TokenHash,
		Name:        t.Name,
		ScopeDeploy: boolInt(t.ScopeDeploy),
		ScopeManage: boolInt(t.ScopeManage),
		CreatedAt:   ms(t.CreatedAt),
		ExpiresAt:   nullMs(t.ExpiresAt),
		LastUsedAt:  nullMs(t.LastUsedAt),
		RevokedAt:   nullMs(t.RevokedAt),
	}))
}

func (r apiTokensRepo) CreateCLILogin(ctx context.Context, l CLILoginRequest) error {
	return mapErr("create CLI login request", r.db.q.CreateCLILoginRequest(ctx, sqlcgen.CreateCLILoginRequestParams{
		ID:             l.ID,
		DeviceCodeHash: l.DeviceCodeHash,
		UserCode:       l.UserCode,
		Status:         string(l.Status),
		OwnerID:        nullString(l.OwnerID),
		ScopeDeploy:    boolInt(l.ScopeDeploy),
		ScopeManage:    boolInt(l.ScopeManage),
		ApiTokenID:     nullString(l.APITokenID),
		CreatedAt:      ms(l.CreatedAt),
		ExpiresAt:      ms(l.ExpiresAt),
		DecidedAt:      nullMs(l.DecidedAt),
	}))
}
