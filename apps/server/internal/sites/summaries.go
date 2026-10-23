// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"wardlock.nz/server/internal/store"
)

// Site summaries (V5.5): one read of an owner's sites for the dashboard's
// lists, navigator, map and overview, with the filters, saved views and
// search SPEC "Site summaries" defines.

// The windows and limits SPEC "Site summaries" sets.
const (
	// ExpiringWithin is how soon a working link must expire to count as
	// expiring soon.
	ExpiringWithin = 48 * time.Hour
	// ShortLinkLifetime is the longest lifetime, creation to expiry, of a
	// link that never counts as expiring soon, such as the Open site
	// button's 10-minute links.
	ShortLinkLifetime = time.Hour
	// SummaryDays is how many UTC days of requests a summary carries, the
	// last being today; QuietDays how many without any make a site quiet.
	SummaryDays = 7
	QuietDays   = 30
	// MaxSummaryQuery is the longest search, in characters.
	MaxSummaryQuery = 200
)

// View is a saved view of an owner's sites.
type View string

const (
	ViewPinned   View = "pinned"
	ViewExpiring View = "expiring"
	ViewPublic   View = "public"
	ViewQuiet    View = "quiet"
	ViewUntagged View = "untagged"
	ViewDown     View = "down"
)

// Views are the saved views in the order the navigator lists them.
var Views = []View{ViewPinned, ViewExpiring, ViewPublic, ViewQuiet, ViewUntagged, ViewDown}

// SummarySort orders a list of summaries. Every order breaks ties by name.
type SummarySort string

const (
	// SortDeploy is the live deploy's activation, newest first, with the
	// sites that have nothing live last.
	SortDeploy SummarySort = "deploy"
	SortName   SummarySort = "name"
	// SortRequests is requests over the SummaryDays, most first.
	SortRequests SummarySort = "requests"
	// SortLinks is working links, most first.
	SortLinks SummarySort = "links"
)

// SummaryFilter narrows a list of summaries; a site must match every part
// that is set.
type SummaryFilter struct {
	// GroupID keeps the sites in that group; NoGroup the ungrouped ones.
	GroupID string
	NoGroup bool
	// TagID keeps the sites with that tag.
	TagID string
	View  View
	// Query is the search box's text: tokens and free-text words.
	Query string
}

// SummaryCursor is where the next page starts: after the site with this
// sort key and name.
type SummaryCursor struct {
	Key  int64
	Name string
}

// SummaryPage is one page of summaries; Next is nil on the last page.
type SummaryPage struct {
	Items []store.SiteSummary
	Next  *SummaryCursor
}

// SiteCounts is how many sites each view, group and tag holds.
type SiteCounts struct {
	All     int
	Views   map[View]int
	Groups  []GroupCount // every group, in ListGroups order
	NoGroup int
	Tags    []TagCount // every tag, in ListTags order
}

type GroupCount struct {
	GroupID string
	Sites   int
}

type TagCount struct {
	TagID string
	Sites int
}

// summarySet is every one of an owner's sites with their groups and tags.
type summarySet struct {
	sites  []store.SiteSummary
	groups []store.Group
	tags   []store.Tag
	// groupKey and tagName find a group's folded name and a tag's name
	// by ID.
	groupKey map[string]string
	tagName  map[string]string
}

// summaryWindow is the window SPEC "Site summaries" sets, from now.
func (s *Service) summaryWindow() store.SummaryWindow {
	now := s.now()
	today := now.Truncate(24 * time.Hour)
	return store.SummaryWindow{
		Now:         now,
		ExpiringBy:  now.Add(ExpiringWithin),
		MinLifetime: ShortLinkLifetime,
		DaysFrom:    today.AddDate(0, 0, 1-SummaryDays),
		Days:        SummaryDays,
		ActiveFrom:  today.AddDate(0, 0, 1-QuietDays),
	}
}

// summaries reads the owner's sites, groups and tags: three repository
// calls, whatever the number of sites.
func (s *Service) summaries(ctx context.Context, ownerID string) (summarySet, error) {
	list, err := s.c.Sites.Summaries(ctx, ownerID, s.summaryWindow())
	if err != nil {
		return summarySet{}, fmt.Errorf("list site summaries: %w", err)
	}
	groups, err := s.c.Organisation.ListGroups(ctx, ownerID)
	if err != nil {
		return summarySet{}, fmt.Errorf("list groups: %w", err)
	}
	tags, err := s.c.Organisation.ListTags(ctx, ownerID)
	if err != nil {
		return summarySet{}, fmt.Errorf("list tags: %w", err)
	}
	set := summarySet{sites: list, groups: groups, tags: tags, groupKey: map[string]string{}, tagName: map[string]string{}}
	for _, g := range groups {
		set.groupKey[g.ID] = GroupNameKey(g.Name)
	}
	for _, t := range tags {
		set.tagName[t.ID] = t.Name
	}
	return set, nil
}

// SiteSummaries returns a page of the owner's sites matching f, in sort
// order, after the cursor (nil for the first page). A group or tag in f
// that is missing or another owner's is ErrNotFound.
func (s *Service) SiteSummaries(ctx context.Context, ownerID string, f SummaryFilter, sort SummarySort, after *SummaryCursor, limit int) (SummaryPage, error) {
	if utf8.RuneCountInString(f.Query) > MaxSummaryQuery {
		return SummaryPage{}, invalid("q", fmt.Sprintf("A search must be at most %d characters.", MaxSummaryQuery))
	}
	set, err := s.summaries(ctx, ownerID)
	if err != nil {
		return SummaryPage{}, err
	}
	if _, ok := set.groupKey[f.GroupID]; f.GroupID != "" && !ok {
		return SummaryPage{}, fmt.Errorf("site summaries: group: %w", ErrNotFound)
	}
	if _, ok := set.tagName[f.TagID]; f.TagID != "" && !ok {
		return SummaryPage{}, fmt.Errorf("site summaries: tag: %w", ErrNotFound)
	}
	q := parseQuery(f.Query)
	var matched []store.SiteSummary
	for _, site := range set.sites {
		if s.matches(set, f, q, site) {
			matched = append(matched, site)
		}
	}
	slices.SortFunc(matched, func(a, b store.SiteSummary) int {
		return cmp.Or(cmp.Compare(sortKey(sort, b), sortKey(sort, a)), strings.Compare(a.Name, b.Name))
	})
	if after != nil {
		i, _ := slices.BinarySearchFunc(matched, *after, func(site store.SiteSummary, c SummaryCursor) int {
			return cmp.Or(cmp.Compare(c.Key, sortKey(sort, site)), strings.Compare(site.Name, c.Name), -1)
		})
		matched = matched[i:]
	}
	page := SummaryPage{Items: matched}
	if len(matched) > limit {
		page.Items = matched[:limit]
		last := page.Items[limit-1]
		page.Next = &SummaryCursor{Key: sortKey(sort, last), Name: last.Name}
	}
	return page, nil
}

// ExpiringLinks returns up to limit of the owner's expiring links, soonest
// first: exactly the links each summary's ExpiringLinks counts.
func (s *Service) ExpiringLinks(ctx context.Context, ownerID string, limit int) ([]store.ExpiringLink, error) {
	links, err := s.c.Sites.ExpiringLinks(ctx, ownerID, s.summaryWindow(), limit)
	if err != nil {
		return nil, fmt.Errorf("list expiring links: %w", err)
	}
	return links, nil
}

// SiteCounts counts the owner's sites in each saved view, group and tag,
// as SiteSummaries would list them.
func (s *Service) SiteCounts(ctx context.Context, ownerID string) (SiteCounts, error) {
	set, err := s.summaries(ctx, ownerID)
	if err != nil {
		return SiteCounts{}, err
	}
	out := SiteCounts{All: len(set.sites), Views: map[View]int{}}
	inGroup, withTag := map[string]int{}, map[string]int{}
	for _, site := range set.sites {
		for _, v := range Views {
			if inView(v, site) {
				out.Views[v]++
			}
		}
		if site.GroupID == "" {
			out.NoGroup++
		} else {
			inGroup[site.GroupID]++
		}
		for _, id := range site.TagIDs {
			withTag[id]++
		}
	}
	for _, g := range set.groups {
		out.Groups = append(out.Groups, GroupCount{GroupID: g.ID, Sites: inGroup[g.ID]})
	}
	for _, t := range set.tags {
		out.Tags = append(out.Tags, TagCount{TagID: t.ID, Sites: withTag[t.ID]})
	}
	return out, nil
}

// sortKey is site's place in sort: larger first, then by name.
func sortKey(sort SummarySort, site store.SiteSummary) int64 {
	switch sort {
	case SortDeploy:
		if site.ActiveDeployID == "" {
			return -1
		}
		return site.ActivatedAt.UnixMilli()
	case SortRequests:
		var n int64
		for _, d := range site.Requests {
			n += d
		}
		return n
	case SortLinks:
		return int64(site.WorkingLinks)
	}
	return 0
}

// Down reports whether the operator has taken site down.
func Down(site store.SiteSummary) bool { return !site.TakenDownAt.IsZero() }

func inView(v View, site store.SiteSummary) bool {
	switch v {
	case ViewPinned:
		return !site.PinnedAt.IsZero()
	case ViewExpiring:
		return site.ExpiringLinks > 0
	case ViewPublic:
		return !Down(site) && site.Default.Visibility == store.Public
	case ViewQuiet:
		return !Down(site) && !site.Active
	case ViewUntagged:
		return len(site.TagIDs) == 0
	case ViewDown:
		return Down(site)
	}
	return false
}

// summaryQuery is a parsed search: every part must match.
type summaryQuery struct {
	words, tags, groups, is []string
}

// parseQuery splits q at spaces into tokens (tag:, group: and is: with a
// value) and free-text words, all folded as group names are.
func parseQuery(q string) summaryQuery {
	var out summaryQuery
	for _, w := range strings.Fields(q) {
		w = GroupNameKey(w)
		key, value, ok := strings.Cut(w, ":")
		switch {
		case ok && value != "" && key == "tag":
			out.tags = append(out.tags, value)
		case ok && value != "" && key == "group":
			out.groups = append(out.groups, value)
		case ok && value != "" && key == "is":
			out.is = append(out.is, value)
		default:
			out.words = append(out.words, w)
		}
	}
	return out
}

// groupSlug is a group's folded name with each run of spaces as one '-',
// as a group: token names it.
func groupSlug(key string) string { return strings.Join(strings.Fields(key), "-") }

func (s *Service) matches(set summarySet, f SummaryFilter, q summaryQuery, site store.SiteSummary) bool {
	switch {
	case f.NoGroup && site.GroupID != "",
		f.GroupID != "" && site.GroupID != f.GroupID,
		f.TagID != "" && !slices.Contains(site.TagIDs, f.TagID),
		f.View != "" && !inView(f.View, site):
		return false
	}
	hasTag := func(name string) bool {
		return slices.ContainsFunc(site.TagIDs, func(id string) bool { return set.tagName[id] == name })
	}
	for _, name := range q.tags {
		if !hasTag(name) {
			return false
		}
	}
	groupKey := set.groupKey[site.GroupID]
	for _, g := range q.groups {
		if g == "none" && site.GroupID != "" || g != "none" && (site.GroupID == "" || !strings.HasPrefix(groupSlug(groupKey), g)) {
			return false
		}
	}
	for _, is := range q.is {
		var ok bool
		switch is {
		case "public":
			ok = inView(ViewPublic, site)
		case "protected":
			ok = !Down(site) && site.Default.Visibility == store.Protected
		case "down":
			ok = Down(site)
		case "pinned":
			ok = !site.PinnedAt.IsZero()
		}
		if !ok {
			return false
		}
	}
	for _, w := range q.words {
		found := strings.Contains(s.Hostname(site.Name), w) || strings.Contains(groupKey, w) ||
			slices.ContainsFunc(site.Domains, func(d string) bool { return strings.Contains(d, w) }) ||
			slices.ContainsFunc(site.TagIDs, func(id string) bool { return strings.Contains(set.tagName[id], w) })
		if !found {
			return false
		}
	}
	return true
}
