// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // Pacific/Auckland on systems without a zoneinfo database.

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/store"
)

// shape is how a site's daily requests move over its life.
type shape int

const (
	shapeSteady shape = iota
	shapeLaunch       // quiet previews, a spike on launch day, then a plateau
	shapeRising
	shapeFading
	shapeDead // steady until deadAgeDays ago, then nothing
)

// trafficPlan is a site's request pattern.
type trafficPlan struct {
	daily         float64 // requests on an ordinary day
	shape         shape
	launchAgeDays float64 // shapeLaunch: days ago the launch was
	deadAgeDays   float64 // shapeDead: days ago the last request was
	work          bool    // quiet at weekends
	local         float64 // share of requests from New Zealand on top of the usual mix
	tools         bool    // more scripts and crawlers than browsers
}

// siteTraffic is everything the generator needs about one created site.
type siteTraffic struct {
	id, name, host string
	created        time.Time
	plan           trafficPlan
	paths          []string
	// protectedPrefix is the path the protected policy covers: "/" for
	// the whole site, "" for a public site.
	protectedPrefix string
	// ways are how visitors are let in past the protected policy.
	ways  []store.AccessVia
	links []linkVisit
}

// linkVisit is a share link visitors can arrive with.
type linkVisit struct {
	publicID string
	created  time.Time
	expires  time.Time // zero: never
}

// missingPath is the page that does not exist on any demo site.
const missingPath = "/missing-page"

var nz = mustZone("Pacific/Auckland")

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// hourWeights are relative requests per New Zealand local hour.
var hourWeights = [24]float64{
	0.15, 0.1, 0.08, 0.07, 0.08, 0.15, 0.35, 0.7, 1.1, 1.35, 1.4, 1.35,
	1.25, 1.3, 1.35, 1.3, 1.15, 0.95, 0.8, 0.75, 0.7, 0.6, 0.45, 0.25,
}

var meanHourWeight = func() float64 {
	var sum float64
	for _, w := range hourWeights {
		sum += w
	}
	return sum / 24
}()

type weighted[T any] struct {
	value  T
	weight float64
}

func pick[T any](r *rand.Rand, items []weighted[T]) T {
	var total float64
	for _, it := range items {
		total += it.weight
	}
	x := r.Float64() * total
	for _, it := range items {
		if x < it.weight {
			return it.value
		}
		x -= it.weight
	}
	return items[len(items)-1].value
}

// countries is the usual mix, with a few at the map's edges (IS, BR) and
// addresses the country database has no match for.
var countries = []weighted[string]{
	{"NZ", 34}, {"AU", 16}, {"US", 14}, {"GB", 8}, {"DE", 4}, {"CA", 3.5},
	{"JP", 3}, {"SG", 2.5}, {"FR", 2}, {"IN", 2}, {"NL", 1.5}, {"IE", 1.2},
	{"ZA", 0.8}, {"BR", 1}, {"IS", 0.5}, {store.CountryUnknown, 2},
}

var browserAgents = []weighted[string]{
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36", 26},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36", 12},
	{"Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36", 8},
	{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1", 18},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Safari/605.1.15", 8},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:143.0) Gecko/20100101 Firefox/143.0", 6},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0", 7},
	{"Mozilla/5.0 (Linux; Android 15; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/28.0 Chrome/130.0.0.0 Mobile Safari/537.36", 2},
}

var toolAgents = []weighted[string]{
	{"curl/8.15.0", 4},
	{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", 3},
	{"Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)", 2},
	{"python-requests/2.32.5", 1.5},
	{"Go-http-client/2.0", 1},
}

// agentFor picks a User-Agent: mostly browsers, a few tools, and many more
// tools on a site that serves scripts.
func agentFor(r *rand.Rand, tools bool) string {
	share := 0.06
	if tools {
		share = 0.45
	}
	if r.Float64() < share {
		return pick(r, toolAgents)
	}
	return pick(r, browserAgents)
}

var (
	docIPv4 = netip.MustParsePrefix("198.18.0.0/15")
	docIPv6 = netip.MustParsePrefix("2001:db8::/32")
)

// documentationIP returns a random address in 198.18.0.0/15 or
// 2001:db8::/32, truncated as the access log stores it.
func documentationIP(r *rand.Rand) string {
	var addr netip.Addr
	if r.Float64() < 0.85 {
		var random [8]byte
		binary.BigEndian.PutUint64(random[:], r.Uint64())
		b := docIPv4.Addr().As4()
		b[1] += random[0] & 1
		b[2], b[3] = random[1], random[2]
		addr = netip.AddrFrom4(b)
	} else {
		b := docIPv6.Addr().As16()
		binary.BigEndian.PutUint64(b[4:12], r.Uint64())
		binary.BigEndian.PutUint32(b[12:16], r.Uint32())
		addr = netip.AddrFrom16(b)
	}
	return accesslog.DefaultTruncation.Truncate(addr.String())
}

// factor is the plan's multiple of its daily requests daysAgo days before
// now, for a site created ageDays ago.
func (p trafficPlan) factor(daysAgo, ageDays float64) float64 {
	switch p.shape {
	case shapeLaunch:
		if daysAgo > p.launchAgeDays {
			return 0.12
		}
		return 1 + 9*math.Exp(-(p.launchAgeDays-daysAgo)/2.5)
	case shapeRising:
		return 0.2 + 1.6*(1-daysAgo/ageDays)
	case shapeFading:
		return 0.06 + 1.9*math.Exp(-(ageDays-daysAgo)/18)
	case shapeDead:
		if daysAgo < p.deadAgeDays {
			return 0
		}
	}
	return 1
}

// poisson draws a count with mean lambda (Knuth's method; lambda stays
// well under a few hundred here).
func poisson(r *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	limit, n, p := math.Exp(-lambda), 0, r.Float64()
	for p > limit {
		n++
		p *= r.Float64()
	}
	return n
}

// generate returns the sites' requests from from to now, oldest first. The
// same seed gives the same requests, apart from the entries' IDs.
func generate(sites []siteTraffic, from, now time.Time, seed uint64) []store.AccessEntry {
	var entries []store.AccessEntry
	for i, s := range sites {
		r := rand.New(rand.NewPCG(seed, uint64(i)+1)) //nolint:gosec // G404: synthetic traffic must repeat for a seed
		entries = append(entries, s.requests(r, from, now)...)
	}
	slices.SortStableFunc(entries, func(a, b store.AccessEntry) int { return a.OccurredAt.Compare(b.OccurredAt) })
	return entries
}

func (s siteTraffic) requests(r *rand.Rand, from, now time.Time) []store.AccessEntry {
	start := from
	if s.created.After(start) {
		start = s.created
	}
	ageDays := now.Sub(s.created).Hours() / 24
	var out []store.AccessEntry
	noise, noiseDay := 1.0, ""
	for hour := start.Truncate(time.Hour); hour.Before(now); hour = hour.Add(time.Hour) {
		local := hour.In(nz)
		if day := local.Format(time.DateOnly); day != noiseDay {
			noise, noiseDay = 0.75+0.5*r.Float64(), day
		}
		weekend := 1.0
		if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
			weekend = 0.85
			if s.plan.work {
				weekend = 0.25
			}
		}
		daysAgo := now.Sub(hour).Hours() / 24
		lambda := s.plan.daily / 24 * s.plan.factor(daysAgo, ageDays) * weekend * noise * hourWeights[local.Hour()] / meanHourWeight
		for range poisson(r, lambda) {
			at := hour.Add(time.Duration(r.Int64N(int64(time.Hour))))
			if at.Before(s.created) || !at.Before(now) {
				continue
			}
			out = append(out, s.request(r, at))
		}
	}
	return out
}

// request makes one access-log entry at at.
func (s siteTraffic) request(r *rand.Rand, at time.Time) store.AccessEntry {
	path := s.paths[0]
	switch x := r.Float64(); {
	case x < 0.025:
		path = missingPath
	case x < 0.5 && len(s.paths) > 1:
		path = s.paths[1+r.IntN(len(s.paths)-1)]
	}
	country := pick(r, countries)
	if r.Float64() < s.plan.local {
		country = "NZ"
	}
	ua := agentFor(r, s.plan.tools)
	e := store.AccessEntry{
		ID:         store.NewID(),
		OccurredAt: at,
		SiteID:     s.id,
		Host:       s.host,
		Path:       path,
		Status:     200,
		Via:        store.ViaPublic,
		IP:         documentationIP(r),
		UserAgent:  ua,
		Country:    country,
		Browser:    accesslog.Classify(ua),
	}
	if path == missingPath {
		e.Status = 404
	}
	if s.protectedPrefix == "" || !strings.HasPrefix(path, s.protectedPrefix) {
		return e
	}
	// Refused: the login page (401), an address the allowlist does not let
	// in (403), or too many attempts (429).
	switch x := r.Float64(); {
	case x < 0.2:
		e.Status, e.Via = 401, store.ViaNone
		return e
	case x < 0.25:
		e.Status, e.Via = 403, store.ViaNone
		return e
	case x < 0.26:
		e.Status, e.Via = 429, store.ViaNone
		return e
	}
	e.Via = s.ways[r.IntN(len(s.ways))]
	if e.Via == store.ViaLink {
		live := s.liveLinks(at)
		if len(live) == 0 {
			// No link yet: another way in, or the login page.
			e.Status, e.Via = 401, store.ViaNone
			for _, w := range s.ways {
				if w != store.ViaLink {
					e.Status, e.Via = 200, w
				}
			}
			if path == missingPath && e.Via != store.ViaNone {
				e.Status = 404
			}
		} else {
			// Older links have had longer to spread: favour the first.
			e.LinkPublicID = live[min(r.IntN(len(live)+1), len(live)-1)].publicID
		}
	}
	return e
}

// liveLinks are the links that work at at.
func (s siteTraffic) liveLinks(at time.Time) []linkVisit {
	var live []linkVisit
	for _, l := range s.links {
		if !l.created.After(at) && (l.expires.IsZero() || l.expires.After(at)) {
			live = append(live, l)
		}
	}
	return live
}

// backdatedLink is when a link should look created: ageDays before now,
// unless it expires within a week of now, when a long history would make it
// look older than its lifetime (a 30-minute link created weeks ago), so it
// keeps created.
func backdatedLink(created, expires, now time.Time, ageDays float64) time.Time {
	if !expires.IsZero() && expires.Sub(now) <= 7*24*time.Hour {
		return created
	}
	return now.Add(-days(ageDays))
}

func days(n float64) time.Duration { return time.Duration(n * float64(24*time.Hour)) }

// summary counts entries by a key, for printing.
func summary(entries []store.AccessEntry, key func(store.AccessEntry) string) string {
	counts := map[string]int{}
	for _, e := range entries {
		counts[key(e)]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return counts[b] - counts[a] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}
