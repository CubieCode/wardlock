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

// APITokenQuery selects one page of an owner's usable API tokens, newest
// first.
type APITokenQuery struct {
	OwnerID string
	// Now excludes tokens that have expired by then.
	Now time.Time
	// BeforeCreated and BeforeID are the last row of the previous page;
	// zero values start at the first page.
	BeforeCreated time.Time
	BeforeID      string
	Limit         int
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
	// ClientName is what the CLI called itself; once approved, the name of
	// the token it will receive. Empty if none.
	ClientName string
}

// CLILoginDecision approves or denies the pending request with UserCode.
type CLILoginDecision struct {
	UserCode    string
	Status      CLILoginStatus // CLILoginApproved or CLILoginDenied
	OwnerID     string
	ScopeDeploy bool
	ScopeManage bool
	// TokenName replaces ClientName when not empty.
	TokenName string
	At        time.Time
}

// APITokens stores API tokens and the CLI login requests that mint them.
type APITokens interface {
	Create(ctx context.Context, t APIToken) error
	// GetByPublicID finds a token by its public ID, whatever its state;
	// callers compare the hash and check expiry and revocation.
	// Instance-wide: Bearer authentication calls it for every owner.
	GetByPublicID(ctx context.Context, publicID string) (APIToken, error)
	// Touch moves the token's last use forward to at; an earlier at
	// changes nothing.
	Touch(ctx context.Context, id string, at time.Time) error
	// Revoke revokes ownerID's token id. Another owner's or a missing token
	// is ErrNotFound; revoking twice keeps the first time.
	Revoke(ctx context.Context, ownerID, id string, at time.Time) error
	// List returns one page of usable (unrevoked, unexpired) tokens.
	List(ctx context.Context, q APITokenQuery) ([]APIToken, error)

	CreateCLILogin(ctx context.Context, r CLILoginRequest) error
	// GetCLILoginByDeviceCodeHash finds a request by its device code hash,
	// whatever its state.
	GetCLILoginByDeviceCodeHash(ctx context.Context, hash string) (CLILoginRequest, error)
	// DecideCLILogin approves or denies the pending request with the
	// decision's user code. A missing, decided or expired (at d.At) request
	// is ErrNotFound.
	DecideCLILogin(ctx context.Context, d CLILoginDecision) error
	// RedeemCLILogin creates t and marks the approved request loginID
	// redeemed with it, in one transaction. A request that is not approved
	// or has expired at now is ErrNotFound, and t is not created.
	RedeemCLILogin(ctx context.Context, loginID string, t APIToken, now time.Time) error
	// DeleteCLILoginsExpiredBefore removes requests that expired before t.
	DeleteCLILoginsExpiredBefore(ctx context.Context, t time.Time) error
}

// APITokens returns the SQLite APITokens repository.
func (db *DB) APITokens() APITokens { return apiTokensRepo{db} }

type apiTokensRepo struct{ db *DB }

func (r apiTokensRepo) Create(ctx context.Context, t APIToken) error {
	return createAPIToken(ctx, r.db.q, t)
}

func createAPIToken(ctx context.Context, q *sqlcgen.Queries, t APIToken) error {
	return mapErr("create API token", q.CreateAPIToken(ctx, sqlcgen.CreateAPITokenParams{
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

func apiTokenFromRow(row sqlcgen.ApiToken) APIToken {
	return APIToken{
		ID:          row.ID,
		OwnerID:     row.OwnerID,
		PublicID:    row.PublicID,
		TokenHash:   row.TokenHash,
		Name:        row.Name,
		ScopeDeploy: row.ScopeDeploy == 1,
		ScopeManage: row.ScopeManage == 1,
		CreatedAt:   fromMs(row.CreatedAt),
		ExpiresAt:   fromNullMs(row.ExpiresAt),
		LastUsedAt:  fromNullMs(row.LastUsedAt),
		RevokedAt:   fromNullMs(row.RevokedAt),
	}
}

func (r apiTokensRepo) GetByPublicID(ctx context.Context, publicID string) (APIToken, error) {
	row, err := r.db.q.GetAPITokenByPublicID(ctx, publicID)
	if err != nil {
		return APIToken{}, mapErr("get API token", err)
	}
	return apiTokenFromRow(row), nil
}

func (r apiTokensRepo) Touch(ctx context.Context, id string, at time.Time) error {
	return mapErr("touch API token", r.db.q.TouchAPIToken(ctx, sqlcgen.TouchAPITokenParams{ID: id, LastUsedAt: nullMs(at)}))
}

func (r apiTokensRepo) Revoke(ctx context.Context, ownerID, id string, at time.Time) error {
	n, err := r.db.q.RevokeAPIToken(ctx, sqlcgen.RevokeAPITokenParams{ID: id, OwnerID: ownerID, RevokedAt: nullMs(at)})
	return rowsAffected("revoke API token", n, err)
}

func (r apiTokensRepo) List(ctx context.Context, q APITokenQuery) ([]APIToken, error) {
	before := int64(math.MaxInt64)
	if q.BeforeID != "" {
		before = ms(q.BeforeCreated)
	}
	rows, err := r.db.q.ListAPITokens(ctx, sqlcgen.ListAPITokensParams{
		OwnerID:       q.OwnerID,
		Now:           sql.NullInt64{Int64: ms(q.Now), Valid: true},
		BeforeCreated: before,
		BeforeID:      q.BeforeID,
		RowLimit:      int64(q.Limit),
	})
	if err != nil {
		return nil, mapErr("list API tokens", err)
	}
	out := make([]APIToken, len(rows))
	for i, row := range rows {
		out[i] = apiTokenFromRow(row)
	}
	return out, nil
}
