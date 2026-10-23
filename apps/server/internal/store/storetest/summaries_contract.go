// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// summaryWindow is the window internal/sites asks for, at the fixed now.
func summaryWindow() store.SummaryWindow {
	today := now.Truncate(24 * time.Hour)
	return store.SummaryWindow{
		Now: now, ExpiringBy: now.Add(48 * time.Hour), MinLifetime: time.Hour,
		DaysFrom: today.AddDate(0, 0, -6), Days: 7, ActiveFrom: today.AddDate(0, 0, -29),
	}
}

func siteSummariesContract(t *testing.T, open Opener) {
	t.Run("Summaries", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		w := summaryWindow()

		alpha := f.validSite(o.ID)
		alpha.Name = "alpha"
		f.must(f.r.Sites.Create(t.Context(), alpha, store.Quotas{}))
		d := f.validDeploy(alpha.ID)
		d.Message = "First cut"
		f.must(f.r.Deploys.Create(t.Context(), o.ID, d, []store.DeployFile{{Path: "index.html", SHA256: sha('a'), Size: 5}}, store.Quotas{}))
		f.uploadAll(o.ID, alpha.ID, d.ID)
		_, err := f.r.Deploys.Activate(t.Context(), o.ID, alpha.ID, d.ID, now.Add(-time.Hour))
		f.must(err)

		bravo := f.validSite(o.ID)
		bravo.Name = "bravo"
		f.must(f.r.Sites.Create(t.Context(), bravo, store.Quotas{}))
		pol := f.validPolicy(bravo.ID)
		pol.EmailEnabled, pol.Allowlist = true, store.Allowlist{Domains: []string{"example.test"}}
		f.must(f.r.Policies.Create(t.Context(), o.ID, pol))
		group := f.group(o.ID, "Clients")
		zulu, mike := f.tag(o.ID, "zulu"), f.tag(o.ID, "mike")
		_, err = f.r.Organisation.Organise(t.Context(), o.ID, []string{"bravo"}, store.OrganiseChange{
			SetGroup: true, GroupID: group.ID, SetTags: true, TagIDs: []string{zulu.ID, mike.ID}, SetPinned: true, PinnedAt: now,
		})
		f.must(err)
		dom := f.validDomain(bravo.ID)
		f.must(f.r.Domains.Create(t.Context(), o.ID, dom))
		link := func(siteID string, created, expires time.Duration, change func(*store.ShareLink)) {
			l := f.validLink(siteID)
			l.CreatedAt = now.Add(created)
			if expires != 0 {
				l.ExpiresAt = now.Add(expires)
			}
			if change != nil {
				change(&l)
			}
			f.must(f.r.Links.Create(t.Context(), o.ID, l))
		}
		link(bravo.ID, -time.Hour, 0, nil)                                // works, never expires
		link(bravo.ID, -time.Hour, time.Hour, nil)                        // expiring
		link(bravo.ID, -20*time.Minute, 30*time.Minute, nil)              // works; lives under an hour
		link(bravo.ID, -50*time.Minute, 10*time.Minute, nil)              // works; lives exactly an hour
		link(bravo.ID, -24*time.Hour, 48*time.Hour, nil)                  // expiring, on the boundary
		link(bravo.ID, -24*time.Hour, 48*time.Hour+time.Millisecond, nil) // works, expires later
		link(bravo.ID, -24*time.Hour, -time.Millisecond, nil)             // expired
		link(bravo.ID, -24*time.Hour, time.Hour, func(l *store.ShareLink) { l.RevokedAt = now.Add(-time.Minute) })
		link(bravo.ID, -24*time.Hour, time.Hour, func(l *store.ShareLink) { l.MaxUses, l.UseCount = 1, 1 })   // used up
		link(bravo.ID, -24*time.Hour, 2*time.Hour, func(l *store.ShareLink) { l.MaxUses, l.UseCount = 2, 1 }) // expiring

		// charlie's last request was just before the quiet window; delta's
		// at its start.
		charlie, delta := f.validSite(o.ID), f.validSite(o.ID)
		charlie.Name, delta.Name = "charlie", "delta"
		f.must(f.r.Sites.Create(t.Context(), charlie, store.Quotas{}))
		f.must(f.r.Sites.Create(t.Context(), delta, store.Quotas{}))

		theirs := f.site(other.ID)
		f.link(other.ID, theirs.ID)
		theirsExpiring := f.validLink(theirs.ID)
		theirsExpiring.CreatedAt, theirsExpiring.ExpiresAt = now.Add(-24*time.Hour), now.Add(time.Hour)
		f.must(f.r.Links.Create(t.Context(), other.ID, theirsExpiring))

		f.backfill()
		hit := func(siteID string, at time.Time, status int) store.AccessEntry {
			return f.entry(siteID, at, status, store.ViaPublic, "", "/", "curl/8")
		}
		f.must(f.r.AccessLog.Append(t.Context(), []store.AccessEntry{
			hit(bravo.ID, now, 200),
			hit(bravo.ID, now.Truncate(24*time.Hour), 404),
			hit(bravo.ID, w.DaysFrom.AddDate(0, 0, 3), 401),
			hit(bravo.ID, w.DaysFrom, 200),
			hit(bravo.ID, w.DaysFrom.Add(-time.Millisecond), 200), // the day before the seven
			hit(charlie.ID, w.ActiveFrom.Add(-time.Millisecond), 200),
			hit(delta.ID, w.ActiveFrom, 200),
			hit(theirs.ID, now, 200),
		}))

		got, err := f.r.Sites.Summaries(t.Context(), o.ID, w)
		f.must(err)
		names := make([]string, len(got))
		for i, s := range got {
			names[i] = s.Name
		}
		if !slices.Equal(names, []string{"alpha", "bravo", "charlie", "delta"}) {
			t.Fatalf("Summaries() sites = %v, want alpha, bravo, charlie, delta by name", names)
		}
		a, b, c, dl := got[0], got[1], got[2], got[3]
		if a.ActiveDeployID != d.ID || a.DeployMessage != "First cut" || !a.ActivatedAt.Equal(now.Add(-time.Hour)) ||
			a.Default.Visibility != store.Public || a.Default.LinksEnabled || a.WorkingLinks != 0 || a.Active ||
			a.GroupID != "" || a.TagIDs != nil || a.Domains != nil || !slices.Equal(a.Requests, make([]int64, 7)) {
			t.Errorf("alpha = %+v", a)
		}
		if b.Default.Visibility != store.Protected || !b.Default.LinksEnabled || !b.Default.EmailEnabled || b.Default.PasswordEnabled ||
			b.GroupID != group.ID || !slices.Equal(b.TagIDs, []string{mike.ID, zulu.ID}) || !b.PinnedAt.Equal(now) ||
			!slices.Equal(b.Domains, []string{dom.Hostname}) || b.ActiveDeployID != "" {
			t.Errorf("bravo = %+v", b)
		}
		if b.WorkingLinks != 7 || b.ExpiringLinks != 3 {
			t.Errorf("bravo links = %d working, %d expiring; want 7 and 3", b.WorkingLinks, b.ExpiringLinks)
		}
		if !slices.Equal(b.Requests, []int64{1, 0, 0, 1, 0, 0, 2}) || !b.Active {
			t.Errorf("bravo requests = %v, active %v; want [1 0 0 1 0 0 2], true", b.Requests, b.Active)
		}
		if c.Active || !dl.Active || !slices.Equal(dl.Requests, make([]int64, 7)) {
			t.Errorf("charlie active %v, delta active %v with %v; want false, true with none in the seven days", c.Active, dl.Active, dl.Requests)
		}

		if got, err := f.r.Sites.Summaries(t.Context(), f.owner().ID, w); err != nil || len(got) != 0 {
			t.Fatalf("Summaries(owner with no sites) = %v, %v; want none", got, err)
		}

		// ExpiringLinks lists exactly the links ExpiringLinks counts,
		// soonest first, and never another owner's.
		expiring, err := f.r.Sites.ExpiringLinks(t.Context(), o.ID, w, 100)
		f.must(err)
		var counted int
		for _, s := range got {
			counted += s.ExpiringLinks
		}
		if len(expiring) != counted {
			t.Fatalf("ExpiringLinks() = %d links, summaries count %d", len(expiring), counted)
		}
		wantAt := []time.Time{now.Add(time.Hour), now.Add(2 * time.Hour), now.Add(48 * time.Hour)}
		for i, l := range expiring {
			if l.SiteName != "bravo" || !l.ExpiresAt.Equal(wantAt[i]) || l.Label != "Linear ENG-1" || l.ID == theirsExpiring.ID {
				t.Errorf("ExpiringLinks()[%d] = %+v; want bravo's, expiring at %v", i, l, wantAt[i])
			}
		}
		if l := expiring[1]; l.MaxUses != 2 || l.UseCount != 1 || expiring[0].MaxUses != 0 {
			t.Errorf("uses = %+v and %+v; want 1 of 2 spent, then no limit", l, expiring[0])
		}
		if first, err := f.r.Sites.ExpiringLinks(t.Context(), o.ID, w, 1); err != nil || len(first) != 1 || first[0].ID != expiring[0].ID {
			t.Errorf("ExpiringLinks(limit 1) = %v, %v; want the soonest", first, err)
		}
		if theirs, err := f.r.Sites.ExpiringLinks(t.Context(), other.ID, w, 100); err != nil || len(theirs) != 1 || theirs[0].ID != theirsExpiring.ID {
			t.Errorf("ExpiringLinks(other owner) = %v, %v; want only their own link", theirs, err)
		}
	})
}
