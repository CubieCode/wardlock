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
	// NameKey is what makes the name unique per owner: the caller derives
	// it from Name (internal/sites folds case) and sets it with Name.
	NameKey string
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

// pinOnly reports whether c changes nothing but the actor's own pin.
func (c OrganiseChange) pinOnly() bool {
	return c.SetPinned && !c.SetGroup && !c.SetTags && len(c.AddTagIDs) == 0 && len(c.RemoveTagIDs) == 0
}

// Organisation stores accounts' groups and tags and which sites are in
// them. Groups and tags belong to an account (an owner, or a team, T7.1) and
// the sites they hold to the same account: Group.OwnerID and Tag.OwnerID name
// it, and every method checks the actor against it. A group, tag or site the
// actor cannot see is ErrNotFound, exactly like a missing one, and changing
// one with too low a role is ErrForbiddenRole.
type Organisation interface {
	// ListGroups returns every one of the account's groups by name key.
	ListGroups(ctx context.Context, actor Actor, accountID string) ([]Group, error)
	GetGroup(ctx context.Context, actor Actor, id string) (Group, error)
	// CreateGroup inserts g. A NameKey the account already uses is
	// ErrConflict; an account that already has max groups (when max > 0)
	// gets ErrLimitReached. Concurrent creates by one account are serialised.
	CreateGroup(ctx context.Context, actor Actor, g Group, max int) error
	// UpdateGroup sets g's name, name key and note.
	UpdateGroup(ctx context.Context, actor Actor, g Group) error
	// DeleteGroup deletes the group; its sites become ungrouped.
	DeleteGroup(ctx context.Context, actor Actor, id string) error

	// ListTags returns every one of the account's tags by name.
	ListTags(ctx context.Context, actor Actor, accountID string) ([]Tag, error)
	GetTag(ctx context.Context, actor Actor, id string) (Tag, error)
	// CreateTag inserts t, choosing its colour when t.Colour is AutoColour,
	// and returns it as stored. Errors as for CreateGroup.
	CreateTag(ctx context.Context, actor Actor, t Tag, max int) (Tag, error)
	// UpdateTag sets t's name and colour.
	UpdateTag(ctx context.Context, actor Actor, t Tag) error
	// DeleteTag deletes the tag and removes it from its sites.
	DeleteTag(ctx context.Context, actor Actor, id string) error

	// Organise applies c to each of accountID's sites named in siteNames,
	// all or nothing, and returns each site's organisation from before
	// the change, in siteNames order. Any site, group or tag that is not
	// the account's is ErrNotFound and changes nothing. A change that only
	// pins needs a viewer, since a pin is the actor's own (T7.1); anything
	// else needs an editor.
	Organise(ctx context.Context, actor Actor, accountID string, siteNames []string, c OrganiseChange) ([]SiteOrganisation, error)
}

// Organisation returns the SQLite Organisation repository.
func (db *DB) Organisation() Organisation { return organisationRepo{db} }

type organisationRepo struct{ db *DB }

func (r organisationRepo) ListGroups(ctx context.Context, actor Actor, accountID string) ([]Group, error) {
	if err := actor.Require(accountID, RoleViewer); err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	rows, err := r.db.q.ListGroups(ctx, accountID)
	if err != nil {
		return nil, mapErr("list groups", err)
	}
	out := make([]Group, len(rows))
	for i, row := range rows {
		out[i] = groupFrom(row)
	}
	return out, nil
}

func (r organisationRepo) GetGroup(ctx context.Context, actor Actor, id string) (Group, error) {
	row, err := r.db.q.GetGroupByID(ctx, id)
	if err != nil {
		return Group{}, mapErr("get group", err)
	}
	if err := actor.Require(row.OwnerID, RoleViewer); err != nil {
		return Group{}, fmt.Errorf("get group: %w", err)
	}
	return groupFrom(row), nil
}

func (r organisationRepo) CreateGroup(ctx context.Context, actor Actor, g Group, max int) error {
	if err := actor.Require(g.OwnerID, RoleEditor); err != nil {
		return fmt.Errorf("create group: %w", err)
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := lockOwnerCount(ctx, q, g.OwnerID, max, q.CountGroups); err != nil {
			return fmt.Errorf("create group: %w", err)
		}
		return mapErr("create group", q.CreateGroup(ctx, sqlcgen.CreateGroupParams{
			ID: g.ID, OwnerID: g.OwnerID, Name: g.Name, NameKey: g.NameKey, Note: nullString(g.Note), CreatedAt: ms(g.CreatedAt),
		}))
	})
}

func (r organisationRepo) UpdateGroup(ctx context.Context, actor Actor, g Group) error {
	cur, err := r.GetGroup(ctx, actor, g.ID)
	if err != nil {
		return err
	}
	if err := actor.Require(cur.OwnerID, RoleEditor); err != nil {
		return fmt.Errorf("update group: %w", err)
	}
	n, err := r.db.q.UpdateGroup(ctx, sqlcgen.UpdateGroupParams{ID: g.ID, OwnerID: cur.OwnerID, Name: g.Name, NameKey: g.NameKey, Note: nullString(g.Note)})
	return rowsAffected("update group", n, err)
}

func (r organisationRepo) DeleteGroup(ctx context.Context, actor Actor, id string) error {
	cur, err := r.GetGroup(ctx, actor, id)
	if err != nil {
		return err
	}
	if err := actor.Require(cur.OwnerID, RoleEditor); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	n, err := r.db.q.DeleteGroup(ctx, sqlcgen.DeleteGroupParams{ID: id, OwnerID: cur.OwnerID})
	return rowsAffected("delete group", n, err)
}

func (r organisationRepo) ListTags(ctx context.Context, actor Actor, accountID string) ([]Tag, error) {
	if err := actor.Require(accountID, RoleViewer); err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	rows, err := r.db.q.ListTags(ctx, accountID)
	if err != nil {
		return nil, mapErr("list tags", err)
	}
	out := make([]Tag, len(rows))
	for i, row := range rows {
		out[i] = tagFrom(row)
	}
	return out, nil
}

func (r organisationRepo) GetTag(ctx context.Context, actor Actor, id string) (Tag, error) {
	row, err := r.db.q.GetTagByID(ctx, id)
	if err != nil {
		return Tag{}, mapErr("get tag", err)
	}
	if err := actor.Require(row.OwnerID, RoleViewer); err != nil {
		return Tag{}, fmt.Errorf("get tag: %w", err)
	}
	return tagFrom(row), nil
}

func (r organisationRepo) CreateTag(ctx context.Context, actor Actor, t Tag, max int) (Tag, error) {
	if err := actor.Require(t.OwnerID, RoleEditor); err != nil {
		return Tag{}, fmt.Errorf("create tag: %w", err)
	}
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

func (r organisationRepo) UpdateTag(ctx context.Context, actor Actor, t Tag) error {
	cur, err := r.GetTag(ctx, actor, t.ID)
	if err != nil {
		return err
	}
	if err := actor.Require(cur.OwnerID, RoleEditor); err != nil {
		return fmt.Errorf("update tag: %w", err)
	}
	n, err := r.db.q.UpdateTag(ctx, sqlcgen.UpdateTagParams{ID: t.ID, OwnerID: cur.OwnerID, Name: t.Name, Colour: int64(t.Colour)})
	return rowsAffected("update tag", n, err)
}

func (r organisationRepo) DeleteTag(ctx context.Context, actor Actor, id string) error {
	cur, err := r.GetTag(ctx, actor, id)
	if err != nil {
		return err
	}
	if err := actor.Require(cur.OwnerID, RoleEditor); err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	n, err := r.db.q.DeleteTag(ctx, sqlcgen.DeleteTagParams{ID: id, OwnerID: cur.OwnerID})
	return rowsAffected("delete tag", n, err)
}

func (r organisationRepo) Organise(ctx context.Context, actor Actor, ownerID string, siteNames []string, c OrganiseChange) ([]SiteOrganisation, error) {
	need := RoleEditor
	if c.pinOnly() {
		need = RoleViewer
	}
	if err := actor.Require(ownerID, need); err != nil {
		return nil, fmt.Errorf("organise sites: %w", err)
	}
	var prior []SiteOrganisation
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		// Concurrent changes by one account take turns, so neither can take
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
			before, err := organiseSite(ctx, q, actor, ownerID, name, c)
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
func organiseSite(ctx context.Context, q *sqlcgen.Queries, actor Actor, ownerID, name string, c OrganiseChange) (SiteOrganisation, error) {
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
	before := SiteOrganisation{SiteID: row.ID, SiteName: name, GroupID: row.GroupID.String, TagIDs: tagIDs}
	pinned, err := q.GetSitePin(ctx, sqlcgen.GetSitePinParams{OwnerID: actor.OwnerID, SiteID: row.ID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SiteOrganisation{}, mapErr("get site pin", err)
	}
	if err == nil {
		before.PinnedAt = fromMs(pinned)
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
		if c.PinnedAt.IsZero() {
			err = q.UnpinSite(ctx, sqlcgen.UnpinSiteParams{SiteID: row.ID, OwnerID: actor.OwnerID})
		} else {
			err = q.PinSite(ctx, sqlcgen.PinSiteParams{PinnedAt: ms(c.PinnedAt), SiteID: row.ID, OwnerID: actor.OwnerID})
		}
		if err != nil {
			return SiteOrganisation{}, mapErr("pin site", err)
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
	return Group{ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, NameKey: row.NameKey, Note: row.Note.String, CreatedAt: fromMs(row.CreatedAt)}
}

func tagFrom(row sqlcgen.Tag) Tag {
	return Tag{ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, Colour: int(row.Colour), CreatedAt: fromMs(row.CreatedAt)}
}
