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
	SPAFallback    bool
	ActiveDeployID string // empty until the first activation
	CreatedAt      time.Time
}

// Sites stores sites. Every method that takes ownerID matches only that
// owner's sites; another owner's site is ErrNotFound.
type Sites interface {
	// Create inserts s; ActiveDeployID is ignored (see SetActiveDeploy).
	Create(ctx context.Context, s Site) error
	Get(ctx context.Context, ownerID, id string) (Site, error)
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
		ID:          s.ID,
		OwnerID:     s.OwnerID,
		Name:        s.Name,
		SpaFallback: boolInt(s.SPAFallback),
		CreatedAt:   ms(s.CreatedAt),
	}))
}

func (r sitesRepo) Get(ctx context.Context, ownerID, id string) (Site, error) {
	row, err := r.db.q.GetSite(ctx, sqlcgen.GetSiteParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return Site{}, mapErr("get site", err)
	}
	return Site{
		ID:             row.ID,
		OwnerID:        row.OwnerID,
		Name:           row.Name,
		SPAFallback:    row.SpaFallback == 1,
		ActiveDeployID: row.ActiveDeployID.String,
		CreatedAt:      fromMs(row.CreatedAt),
	}, nil
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

// requireSite returns ErrNotFound unless siteID is one of ownerID's sites.
// Child rows (domains, deploys, policies, links) are created only after it.
func requireSite(ctx context.Context, q *sqlcgen.Queries, ownerID, siteID string) error {
	_, err := q.SiteOwnedBy(ctx, sqlcgen.SiteOwnedByParams{ID: siteID, OwnerID: ownerID})
	return mapErr("find site", err)
}
