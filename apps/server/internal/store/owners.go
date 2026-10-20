// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"fmt"
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
	// SuspendedAt is when the operator suspended the owner (A2.13); zero
	// while not suspended.
	SuspendedAt time.Time
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
	// GetByEmail finds an owner by normalised (lowercase) address.
	GetByEmail(ctx context.Context, email string) (Owner, error)
	// Count returns the number of owners on the instance.
	Count(ctx context.Context) (int, error)
	// MarkAccepted records the owner's first sign-in; later calls keep the
	// first time. An unknown owner is not an error.
	MarkAccepted(ctx context.Context, id string, at time.Time) error
	// CreateWithLoginCode creates o and its first login code c in one
	// transaction (`wardlockd admin create`, invitations).
	CreateWithLoginCode(ctx context.Context, o Owner, c OwnerLoginCode) error
	// CompleteSetup spends the setup code codeID and creates the first owner
	// o in one transaction. A used or expired code is ErrNotFound; an
	// instance that already has an owner is ErrConflict. Nothing changes on
	// error.
	CompleteSetup(ctx context.Context, codeID string, o Owner) error
	// GetLoginCodeByHash finds a code of purpose by its hash, used or not.
	GetLoginCodeByHash(ctx context.Context, purpose LoginCodePurpose, hash string) (OwnerLoginCode, error)
	// LatestLoginCode returns the owner's most recently created code of
	// purpose, used or not.
	LatestLoginCode(ctx context.Context, ownerID string, purpose LoginCodePurpose) (OwnerLoginCode, error)
	// AddLoginCodeAttempt counts one verification attempt against an unused
	// code with fewer than maxAttempts so far and returns the new count. A
	// used or exhausted code is ErrNotFound, so concurrent attempts never
	// count past maxAttempts.
	AddLoginCodeAttempt(ctx context.Context, id string, maxAttempts int) (int, error)
	// ReplaceLoginCode retires the owner's unused codes of c's purpose and
	// creates c, in one transaction, so at most one is ever valid.
	ReplaceLoginCode(ctx context.Context, c OwnerLoginCode) error
	// UseLoginCode marks an unused code used at at. A code that is already
	// used or expired by at is ErrNotFound, so each code works once even
	// under concurrent redemption.
	UseLoginCode(ctx context.Context, id string, at time.Time) error
	// RetireLoginCodes marks every unused code of purpose for ownerID used,
	// so only the next one issued works. For LoginCodeSetup, ownerID is
	// ignored and every unused setup code is retired.
	RetireLoginCodes(ctx context.Context, ownerID string, purpose LoginCodePurpose, at time.Time) error

	// Operator controls (A2.13), for `wardlockd admin` only.

	// Suspend marks the owner suspended at at (an already suspended owner
	// keeps the first time) and, in the same transaction, revokes every
	// dashboard session and denies every approved CLI login not yet
	// collected. Session and API token lookups skip a suspended owner's
	// credentials. An unknown owner is ErrNotFound.
	Suspend(ctx context.Context, id string, at time.Time) error
	// Reinstate clears the suspension; the owner's API tokens work again,
	// but revoked sessions stay revoked. An unknown owner is ErrNotFound.
	Reinstate(ctx context.Context, id string) error
	// ListAll returns up to limit owners on the instance ordered by
	// address, starting after afterEmail ("" for the first page).
	ListAll(ctx context.Context, afterEmail string, limit int) ([]Owner, error)
	// Quotas returns the owner's quota overrides and what counts against
	// them, with deploys counted over the DeployWindow before now (A2.14).
	// An unknown owner is ErrNotFound.
	Quotas(ctx context.Context, id string, now time.Time) (QuotaOverrides, OwnerUsage, error)
	// SetQuotas replaces the owner's quota overrides. An unknown owner is
	// ErrNotFound.
	SetQuotas(ctx context.Context, id string, o QuotaOverrides) error
}

// Owners returns the SQLite Owners repository.
func (db *DB) Owners() Owners { return ownersRepo{db} }

type ownersRepo struct{ db *DB }

func (r ownersRepo) Create(ctx context.Context, o Owner) error {
	return mapErr("create owner", r.db.q.CreateOwner(ctx, ownerParams(o)))
}

func (r ownersRepo) Get(ctx context.Context, id string) (Owner, error) {
	row, err := r.db.q.GetOwner(ctx, id)
	if err != nil {
		return Owner{}, mapErr("get owner", err)
	}
	return ownerFromRow(row), nil
}

func ownerFromRow(row sqlcgen.GetOwnerRow) Owner {
	return Owner{
		ID:          row.ID,
		Email:       row.Email,
		InvitedBy:   row.InvitedBy.String,
		CreatedAt:   fromMs(row.CreatedAt),
		AcceptedAt:  fromNullMs(row.AcceptedAt),
		SuspendedAt: fromNullMs(row.SuspendedAt),
	}
}

func (r ownersRepo) CreateLoginCode(ctx context.Context, c OwnerLoginCode) error {
	return mapErr("create owner login code", r.db.q.CreateOwnerLoginCode(ctx, loginCodeParams(c)))
}

func loginCodeParams(c OwnerLoginCode) sqlcgen.CreateOwnerLoginCodeParams {
	return sqlcgen.CreateOwnerLoginCodeParams{
		ID:        c.ID,
		OwnerID:   nullString(c.OwnerID),
		Purpose:   string(c.Purpose),
		CodeHash:  c.CodeHash,
		Attempts:  int64(c.Attempts),
		CreatedAt: ms(c.CreatedAt),
		ExpiresAt: ms(c.ExpiresAt),
		UsedAt:    nullMs(c.UsedAt),
	}
}

func ownerParams(o Owner) sqlcgen.CreateOwnerParams {
	return sqlcgen.CreateOwnerParams{
		ID:         o.ID,
		Email:      o.Email,
		InvitedBy:  nullString(o.InvitedBy),
		CreatedAt:  ms(o.CreatedAt),
		AcceptedAt: nullMs(o.AcceptedAt),
	}
}

func loginCodeFromRow(row sqlcgen.OwnerLoginCode) OwnerLoginCode {
	return OwnerLoginCode{
		ID:        row.ID,
		OwnerID:   row.OwnerID.String,
		Purpose:   LoginCodePurpose(row.Purpose),
		CodeHash:  row.CodeHash,
		Attempts:  int(row.Attempts),
		CreatedAt: fromMs(row.CreatedAt),
		ExpiresAt: fromMs(row.ExpiresAt),
		UsedAt:    fromNullMs(row.UsedAt),
	}
}

func (r ownersRepo) GetByEmail(ctx context.Context, email string) (Owner, error) {
	row, err := r.db.q.GetOwnerByEmail(ctx, email)
	if err != nil {
		return Owner{}, mapErr("get owner by email", err)
	}
	return ownerFromRow(sqlcgen.GetOwnerRow(row)), nil
}

func (r ownersRepo) Count(ctx context.Context) (int, error) {
	n, err := r.db.q.CountOwners(ctx)
	if err != nil {
		return 0, mapErr("count owners", err)
	}
	return int(n), nil
}

func (r ownersRepo) MarkAccepted(ctx context.Context, id string, at time.Time) error {
	return mapErr("mark owner accepted", r.db.q.MarkOwnerAccepted(ctx, sqlcgen.MarkOwnerAcceptedParams{
		ID:         id,
		AcceptedAt: nullMs(at),
	}))
}

func (r ownersRepo) CreateWithLoginCode(ctx context.Context, o Owner, c OwnerLoginCode) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := q.CreateOwner(ctx, ownerParams(o)); err != nil {
			return mapErr("create owner", err)
		}
		return mapErr("create owner login code", q.CreateOwnerLoginCode(ctx, loginCodeParams(c)))
	})
}

func (r ownersRepo) CompleteSetup(ctx context.Context, codeID string, o Owner) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		n, err := q.UseOwnerLoginCode(ctx, sqlcgen.UseOwnerLoginCodeParams{ID: codeID, UsedAt: nullMs(o.CreatedAt)})
		if err := rowsAffected("use setup code", n, err); err != nil {
			return err
		}
		count, err := q.CountOwners(ctx)
		if err != nil {
			return mapErr("count owners", err)
		}
		if count > 0 {
			return fmt.Errorf("complete setup: an owner exists: %w", ErrConflict)
		}
		return mapErr("create owner", q.CreateOwner(ctx, ownerParams(o)))
	})
}

func (r ownersRepo) GetLoginCodeByHash(ctx context.Context, purpose LoginCodePurpose, hash string) (OwnerLoginCode, error) {
	row, err := r.db.q.GetOwnerLoginCodeByHash(ctx, sqlcgen.GetOwnerLoginCodeByHashParams{Purpose: string(purpose), CodeHash: hash})
	if err != nil {
		return OwnerLoginCode{}, mapErr("get owner login code", err)
	}
	return loginCodeFromRow(row), nil
}

func (r ownersRepo) LatestLoginCode(ctx context.Context, ownerID string, purpose LoginCodePurpose) (OwnerLoginCode, error) {
	row, err := r.db.q.LatestOwnerLoginCode(ctx, sqlcgen.LatestOwnerLoginCodeParams{OwnerID: nullString(ownerID), Purpose: string(purpose)})
	if err != nil {
		return OwnerLoginCode{}, mapErr("latest owner login code", err)
	}
	return loginCodeFromRow(row), nil
}

func (r ownersRepo) AddLoginCodeAttempt(ctx context.Context, id string, maxAttempts int) (int, error) {
	n, err := r.db.q.AddOwnerLoginCodeAttempt(ctx, sqlcgen.AddOwnerLoginCodeAttemptParams{ID: id, MaxAttempts: int64(maxAttempts)})
	if err != nil {
		return 0, mapErr("add owner login code attempt", err)
	}
	return int(n), nil
}

func (r ownersRepo) ReplaceLoginCode(ctx context.Context, c OwnerLoginCode) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		var err error
		if c.Purpose == LoginCodeSetup {
			err = q.RetireSetupCodes(ctx, nullMs(c.CreatedAt))
		} else {
			err = q.RetireOwnerLoginCodes(ctx, sqlcgen.RetireOwnerLoginCodesParams{
				OwnerID: nullString(c.OwnerID), Purpose: string(c.Purpose), UsedAt: nullMs(c.CreatedAt),
			})
		}
		if err != nil {
			return mapErr("retire owner login codes", err)
		}
		return mapErr("create owner login code", q.CreateOwnerLoginCode(ctx, loginCodeParams(c)))
	})
}

func (r ownersRepo) UseLoginCode(ctx context.Context, id string, at time.Time) error {
	n, err := r.db.q.UseOwnerLoginCode(ctx, sqlcgen.UseOwnerLoginCodeParams{ID: id, UsedAt: nullMs(at)})
	return rowsAffected("use owner login code", n, err)
}

func (r ownersRepo) RetireLoginCodes(ctx context.Context, ownerID string, purpose LoginCodePurpose, at time.Time) error {
	if purpose == LoginCodeSetup {
		return mapErr("retire setup codes", r.db.q.RetireSetupCodes(ctx, nullMs(at)))
	}
	return mapErr("retire owner login codes", r.db.q.RetireOwnerLoginCodes(ctx, sqlcgen.RetireOwnerLoginCodesParams{
		OwnerID: nullString(ownerID),
		Purpose: string(purpose),
		UsedAt:  nullMs(at),
	}))
}

func (r ownersRepo) Suspend(ctx context.Context, id string, at time.Time) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		n, err := q.SuspendOwner(ctx, sqlcgen.SuspendOwnerParams{ID: id, SuspendedAt: nullMs(at)})
		if err := rowsAffected("suspend owner", n, err); err != nil {
			return err
		}
		if err := q.RevokeAllOwnerSessions(ctx, sqlcgen.RevokeAllOwnerSessionsParams{OwnerID: id, RevokedAt: nullMs(at)}); err != nil {
			return mapErr("revoke owner sessions", err)
		}
		return mapErr("deny cli logins", q.DenyApprovedCLILogins(ctx, nullString(id)))
	})
}

func (r ownersRepo) Reinstate(ctx context.Context, id string) error {
	n, err := r.db.q.ReinstateOwner(ctx, id)
	return rowsAffected("reinstate owner", n, err)
}

func (r ownersRepo) ListAll(ctx context.Context, afterEmail string, limit int) ([]Owner, error) {
	rows, err := r.db.q.ListAllOwners(ctx, sqlcgen.ListAllOwnersParams{AfterEmail: afterEmail, RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list all owners", err)
	}
	out := make([]Owner, len(rows))
	for i, row := range rows {
		out[i] = ownerFromRow(sqlcgen.GetOwnerRow(row))
	}
	return out, nil
}

func (r ownersRepo) Quotas(ctx context.Context, id string, now time.Time) (QuotaOverrides, OwnerUsage, error) {
	row, err := r.db.q.GetOwnerQuotas(ctx, id)
	if err != nil {
		return QuotaOverrides{}, OwnerUsage{}, mapErr("get owner quotas", err)
	}
	u := OwnerUsage{StoredBytes: row.StoredBytes}
	if u.Sites, err = r.db.q.CountOwnerSites(ctx, id); err != nil {
		return QuotaOverrides{}, OwnerUsage{}, mapErr("count sites", err)
	}
	after := ms(now.Add(-DeployWindow))
	if u.DeploysToday, err = r.db.q.CountOwnerDeploysSince(ctx, sqlcgen.CountOwnerDeploysSinceParams{OwnerID: id, After: after}); err != nil {
		return QuotaOverrides{}, OwnerUsage{}, mapErr("count recent deploys", err)
	}
	return overridesFrom(row.MaxStorageBytes, row.MaxSites, row.MaxDeploysPerDay), u, nil
}

func (r ownersRepo) SetQuotas(ctx context.Context, id string, o QuotaOverrides) error {
	n, err := r.db.q.SetOwnerQuotas(ctx, sqlcgen.SetOwnerQuotasParams{
		MaxStorageBytes:  nullOverride(o.StorageBytes),
		MaxSites:         nullOverride(o.Sites),
		MaxDeploysPerDay: nullOverride(o.DeploysPerDay),
		ID:               id,
	})
	return rowsAffected("set owner quotas", n, err)
}
