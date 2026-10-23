// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/store"
)

// Groups, tags and pins (V5.3): how an owner organises their sites. They
// are the owner's alone and never reach visitors.

// The limits SPEC "Groups, tags and pins" sets.
const (
	MaxGroupName     = 40
	MaxGroupNote     = 200
	MaxTagName       = 30
	MaxGroups        = 100
	MaxTags          = 200
	MaxTagsPerSite   = 20
	MaxOrganiseSites = 500
	MaxTagColour     = 8
)

// Errors returned by the organisation methods, besides ErrNotFound, which
// covers a missing or foreign group, tag or site alike.
var (
	// ErrInvalid means a field breaks a rule; the error is an
	// *InvalidError.
	ErrInvalid = errors.New("invalid organisation field")
	// ErrGroupNameTaken and ErrTagNameTaken mean the owner already has a
	// group or tag with the name.
	ErrGroupNameTaken = errors.New("group name taken")
	ErrTagNameTaken   = errors.New("tag name taken")
	// ErrTooMany means the change would pass a count limit; the error is a
	// *TooManyError.
	ErrTooMany = errors.New("organisation limit reached")
)

// InvalidError is ErrInvalid with the field and a message safe to show the
// owner.
type InvalidError struct{ Target, Message string }

func (e *InvalidError) Error() string { return e.Target + ": " + e.Message }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// TooManyError is ErrTooMany with the field and a message safe to show the
// owner.
type TooManyError struct{ Target, Message string }

func (e *TooManyError) Error() string { return e.Target + ": " + e.Message }
func (e *TooManyError) Unwrap() error { return ErrTooMany }

func invalid(target, message string) error { return &InvalidError{Target: target, Message: message} }

// checkText trims s and checks it is 1 to max characters with no control
// characters.
func checkText(target, label, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n == 0 || n > max || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", invalid(target, fmt.Sprintf("%s must be 1-%d characters with no control characters.", label, max))
	}
	return s, nil
}

func checkGroupName(name string) (string, error) {
	return checkText("name", "A group name", name, MaxGroupName)
}

// checkNote returns the trimmed note; an empty one is no note.
func checkNote(note string) (string, error) {
	if strings.TrimSpace(note) == "" {
		return "", nil
	}
	return checkText("note", "A note", note, MaxGroupNote)
}

// ValidTagName reports whether name is 1-30 characters of a-z, 0-9 and '-'.
func ValidTagName(name string) bool {
	if name == "" || len(name) > MaxTagName {
		return false
	}
	for i := range len(name) {
		if c := name[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func checkTagName(name string) error {
	if !ValidTagName(name) {
		return invalid("name", fmt.Sprintf("A tag name must be 1-%d characters of a-z, 0-9 and '-'.", MaxTagName))
	}
	return nil
}

func checkColour(c int) error {
	if c < 0 || c > MaxTagColour {
		return invalid("colour", "colour must be 0 (neutral) or 1-8.")
	}
	return nil
}

// ListGroups returns the owner's groups by name.
func (s *Service) ListGroups(ctx context.Context, ownerID string) ([]store.Group, error) {
	groups, err := s.c.Organisation.ListGroups(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return groups, nil
}

// CreateGroup creates a group with an optional note.
func (s *Service) CreateGroup(ctx context.Context, ownerID, name, note string) (store.Group, error) {
	name, err := checkGroupName(name)
	if err != nil {
		return store.Group{}, err
	}
	if note, err = checkNote(note); err != nil {
		return store.Group{}, err
	}
	g := store.Group{ID: store.NewID(), OwnerID: ownerID, Name: name, Note: note, CreatedAt: s.now()}
	err = s.c.Organisation.CreateGroup(ctx, g, MaxGroups)
	switch {
	case errors.Is(err, store.ErrConflict):
		return store.Group{}, ErrGroupNameTaken
	case errors.Is(err, store.ErrLimitReached):
		return store.Group{}, &TooManyError{Target: "name", Message: fmt.Sprintf("You already have %d groups, the most allowed. Delete one you no longer need.", MaxGroups)}
	case err != nil:
		return store.Group{}, fmt.Errorf("create group: %w", err)
	}
	return g, nil
}

// UpdateGroup renames the group or changes its note; nil leaves a field
// as it is and an empty note removes it.
func (s *Service) UpdateGroup(ctx context.Context, ownerID, id string, name, note *string) (store.Group, error) {
	g, err := s.c.Organisation.GetGroup(ctx, ownerID, id)
	if err != nil {
		return store.Group{}, storeErr("get group", err)
	}
	if name != nil {
		if g.Name, err = checkGroupName(*name); err != nil {
			return store.Group{}, err
		}
	}
	if note != nil {
		if g.Note, err = checkNote(*note); err != nil {
			return store.Group{}, err
		}
	}
	err = s.c.Organisation.UpdateGroup(ctx, g)
	if errors.Is(err, store.ErrConflict) {
		return store.Group{}, ErrGroupNameTaken
	}
	return g, storeErr("update group", err)
}

// DeleteGroup deletes the group; its sites become ungrouped.
func (s *Service) DeleteGroup(ctx context.Context, ownerID, id string) error {
	return storeErr("delete group", s.c.Organisation.DeleteGroup(ctx, ownerID, id))
}

// ListTags returns the owner's tags by name.
func (s *Service) ListTags(ctx context.Context, ownerID string) ([]store.Tag, error) {
	tags, err := s.c.Organisation.ListTags(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return tags, nil
}

// CreateTag creates a tag. Without a colour it takes the lowest slot 1-8
// none of the owner's tags has, or 0 when all eight are taken.
func (s *Service) CreateTag(ctx context.Context, ownerID, name string, colour *int) (store.Tag, error) {
	if err := checkTagName(name); err != nil {
		return store.Tag{}, err
	}
	t := store.Tag{ID: store.NewID(), OwnerID: ownerID, Name: name, Colour: store.AutoColour, CreatedAt: s.now()}
	if colour != nil {
		if err := checkColour(*colour); err != nil {
			return store.Tag{}, err
		}
		t.Colour = *colour
	}
	t, err := s.c.Organisation.CreateTag(ctx, t, MaxTags)
	switch {
	case errors.Is(err, store.ErrConflict):
		return store.Tag{}, ErrTagNameTaken
	case errors.Is(err, store.ErrLimitReached):
		return store.Tag{}, &TooManyError{Target: "name", Message: fmt.Sprintf("You already have %d tags, the most allowed. Delete one you no longer need.", MaxTags)}
	case err != nil:
		return store.Tag{}, fmt.Errorf("create tag: %w", err)
	}
	return t, nil
}

// UpdateTag renames the tag or changes its colour; nil leaves a field as
// it is.
func (s *Service) UpdateTag(ctx context.Context, ownerID, id string, name *string, colour *int) (store.Tag, error) {
	t, err := s.c.Organisation.GetTag(ctx, ownerID, id)
	if err != nil {
		return store.Tag{}, storeErr("get tag", err)
	}
	if name != nil {
		if err := checkTagName(*name); err != nil {
			return store.Tag{}, err
		}
		t.Name = *name
	}
	if colour != nil {
		if err := checkColour(*colour); err != nil {
			return store.Tag{}, err
		}
		t.Colour = *colour
	}
	err = s.c.Organisation.UpdateTag(ctx, t)
	if errors.Is(err, store.ErrConflict) {
		return store.Tag{}, ErrTagNameTaken
	}
	return t, storeErr("update tag", err)
}

// DeleteTag deletes the tag and removes it from its sites.
func (s *Service) DeleteTag(ctx context.Context, ownerID, id string) error {
	return storeErr("delete tag", s.c.Organisation.DeleteTag(ctx, ownerID, id))
}

// TagIDs returns each site's tag IDs in tag-name order, keyed by site ID;
// a site with no tags has none.
func (s *Service) TagIDs(ctx context.Context, ownerID string, sites ...store.Site) (map[string][]string, error) {
	ids := make([]string, len(sites))
	for i, site := range sites {
		ids[i] = site.ID
	}
	tags, err := s.c.Sites.TagIDs(ctx, ownerID, ids...)
	if err != nil {
		return nil, fmt.Errorf("list site tags: %w", err)
	}
	return tags, nil
}

// SetOrganisation puts the owner's site in groupID (none when empty) and
// replaces its tags with tagIDs, naming only existing groups and tags. It
// returns the site as it is now. A deploy-scoped token may call it.
func (s *Service) SetOrganisation(ctx context.Context, ownerID, siteName, groupID string, tagIDs []string) (store.Site, error) {
	if err := checkIDs("tagIds", tagIDs); err != nil {
		return store.Site{}, err
	}
	if len(tagIDs) > MaxTagsPerSite {
		return store.Site{}, tooManyTags("tagIds")
	}
	return s.organiseOne(ctx, ownerID, siteName, store.OrganiseChange{
		SetGroup: true, GroupID: groupID, SetTags: true, TagIDs: tagIDs,
	})
}

// SetPinned pins or unpins the owner's site; pinning a pinned site keeps
// its first time.
func (s *Service) SetPinned(ctx context.Context, ownerID, siteName string, pinned bool) (store.Site, error) {
	c := store.OrganiseChange{SetPinned: true}
	if pinned {
		c.PinnedAt = s.now()
	}
	return s.organiseOne(ctx, ownerID, siteName, c)
}

func (s *Service) organiseOne(ctx context.Context, ownerID, siteName string, c store.OrganiseChange) (store.Site, error) {
	if !ValidName(siteName) {
		return store.Site{}, ErrNotFound
	}
	c.MaxTagsPerSite = MaxTagsPerSite
	if _, err := s.organise(ctx, ownerID, []string{siteName}, c, "tagIds"); err != nil {
		return store.Site{}, err
	}
	return s.Get(ctx, ownerID, siteName)
}

// Bulk is one change to many sites (Organise). A zero field changes
// nothing; at least one must change something.
type Bulk struct {
	AddTagIDs    []string
	RemoveTagIDs []string
	// SetGroup puts the sites in GroupID, or ungroups them when GroupID is
	// empty.
	SetGroup bool
	GroupID  string
	// Pinned, when not nil, pins (true) or unpins (false) the sites.
	Pinned *bool
}

// Organise applies b to up to MaxOrganiseSites of the owner's sites, all
// or nothing: any missing or foreign site, group or tag is ErrNotFound for
// the whole request. It returns each site's group, tags and pin from
// before, in the order named, so the caller can undo by sending the
// inverse.
func (s *Service) Organise(ctx context.Context, ownerID string, siteNames []string, b Bulk) ([]store.SiteOrganisation, error) {
	if len(siteNames) == 0 || len(siteNames) > MaxOrganiseSites {
		return nil, invalid("sites", fmt.Sprintf("sites must name 1-%d sites.", MaxOrganiseSites))
	}
	seen := make(map[string]bool, len(siteNames))
	for _, name := range siteNames {
		if seen[name] {
			return nil, invalid("sites", "sites must not name a site twice.")
		}
		seen[name] = true
		if !ValidName(name) {
			return nil, ErrNotFound
		}
	}
	if err := checkIDs("addTagIds", b.AddTagIDs); err != nil {
		return nil, err
	}
	if err := checkIDs("removeTagIds", b.RemoveTagIDs); err != nil {
		return nil, err
	}
	for _, id := range b.AddTagIDs {
		if slices.Contains(b.RemoveTagIDs, id) {
			return nil, invalid("removeTagIds", "A tag cannot be both added and removed.")
		}
	}
	if len(b.AddTagIDs) > MaxTagsPerSite {
		return nil, tooManyTags("addTagIds")
	}
	if len(b.AddTagIDs) == 0 && len(b.RemoveTagIDs) == 0 && !b.SetGroup && b.Pinned == nil {
		return nil, invalid("", "Name at least one change: addTagIds, removeTagIds, groupId, ungroup or pinned.")
	}
	c := store.OrganiseChange{
		AddTagIDs: b.AddTagIDs, RemoveTagIDs: b.RemoveTagIDs,
		SetGroup: b.SetGroup, GroupID: b.GroupID,
		MaxTagsPerSite: MaxTagsPerSite,
	}
	if b.Pinned != nil {
		c.SetPinned = true
		if *b.Pinned {
			c.PinnedAt = s.now()
		}
	}
	return s.organise(ctx, ownerID, siteNames, c, "addTagIds")
}

func (s *Service) organise(ctx context.Context, ownerID string, siteNames []string, c store.OrganiseChange, tagsTarget string) ([]store.SiteOrganisation, error) {
	prior, err := s.c.Organisation.Organise(ctx, ownerID, siteNames, c)
	if errors.Is(err, store.ErrLimitReached) {
		return nil, tooManyTags(tagsTarget)
	}
	if err != nil {
		return nil, storeErr("organise sites", err)
	}
	return prior, nil
}

func tooManyTags(target string) error {
	return &TooManyError{Target: target, Message: fmt.Sprintf("A site can have at most %d tags.", MaxTagsPerSite)}
}

// checkIDs refuses a list that names one ID twice.
func checkIDs(target string, ids []string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return invalid(target, target+" must not name a tag twice.")
		}
		seen[id] = true
	}
	return nil
}
