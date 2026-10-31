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
	// CreatedBy is who made it: the owner, or for a team token's work the
	// team, with CreatedByToken naming the token (T7.1). Read only: the
	// repository records its actor.
	CreatedBy      string
	CreatedByToken string
	CreatedAt      time.Time
	LastCheckedAt  time.Time
	// VerifiedAt is when the domain last became active; zero if never.
	VerifiedAt time.Time
	// FailingSince is the first failure in the current run of failed checks.
	FailingSince      time.Time
	LastFailureReason DomainFailure
	// FailureDetail is a plain explanation of the last failure for the owner.
	FailureDetail string
	// Primary means the owner chose the domain as its site's primary
	// hostname (C1.18), whatever its status. Read only, and set only by
	// Get and List; see Sites.SetPrimaryDomain.
	Primary bool
}

// Domains stores custom domains. A hostname belongs to at most one site on
// the instance (ErrConflict otherwise).
type Domains interface {
	// Create attaches d to a site the actor can edit.
	Create(ctx context.Context, actor Actor, d Domain) error
	// Get returns the domain hostname of site siteID, whatever
	// its status. A missing domain, site or another owner's site is
	// ErrNotFound alike.
	Get(ctx context.Context, actor Actor, siteID, hostname string) (Domain, error)
	// List returns up to limit of the domains of site siteID
	// ordered by hostname, after afterHostname ("" for the first page).
	// Another owner's site lists nothing.
	List(ctx context.Context, actor Actor, siteID, afterHostname string, limit int) ([]Domain, error)
	// Delete detaches hostname from site siteID (ErrNotFound if
	// it is not attached there).
	Delete(ctx context.Context, actor Actor, siteID, hostname string) error
	// GetActiveByHostname finds an active domain on any site. It is the
	// instance-wide routing lookup for the edge; a pending or lapsed
	// hostname is ErrNotFound.
	GetActiveByHostname(ctx context.Context, hostname string) (Domain, error)
	// RecordCheck stores the outcome of a verification check of prev, the
	// domain as the checker read it: next's status, LastCheckedAt,
	// VerifiedAt, FailingSince, LastFailureReason and FailureDetail. It is
	// a compare-and-set on prev's ID, status and LastCheckedAt, so it is
	// ErrNotFound, changing nothing, once the claim is gone or another
	// check has been recorded since prev was read. Instance-wide.
	RecordCheck(ctx context.Context, prev, next Domain) error
	// ListToCheck returns up to limit domains, by ID after afterID ("" for
	// the first page), that may be due a check: pending domains created
	// at or after windowStart, and active or lapsed ones never checked or
	// last checked at or before recheckBefore. Instance-wide.
	ListToCheck(ctx context.Context, windowStart, recheckBefore time.Time, afterID string, limit int) ([]Domain, error)
	// ListExpiredPending returns up to limit pending domains created at or
	// before createdBefore, by ID after afterID ("" for the first page).
	// Instance-wide.
	ListExpiredPending(ctx context.Context, createdBefore time.Time, afterID string, limit int) ([]Domain, error)
	// DeleteExpiredPending deletes the claim with ID id only while it is
	// still pending and was created at or before createdBefore; otherwise
	// (gone, or verified meanwhile) it is ErrNotFound and changes nothing.
	// Instance-wide.
	DeleteExpiredPending(ctx context.Context, id string, createdBefore time.Time) error
}

// Domains returns the SQLite Domains repository.
func (db *DB) Domains() Domains { return domainsRepo{db} }

type domainsRepo struct{ db *DB }

func (r domainsRepo) Create(ctx context.Context, actor Actor, d Domain) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, d.SiteID, RoleEditor)
	if err != nil {
		return err
	}
	by, token := actor.MadeBy()
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
			VerifiedAt:        nullMs(d.VerifiedAt),
			FailingSince:      nullMs(d.FailingSince),
			LastFailureReason: nullString(string(d.LastFailureReason)),
			FailureDetail:     nullString(d.FailureDetail),
			CreatedBy:         nullString(by),
			CreatedByToken:    nullString(token),
		}))
	})
}

func (r domainsRepo) Get(ctx context.Context, actor Actor, siteID, hostname string) (Domain, error) {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleViewer)
	if err != nil {
		return Domain{}, err
	}
	row, err := r.db.q.GetDomain(ctx, sqlcgen.GetDomainParams{SiteID: siteID, Hostname: hostname, OwnerID: ownerID})
	if err != nil {
		return Domain{}, mapErr("get domain", err)
	}
	return ownedDomainFrom(row.Domain, row.IsPrimary), nil
}

func (r domainsRepo) List(ctx context.Context, actor Actor, siteID, afterHostname string, limit int) ([]Domain, error) {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleViewer)
	if errors.Is(err, ErrNotFound) {
		return []Domain{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.q.ListDomains(ctx, sqlcgen.ListDomainsParams{
		SiteID: siteID, OwnerID: ownerID, AfterHostname: afterHostname, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, mapErr("list domains", err)
	}
	out := make([]Domain, 0, len(rows))
	for _, row := range rows {
		out = append(out, ownedDomainFrom(row.Domain, row.IsPrimary))
	}
	return out, nil
}

func (r domainsRepo) Delete(ctx context.Context, actor Actor, siteID, hostname string) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return err
	}
	_, err = r.db.q.DeleteDomain(ctx, sqlcgen.DeleteDomainParams{SiteID: siteID, Hostname: hostname, OwnerID: ownerID})
	return mapErr("delete domain", err)
}

func (r domainsRepo) GetActiveByHostname(ctx context.Context, hostname string) (Domain, error) {
	row, err := r.db.q.GetActiveDomainByHostname(ctx, hostname)
	if err != nil {
		return Domain{}, mapErr("get active domain", err)
	}
	return domainFrom(sqlcgen.GetActiveDomainByHostnameRow(row)), nil
}

func (r domainsRepo) RecordCheck(ctx context.Context, prev, next Domain) error {
	_, err := r.db.q.RecordDomainCheck(ctx, sqlcgen.RecordDomainCheckParams{
		ID:                prev.ID,
		PrevStatus:        string(prev.Status),
		PrevLastCheckedAt: nullMs(prev.LastCheckedAt),
		Status:            string(next.Status),
		LastCheckedAt:     nullMs(next.LastCheckedAt),
		VerifiedAt:        nullMs(next.VerifiedAt),
		FailingSince:      nullMs(next.FailingSince),
		LastFailureReason: nullString(string(next.LastFailureReason)),
		FailureDetail:     nullString(next.FailureDetail),
	})
	return mapErr("record domain check", err)
}

func (r domainsRepo) ListToCheck(ctx context.Context, windowStart, recheckBefore time.Time, afterID string, limit int) ([]Domain, error) {
	rows, err := r.db.q.ListDomainsToCheck(ctx, sqlcgen.ListDomainsToCheckParams{
		AfterID:       afterID,
		WindowStart:   ms(windowStart),
		RecheckBefore: sql.NullInt64{Int64: ms(recheckBefore), Valid: true},
		RowLimit:      int64(limit),
	})
	if err != nil {
		return nil, mapErr("list domains to check", err)
	}
	out := make([]Domain, 0, len(rows))
	for _, row := range rows {
		out = append(out, domainFrom(sqlcgen.GetActiveDomainByHostnameRow(row)))
	}
	return out, nil
}

func (r domainsRepo) ListExpiredPending(ctx context.Context, createdBefore time.Time, afterID string, limit int) ([]Domain, error) {
	rows, err := r.db.q.ListExpiredPendingDomains(ctx, sqlcgen.ListExpiredPendingDomainsParams{
		AfterID: afterID, CreatedBefore: ms(createdBefore), RowLimit: int64(limit),
	})
	if err != nil {
		return nil, mapErr("list expired pending domains", err)
	}
	out := make([]Domain, 0, len(rows))
	for _, row := range rows {
		out = append(out, domainFrom(sqlcgen.GetActiveDomainByHostnameRow(row)))
	}
	return out, nil
}

func (r domainsRepo) DeleteExpiredPending(ctx context.Context, id string, createdBefore time.Time) error {
	_, err := r.db.q.DeleteExpiredPendingDomain(ctx, sqlcgen.DeleteExpiredPendingDomainParams{ID: id, CreatedBefore: ms(createdBefore)})
	return mapErr("delete expired pending domain", err)
}

func domainFrom(row sqlcgen.GetActiveDomainByHostnameRow) Domain {
	return Domain{
		ID:                row.ID,
		SiteID:            row.SiteID,
		Hostname:          row.Hostname,
		Status:            DomainStatus(row.Status),
		VerificationToken: row.VerificationToken,
		CreatedAt:         fromMs(row.CreatedAt),
		LastCheckedAt:     fromNullMs(row.LastCheckedAt),
		VerifiedAt:        fromNullMs(row.VerifiedAt),
		FailingSince:      fromNullMs(row.FailingSince),
		LastFailureReason: DomainFailure(row.LastFailureReason.String),
		FailureDetail:     row.FailureDetail.String,
		CreatedBy:         row.CreatedBy.String,
		CreatedByToken:    row.CreatedByToken.String,
	}
}

// ownedDomainFrom converts an owner read's row, with its primary flag.
func ownedDomainFrom(d sqlcgen.Domain, isPrimary int64) Domain {
	out := domainFrom(sqlcgen.GetActiveDomainByHostnameRow{
		ID: d.ID, SiteID: d.SiteID, Hostname: d.Hostname, Status: d.Status, VerificationToken: d.VerificationToken,
		CreatedAt: d.CreatedAt, LastCheckedAt: d.LastCheckedAt, VerifiedAt: d.VerifiedAt, FailingSince: d.FailingSince,
		LastFailureReason: d.LastFailureReason, FailureDetail: d.FailureDetail,
		CreatedBy: d.CreatedBy, CreatedByToken: d.CreatedByToken,
	})
	out.Primary = isPrimary == 1
	return out
}
