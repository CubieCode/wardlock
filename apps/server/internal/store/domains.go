// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// DomainStatus is a custom domain's verification state (SPEC "Custom domains").
type DomainStatus string

const (
	DomainPending DomainStatus = "pending"
	DomainActive  DomainStatus = "active"
	DomainLapsed  DomainStatus = "lapsed"
)

// DomainFailure is why the last verification check failed.
type DomainFailure string

const (
	DomainNoRecord       DomainFailure = "no_record"
	DomainCNAMEElsewhere DomainFailure = "cname_elsewhere"
	DomainTXTMismatch    DomainFailure = "txt_mismatch"
	DomainDNSError       DomainFailure = "dns_error"
)

// Domain is a custom hostname attached to a site. VerificationToken is
// published in DNS, so it is stored as is rather than hashed.
type Domain struct {
	ID                string
	SiteID            string
	Hostname          string // lowercase A-label, no trailing dot or port
	Status            DomainStatus
	VerificationToken string
	CreatedAt         time.Time
	LastCheckedAt     time.Time
	// FailingSince is the first failure in the current run of failed checks.
	FailingSince      time.Time
	LastFailureReason DomainFailure
}

// Domains stores custom domains. A hostname belongs to at most one site on
// the instance (ErrConflict otherwise).
type Domains interface {
	// Create attaches d to one of ownerID's sites.
	Create(ctx context.Context, ownerID string, d Domain) error
}

// Domains returns the SQLite Domains repository.
func (db *DB) Domains() Domains { return domainsRepo{db} }

type domainsRepo struct{ db *DB }

func (r domainsRepo) Create(ctx context.Context, ownerID string, d Domain) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, d.SiteID); err != nil {
			return err
		}
		return mapErr("create domain", q.CreateDomain(ctx, sqlcgen.CreateDomainParams{
			ID:                d.ID,
			SiteID:            d.SiteID,
			Hostname:          d.Hostname,
			Status:            string(d.Status),
			VerificationToken: d.VerificationToken,
			CreatedAt:         ms(d.CreatedAt),
			LastCheckedAt:     nullMs(d.LastCheckedAt),
			FailingSince:      nullMs(d.FailingSince),
			LastFailureReason: nullString(string(d.LastFailureReason)),
		}))
	})
}
