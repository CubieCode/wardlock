// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// SummaryWindow is when Sites.Summaries counts links and requests from
// (V5.5); internal/sites sets the rules.
type SummaryWindow struct {
	// Now decides which links work: unrevoked, not used up and not
	// expired at Now.
	Now time.Time
	// ExpiringBy and MinLifetime pick the working links that expire soon:
	// by ExpiringBy, and living longer than MinLifetime from creation to
	// expiry.
	ExpiringBy  time.Time
	MinLifetime time.Duration
	// DaysFrom is a UTC midnight: Requests counts each UTC day from it on.
	DaysFrom time.Time
	// Days is how many days Requests counts, the last being DaysFrom +
	// Days - 1.
	Days int
	// ActiveFrom is when Active starts looking for requests.
	ActiveFrom time.Time
}

// SiteSummary is one site with what the dashboard's lists show of it.
type SiteSummary struct {
	Site
	// DeployMessage is the active deploy's message, if any.
	DeployMessage string
	// Default is the site's default policy's visibility and methods; a site
	// that has not stored one is Public with none. Only Visibility and the
	// *Enabled fields are set.
	Default Policy
	// Domains are its custom domains, whatever their status, by hostname.
	Domains []string
	// TagIDs are its tags in tag-name order.
	TagIDs []string
	// WorkingLinks and ExpiringLinks count links as SummaryWindow says.
	WorkingLinks  int
	ExpiringLinks int
	// Requests is its requests (allowed and refused) on each of the
	// window's Days, oldest first.
	Requests []int64
	// Active reports whether it had any request from ActiveFrom on.
	Active bool
}

// Summaries returns every a site the actor can edit, by name, with what w
// counts. It costs the same four queries whatever the number of sites.
func (r sitesRepo) Summaries(ctx context.Context, actor Actor, ownerID string, w SummaryWindow) ([]SiteSummary, error) {
	if err := actor.Require(ownerID, RoleViewer); err != nil {
		return nil, fmt.Errorf("list site summaries: %w", err)
	}
	rows, err := r.db.q.ListSiteSummaries(ctx, sqlcgen.ListSiteSummariesParams{
		Now: ms(w.Now), ExpiringBy: ms(w.ExpiringBy), MinLifetime: w.MinLifetime.Milliseconds(),
		ActiveFrom: ms(w.ActiveFrom), OwnerID: ownerID,
	})
	if err != nil {
		return nil, mapErr("list site summaries", err)
	}
	pins, err := r.db.pins(ctx, actor)
	if err != nil {
		return nil, err
	}
	out := make([]SiteSummary, len(rows))
	byID := make(map[string]*SiteSummary, len(rows))
	for i, row := range rows {
		s, _ := siteFrom("", siteColumns{
			ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, ActiveDeployID: row.ActiveDeployID,
			CreatedAt: row.CreatedAt, ActivatedAt: row.ActivatedAt, TakenDownAt: row.TakenDownAt,
			GroupID: row.GroupID, OwnerKind: row.OwnerKind, OwnerEmail: row.OwnerEmail, TeamName: row.TeamName,
		}, nil)
		s.PinnedAt = pins[s.ID]
		out[i] = SiteSummary{
			Site:          s,
			DeployMessage: row.DeployMessage.String,
			Default:       Policy{Visibility: Public},
			WorkingLinks:  int(row.WorkingLinks),
			ExpiringLinks: int(row.ExpiringLinks),
			Requests:      make([]int64, w.Days),
			Active:        row.Active == 1,
		}
		if row.Visibility.Valid {
			out[i].Default = Policy{
				Visibility:      Visibility(row.Visibility.String),
				LinksEnabled:    row.LinksEnabled.Int64 == 1,
				PasswordEnabled: row.PasswordEnabled.Int64 == 1,
				EmailEnabled:    row.EmailEnabled.Int64 == 1,
				OAuthEnabled:    row.OauthEnabled.Int64 == 1,
			}
		}
		byID[row.ID] = &out[i]
	}
	if len(out) == 0 {
		return out, nil
	}

	tags, err := r.db.q.ListOwnerSiteTags(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list site tags", err)
	}
	for _, row := range tags {
		if s := byID[row.SiteID]; s != nil {
			s.TagIDs = append(s.TagIDs, row.TagID)
		}
	}
	domains, err := r.db.q.ListOwnerSiteDomains(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list site domains", err)
	}
	for _, row := range domains {
		if s := byID[row.SiteID]; s != nil {
			s.Domains = append(s.Domains, row.Hostname)
		}
	}
	days, err := r.db.q.OwnerRequestsByDay(ctx, sqlcgen.OwnerRequestsByDayParams{FromDay: ms(w.DaysFrom), OwnerID: ownerID})
	if err != nil {
		return nil, mapErr("count site requests by day", err)
	}
	for _, row := range days {
		if s := byID[row.SiteID]; s != nil && row.Day >= 0 && row.Day < int64(w.Days) {
			s.Requests[row.Day] = row.Requests
		}
	}
	return out, nil
}

// ExpiringLink is a working link that expires soon, as SummaryWindow
// says, with what the overview shows of it.
type ExpiringLink struct {
	ID       string
	SiteName string
	Label    string
	// ExpiresAt is always set: a link that never expires never expires
	// soon.
	ExpiresAt time.Time
	// MaxUses is 0 for a link without a use limit.
	MaxUses  int
	UseCount int
}

// ExpiringLinks returns up to limit of the account's expiring links, soonest
// first, by the same rule Summaries counts them with.
func (r sitesRepo) ExpiringLinks(ctx context.Context, actor Actor, ownerID string, w SummaryWindow, limit int) ([]ExpiringLink, error) {
	if err := actor.Require(ownerID, RoleViewer); err != nil {
		return nil, fmt.Errorf("list expiring links: %w", err)
	}
	rows, err := r.db.q.ListExpiringLinks(ctx, sqlcgen.ListExpiringLinksParams{
		OwnerID: ownerID, Now: ms(w.Now), ExpiringBy: ms(w.ExpiringBy),
		MinLifetime: w.MinLifetime.Milliseconds(), RowLimit: int64(limit),
	})
	if err != nil {
		return nil, mapErr("list expiring links", err)
	}
	out := make([]ExpiringLink, len(rows))
	for i, row := range rows {
		out[i] = ExpiringLink{
			ID: row.ID, SiteName: row.SiteName, Label: row.Label.String,
			ExpiresAt: fromNullMs(row.ExpiresAt), MaxUses: int(row.MaxUses.Int64), UseCount: int(row.UseCount),
		}
	}
	return out, nil
}
