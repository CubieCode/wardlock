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

// Preview lifetimes (C1.19), in days after each deploy.
const (
	DefaultPreviewDays = 14
	MaxPreviewDays     = 90
)

// Preview is a named preview of a site (C1.19), served at
// <site>--<name>.<base domain> from its own deploy.
type Preview struct {
	SiteID   string
	Name     string
	DeployID string
	// Days is how long the preview lasts after each deploy: 1 to
	// MaxPreviewDays, or 0 for never.
	Days int
	// CreatedAt is when the preview was first deployed (or deployed again
	// after it had expired); DeployedAt when its deploy went live as it.
	CreatedAt  time.Time
	DeployedAt time.Time
	// ExpiresAt is when it stops being served; zero for never.
	ExpiresAt time.Time
}

// Previews stores sites' previews. Their deploys are made with
// Deploys.Create and go live as the preview with Deploys.Activate. Methods
// that take an Actor match only sites the actor can see; another site's preview
// is ErrNotFound, and so is one that has expired at now.
type Previews interface {
	// Get returns one of the site's previews by name.
	Get(ctx context.Context, actor Actor, siteID, name string, now time.Time) (Preview, error)
	// List returns up to limit of the site's previews ordered by name,
	// after afterName ("" for the first page).
	List(ctx context.Context, actor Actor, siteID, afterName string, limit int, now time.Time) ([]Preview, error)
	// Delete removes one of the site's previews; its deploy is left for
	// garbage collection. A taken-down site's preview is ErrNotFound.
	Delete(ctx context.Context, actor Actor, siteID, name string, now time.Time) error
	// Promote makes the preview's deploy the site's active deploy in one
	// transaction, stamping it with at as Deploys.Activate does; the
	// preview keeps serving it. A path deploy whose base is no longer the
	// site's active deploy is ErrStaleBase. changed is false when the
	// deploy was already active.
	Promote(ctx context.Context, actor Actor, siteID, name string, at time.Time) (d Deploy, changed bool, err error)
	// SiteNameExists reports whether any site on the instance holds name,
	// so a preview whose hostname label would be that site's is refused.
	// Site names are public through DNS, so the answer reveals nothing.
	SiteNameExists(ctx context.Context, name string) (bool, error)
	// Served returns the preview of siteID called name that has not
	// expired at now. It is the instance-wide routing lookup for the edge
	// and must never back an owner API handler.
	Served(ctx context.Context, siteID, name string, now time.Time) (Preview, error)
}

// Previews returns the SQLite Previews repository.
func (db *DB) Previews() Previews { return previewsRepo{db} }

type previewsRepo struct{ db *DB }

func previewFrom(row sqlcgen.Preview) Preview {
	return Preview{
		SiteID:     row.SiteID,
		Name:       row.Name,
		DeployID:   row.DeployID,
		Days:       int(row.LifetimeDays),
		CreatedAt:  fromMs(row.CreatedAt),
		DeployedAt: fromMs(row.DeployedAt),
		ExpiresAt:  fromNullMs(row.ExpiresAt),
	}
}

func (r previewsRepo) Get(ctx context.Context, actor Actor, siteID, name string, now time.Time) (Preview, error) {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleViewer)
	if err != nil {
		return Preview{}, err
	}
	return getPreview(ctx, r.db.q, ownerID, siteID, name, now)
}

func getPreview(ctx context.Context, q *sqlcgen.Queries, ownerID, siteID, name string, now time.Time) (Preview, error) {
	row, err := q.GetPreview(ctx, sqlcgen.GetPreviewParams{SiteID: siteID, Name: name, OwnerID: ownerID, Now: ms(now)})
	if err != nil {
		return Preview{}, mapErr("get preview", err)
	}
	return previewFrom(row), nil
}

func (r previewsRepo) List(ctx context.Context, actor Actor, siteID, afterName string, limit int, now time.Time) ([]Preview, error) {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleViewer)
	if errors.Is(err, ErrNotFound) {
		return []Preview{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.q.ListPreviews(ctx, sqlcgen.ListPreviewsParams{
		SiteID: siteID, OwnerID: ownerID, Now: ms(now), AfterName: afterName, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, mapErr("list previews", err)
	}
	out := make([]Preview, 0, len(rows))
	for _, row := range rows {
		out = append(out, previewFrom(row))
	}
	return out, nil
}

func (r previewsRepo) Delete(ctx context.Context, actor Actor, siteID, name string, now time.Time) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return err
	}
	n, err := r.db.q.DeletePreview(ctx, sqlcgen.DeletePreviewParams{SiteID: siteID, Name: name, Now: ms(now), OwnerID: ownerID})
	return rowsAffected("delete preview", n, err)
}

func (r previewsRepo) Promote(ctx context.Context, actor Actor, siteID, name string, at time.Time) (Deploy, bool, error) {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return Deploy{}, false, err
	}
	var d Deploy
	changed := false
	err = r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		p, err := getPreview(ctx, q, ownerID, siteID, name, at)
		if err != nil {
			return err
		}
		site, err := q.GetSiteByID(ctx, siteID)
		if err != nil {
			return mapErr("get site", err)
		}
		if d, err = getDeploy(ctx, q, ownerID, siteID, p.DeployID); err != nil {
			return err
		}
		if d.ID == site.ActiveDeployID.String {
			return nil
		}
		// Promoting a path preview made on an older live deploy would drop
		// what went live since.
		if d.PathPrefix != "" && d.BaseDeployID != site.ActiveDeployID.String {
			return fmt.Errorf("promote preview: %w", ErrStaleBase)
		}
		if err := setActive(ctx, q, ownerID, &d, at); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return Deploy{}, false, err
	}
	return d, changed, nil
}

func (r previewsRepo) SiteNameExists(ctx context.Context, name string) (bool, error) {
	n, err := r.db.q.SiteNameExists(ctx, name)
	if err != nil {
		return false, mapErr("find site name", err)
	}
	return n > 0, nil
}

func (r previewsRepo) Served(ctx context.Context, siteID, name string, now time.Time) (Preview, error) {
	row, err := r.db.q.GetServedPreview(ctx, sqlcgen.GetServedPreviewParams{SiteID: siteID, Name: name, Now: ms(now)})
	if err != nil {
		return Preview{}, mapErr("get served preview", err)
	}
	return previewFrom(row), nil
}

func (r gcRepo) DeleteExpiredPreviews(ctx context.Context, now time.Time) (int64, error) {
	n, err := r.db.q.DeleteExpiredPreviews(ctx, ms(now))
	if err != nil {
		return 0, mapErr("delete expired previews", err)
	}
	return n, nil
}

// setPreview makes d, a preview deploy, its preview's deploy as of at: the
// preview's lifetime is d's, or the preview's own while it has not expired,
// or DefaultPreviewDays, counted from at.
func setPreview(ctx context.Context, q *sqlcgen.Queries, d *Deploy, at time.Time) error {
	days := DefaultPreviewDays
	if d.PreviewDays != nil {
		days = *d.PreviewDays
	} else {
		kept, err := q.PreviewLifetime(ctx, sqlcgen.PreviewLifetimeParams{SiteID: d.SiteID, Name: d.Preview, Now: ms(at)})
		switch {
		case err == nil:
			days = int(kept)
		case !errors.Is(err, sql.ErrNoRows):
			return mapErr("get preview lifetime", err)
		}
	}
	var expires sql.NullInt64
	if days > 0 {
		expires = nullMs(at.Add(time.Duration(days) * 24 * time.Hour))
	}
	err := q.MarkDeployPreviewActivated(ctx, sqlcgen.MarkDeployPreviewActivatedParams{PreviewActivatedAt: nullMs(at), ID: d.ID})
	if err != nil {
		return mapErr("mark deploy preview activated", err)
	}
	err = q.UpsertPreview(ctx, sqlcgen.UpsertPreviewParams{
		SiteID: d.SiteID, Name: d.Preview, DeployID: d.ID, LifetimeDays: int64(days),
		DeployedAt: ms(at), ExpiresAt: expires,
	})
	if err != nil {
		return mapErr("set preview", err)
	}
	d.PreviewActivatedAt = fromMs(ms(at))
	return nil
}
