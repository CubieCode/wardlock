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

// ErrLimitReached means a write would take the owner past a count limit
// the caller passed in: groups, tags, or tags on one site (V5.3).
var ErrLimitReached = errors.New("limit reached")

// Group is a named set of an owner's sites; a site is in at most one.
type Group struct {
	ID      string
	OwnerID string
	Name    string
	Note    string // empty for no note
	// CreatedAt is set by the caller on create and read only after.
	CreatedAt time.Time
}

// Tag is a named, coloured label; a site has any number.
type Tag struct {
	ID      string
	OwnerID string
	Name    string
	// Colour is a palette slot: 0 neutral, 1-8 the categorical colours.
	Colour    int
	CreatedAt time.Time
}

// AutoColour as a new Tag's Colour asks CreateTag for the lowest slot 1-8
// none of the owner's tags has, or 0 when all eight are taken.
const AutoColour = -1

// SiteOrganisation is one site's group, tags and pin.
type SiteOrganisation struct {
	SiteID   string
	SiteName string
	GroupID  string   // empty when ungrouped
	TagIDs   []string // in tag-name order
	PinnedAt time.Time
}

// OrganiseChange is what Organise does to every site it names. Each part
// applies only when its Set flag is true or its list is not empty.
type OrganiseChange struct {
	// SetGroup puts the sites in GroupID, or in no group when it is empty.
	SetGroup bool
	GroupID  string
	// SetTags replaces the sites' tags with TagIDs (which may be empty).
	SetTags bool
	TagIDs  []string
	// AddTagIDs and RemoveTagIDs add and remove tags, keeping the others.
	AddTagIDs    []string
	RemoveTagIDs []string
	// SetPinned pins the sites at PinnedAt (a pinned site keeps its first
	// time), or unpins them when PinnedAt is zero.
	SetPinned bool
	PinnedAt  time.Time
	// MaxTagsPerSite refuses the change with ErrLimitReached when any site
	// would end up with more tags; 0 is unlimited.
	MaxTagsPerSite int
}

// Organisation stores owners' groups and tags and which sites are in them.
// Every method matches only ownerID's rows; another owner's group, tag or
// site is ErrNotFound, exactly like a missing one.
type Organisation interface {
	// ListGroups returns every one of the owner's groups by name.
	ListGroups(ctx context.Context, ownerID string) ([]Group, error)
	GetGroup(ctx context.Context, ownerID, id string) (Group, error)
	// CreateGroup inserts g. A name the owner already uses is ErrConflict;
	// an owner who already has max groups (when max > 0) gets
	// ErrLimitReached. Concurrent creates by one owner are serialised.
	CreateGroup(ctx context.Context, g Group, max int) error
	// UpdateGroup sets g's name and note.
	UpdateGroup(ctx context.Context, g Group) error
	// DeleteGroup deletes the group; its sites become ungrouped.
	DeleteGroup(ctx context.Context, ownerID, id string) error

	// ListTags returns every one of the owner's tags by name.
	ListTags(ctx context.Context, ownerID string) ([]Tag, error)
	GetTag(ctx context.Context, ownerID, id string) (Tag, error)
	// CreateTag inserts t, choosing its colour when t.Colour is AutoColour,
	// and returns it as stored. Errors as for CreateGroup.
	CreateTag(ctx context.Context, t Tag, max int) (Tag, error)
	// UpdateTag sets t's name and colour.
	UpdateTag(ctx context.Context, t Tag) error
	// DeleteTag deletes the tag and removes it from its sites.
	DeleteTag(ctx context.Context, ownerID, id string) error

	// Organise applies c to each of the owner's sites named in siteNames,
	// all or nothing, and returns each site's organisation from before
	// the change, in siteNames order. Any site, group or tag that is not
	// the owner's is ErrNotFound and changes nothing.
	Organise(ctx context.Context, ownerID string, siteNames []string, c OrganiseChange) ([]SiteOrganisation, error)
}

// Organisation returns the SQLite Organisation repository.
func (db *DB) Organisation() Organisation { return organisationRepo{db} }

type organisationRepo struct{ db *DB }

func (r organisationRepo) ListGroups(ctx context.Context, ownerID string) ([]Group, error) {
	rows, err := r.db.q.ListGroups(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list groups", err)
	}
	out := make([]Group, len(rows))
	for i, row := range rows {
		out[i] = groupFrom(row)
	}
	return out, nil
}

func (r organisationRepo) GetGroup(ctx context.Context, ownerID, id string) (Group, error) {
	row, err := r.db.q.GetGroup(ctx, sqlcgen.GetGroupParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return Group{}, mapErr("get group", err)
	}
	return groupFrom(row), nil
}

func (r organisationRepo) CreateGroup(ctx context.Context, g Group, max int) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := lockOwnerCount(ctx, q, g.OwnerID, max, q.CountGroups); err != nil {
			return fmt.Errorf("create group: %w", err)
		}
		return mapErr("create group", q.CreateGroup(ctx, sqlcgen.CreateGroupParams{
			ID: g.ID, OwnerID: g.OwnerID, Name: g.Name, Note: nullString(g.Note), CreatedAt: ms(g.CreatedAt),
		}))
	})
}

func (r organisationRepo) UpdateGroup(ctx context.Context, g Group) error {
	n, err := r.db.q.UpdateGroup(ctx, sqlcgen.UpdateGroupParams{ID: g.ID, OwnerID: g.OwnerID, Name: g.Name, Note: nullString(g.Note)})
	return rowsAffected("update group", n, err)
}

func (r organisationRepo) DeleteGroup(ctx context.Context, ownerID, id string) error {
	n, err := r.db.q.DeleteGroup(ctx, sqlcgen.DeleteGroupParams{ID: id, OwnerID: ownerID})
	return rowsAffected("delete group", n, err)
}

func (r organisationRepo) ListTags(ctx context.Context, ownerID string) ([]Tag, error) {
	rows, err := r.db.q.ListTags(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list tags", err)
	}
	out := make([]Tag, len(rows))
	for i, row := range rows {
		out[i] = tagFrom(row)
	}
	return out, nil
}

func (r organisationRepo) GetTag(ctx context.Context, ownerID, id string) (Tag, error) {
	row, err := r.db.q.GetTag(ctx, sqlcgen.GetTagParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return Tag{}, mapErr("get tag", err)
	}
	return tagFrom(row), nil
}

func (r organisationRepo) CreateTag(ctx context.Context, t Tag, max int) (Tag, error) {
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := lockOwnerCount(ctx, q, t.OwnerID, max, q.CountTags); err != nil {
			return fmt.Errorf("create tag: %w", err)
		}
		if t.Colour == AutoColour {
			used, err := q.TagColours(ctx, t.OwnerID)
			if err != nil {
				return mapErr("list tag colours", err)
			}
			t.Colour = freeColour(used)
		}
		return mapErr("create tag", q.CreateTag(ctx, sqlcgen.CreateTagParams{
			ID: t.ID, OwnerID: t.OwnerID, Name: t.Name, Colour: int64(t.Colour), CreatedAt: ms(t.CreatedAt),
		}))
	})
	if err != nil {
		return Tag{}, err
	}
	return t, nil
}

// freeColour returns the lowest slot 1-8 not in used, or 0.
func freeColour(used []int64) int {
	taken := [9]bool{}
	for _, c := range used {
		if c >= 0 && c <= 8 {
			taken[c] = true
		}
	}
	for c := 1; c <= 8; c++ {
		if !taken[c] {
			return c
		}
	}
	return 0
}

func (r organisationRepo) UpdateTag(ctx context.Context, t Tag) error {
	n, err := r.db.q.UpdateTag(ctx, sqlcgen.UpdateTagParams{ID: t.ID, OwnerID: t.OwnerID, Name: t.Name, Colour: int64(t.Colour)})
	return rowsAffected("update tag", n, err)
}

func (r organisationRepo) DeleteTag(ctx context.Context, ownerID, id string) error {
	n, err := r.db.q.DeleteTag(ctx, sqlcgen.DeleteTagParams{ID: id, OwnerID: ownerID})
	return rowsAffected("delete tag", n, err)
}

func (r organisationRepo) Organise(ctx context.Context, ownerID string, siteNames []string, c OrganiseChange) ([]SiteOrganisation, error) {
	var prior []SiteOrganisation
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		// Concurrent changes by one owner take turns, so neither can take
		// a site past MaxTagsPerSite.
		if _, err := q.LockOwnerQuotas(ctx, ownerID); err != nil {
			return mapErr("lock owner", err)
		}
		if c.SetGroup && c.GroupID != "" {
			if _, err := q.GetGroup(ctx, sqlcgen.GetGroupParams{ID: c.GroupID, OwnerID: ownerID}); err != nil {
				return mapErr("find group", err)
			}
		}
		for _, list := range [][]string{c.TagIDs, c.AddTagIDs, c.RemoveTagIDs} {
			for _, id := range list {
				if _, err := q.GetTag(ctx, sqlcgen.GetTagParams{ID: id, OwnerID: ownerID}); err != nil {
					return mapErr("find tag", err)
				}
			}
		}
		prior = make([]SiteOrganisation, 0, len(siteNames))
		for _, name := range siteNames {
			before, err := organiseSite(ctx, q, ownerID, name, c)
			if err != nil {
				return err
			}
			prior = append(prior, before)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return prior, nil
}

// organiseSite applies c to one site in Organise's transaction and returns
// its organisation from before.
func organiseSite(ctx context.Context, q *sqlcgen.Queries, ownerID, name string, c OrganiseChange) (SiteOrganisation, error) {
	row, err := q.GetSiteOrganisation(ctx, sqlcgen.GetSiteOrganisationParams{Name: name, OwnerID: ownerID})
	if err != nil {
		return SiteOrganisation{}, mapErr("find site", err)
	}
	tagIDs, err := q.ListSiteTagIDs(ctx, sqlcgen.ListSiteTagIDsParams{SiteID: row.ID, OwnerID: ownerID})
	if err != nil {
		return SiteOrganisation{}, mapErr("list site tags", err)
	}
	if tagIDs == nil {
		tagIDs = []string{}
	}
	before := SiteOrganisation{
		SiteID: row.ID, SiteName: name, GroupID: row.GroupID.String, TagIDs: tagIDs, PinnedAt: fromNullMs(row.PinnedAt),
	}
	if c.SetGroup {
		n, err := q.SetSiteGroup(ctx, sqlcgen.SetSiteGroupParams{GroupID: nullString(c.GroupID), SiteID: row.ID, OwnerID: ownerID})
		if err := rowsAffected("set site group", n, err); err != nil {
			return SiteOrganisation{}, err
		}
	}
	if c.SetTags {
		if err := q.ClearSiteTags(ctx, row.ID); err != nil {
			return SiteOrganisation{}, mapErr("clear site tags", err)
		}
	}
	for _, id := range c.RemoveTagIDs {
		if err := q.RemoveSiteTag(ctx, sqlcgen.RemoveSiteTagParams{SiteID: row.ID, TagID: id}); err != nil {
			return SiteOrganisation{}, mapErr("remove site tag", err)
		}
	}
	for _, list := range [][]string{c.TagIDs, c.AddTagIDs} {
		for _, id := range list {
			if err := q.AddSiteTag(ctx, sqlcgen.AddSiteTagParams{SiteID: row.ID, OwnerID: ownerID, TagID: id}); err != nil {
				return SiteOrganisation{}, mapErr("add site tag", err)
			}
		}
	}
	if c.MaxTagsPerSite > 0 && (c.SetTags || len(c.AddTagIDs) > 0) {
		n, err := q.CountSiteTags(ctx, row.ID)
		if err != nil {
			return SiteOrganisation{}, mapErr("count site tags", err)
		}
		if n > int64(c.MaxTagsPerSite) {
			return SiteOrganisation{}, fmt.Errorf("tag site %s: %w", name, ErrLimitReached)
		}
	}
	if c.SetPinned {
		var n int64
		if c.PinnedAt.IsZero() {
			n, err = q.UnpinSite(ctx, sqlcgen.UnpinSiteParams{SiteID: row.ID, OwnerID: ownerID})
		} else {
			n, err = q.PinSite(ctx, sqlcgen.PinSiteParams{PinnedAt: nullMs(c.PinnedAt), SiteID: row.ID, OwnerID: ownerID})
		}
		if err := rowsAffected("pin site", n, err); err != nil {
			return SiteOrganisation{}, err
		}
	}
	return before, nil
}

// lockOwnerCount locks the owner's row, so concurrent creates by one owner
// take turns, then refuses with ErrLimitReached when count already reaches
// max (when max > 0). An unknown owner is ErrConstraint.
func lockOwnerCount(ctx context.Context, q *sqlcgen.Queries, ownerID string, max int, count func(context.Context, string) (int64, error)) error {
	if _, err := q.LockOwnerQuotas(ctx, ownerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConstraint
		}
		return mapErr("lock owner", err)
	}
	if max <= 0 {
		return nil
	}
	n, err := count(ctx, ownerID)
	if err != nil {
		return mapErr("count", err)
	}
	if n >= int64(max) {
		return ErrLimitReached
	}
	return nil
}

func groupFrom(row sqlcgen.SiteGroup) Group {
	return Group{ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, Note: row.Note.String, CreatedAt: fromMs(row.CreatedAt)}
}

func tagFrom(row sqlcgen.Tag) Tag {
	return Tag{ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, Colour: int(row.Colour), CreatedAt: fromMs(row.CreatedAt)}
}
