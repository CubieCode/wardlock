// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Browser is the browser family an access-log entry's User-Agent was
// classified as when it was written (V5.4).
type Browser string

const (
	BrowserChrome  Browser = "chrome"
	BrowserSafari  Browser = "safari"
	BrowserFirefox Browser = "firefox"
	BrowserEdge    Browser = "edge"
	// BrowserTool is scripts and tools: command-line clients, HTTP
	// libraries, crawlers and other bots.
	BrowserTool  Browser = "tool"
	BrowserOther Browser = "other"
)

// CountryUnknown is the country of an entry whose IP the country database
// has no match for (V5.13).
const CountryUnknown = "unknown"

// Rollup limits (V5.4).
const (
	// MaxTrafficPaths is how many distinct paths a site keeps per hour:
	// the first that many it sees. Requests for any other path in that
	// hour, and for any path longer than MaxTrafficPathBytes, count under
	// the empty path.
	MaxTrafficPaths     = 50
	MaxTrafficPathBytes = 256
	// TrafficTopLimit is how many share links and paths a report lists.
	TrafficTopLimit = 10
	// backfillChunk is how many log entries the backfill reads at a time.
	backfillChunk = 5000
)

// TrafficCount is requests answered below 400 (Allowed) and the rest
// (Refused).
type TrafficCount struct {
	Allowed int64
	Refused int64
}

// TrafficScope narrows a report to at most one of a site, a group or a
// tag, by ID; the zero value is all the owner's sites. Groups and tags are
// read as they are now, so a site's history moves with it.
type TrafficScope struct {
	SiteID  string
	GroupID string
	TagID   string
}

// TrafficReport is an owner's requests over a range of hours.
type TrafficReport struct {
	// Hours lists each hour that had requests, oldest first.
	Hours []TrafficHour
	// Via, Sites, Groups, Tags and Browsers list each value with requests,
	// in a fixed order: by via, site name, group ID ("" for ungrouped,
	// first), tag ID and browser.
	Via      []TrafficVia
	Sites    []TrafficSite
	Groups   []TrafficGroup
	Tags     []TrafficTag
	Browsers []TrafficBrowser
	// Links and Paths are the TrafficTopLimit busiest, busiest first.
	Links []TrafficLink
	Paths []TrafficPath
	// OtherPaths counts the requests for paths the rollup did not keep.
	OtherPaths TrafficCount
	// Countries lists each country with requests, CountryUnknown among
	// them, busiest first (V5.13). Entries logged without a country are
	// not in it.
	Countries []TrafficCountry
}

type TrafficHour struct {
	Hour time.Time
	TrafficCount
}

type TrafficVia struct {
	Via AccessVia
	TrafficCount
}

type TrafficSite struct {
	SiteName string
	TrafficCount
}

type TrafficGroup struct {
	GroupID string // "" for the ungrouped sites
	TrafficCount
}

type TrafficTag struct {
	TagID string
	TrafficCount
}

type TrafficBrowser struct {
	Browser Browser
	TrafficCount
}

type TrafficCountry struct {
	Country string
	TrafficCount
}

type TrafficLink struct {
	SiteName string
	PublicID string
	// Label is the link's label while the link exists, revoked or not.
	Label string
	TrafficCount
}

type TrafficPath struct {
	SiteName string
	Path     string
	TrafficCount
}

// Traffic is the analytics rollup of the access log (V5.4). AccessLog's
// Append adds each batch to it in the same transaction.
type Traffic interface {
	// Backfill counts the access log into the rollup once: the first call
	// counts every entry of a site that exists, classifying each
	// User-Agent with classify, in one transaction, and returns how many
	// entries it counted and true. Every later call changes nothing and
	// returns false. Until it has run, Append adds entries to the log only,
	// so each entry is counted exactly once.
	Backfill(ctx context.Context, classify func(userAgent string) Browser, at time.Time) (int64, bool, error)
	// PruneHour deletes the oldest hour before cutoff from every rollup
	// table, on every site except a taken-down site or a suspended owner's,
	// and reports whether there was one. It is the retention job.
	PruneHour(ctx context.Context, cutoff time.Time) (bool, error)
	// Report sums ownerID's requests in scope over the hours from from up
	// to but not including to. A missing or foreign site, group or tag is
	// ErrNotFound.
	Report(ctx context.Context, ownerID string, scope TrafficScope, from, to time.Time) (TrafficReport, error)
}

// Traffic returns the SQLite Traffic repository.
func (db *DB) Traffic() Traffic { return trafficRepo{db} }

type trafficRepo struct{ db *DB }

// hourOf is the start of t's UTC hour in Unix milliseconds.
func hourOf(t time.Time) int64 { return ms(t.UTC().Truncate(time.Hour)) }

// refusedStatus is the rollup's outcome rule: 400 and above is refused.
func refusedStatus(status int) bool { return status >= 400 }

type trafficKey struct {
	siteID string
	hour   int64
	value  string
}

// trafficBatch is a batch's counts per table, each key once. paths keeps
// the order paths first appear in, which decides which are kept.
type trafficBatch struct {
	hours, links, browsers, countries map[trafficKey]*TrafficCount
	paths                             map[trafficKey]*TrafficCount
	pathOrder                         []trafficKey
}

func newTrafficBatch() *trafficBatch {
	return &trafficBatch{
		hours:     map[trafficKey]*TrafficCount{},
		links:     map[trafficKey]*TrafficCount{},
		browsers:  map[trafficKey]*TrafficCount{},
		countries: map[trafficKey]*TrafficCount{},
		paths:     map[trafficKey]*TrafficCount{},
	}
}

func (b *trafficBatch) add(e AccessEntry) {
	hour := hourOf(e.OccurredAt)
	bump := func(m map[trafficKey]*TrafficCount, value string) *TrafficCount {
		k := trafficKey{e.SiteID, hour, value}
		c, ok := m[k]
		if !ok {
			c = &TrafficCount{}
			m[k] = c
		}
		if refusedStatus(e.Status) {
			c.Refused++
		} else {
			c.Allowed++
		}
		return c
	}
	bump(b.hours, string(e.Via))
	if e.LinkPublicID != "" {
		bump(b.links, e.LinkPublicID)
	}
	browser := e.Browser
	if browser == "" {
		browser = BrowserOther
	}
	bump(b.browsers, string(browser))
	if e.Country != "" {
		bump(b.countries, e.Country)
	}
	path := e.Path
	if len(path) > MaxTrafficPathBytes {
		path = ""
	}
	k := trafficKey{e.SiteID, hour, path}
	if _, seen := b.paths[k]; !seen {
		b.pathOrder = append(b.pathOrder, k)
	}
	bump(b.paths, path)
}

// write adds the batch's counts to the rollup inside q's transaction.
func (b *trafficBatch) write(ctx context.Context, q *sqlcgen.Queries) error {
	for k, c := range b.hours {
		if err := q.AddTrafficHour(ctx, sqlcgen.AddTrafficHourParams{SiteID: k.siteID, Hour: k.hour, Via: k.value, Allowed: c.Allowed, Refused: c.Refused}); err != nil {
			return mapErr("add traffic hour", err)
		}
	}
	for k, c := range b.links {
		if err := q.AddTrafficLink(ctx, sqlcgen.AddTrafficLinkParams{SiteID: k.siteID, Hour: k.hour, LinkPublicID: k.value, Allowed: c.Allowed, Refused: c.Refused}); err != nil {
			return mapErr("add traffic link", err)
		}
	}
	for k, c := range b.browsers {
		if err := q.AddTrafficBrowser(ctx, sqlcgen.AddTrafficBrowserParams{SiteID: k.siteID, Hour: k.hour, Browser: k.value, Allowed: c.Allowed, Refused: c.Refused}); err != nil {
			return mapErr("add traffic browser", err)
		}
	}
	for k, c := range b.countries {
		if err := q.AddTrafficCountry(ctx, sqlcgen.AddTrafficCountryParams{SiteID: k.siteID, Hour: k.hour, Country: k.value, Allowed: c.Allowed, Refused: c.Refused}); err != nil {
			return mapErr("add traffic country", err)
		}
	}
	for _, k := range b.pathOrder {
		c := b.paths[k]
		p := sqlcgen.AddTrafficPathParams{SiteID: k.siteID, Hour: k.hour, Path: k.value, Allowed: c.Allowed, Refused: c.Refused, MaxPaths: MaxTrafficPaths}
		n, err := q.AddTrafficPath(ctx, p)
		if err == nil && n == 0 && p.Path != "" {
			// Past the cap (or the site is gone, when this adds nothing
			// either): count it with every other path.
			p.Path = ""
			_, err = q.AddTrafficPath(ctx, p)
		}
		if err != nil {
			return mapErr("add traffic path", err)
		}
	}
	return nil
}

// addTraffic counts entries into the rollup inside q's transaction, once
// the backfill has run.
func addTraffic(ctx context.Context, q *sqlcgen.Queries, entries []AccessEntry) error {
	done, err := q.TrafficBackfilled(ctx)
	if err != nil {
		return mapErr("check traffic backfill", err)
	}
	if done == 0 {
		return nil
	}
	b := newTrafficBatch()
	for _, e := range entries {
		b.add(e)
	}
	return b.write(ctx, q)
}

func (r trafficRepo) Backfill(ctx context.Context, classify func(string) Browser, at time.Time) (int64, bool, error) {
	var total int64
	var ran bool
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		// Claiming first takes the write lock, so no batch lands between
		// reading the log and marking it counted.
		n, err := q.MarkTrafficBackfilled(ctx, ms(at))
		if err != nil {
			return mapErr("claim traffic backfill", err)
		}
		if n == 0 {
			return nil
		}
		ran = true
		afterAt, afterID := int64(-1), ""
		for {
			rows, err := q.ListAccessLogForRollup(ctx, sqlcgen.ListAccessLogForRollupParams{AfterOccurredAt: afterAt, AfterID: afterID, RowLimit: backfillChunk})
			if err != nil {
				return mapErr("read access log for backfill", err)
			}
			b := newTrafficBatch()
			for _, row := range rows {
				b.add(AccessEntry{
					OccurredAt:   fromMs(row.OccurredAt),
					SiteID:       row.SiteID,
					Path:         row.Path,
					Status:       int(row.Status),
					Via:          AccessVia(row.Via),
					LinkPublicID: row.LinkPublicID.String,
					Country:      row.Country.String,
					Browser:      classify(row.UserAgent.String),
				})
			}
			if err := b.write(ctx, q); err != nil {
				return err
			}
			total += int64(len(rows))
			if len(rows) < backfillChunk {
				return nil
			}
			last := rows[len(rows)-1]
			afterAt, afterID = last.OccurredAt, last.ID
		}
	})
	if err != nil {
		return 0, false, fmt.Errorf("backfill traffic: %w", err)
	}
	return total, ran, nil
}

func (r trafficRepo) PruneHour(ctx context.Context, cutoff time.Time) (bool, error) {
	var pruned bool
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		hour, err := q.OldestPrunableTrafficHour(ctx, ms(cutoff))
		if err != nil {
			return mapErr("find prunable traffic hour", err)
		}
		if hour < 0 {
			return nil
		}
		pruned = true
		for _, prune := range []func(context.Context, int64) (int64, error){
			q.PruneTrafficHours, q.PruneTrafficLinks, q.PruneTrafficPaths, q.PruneTrafficBrowsers, q.PruneTrafficCountries,
		} {
			if _, err := prune(ctx, hour); err != nil {
				return mapErr("prune traffic", err)
			}
		}
		return nil
	})
	return pruned, err
}

func (r trafficRepo) Report(ctx context.Context, ownerID string, scope TrafficScope, from, to time.Time) (TrafficReport, error) {
	if err := r.checkScope(ctx, ownerID, scope); err != nil {
		return TrafficReport{}, err
	}
	q := r.db.q
	site, group, tag := nullString(scope.SiteID), nullString(scope.GroupID), nullString(scope.TagID)
	fromHour, toHour := ms(from), ms(to)
	var out TrafficReport

	hours, err := q.TrafficByHour(ctx, sqlcgen.TrafficByHourParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("traffic by hour", err)
	}
	for _, row := range hours {
		out.Hours = append(out.Hours, TrafficHour{fromMs(row.Hour), TrafficCount{row.Allowed, row.Refused}})
	}
	via, err := q.TrafficByVia(ctx, sqlcgen.TrafficByViaParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("traffic by via", err)
	}
	for _, row := range via {
		out.Via = append(out.Via, TrafficVia{AccessVia(row.Via), TrafficCount{row.Allowed, row.Refused}})
	}
	sites, err := q.TrafficBySite(ctx, sqlcgen.TrafficBySiteParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("traffic by site", err)
	}
	for _, row := range sites {
		out.Sites = append(out.Sites, TrafficSite{row.Name, TrafficCount{row.Allowed, row.Refused}})
	}
	groups, err := q.TrafficByGroup(ctx, sqlcgen.TrafficByGroupParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("traffic by group", err)
	}
	for _, row := range groups {
		out.Groups = append(out.Groups, TrafficGroup{row.GroupID.String, TrafficCount{row.Allowed, row.Refused}})
	}
	tags, err := q.TrafficByTag(ctx, sqlcgen.TrafficByTagParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("traffic by tag", err)
	}
	for _, row := range tags {
		out.Tags = append(out.Tags, TrafficTag{row.TagID, TrafficCount{row.Allowed, row.Refused}})
	}
	browsers, err := q.TrafficByBrowser(ctx, sqlcgen.TrafficByBrowserParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("traffic by browser", err)
	}
	for _, row := range browsers {
		out.Browsers = append(out.Browsers, TrafficBrowser{Browser(row.Browser), TrafficCount{row.Allowed, row.Refused}})
	}
	links, err := q.TopTrafficLinks(ctx, sqlcgen.TopTrafficLinksParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour, RowLimit: TrafficTopLimit})
	if err != nil {
		return TrafficReport{}, mapErr("top traffic links", err)
	}
	for _, row := range links {
		out.Links = append(out.Links, TrafficLink{row.Name, row.LinkPublicID, row.Label, TrafficCount{row.Allowed, row.Refused}})
	}
	paths, err := q.TopTrafficPaths(ctx, sqlcgen.TopTrafficPathsParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour, RowLimit: TrafficTopLimit})
	if err != nil {
		return TrafficReport{}, mapErr("top traffic paths", err)
	}
	for _, row := range paths {
		out.Paths = append(out.Paths, TrafficPath{row.Name, row.Path, TrafficCount{row.Allowed, row.Refused}})
	}
	other, err := q.TrafficOtherPaths(ctx, sqlcgen.TrafficOtherPathsParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("other traffic paths", err)
	}
	out.OtherPaths = TrafficCount{other.Allowed, other.Refused}
	countries, err := q.TrafficByCountry(ctx, sqlcgen.TrafficByCountryParams{OwnerID: ownerID, SiteID: site, GroupID: group, TagID: tag, FromHour: fromHour, ToHour: toHour})
	if err != nil {
		return TrafficReport{}, mapErr("traffic by country", err)
	}
	for _, row := range countries {
		out.Countries = append(out.Countries, TrafficCountry{row.Country, TrafficCount{row.Allowed, row.Refused}})
	}
	return out, nil
}

// checkScope answers ErrNotFound for a scope naming more than one thing
// or a site, group or tag that is missing or not ownerID's.
func (r trafficRepo) checkScope(ctx context.Context, ownerID string, s TrafficScope) error {
	named := 0
	var err error
	if s.SiteID != "" {
		named++
		_, err = r.db.q.SiteOwnedBy(ctx, sqlcgen.SiteOwnedByParams{ID: s.SiteID, OwnerID: ownerID})
	}
	if s.GroupID != "" && err == nil {
		named++
		_, err = r.db.q.GetGroup(ctx, sqlcgen.GetGroupParams{ID: s.GroupID, OwnerID: ownerID})
	}
	if s.TagID != "" && err == nil {
		named++
		_, err = r.db.q.GetTag(ctx, sqlcgen.GetTagParams{ID: s.TagID, OwnerID: ownerID})
	}
	if named > 1 {
		return fmt.Errorf("traffic report: one scope at most: %w", ErrNotFound)
	}
	return mapErr("traffic report", err)
}
