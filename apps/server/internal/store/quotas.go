// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Quotas are per-owner limits (A2.14): bytes stored, counting each distinct
// blob once however many deploys and paths list it; sites; and deploys
// created in any 24 hours. 0 is unlimited.
type Quotas struct {
	StorageBytes  int64
	Sites         int64
	DeploysPerDay int64
}

// QuotaOverrides are one owner's own limits, set by the operator. A nil
// field follows the instance's Quotas; 0 is unlimited.
type QuotaOverrides struct {
	StorageBytes  *int64
	Sites         *int64
	DeploysPerDay *int64
}

// Apply returns q with o's overrides in place of the instance values.
func (q Quotas) Apply(o QuotaOverrides) Quotas {
	for _, f := range []struct {
		dst *int64
		v   *int64
	}{{&q.StorageBytes, o.StorageBytes}, {&q.Sites, o.Sites}, {&q.DeploysPerDay, o.DeploysPerDay}} {
		if f.v != nil {
			*f.dst = *f.v
		}
	}
	return q
}

// OwnerUsage is what counts against an owner's quotas.
type OwnerUsage struct {
	StoredBytes  int64
	Sites        int64
	DeploysToday int64 // in the 24 hours before the time asked about
}

// Quota names one of the limits in Quotas.
type Quota string

const (
	QuotaStorage       Quota = "storage"
	QuotaSites         Quota = "sites"
	QuotaDeploysPerDay Quota = "deploys_per_day"
)

// ErrQuotaExceeded means a write would take the owner past one of their
// quotas; the error is a *QuotaError saying which. Nothing was written.
var ErrQuotaExceeded = errors.New("quota exceeded")

// QuotaError says which quota a refused write would exceed. Used is what
// the owner had before the write; Adding is what it would have added.
type QuotaError struct {
	Quota  Quota
	Limit  int64
	Used   int64
	Adding int64
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("%s quota exceeded: %d used, adding %d, limit %d", e.Quota, e.Used, e.Adding, e.Limit)
}

func (e *QuotaError) Unwrap() error { return ErrQuotaExceeded }

// DeployWindow is the period the daily deploy quota counts over.
const DeployWindow = 24 * time.Hour

func overridesFrom(storage, sites, deploys sql.NullInt64) QuotaOverrides {
	ptr := func(v sql.NullInt64) *int64 {
		if !v.Valid {
			return nil
		}
		return &v.Int64
	}
	return QuotaOverrides{StorageBytes: ptr(storage), Sites: ptr(sites), DeploysPerDay: ptr(deploys)}
}

func nullOverride(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

// lockOwnerQuotas locks the owner's row for the transaction (see the
// LockOwnerQuotas query) and returns their stored bytes and quotas.
func lockOwnerQuotas(ctx context.Context, q *sqlcgen.Queries, ownerID string, defaults Quotas) (int64, Quotas, error) {
	row, err := q.LockOwnerQuotas(ctx, ownerID)
	if err != nil {
		return 0, Quotas{}, mapErr("lock owner quotas", err)
	}
	return row.StoredBytes, defaults.Apply(overridesFrom(row.MaxStorageBytes, row.MaxSites, row.MaxDeploysPerDay)), nil
}

// releaseDeploys hands back the storage the owner's deploys hold, before
// they are deleted in the same transaction: each distinct hash counts one
// deploy fewer, and hashes no deploy lists any more leave stored_bytes.
func releaseDeploys(ctx context.Context, q *sqlcgen.Queries, ownerID string, deployIDs ...string) error {
	for _, id := range deployIDs {
		err := q.ReleaseDeployBlobRefs(ctx, sqlcgen.ReleaseDeployBlobRefsParams{OwnerID: ownerID, DeployID: id})
		if err != nil {
			return mapErr("release deploy blobs", err)
		}
	}
	if err := q.SubtractReleasedBytes(ctx, ownerID); err != nil {
		return mapErr("subtract released bytes", err)
	}
	return mapErr("delete released blob refs", q.DeleteReleasedBlobRefs(ctx, ownerID))
}

// releaseSite releases every deploy of the owner's site, before the site is
// deleted in the same transaction.
func releaseSite(ctx context.Context, q *sqlcgen.Queries, ownerID, siteID string) error {
	ids, err := q.SiteDeployIDs(ctx, siteID)
	if err != nil {
		return mapErr("list site deploys", err)
	}
	return releaseDeploys(ctx, q, ownerID, ids...)
}

// errNothingDeleted rolls back a release whose delete matched no row.
var errNothingDeleted = errors.New("nothing deleted")
