// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// CLI login requests are part of the APITokens repository (api_tokens.go).

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
		ClientName:     nullString(l.ClientName),
	}))
}

func (r apiTokensRepo) GetCLILoginByDeviceCodeHash(ctx context.Context, hash string) (CLILoginRequest, error) {
	row, err := r.db.q.GetCLILoginByDeviceCodeHash(ctx, hash)
	if err != nil {
		return CLILoginRequest{}, mapErr("get CLI login request", err)
	}
	return CLILoginRequest{
		ID:             row.ID,
		DeviceCodeHash: row.DeviceCodeHash,
		UserCode:       row.UserCode,
		Status:         CLILoginStatus(row.Status),
		OwnerID:        row.OwnerID.String,
		ScopeDeploy:    row.ScopeDeploy == 1,
		ScopeManage:    row.ScopeManage == 1,
		APITokenID:     row.ApiTokenID.String,
		CreatedAt:      fromMs(row.CreatedAt),
		ExpiresAt:      fromMs(row.ExpiresAt),
		DecidedAt:      fromNullMs(row.DecidedAt),
		ClientName:     row.ClientName.String,
	}, nil
}

func (r apiTokensRepo) DecideCLILogin(ctx context.Context, d CLILoginDecision) error {
	n, err := r.db.q.DecideCLILogin(ctx, sqlcgen.DecideCLILoginParams{
		Status:      string(d.Status),
		OwnerID:     nullString(d.OwnerID),
		ScopeDeploy: boolInt(d.ScopeDeploy),
		ScopeManage: boolInt(d.ScopeManage),
		ClientName:  nullString(d.TokenName),
		DecidedAt:   nullMs(d.At),
		UserCode:    d.UserCode,
	})
	return rowsAffected("decide CLI login request", n, err)
}

func (r apiTokensRepo) RedeemCLILogin(ctx context.Context, loginID string, t APIToken, now time.Time) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := createAPIToken(ctx, q, t); err != nil {
			return err
		}
		n, err := q.RedeemCLILogin(ctx, sqlcgen.RedeemCLILoginParams{
			ID: loginID, ApiTokenID: sql.NullString{String: t.ID, Valid: true}, Now: ms(now),
		})
		return rowsAffected("redeem CLI login request", n, err)
	})
}

func (r apiTokensRepo) DeleteCLILoginsExpiredBefore(ctx context.Context, t time.Time) error {
	return mapErr("delete expired CLI login requests", r.db.q.DeleteCLILoginsExpiredBefore(ctx, ms(t)))
}
