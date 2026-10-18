// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// VisitorEmailCode is a code emailed to a visitor for one policy (A2.4).
// Neither the code nor the address is stored, only their HMACs.
type VisitorEmailCode struct {
	ID            string
	SiteID        string
	PolicyID      string
	PolicyVersion int
	AddressHash   string
	CodeHash      string
	Attempts      int
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// EmailCodes stores visitor email codes and the site sessions they earn.
type EmailCodes interface {
	// Put stores c as the code for (c.SiteID, c.PolicyID, c.AddressHash),
	// replacing an earlier one with no attempts counted, only while the
	// policy is still protected with the email method at c.PolicyVersion;
	// otherwise it stores nothing and returns ErrNotFound. It also deletes
	// every code, on any site, that expired by c.CreatedAt.
	Put(ctx context.Context, c VisitorEmailCode) error
	// Get returns the code for an address hash under a policy, expired or
	// not.
	Get(ctx context.Context, siteID, policyID, addressHash string) (VisitorEmailCode, error)
	// AddAttempt counts one attempt on code id while it is unexpired at
	// now and has fewer than max, and returns the new count; otherwise
	// ErrNotFound. Concurrent attempts never exceed max.
	AddAttempt(ctx context.Context, id string, max int, now time.Time) (int, error)
	// Use deletes code id if it is unexpired at now, so it works once;
	// otherwise ErrNotFound.
	Use(ctx context.Context, id string, now time.Time) error
	// CreateSession stores s, an email session, only while its policy
	// (s.SiteID, s.PolicyID) is still protected with the email method at
	// version s.PolicyVersion, in one statement; otherwise it stores
	// nothing and returns ErrNotFound, so an allowlist change that commits
	// first cannot race a sign-in into a live session.
	CreateSession(ctx context.Context, s SiteSession) error
}

// EmailCodes returns the SQLite EmailCodes repository.
func (db *DB) EmailCodes() EmailCodes { return emailCodesRepo{db} }

type emailCodesRepo struct{ db *DB }

func (r emailCodesRepo) Put(ctx context.Context, c VisitorEmailCode) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := q.DeleteExpiredVisitorEmailCodes(ctx, ms(c.CreatedAt)); err != nil {
			return mapErr("delete expired email codes", err)
		}
		n, err := q.UpsertVisitorEmailCode(ctx, sqlcgen.UpsertVisitorEmailCodeParams{
			ID:            c.ID,
			AddressHash:   c.AddressHash,
			CodeHash:      c.CodeHash,
			CreatedAt:     ms(c.CreatedAt),
			ExpiresAt:     ms(c.ExpiresAt),
			PolicyID:      c.PolicyID,
			SiteID:        c.SiteID,
			PolicyVersion: int64(c.PolicyVersion),
		})
		return rowsAffected("put email code", n, err)
	})
}

func (r emailCodesRepo) Get(ctx context.Context, siteID, policyID, addressHash string) (VisitorEmailCode, error) {
	row, err := r.db.q.GetVisitorEmailCode(ctx, sqlcgen.GetVisitorEmailCodeParams{
		SiteID: siteID, PolicyID: policyID, AddressHash: addressHash,
	})
	if err != nil {
		return VisitorEmailCode{}, mapErr("get email code", err)
	}
	return VisitorEmailCode{
		ID:            row.ID,
		SiteID:        row.SiteID,
		PolicyID:      row.PolicyID,
		PolicyVersion: int(row.PolicyVersion),
		AddressHash:   row.AddressHash,
		CodeHash:      row.CodeHash,
		Attempts:      int(row.Attempts),
		CreatedAt:     fromMs(row.CreatedAt),
		ExpiresAt:     fromMs(row.ExpiresAt),
	}, nil
}

func (r emailCodesRepo) AddAttempt(ctx context.Context, id string, max int, now time.Time) (int, error) {
	n, err := r.db.q.AddVisitorEmailCodeAttempt(ctx, sqlcgen.AddVisitorEmailCodeAttemptParams{
		ID: id, MaxAttempts: int64(max), Now: ms(now),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, mapErr("count email code attempt", err)
	}
	return int(n), nil
}

func (r emailCodesRepo) Use(ctx context.Context, id string, now time.Time) error {
	n, err := r.db.q.UseVisitorEmailCode(ctx, sqlcgen.UseVisitorEmailCodeParams{ID: id, Now: ms(now)})
	return rowsAffected("use email code", n, err)
}

func (r emailCodesRepo) CreateSession(ctx context.Context, s SiteSession) error {
	n, err := r.db.q.CreateEmailSession(ctx, sqlcgen.CreateEmailSessionParams{
		ID:               s.ID,
		SessionTokenHash: s.TokenHash,
		Subject:          nullString(s.Subject),
		CreatedAt:        ms(s.CreatedAt),
		ExpiresAt:        ms(s.ExpiresAt),
		PolicyID:         s.PolicyID,
		SiteID:           s.SiteID,
		PolicyVersion:    int64(s.PolicyVersion),
	})
	return rowsAffected("create email session", n, err)
}
