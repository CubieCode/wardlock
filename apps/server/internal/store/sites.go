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
	// PinnedAt is when the actor reading it pinned the site (V5.3, read
	// only; zero while it is not pinned, and on instance-wide reads). Pins
	// are personal (T7.1).
	PinnedAt time.Time
	// OwnerKind and OwnerName describe the account in OwnerID (T7.1, read
	// only): the owner's address, or the team's name.
	OwnerKind AccountKind
	OwnerName string
	// The owner's choice of primary hostname (C1.18, read only; see
	// Sites.SetPrimaryBase and SetPrimaryDomain): PrimaryBase for the
	// base-domain hostname, or PrimaryDomain, the hostname of one of the
	// site's custom domains whatever its status, with PrimaryDomainActive
	// while it is active. A chosen domain is in effect only while active;
	// removing it clears the choice.
	PrimaryBase         bool
	PrimaryDomain       string
	PrimaryDomainActive bool
}

// OperatorSite is a site as `wardlockd admin list-sites` shows it.
type OperatorSite struct {
	Site
	OwnerEmail       string
	OwnerSuspendedAt time.Time
}

// Sites stores sites. Every method that takes an Actor matches only sites
// the actor can see: its own, and its teams'. Any other site is ErrNotFound,
// and one it can see with too low a role is ErrForbiddenRole (T7.1).
type Sites interface {
	// Create inserts s under the account s.OwnerID, which the actor must be
	// able to edit; ActiveDeployID, ActivatedAt, GroupID and PinnedAt are
	// ignored (see Deploys.Activate and Organisation). A name held by any
	// site is ErrConflict. An account that already has as many sites as
	// quotas.Sites, after its own override (A2.14), gets a *QuotaError
	// (ErrQuotaExceeded); concurrent creates by one account are serialised
	// so they cannot both take the last place.
	Create(ctx context.Context, actor Actor, s Site, quotas Quotas) error
	Get(ctx context.Context, actor Actor, id string) (Site, error)
	// GetByOwnerName finds a site the actor can see by name.
	GetByOwnerName(ctx context.Context, actor Actor, name string) (Site, error)
	// GetByOwnerDomain finds a site the actor can see by its custom domain
	// hostname, whatever the domain's status.
	GetByOwnerDomain(ctx context.Context, actor Actor, hostname string) (Site, error)
	// GetByName finds any site by name. It is the instance-wide routing
	// lookup for the edge and must never back an owner API handler.
	GetByName(ctx context.Context, name string) (Site, error)
	// GetByID finds any site by ID. Like GetByName it is an instance-wide
	// routing lookup for the edge (an active custom domain names its site
	// by ID) and must never back an owner API handler.
	GetByID(ctx context.Context, id string) (Site, error)
	// List returns up to limit of the sites the actor can see, or only
	// account's when it is not empty, ordered by name, starting after
	// afterName ("" for the first page).
	List(ctx context.Context, actor Actor, account, afterName string, limit int) ([]Site, error)
	// TagIDs returns the tag IDs of each site in siteIDs the actor can see,
	// in tag-name order. A site with no tags, a site the actor cannot see
	// and an unknown ID have no entry.
	TagIDs(ctx context.Context, actor Actor, siteIDs ...string) (map[string][]string, error)
	// Summaries returns every one of accountID's sites, by name, with its
	// default policy, tags, custom domains, links and requests as w says
	// (V5.5). It costs the same few queries whatever the number of sites.
	Summaries(ctx context.Context, actor Actor, accountID string, w SummaryWindow) ([]SiteSummary, error)
	// ExpiringLinks returns up to limit of accountID's links that
	// Summaries counts as ExpiringLinks under w, soonest expiry first.
	ExpiringLinks(ctx context.Context, actor Actor, accountID string, w SummaryWindow, limit int) ([]ExpiringLink, error)
	// SetActiveDeploy points the site at one of its own deploys. A
	// taken-down site is ErrNotFound.
	SetActiveDeploy(ctx context.Context, actor Actor, siteID, deployID string) error
	// SetPrimaryBase makes the site's base-domain hostname its primary
	// (C1.18), and SetPrimaryDomain the custom domain domainID, which must
	// be one of the site's and active; ClearPrimary leaves it with none.
	// Each replaces any earlier choice. A taken-down site, another owner's
	// site and, for SetPrimaryDomain, a domain that is not the site's or is
	// not active are ErrNotFound and change nothing.
	SetPrimaryBase(ctx context.Context, actor Actor, siteID string) error
	SetPrimaryDomain(ctx context.Context, actor Actor, siteID, domainID string) error
	ClearPrimary(ctx context.Context, actor Actor, siteID string) error
	// Delete removes the site with its domains, deploys, policies, share
	// links and site sessions, freeing the name and the storage only its
	// deploys held. A taken-down site is ErrNotFound: only the operator can
	// tear it down.
	Delete(ctx context.Context, actor Actor, id string) error
	// Transfer moves the site to the account toID, keeping its deploys,
	// links, domains and analytics (T7.1); see sitesRepo.Transfer.
	Transfer(ctx context.Context, actor Actor, siteID, toID string, quotas Quotas, at time.Time) error

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
	// owners, ordered by name, starting after afterName. A team's site
	// names the team in OwnerName.
	ListAll(ctx context.Context, afterName string, limit int) ([]OperatorSite, error)
	// UnavailableIDs returns the IDs of every site that is taken down or
	// owned by a suspended owner, in ID order.
	UnavailableIDs(ctx context.Context) ([]string, error)
}

// Sites returns the SQLite Sites repository.
func (db *DB) Sites() Sites { return sitesRepo{db} }

type sitesRepo struct{ db *DB }

func (r sitesRepo) Create(ctx context.Context, actor Actor, s Site, quotas Quotas) error {
	if err := actor.Require(s.OwnerID, RoleEditor); err != nil {
		return fmt.Errorf("create site: %w", err)
	}
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

// visible returns the row a lookup found, when the actor can see it, with
// the actor's own pin.
func (r sitesRepo) visible(ctx context.Context, actor Actor, s Site, err error) (Site, error) {
	if err != nil {
		return Site{}, err
	}
	if err := actor.Require(s.OwnerID, RoleViewer); err != nil {
		return Site{}, fmt.Errorf("site: %w", err)
	}
	pinned, err := r.db.q.GetSitePin(ctx, sqlcgen.GetSitePinParams{OwnerID: actor.OwnerID, SiteID: s.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return Site{}, mapErr("get site pin", err)
	}
	s.PinnedAt = fromMs(pinned)
	return s, nil
}

func (r sitesRepo) Get(ctx context.Context, actor Actor, id string) (Site, error) {
	s, err := r.GetByID(ctx, id)
	return r.visible(ctx, actor, s, err)
}

func (r sitesRepo) GetByOwnerName(ctx context.Context, actor Actor, name string) (Site, error) {
	s, err := r.GetByName(ctx, name)
	return r.visible(ctx, actor, s, err)
}

func (r sitesRepo) GetByOwnerDomain(ctx context.Context, actor Actor, hostname string) (Site, error) {
	row, err := r.db.q.GetSiteByDomain(ctx, hostname)
	s, err := siteFrom("get site by domain", siteRow(row), err)
	return r.visible(ctx, actor, s, err)
}

func (r sitesRepo) GetByName(ctx context.Context, name string) (Site, error) {
	row, err := r.db.q.GetSiteByName(ctx, name)
	return siteFrom("get site by name", siteRow(row), err)
}

func (r sitesRepo) GetByID(ctx context.Context, id string) (Site, error) {
	row, err := r.db.q.GetSiteByID(ctx, id)
	return siteFrom("get site by id", siteRow(row), err)
}

func (r sitesRepo) List(ctx context.Context, actor Actor, account, afterName string, limit int) ([]Site, error) {
	params := sqlcgen.ListSitesParams{AccountID: actor.OwnerID, AfterName: afterName, RowLimit: int64(limit)}
	if !actor.IsTeamToken() {
		params.MemberID = actor.OwnerID
	}
	if account != "" {
		if err := actor.Require(account, RoleViewer); err != nil {
			return nil, fmt.Errorf("list sites: %w", err)
		}
		params.AccountID, params.MemberID = account, ""
	}
	rows, err := r.db.q.ListSites(ctx, params)
	if err != nil {
		return nil, mapErr("list sites", err)
	}
	pins, err := r.db.pins(ctx, actor)
	if err != nil {
		return nil, err
	}
	sites := make([]Site, 0, len(rows))
	for _, row := range rows {
		s, _ := siteFrom("", siteRow(row), nil)
		s.PinnedAt = pins[s.ID]
		sites = append(sites, s)
	}
	return sites, nil
}

// pins returns when the actor pinned each site they have pinned.
func (db *DB) pins(ctx context.Context, actor Actor) (map[string]time.Time, error) {
	rows, err := db.q.ListOwnerPins(ctx, actor.OwnerID)
	if err != nil {
		return nil, mapErr("list pins", err)
	}
	pins := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		pins[row.SiteID] = fromMs(row.PinnedAt)
	}
	return pins, nil
}

func (r sitesRepo) TagIDs(ctx context.Context, actor Actor, siteIDs ...string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, id := range siteIDs {
		account, err := r.db.authorizeSite(ctx, actor, id, RoleViewer)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ids, err := r.db.q.ListSiteTagIDs(ctx, sqlcgen.ListSiteTagIDsParams{SiteID: id, OwnerID: account})
		if err != nil {
			return nil, mapErr("list site tags", err)
		}
		if len(ids) > 0 {
			out[id] = ids
		}
	}
	return out, nil
}

func (r sitesRepo) SetActiveDeploy(ctx context.Context, actor Actor, siteID, deployID string) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return err
	}
	n, err := r.db.q.SetActiveDeploy(ctx, sqlcgen.SetActiveDeployParams{
		DeployID: nullString(deployID),
		SiteID:   siteID,
		OwnerID:  ownerID,
	})
	return rowsAffected("set active deploy", n, err)
}

func (r sitesRepo) SetPrimaryBase(ctx context.Context, actor Actor, siteID string) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return err
	}
	n, err := r.db.q.SetPrimaryBase(ctx, sqlcgen.SetPrimaryBaseParams{ID: siteID, OwnerID: ownerID})
	return rowsAffected("set primary hostname", n, err)
}

func (r sitesRepo) SetPrimaryDomain(ctx context.Context, actor Actor, siteID, domainID string) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return err
	}
	n, err := r.db.q.SetPrimaryDomain(ctx, sqlcgen.SetPrimaryDomainParams{DomainID: nullString(domainID), ID: siteID, OwnerID: ownerID})
	return rowsAffected("set primary domain", n, err)
}

func (r sitesRepo) ClearPrimary(ctx context.Context, actor Actor, siteID string) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return err
	}
	n, err := r.db.q.ClearPrimary(ctx, sqlcgen.ClearPrimaryParams{ID: siteID, OwnerID: ownerID})
	return rowsAffected("clear primary hostname", n, err)
}

func (r sitesRepo) Delete(ctx context.Context, actor Actor, id string) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, id, RoleEditor)
	if err != nil {
		return err
	}
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
		s, _ := siteFrom("", siteRow(sqlcgen.GetSiteByIDRow{
			ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, ActiveDeployID: row.ActiveDeployID,
			CreatedAt: row.CreatedAt, ActivatedAt: row.ActivatedAt, TakenDownAt: row.TakenDownAt,
			GroupID: row.GroupID, OwnerKind: row.OwnerKind, OwnerEmail: row.OwnerEmail, TeamName: row.TeamName,
			PrimaryBase: row.PrimaryBase, PrimaryDomain: row.PrimaryDomain, PrimaryDomainStatus: row.PrimaryDomainStatus,
		}), nil)
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

// siteColumns is the one row shape every site query shares.
type siteColumns = sqlcgen.GetSiteByIDRow

// siteRow reads any site query's row: sqlc gives each its own struct type.
func siteRow[R sqlcgen.GetSiteByIDRow | sqlcgen.GetSiteByNameRow | sqlcgen.GetSiteByDomainRow | sqlcgen.ListSitesRow](row R) siteColumns {
	return siteColumns(row)
}

func siteFrom(what string, row siteColumns, err error) (Site, error) {
	if err != nil {
		return Site{}, mapErr(what, err)
	}
	s := Site{
		ID:             row.ID,
		OwnerID:        row.OwnerID,
		Name:           row.Name,
		ActiveDeployID: row.ActiveDeployID.String,
		CreatedAt:      fromMs(row.CreatedAt),
		ActivatedAt:    fromNullMs(row.ActivatedAt),
		TakenDownAt:    fromNullMs(row.TakenDownAt),
		GroupID:        row.GroupID.String,
		OwnerKind:      AccountKind(row.OwnerKind),
		OwnerName:      row.OwnerEmail,
		PrimaryBase:    row.PrimaryBase == 1,
		PrimaryDomain:  row.PrimaryDomain.String,
		// The column is NULL only without a chosen domain.
		PrimaryDomainActive: row.PrimaryDomainStatus.String == string(DomainActive),
	}
	if row.TeamName.Valid {
		s.OwnerName = row.TeamName.String
	}
	return s, nil
}

// requireSite returns ErrNotFound unless siteID is one of accountID's sites.
// Actor.Require chose the account; this re-reads it inside the transaction.
// Child rows (domains, deploys, policies, links) are created only after it.
func requireSite(ctx context.Context, q *sqlcgen.Queries, ownerID, siteID string) error {
	_, err := q.SiteOwnedBy(ctx, sqlcgen.SiteOwnedByParams{ID: siteID, OwnerID: ownerID})
	return mapErr("find site", err)
}
