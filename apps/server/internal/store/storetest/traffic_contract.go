// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// testBrowser stands in for the access-log writer's classifier: the store
// counts whatever family it is given.
func testBrowser(userAgent string) store.Browser {
	switch {
	case strings.HasPrefix(userAgent, "curl"):
		return store.BrowserTool
	case strings.Contains(userAgent, "Firefox"):
		return store.BrowserFirefox
	case userAgent == "":
		return store.BrowserOther
	}
	return store.BrowserChrome
}

// allTime covers every hour the tests write.
var allFrom, allTo = now.Add(-1000 * 24 * time.Hour), now.Add(1000 * 24 * time.Hour)

// recount is the rollup computed straight from entries, as Report should
// answer it for all of an owner's sites.
func recount(entries []store.AccessEntry, siteNames map[string]string) store.TrafficReport {
	type key struct{ a, b string }
	hours := map[int64]*store.TrafficCount{}
	via := map[string]*store.TrafficCount{}
	sites := map[string]*store.TrafficCount{}
	links := map[key]*store.TrafficCount{}
	paths := map[key]*store.TrafficCount{}
	browsers := map[string]*store.TrafficCount{}
	bump := func(c *store.TrafficCount, status int) {
		if status >= 400 {
			c.Refused++
		} else {
			c.Allowed++
		}
	}
	get := func(m map[string]*store.TrafficCount, k string) *store.TrafficCount {
		if m[k] == nil {
			m[k] = &store.TrafficCount{}
		}
		return m[k]
	}
	getKey := func(m map[key]*store.TrafficCount, k key) *store.TrafficCount {
		if m[k] == nil {
			m[k] = &store.TrafficCount{}
		}
		return m[k]
	}
	for _, e := range entries {
		name, ok := siteNames[e.SiteID]
		if !ok {
			continue
		}
		h := e.OccurredAt.UTC().Truncate(time.Hour).UnixMilli()
		if hours[h] == nil {
			hours[h] = &store.TrafficCount{}
		}
		bump(hours[h], e.Status)
		bump(get(via, string(e.Via)), e.Status)
		bump(get(sites, name), e.Status)
		if e.LinkPublicID != "" {
			bump(getKey(links, key{name, e.LinkPublicID}), e.Status)
		}
		bump(getKey(paths, key{name, e.Path}), e.Status)
		bump(get(browsers, string(testBrowser(e.UserAgent))), e.Status)
	}
	var out store.TrafficReport
	for _, h := range slices.Sorted(maps.Keys(hours)) {
		out.Hours = append(out.Hours, store.TrafficHour{Hour: time.UnixMilli(h).UTC(), TrafficCount: *hours[h]})
	}
	for _, v := range slices.Sorted(maps.Keys(via)) {
		out.Via = append(out.Via, store.TrafficVia{Via: store.AccessVia(v), TrafficCount: *via[v]})
	}
	for _, s := range slices.Sorted(maps.Keys(sites)) {
		out.Sites = append(out.Sites, store.TrafficSite{SiteName: s, TrafficCount: *sites[s]})
	}
	for _, b := range slices.Sorted(maps.Keys(browsers)) {
		out.Browsers = append(out.Browsers, store.TrafficBrowser{Browser: store.Browser(b), TrafficCount: *browsers[b]})
	}
	busiest := func(m map[key]*store.TrafficCount) []key {
		ks := slices.Collect(maps.Keys(m))
		slices.SortFunc(ks, func(x, y key) int {
			tx, ty := m[x].Allowed+m[x].Refused, m[y].Allowed+m[y].Refused
			if tx != ty {
				return int(ty - tx)
			}
			return strings.Compare(x.a+"\x00"+x.b, y.a+"\x00"+y.b)
		})
		return ks[:min(len(ks), store.TrafficTopLimit)]
	}
	for _, k := range busiest(links) {
		out.Links = append(out.Links, store.TrafficLink{SiteName: k.a, PublicID: k.b, TrafficCount: *links[k]})
	}
	for _, k := range busiest(paths) {
		out.Paths = append(out.Paths, store.TrafficPath{SiteName: k.a, Path: k.b, TrafficCount: *paths[k]})
	}
	return out
}

// compareReports fails unless got matches want in every list recount
// fills; groups, tags, labels and other paths are checked case by case.
func compareReports(t *testing.T, got, want store.TrafficReport) {
	t.Helper()
	if !slices.Equal(got.Hours, want.Hours) {
		t.Errorf("Hours = %+v\nwant %+v", got.Hours, want.Hours)
	}
	if !slices.Equal(got.Via, want.Via) {
		t.Errorf("Via = %+v, want %+v", got.Via, want.Via)
	}
	if !slices.Equal(got.Sites, want.Sites) {
		t.Errorf("Sites = %+v, want %+v", got.Sites, want.Sites)
	}
	if !slices.Equal(got.Browsers, want.Browsers) {
		t.Errorf("Browsers = %+v, want %+v", got.Browsers, want.Browsers)
	}
	gotLinks := slices.Clone(got.Links)
	for i := range gotLinks {
		gotLinks[i].Label = ""
	}
	if !slices.Equal(gotLinks, want.Links) {
		t.Errorf("Links = %+v, want %+v", gotLinks, want.Links)
	}
	if !slices.Equal(got.Paths, want.Paths) {
		t.Errorf("Paths = %+v, want %+v", got.Paths, want.Paths)
	}
}

func (f *fixture) entry(siteID string, at time.Time, status int, via store.AccessVia, link, path, userAgent string) store.AccessEntry {
	return store.AccessEntry{
		ID: store.NewID(), OccurredAt: at, SiteID: siteID, Host: "docs.wardlock.localhost", Path: path,
		Status: status, Via: via, LinkPublicID: link, UserAgent: userAgent, Browser: testBrowser(userAgent),
	}
}

func (f *fixture) backfill() {
	f.t.Helper()
	_, _, err := f.r.Traffic.Backfill(f.t.Context(), testBrowser, now)
	f.must(err)
}

func (f *fixture) report(ownerID string, scope store.TrafficScope) store.TrafficReport {
	f.t.Helper()
	r, err := f.r.Traffic.Report(f.t.Context(), ownerID, scope, allFrom, allTo)
	f.must(err)
	return r
}

func trafficContract(t *testing.T, open Opener) {
	t.Run("BackfillCountsOnceThenAppendCounts", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		before := []store.AccessEntry{
			f.entry(s.ID, now, 200, store.ViaPublic, "", "/", "Mozilla/5.0 Firefox/130"),
			f.entry(s.ID, now.Add(time.Hour), 401, store.ViaNone, "", "/admin/", "curl/8"),
		}
		f.must(f.r.AccessLog.Append(t.Context(), before))
		// Before the backfill, batches only log.
		if r := f.report(o.ID, store.TrafficScope{}); len(r.Hours) != 0 {
			t.Fatalf("Report() before backfill = %+v, want nothing", r.Hours)
		}
		n, ran, err := f.r.Traffic.Backfill(t.Context(), testBrowser, now)
		if err != nil || !ran || n != 2 {
			t.Fatalf("Backfill() = %d, %v, %v; want 2, true", n, ran, err)
		}
		// Idempotent: a second run counts nothing again.
		if n, ran, err := f.r.Traffic.Backfill(t.Context(), testBrowser, now); err != nil || ran || n != 0 {
			t.Fatalf("second Backfill() = %d, %v, %v; want 0, false", n, ran, err)
		}
		after := []store.AccessEntry{f.entry(s.ID, now, 200, store.ViaPublic, "", "/", "")}
		f.must(f.r.AccessLog.Append(t.Context(), after))
		names := map[string]string{s.ID: s.Name}
		compareReports(t, f.report(o.ID, store.TrafficScope{}), recount(append(before, after...), names))
	})
	t.Run("RollupEqualsRecount", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		a, b := f.site(o.ID), f.site(o.ID)
		la, lb := f.link(o.ID, a.ID), f.link(o.ID, b.ID)
		var entries []store.AccessEntry
		agents := []string{"", "curl/8", "Mozilla/5.0 Firefox/130", "Mozilla/5.0 Chrome/129"}
		statuses := []int{200, 304, 401, 404, 500}
		for i := range 400 {
			site, link, via := a, la.PublicID, store.ViaLink
			if i%3 == 0 {
				site, link = b, lb.PublicID
			}
			if i%5 == 0 {
				link, via = "", store.ViaPublic
			}
			at := now.Add(time.Duration(i%30)*time.Hour + time.Duration(i)*time.Second)
			entries = append(entries, f.entry(site.ID, at, statuses[i%len(statuses)], via, link, fmt.Sprintf("/p%d", i%12), agents[i%len(agents)]))
		}
		// An entry of a site that no longer exists is logged, not counted.
		entries = append(entries, f.entry(store.NewID(), now, 200, store.ViaPublic, "", "/", ""))
		// Half before the backfill, half after, in uneven batches.
		f.must(f.r.AccessLog.Append(t.Context(), entries[:150]))
		f.must(f.r.AccessLog.Append(t.Context(), entries[150:200]))
		f.backfill()
		for i := 200; i < len(entries); i += 37 {
			f.must(f.r.AccessLog.Append(t.Context(), entries[i:min(i+37, len(entries))]))
		}
		got := f.report(o.ID, store.TrafficScope{})
		compareReports(t, got, recount(entries, map[string]string{a.ID: a.Name, b.ID: b.Name}))
		for _, l := range got.Links {
			if l.Label != la.Label {
				t.Errorf("link %s label = %q, want %q", l.PublicID, l.Label, la.Label)
			}
		}
		if got.OtherPaths != (store.TrafficCount{}) {
			t.Errorf("OtherPaths = %+v, want none under the cap", got.OtherPaths)
		}
		// One site's scope is that site's recount.
		compareReports(t, f.report(o.ID, store.TrafficScope{SiteID: b.ID}), recount(entries, map[string]string{b.ID: b.Name}))
	})
	t.Run("TopPathsCap", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		f.backfill()
		var batch []store.AccessEntry
		for i := range store.MaxTrafficPaths + 10 {
			batch = append(batch, f.entry(s.ID, now, 200, store.ViaPublic, "", fmt.Sprintf("/page-%02d", i), ""))
		}
		// A kept path keeps counting; a path past the cap or too long is
		// counted with every other path.
		batch = append(batch,
			f.entry(s.ID, now, 200, store.ViaPublic, "", "/page-00", ""),
			f.entry(s.ID, now, 404, store.ViaPublic, "", "/"+strings.Repeat("a", store.MaxTrafficPathBytes), ""),
		)
		f.must(f.r.AccessLog.Append(t.Context(), batch[:30]))
		f.must(f.r.AccessLog.Append(t.Context(), batch[30:]))
		// The next hour starts a fresh set of paths.
		f.must(f.r.AccessLog.Append(t.Context(), []store.AccessEntry{f.entry(s.ID, now.Add(time.Hour), 200, store.ViaPublic, "", "/page-59", "")}))
		r := f.report(o.ID, store.TrafficScope{})
		if want := (store.TrafficCount{Allowed: 10, Refused: 1}); r.OtherPaths != want {
			t.Errorf("OtherPaths = %+v, want %+v", r.OtherPaths, want)
		}
		if len(r.Paths) != store.TrafficTopLimit || r.Paths[0].Path != "/page-00" || r.Paths[0].Allowed != 2 {
			t.Errorf("Paths = %+v, want /page-00 first with 2", r.Paths)
		}
		first, err := f.r.Traffic.Report(t.Context(), o.ID, store.TrafficScope{}, now.Truncate(time.Hour), now.Truncate(time.Hour).Add(time.Hour))
		f.must(err)
		if kept := first.Hours[0].Allowed + first.Hours[0].Refused - first.OtherPaths.Allowed - first.OtherPaths.Refused; kept != store.MaxTrafficPaths+1 {
			t.Errorf("requests on kept paths = %d, want %d", kept, store.MaxTrafficPaths+1)
		}
		next, err := f.r.Traffic.Report(t.Context(), o.ID, store.TrafficScope{}, now.Truncate(time.Hour).Add(time.Hour), allTo)
		f.must(err)
		if len(next.Paths) != 1 || next.Paths[0].Path != "/page-59" {
			t.Errorf("next hour Paths = %+v, want /page-59 kept", next.Paths)
		}
	})
	t.Run("Scopes", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		a, b, c := f.site(o.ID), f.site(o.ID), f.site(o.ID)
		g := f.group(o.ID, "Clients")
		red, blue := f.tag(o.ID, "red"), f.tag(o.ID, "blue")
		_, err := f.r.Organisation.Organise(t.Context(), o.ID, []string{a.Name, b.Name}, store.OrganiseChange{SetGroup: true, GroupID: g.ID, AddTagIDs: []string{red.ID}})
		f.must(err)
		_, err = f.r.Organisation.Organise(t.Context(), o.ID, []string{a.Name}, store.OrganiseChange{AddTagIDs: []string{blue.ID}})
		f.must(err)
		foreignSite, foreignGroup, foreignTag := f.site(other.ID), f.group(other.ID, "Clients"), f.tag(other.ID, "red")
		f.backfill()
		var entries []store.AccessEntry
		for i, s := range []store.Site{a, b, b, c, c, c, foreignSite} {
			entries = append(entries, f.entry(s.ID, now.Add(time.Duration(i)*time.Minute), 200, store.ViaPublic, "", "/", ""))
		}
		f.must(f.r.AccessLog.Append(t.Context(), entries))
		total := func(r store.TrafficReport) (n int64) {
			for _, h := range r.Hours {
				n += h.Allowed + h.Refused
			}
			return n
		}
		for _, tc := range []struct {
			name  string
			scope store.TrafficScope
			want  int64
		}{
			{"all", store.TrafficScope{}, 6},
			{"site", store.TrafficScope{SiteID: c.ID}, 3},
			{"group", store.TrafficScope{GroupID: g.ID}, 3},
			{"tag red", store.TrafficScope{TagID: red.ID}, 3},
			{"tag blue", store.TrafficScope{TagID: blue.ID}, 1},
		} {
			if got := total(f.report(o.ID, tc.scope)); got != tc.want {
				t.Errorf("%s: total = %d, want %d", tc.name, got, tc.want)
			}
		}
		r := f.report(o.ID, store.TrafficScope{})
		wantGroups := []store.TrafficGroup{{GroupID: "", TrafficCount: store.TrafficCount{Allowed: 3}}, {GroupID: g.ID, TrafficCount: store.TrafficCount{Allowed: 3}}}
		if !slices.Equal(r.Groups, wantGroups) {
			t.Errorf("Groups = %+v, want %+v", r.Groups, wantGroups)
		}
		wantTags := []store.TrafficTag{{TagID: red.ID, TrafficCount: store.TrafficCount{Allowed: 3}}, {TagID: blue.ID, TrafficCount: store.TrafficCount{Allowed: 1}}}
		slices.SortFunc(wantTags, func(x, y store.TrafficTag) int { return strings.Compare(x.TagID, y.TagID) })
		if !slices.Equal(r.Tags, wantTags) {
			t.Errorf("Tags = %+v, want %+v", r.Tags, wantTags)
		}
		// Another owner's or a missing site, group or tag, and two scopes
		// at once, are all ErrNotFound.
		for _, scope := range []store.TrafficScope{
			{SiteID: foreignSite.ID}, {GroupID: foreignGroup.ID}, {TagID: foreignTag.ID},
			{SiteID: store.NewID()}, {GroupID: store.NewID()}, {TagID: store.NewID()},
			{SiteID: a.ID, GroupID: g.ID},
		} {
			_, err := f.r.Traffic.Report(t.Context(), o.ID, scope, allFrom, allTo)
			wantErr(t, err, store.ErrNotFound)
		}
		// The other owner sees only their own site.
		if got := f.report(other.ID, store.TrafficScope{}); len(got.Sites) != 1 || got.Sites[0].SiteName != foreignSite.Name {
			t.Errorf("other owner's Sites = %+v", got.Sites)
		}
	})
	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		f.backfill()
		hour := now.Truncate(time.Hour)
		f.must(f.r.AccessLog.Append(t.Context(), []store.AccessEntry{
			f.entry(s.ID, hour.Add(-time.Minute), 200, store.ViaPublic, "", "/", ""),
			f.entry(s.ID, hour, 200, store.ViaPublic, "", "/", ""),
			f.entry(s.ID, hour.Add(59*time.Minute), 200, store.ViaPublic, "", "/", ""),
			f.entry(s.ID, hour.Add(time.Hour), 200, store.ViaPublic, "", "/", ""),
		}))
		// from is inclusive and to exclusive.
		r, err := f.r.Traffic.Report(t.Context(), o.ID, store.TrafficScope{}, hour, hour.Add(time.Hour))
		f.must(err)
		if len(r.Hours) != 1 || !r.Hours[0].Hour.Equal(hour) || r.Hours[0].Allowed != 2 {
			t.Errorf("Hours = %+v, want 2 in %v", r.Hours, hour)
		}
	})
	t.Run("PruneHour", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, suspended := f.owner(), f.owner()
		live, down, held := f.site(o.ID), f.site(o.ID), f.site(suspended.ID)
		l := f.link(o.ID, live.ID)
		f.backfill()
		hour := now.Truncate(time.Hour)
		var entries []store.AccessEntry
		for _, s := range []store.Site{live, down, held} {
			for _, at := range []time.Time{hour.Add(-3 * time.Hour), hour.Add(-2 * time.Hour), hour} {
				entries = append(entries, f.entry(s.ID, at, 200, store.ViaLink, l.PublicID, "/", "curl/8"))
			}
		}
		f.must(f.r.AccessLog.Append(t.Context(), entries))
		f.must(f.r.Sites.TakeDown(t.Context(), down.ID, now))
		f.must(f.r.Owners.Suspend(t.Context(), suspended.ID, now))
		cutoff := hour.Add(-time.Hour)
		for range 2 {
			pruned, err := f.r.Traffic.PruneHour(t.Context(), cutoff)
			if err != nil || !pruned {
				t.Fatalf("PruneHour() = %v, %v; want true", pruned, err)
			}
		}
		if pruned, err := f.r.Traffic.PruneHour(t.Context(), cutoff); err != nil || pruned {
			t.Fatalf("PruneHour() with nothing left = %v, %v; want false", pruned, err)
		}
		hoursOf := func(ownerID string, s store.Site) int {
			r, err := f.r.Traffic.Report(t.Context(), ownerID, store.TrafficScope{SiteID: s.ID}, allFrom, allTo)
			f.must(err)
			if len(r.Paths) != 1 || len(r.Links) != 1 || len(r.Browsers) != 1 {
				t.Errorf("%s: paths %v, links %v, browsers %v; want one each", s.Name, r.Paths, r.Links, r.Browsers)
			}
			return len(r.Hours)
		}
		// The live site keeps only the hour since the cutoff; a taken-down
		// site and a suspended owner's keep all of theirs.
		if got := hoursOf(o.ID, live); got != 1 {
			t.Errorf("live site hours = %d, want 1", got)
		}
		if got := hoursOf(o.ID, down); got != 3 {
			t.Errorf("taken-down site hours = %d, want 3", got)
		}
		if got := hoursOf(suspended.ID, held); got != 3 {
			t.Errorf("suspended owner's site hours = %d, want 3", got)
		}
		r, err := f.r.Traffic.Report(t.Context(), o.ID, store.TrafficScope{SiteID: live.ID}, allFrom, hour)
		f.must(err)
		if len(r.Paths)+len(r.Links)+len(r.Browsers)+len(r.Via) != 0 {
			t.Errorf("pruned hours left rows: %+v", r)
		}
	})
	t.Run("TeardownDeletesTakedownKeeps", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s, deleted := f.site(o.ID), f.site(o.ID)
		f.backfill()
		f.must(f.r.AccessLog.Append(t.Context(), []store.AccessEntry{
			f.entry(s.ID, now, 200, store.ViaPublic, "", "/", ""),
			f.entry(deleted.ID, now, 200, store.ViaPublic, "", "/", ""),
		}))
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, now))
		if r := f.report(o.ID, store.TrafficScope{}); len(r.Sites) != 2 {
			t.Fatalf("after takedown Sites = %+v, want both kept", r.Sites)
		}
		f.must(f.r.Sites.TearDown(t.Context(), s.ID))
		f.must(f.r.Sites.Delete(t.Context(), o.ID, deleted.ID))
		// A new site with a torn-down site's name starts with nothing.
		reused := f.validSite(o.ID)
		reused.Name = s.Name
		f.must(f.r.Sites.Create(t.Context(), reused, store.Quotas{}))
		if r := f.report(o.ID, store.TrafficScope{}); len(r.Hours) != 0 {
			t.Fatalf("after teardown and delete Report() = %+v, want nothing", r)
		}
		// Nothing is left behind for retention either.
		if pruned, err := f.r.Traffic.PruneHour(t.Context(), allTo); err != nil || pruned {
			t.Fatalf("PruneHour() = %v, %v; want no rows left", pruned, err)
		}
	})
}
