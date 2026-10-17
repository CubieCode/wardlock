// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
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
}

// Sites stores sites. Every method that takes ownerID matches only that
// owner's sites; another owner's site is ErrNotFound.
type Sites interface {
	// Create inserts s; ActiveDeployID and ActivatedAt are ignored (see
	// Deploys.Activate). A name held by any site is ErrConflict.
	Create(ctx context.Context, s Site) error
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
	// SetActiveDeploy points the site at one of its own deploys.
	SetActiveDeploy(ctx context.Context, ownerID, siteID, deployID string) error
	// Delete removes the site with its domains, deploys, policies, share
	// links and site sessions, freeing the name.
	Delete(ctx context.Context, ownerID, id string) error
}

// Sites returns the SQLite Sites repository.
func (db *DB) Sites() Sites { return sitesRepo{db} }

type sitesRepo struct{ db *DB }

func (r sitesRepo) Create(ctx context.Context, s Site) error {
	return mapErr("create site", r.db.q.CreateSite(ctx, sqlcgen.CreateSiteParams{
		ID:        s.ID,
		OwnerID:   s.OwnerID,
		Name:      s.Name,
		CreatedAt: ms(s.CreatedAt),
	}))
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

func (r sitesRepo) SetActiveDeploy(ctx context.Context, ownerID, siteID, deployID string) error {
	n, err := r.db.q.SetActiveDeploy(ctx, sqlcgen.SetActiveDeployParams{
		DeployID: nullString(deployID),
		SiteID:   siteID,
		OwnerID:  ownerID,
	})
	return rowsAffected("set active deploy", n, err)
}

func (r sitesRepo) Delete(ctx context.Context, ownerID, id string) error {
	n, err := r.db.q.DeleteSite(ctx, sqlcgen.DeleteSiteParams{ID: id, OwnerID: ownerID})
	return rowsAffected("delete site", n, err)
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
	}, nil
}

// requireSite returns ErrNotFound unless siteID is one of ownerID's sites.
// Child rows (domains, deploys, policies, links) are created only after it.
func requireSite(ctx context.Context, q *sqlcgen.Queries, ownerID, siteID string) error {
	_, err := q.SiteOwnedBy(ctx, sqlcgen.SiteOwnedByParams{ID: siteID, OwnerID: ownerID})
	return mapErr("find site", err)
}
