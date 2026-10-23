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

// Site is a named place content is published to.
type Site struct {
	ID             string
	OwnerID        string
	Name           string
	ActiveDeployID string // empty until the first activation
	CreatedAt      time.Time
	// ActivatedAt is when the active deploy was last activated (read only;
	// zero without an active deploy).
	ActivatedAt time.Time
	// TakenDownAt is when the operator took the site down (A2.13, read
	// only; zero while it is not).
	TakenDownAt time.Time
	// GroupID is the owner's group the site is in (V5.3, read only; empty
	// when ungrouped). See Organisation.
	GroupID string
	// PinnedAt is when the owner pinned the site (V5.3, read only; zero
	// while it is not pinned).
	PinnedAt time.Time
}

// OperatorSite is a site as `wardlockd admin list-sites` shows it.
type OperatorSite struct {
	Site
	OwnerEmail       string
	OwnerSuspendedAt time.Time
}

// Sites stores sites. Every method that takes ownerID matches only that
// owner's sites; another owner's site is ErrNotFound.
type Sites interface {
	// Create inserts s; ActiveDeployID, ActivatedAt, GroupID and PinnedAt
	// are ignored (see Deploys.Activate and Organisation). A name held by any site is ErrConflict. An owner
	// who already has as many sites as quotas.Sites, after their own
	// override (A2.14), gets a *QuotaError (ErrQuotaExceeded); concurrent
	// creates by one owner are serialised so they cannot both take the
	// last place.
	Create(ctx context.Context, s Site, quotas Quotas) error
	Get(ctx context.Context, ownerID, id string) (Site, error)
	// GetByOwnerName finds one of ownerID's sites by name.
	GetByOwnerName(ctx context.Context, ownerID, name string) (Site, error)
	// GetByOwnerDomain finds ownerID's site with the custom domain hostname,
	// whatever the domain's status.
	GetByOwnerDomain(ctx context.Context, ownerID, hostname string) (Site, error)
	// GetByName finds any site by name. It is the instance-wide routing
	// lookup for the edge and must never back an owner API handler.
	GetByName(ctx context.Context, name string) (Site, error)
	// GetByID finds any site by ID. Like GetByName it is an instance-wide
	// routing lookup for the edge (an active custom domain names its site
	// by ID) and must never back an owner API handler.
	GetByID(ctx context.Context, id string) (Site, error)
	// List returns up to limit of ownerID's sites ordered by name, starting
	// after afterName ("" for the first page).
	List(ctx context.Context, ownerID, afterName string, limit int) ([]Site, error)
	// TagIDs returns the tag IDs of each of ownerID's sites in siteIDs,
	// in tag-name order. A site with no tags, another owner's site and an
	// unknown ID have no entry.
	TagIDs(ctx context.Context, ownerID string, siteIDs ...string) (map[string][]string, error)
	// Summaries returns every one of ownerID's sites, by name, with its
	// default policy, tags, custom domains, links and requests as w says
	// (V5.5). It costs the same few queries whatever the number of sites.
	Summaries(ctx context.Context, ownerID string, w SummaryWindow) ([]SiteSummary, error)
	// SetActiveDeploy points the site at one of its own deploys. A
	// taken-down site is ErrNotFound.
	SetActiveDeploy(ctx context.Context, ownerID, siteID, deployID string) error
	// Delete removes the site with its domains, deploys, policies, share
	// links and site sessions, freeing the name and the storage only its
	// deploys held. A taken-down site is ErrNotFound: only the operator can
	// tear it down.
	Delete(ctx context.Context, ownerID, id string) error

	// Operator controls (A2.13), for `wardlockd admin` and the edge's
	// cache only: instance-wide, never an owner API handler.

	// TakeDown marks the site taken down at at; an already taken-down
	// site keeps the first time. An unknown site is ErrNotFound.
	TakeDown(ctx context.Context, id string, at time.Time) error
	// Restore clears the takedown. An unknown site is ErrNotFound.
	Restore(ctx context.Context, id string) error
	// TearDown deletes a taken-down site as Delete does. A site that is not
	// taken down (or was restored meanwhile) is ErrNotFound.
	TearDown(ctx context.Context, id string) error
	// ListAll returns up to limit sites on the instance with their
	// owners, ordered by name, starting after afterName.
	ListAll(ctx context.Context, afterName string, limit int) ([]OperatorSite, error)
	// UnavailableIDs returns the IDs of every site that is taken down or
	// owned by a suspended owner, in ID order.
	UnavailableIDs(ctx context.Context) ([]string, error)
}

// Sites returns the SQLite Sites repository.
func (db *DB) Sites() Sites { return sitesRepo{db} }

type sitesRepo struct{ db *DB }

func (r sitesRepo) Create(ctx context.Context, s Site, quotas Quotas) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		_, quotas, err := lockOwnerQuotas(ctx, q, s.OwnerID, quotas)
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("create site: %w", ErrConstraint) // no such owner
		}
		if err != nil {
			return err
		}
		if quotas.Sites > 0 {
			n, err := q.CountOwnerSites(ctx, s.OwnerID)
			if err != nil {
				return mapErr("count sites", err)
			}
			if n >= quotas.Sites {
				return &QuotaError{Quota: QuotaSites, Limit: quotas.Sites, Used: n, Adding: 1}
			}
		}
		return mapErr("create site", q.CreateSite(ctx, sqlcgen.CreateSiteParams{
			ID:        s.ID,
			OwnerID:   s.OwnerID,
			Name:      s.Name,
			CreatedAt: ms(s.CreatedAt),
		}))
	})
}

func (r sitesRepo) Get(ctx context.Context, ownerID, id string) (Site, error) {
	row, err := r.db.q.GetSite(ctx, sqlcgen.GetSiteParams{ID: id, OwnerID: ownerID})
	return siteFrom("get site", row, err)
}

func (r sitesRepo) GetByOwnerName(ctx context.Context, ownerID, name string) (Site, error) {
	row, err := r.db.q.GetSiteByOwnerName(ctx, sqlcgen.GetSiteByOwnerNameParams{Name: name, OwnerID: ownerID})
	return siteFrom("get site by name", sqlcgen.GetSiteRow(row), err)
}

func (r sitesRepo) GetByOwnerDomain(ctx context.Context, ownerID, hostname string) (Site, error) {
	row, err := r.db.q.GetSiteByOwnerDomain(ctx, sqlcgen.GetSiteByOwnerDomainParams{Hostname: hostname, OwnerID: ownerID})
	return siteFrom("get site by domain", sqlcgen.GetSiteRow(row), err)
}

func (r sitesRepo) GetByName(ctx context.Context, name string) (Site, error) {
	row, err := r.db.q.GetSiteByName(ctx, name)
	return siteFrom("get site by name", sqlcgen.GetSiteRow(row), err)
}

func (r sitesRepo) GetByID(ctx context.Context, id string) (Site, error) {
	row, err := r.db.q.GetSiteByID(ctx, id)
	return siteFrom("get site by id", sqlcgen.GetSiteRow(row), err)
}

func (r sitesRepo) List(ctx context.Context, ownerID, afterName string, limit int) ([]Site, error) {
	rows, err := r.db.q.ListSites(ctx, sqlcgen.ListSitesParams{OwnerID: ownerID, AfterName: afterName, RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list sites", err)
	}
	sites := make([]Site, 0, len(rows))
	for _, row := range rows {
		s, _ := siteFrom("", sqlcgen.GetSiteRow(row), nil)
		sites = append(sites, s)
	}
	return sites, nil
}

func (r sitesRepo) TagIDs(ctx context.Context, ownerID string, siteIDs ...string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, id := range siteIDs {
		ids, err := r.db.q.ListSiteTagIDs(ctx, sqlcgen.ListSiteTagIDsParams{SiteID: id, OwnerID: ownerID})
		if err != nil {
			return nil, mapErr("list site tags", err)
		}
		if len(ids) > 0 {
			out[id] = ids
		}
	}
	return out, nil
}

func (r sitesRepo) SetActiveDeploy(ctx context.Context, ownerID, siteID, deployID string) error {
	n, err := r.db.q.SetActiveDeploy(ctx, sqlcgen.SetActiveDeployParams{
		DeployID: nullString(deployID),
		SiteID:   siteID,
		OwnerID:  ownerID,
	})
	return rowsAffected("set active deploy", n, err)
}

func (r sitesRepo) Delete(ctx context.Context, ownerID, id string) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, id); err != nil {
			return err
		}
		if err := releaseSite(ctx, q, ownerID, id); err != nil {
			return err
		}
		// No row (a taken-down site) rolls the release back.
		n, err := q.DeleteSite(ctx, sqlcgen.DeleteSiteParams{ID: id, OwnerID: ownerID})
		return rowsAffected("delete site", n, err)
	})
}

func (r sitesRepo) TakeDown(ctx context.Context, id string, at time.Time) error {
	n, err := r.db.q.TakeDownSite(ctx, sqlcgen.TakeDownSiteParams{ID: id, TakenDownAt: nullMs(at)})
	return rowsAffected("take down site", n, err)
}

func (r sitesRepo) Restore(ctx context.Context, id string) error {
	n, err := r.db.q.RestoreSite(ctx, id)
	return rowsAffected("restore site", n, err)
}

func (r sitesRepo) TearDown(ctx context.Context, id string) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		site, err := q.GetSiteByID(ctx, id)
		if err != nil {
			return mapErr("tear down site", err)
		}
		if err := releaseSite(ctx, q, site.OwnerID, id); err != nil {
			return err
		}
		// No row (restored meanwhile) rolls the release back.
		n, err := q.TearDownSite(ctx, id)
		return rowsAffected("tear down site", n, err)
	})
}

func (r sitesRepo) ListAll(ctx context.Context, afterName string, limit int) ([]OperatorSite, error) {
	rows, err := r.db.q.ListAllSites(ctx, sqlcgen.ListAllSitesParams{AfterName: afterName, RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list all sites", err)
	}
	out := make([]OperatorSite, len(rows))
	for i, row := range rows {
		s, _ := siteFrom("", sqlcgen.GetSiteRow{
			ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, ActiveDeployID: row.ActiveDeployID,
			CreatedAt: row.CreatedAt, ActivatedAt: row.ActivatedAt, TakenDownAt: row.TakenDownAt,
			GroupID: row.GroupID, PinnedAt: row.PinnedAt,
		}, nil)
		out[i] = OperatorSite{Site: s, OwnerEmail: row.OwnerEmail, OwnerSuspendedAt: fromNullMs(row.OwnerSuspendedAt)}
	}
	return out, nil
}

func (r sitesRepo) UnavailableIDs(ctx context.Context) ([]string, error) {
	ids, err := r.db.q.ListUnavailableSiteIDs(ctx)
	if err != nil {
		return nil, mapErr("list unavailable sites", err)
	}
	return ids, nil
}

func siteFrom(what string, row sqlcgen.GetSiteRow, err error) (Site, error) {
	if err != nil {
		return Site{}, mapErr(what, err)
	}
	return Site{
		ID:             row.ID,
		OwnerID:        row.OwnerID,
		Name:           row.Name,
		ActiveDeployID: row.ActiveDeployID.String,
		CreatedAt:      fromMs(row.CreatedAt),
		ActivatedAt:    fromNullMs(row.ActivatedAt),
		TakenDownAt:    fromNullMs(row.TakenDownAt),
		GroupID:        row.GroupID.String,
		PinnedAt:       fromNullMs(row.PinnedAt),
	}, nil
}

// requireSite returns ErrNotFound unless siteID is one of ownerID's sites.
// Child rows (domains, deploys, policies, links) are created only after it.
func requireSite(ctx context.Context, q *sqlcgen.Queries, ownerID, siteID string) error {
	_, err := q.SiteOwnedBy(ctx, sqlcgen.SiteOwnedByParams{ID: siteID, OwnerID: ownerID})
	return mapErr("find site", err)
}
