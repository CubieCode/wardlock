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

// ErrSiteTakenDown means the operator has taken the site down, so it cannot
// move: its files and records are evidence where they are.
var ErrSiteTakenDown = errors.New("site is taken down")

// Transfer moves siteID to the account toID (T7.1). The actor needs ownership
// or admin of the source and, of the target, to be it or an admin of it: a
// site never lands in another person's account. Deploys, links, domains,
// policies and analytics stay with the site; its group and tags belong to the
// source, so they go; each person's pin stays theirs. The target's site and
// storage quotas apply, and its share of the site's files moves with it: the
// source hands back the bytes only this site held and the target counts them.
// The target can then read and activate what the source had uploaded, and
// nothing else. A taken-down site is ErrSiteTakenDown. Moving a site to its
// own account changes nothing.
func (r sitesRepo) Transfer(ctx context.Context, actor Actor, siteID, toID string, quotas Quotas, at time.Time) error {
	from, err := r.db.authorizeSite(ctx, actor, siteID, RoleViewer)
	if err != nil {
		return err
	}
	if err := actor.Require(from, RoleAdmin); err != nil {
		return fmt.Errorf("transfer site: %w", err)
	}
	if err := checkTransferTarget(actor, toID); err != nil {
		return err
	}
	if from == toID {
		return nil
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		site, err := q.GetSiteByID(ctx, siteID)
		if err != nil {
			return mapErr("get site", err)
		}
		if site.OwnerID != from {
			return fmt.Errorf("transfer site: %w", ErrNotFound)
		}
		if site.TakenDownAt.Valid {
			return fmt.Errorf("transfer site: %w", ErrSiteTakenDown)
		}
		stored, limits, err := lockAccounts(ctx, q, from, toID, quotas)
		if err != nil {
			return err
		}
		if limits.Sites > 0 {
			n, err := q.CountOwnerSites(ctx, toID)
			if err != nil {
				return mapErr("count sites", err)
			}
			if n >= limits.Sites {
				return &QuotaError{Quota: QuotaSites, Limit: limits.Sites, Used: n, Adding: 1}
			}
		}
		if err := moveStorage(ctx, q, siteID, from, toID, stored, limits.StorageBytes); err != nil {
			return err
		}
		if err := q.CopySiteBlobHoldings(ctx, sqlcgen.CopySiteBlobHoldingsParams{
			ToOwner: toID, FromOwner: from, SiteID: siteID, CreatedAt: ms(at),
		}); err != nil {
			return mapErr("copy blob holdings", err)
		}
		if err := q.ClearSiteTags(ctx, siteID); err != nil {
			return mapErr("clear site tags", err)
		}
		n, err := q.SetSiteOwner(ctx, sqlcgen.SetSiteOwnerParams{ToOwner: toID, ID: siteID, FromOwner: from})
		return rowsAffected("move site", n, err)
	})
}

// checkTransferTarget is the rule for where a site may go: to the actor's own
// account, or to a team they administer, whether the site comes from their
// own account or from another team they administer.
func checkTransferTarget(actor Actor, toID string) error {
	if toID == actor.OwnerID && !actor.IsTeamToken() {
		return nil
	}
	if err := actor.Require(toID, RoleAdmin); err != nil {
		return fmt.Errorf("transfer site: %w", err)
	}
	return nil
}

// lockAccounts locks both accounts' rows in ID order, so opposite transfers
// cannot deadlock, and returns the target's stored bytes and quotas.
func lockAccounts(ctx context.Context, q *sqlcgen.Queries, from, to string, defaults Quotas) (int64, Quotas, error) {
	first := from
	if to < from {
		first = to
	}
	if _, err := q.LockOwnerQuotas(ctx, first); err != nil {
		return 0, Quotas{}, mapErr("lock account", err)
	}
	second := to
	if first == to {
		second = from
	}
	if _, err := q.LockOwnerQuotas(ctx, second); err != nil {
		return 0, Quotas{}, mapErr("lock account", err)
	}
	return lockOwnerQuotas(ctx, q, to, defaults)
}

// moveStorage counts each of the site's deploys against to and hands it back
// from from, all or nothing: going past to's limit is a *QuotaError and the
// transaction rolls back.
func moveStorage(ctx context.Context, q *sqlcgen.Queries, siteID, from, to string, stored, limit int64) error {
	ids, err := q.SiteDeployIDs(ctx, siteID)
	if err != nil {
		return mapErr("list site deploys", err)
	}
	for _, id := range ids {
		rows, err := q.ListDeployFiles(ctx, id)
		if err != nil {
			return mapErr("list deploy files", err)
		}
		files := make([]DeployFile, len(rows))
		for i, row := range rows {
			files[i] = DeployFile{Path: row.Path, SHA256: row.Sha256, Size: row.Size}
		}
		if err := addStorage(ctx, q, to, files, stored, limit); err != nil {
			return err
		}
		row, err := q.GetOwnerQuotas(ctx, to)
		if err != nil {
			return mapErr("get stored bytes", err)
		}
		stored = row.StoredBytes
	}
	return releaseDeploys(ctx, q, from, ids...)
}
