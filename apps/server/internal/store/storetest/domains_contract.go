// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func domainsContract(t *testing.T, open Opener) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		domainsCreateContract(t, open)
	})
	t.Run("GetActiveByHostname", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.validDomain(s.ID)
		active.Status, active.LastCheckedAt, active.VerifiedAt = store.DomainActive, now, now
		f.must(f.r.Domains.Create(t.Context(), o.ID, active))
		got, err := f.r.Domains.GetActiveByHostname(t.Context(), active.Hostname)
		if err != nil {
			t.Fatal(err)
		}
		if got != active {
			t.Fatalf("GetActiveByHostname() = %+v, want %+v", got, active)
		}
		for _, status := range []store.DomainStatus{store.DomainPending, store.DomainLapsed} {
			d := f.validDomain(s.ID)
			d.Status = status
			if status == store.DomainLapsed {
				d.LastCheckedAt, d.FailingSince, d.LastFailureReason, d.FailureDetail = now, now, store.DomainNoRecord, "No CNAME or TXT record found."
			}
			f.must(f.r.Domains.Create(t.Context(), o.ID, d))
			_, err := f.r.Domains.GetActiveByHostname(t.Context(), d.Hostname)
			wantErr(t, err, store.ErrNotFound)
		}
		_, err = f.r.Domains.GetActiveByHostname(t.Context(), "unknown.example.test")
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("Get", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.validDomain(s.ID)
		f.must(f.r.Domains.Create(t.Context(), o.ID, d))
		got, err := f.r.Domains.Get(t.Context(), o.ID, s.ID, d.Hostname)
		if err != nil || got != d {
			t.Fatalf("Get() = %+v, %v; want %+v", got, err, d)
		}
		// Another owner, another of the owner's sites, an unknown hostname
		// and an unknown site are all simply not found.
		other := f.owner()
		_, err = f.r.Domains.Get(t.Context(), other.ID, s.ID, d.Hostname)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Domains.Get(t.Context(), o.ID, f.site(o.ID).ID, d.Hostname)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Domains.Get(t.Context(), o.ID, s.ID, "unknown.example.test")
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Domains.Get(t.Context(), o.ID, store.NewID(), d.Hostname)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("List", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		var want []string
		for _, h := range []string{"b.example.test", "a.example.test", "c.example.test"} {
			d := f.validDomain(s.ID)
			d.Hostname = h
			f.must(f.r.Domains.Create(t.Context(), o.ID, d))
			want = append(want, h)
		}
		// A domain of another site is not listed.
		f.must(f.r.Domains.Create(t.Context(), o.ID, f.validDomain(f.site(o.ID).ID)))
		slices.Sort(want)
		hosts := func(ds []store.Domain) []string {
			var out []string
			for _, d := range ds {
				out = append(out, d.Hostname)
			}
			return out
		}
		got, err := f.r.Domains.List(t.Context(), o.ID, s.ID, "", 10)
		if err != nil || !slices.Equal(hosts(got), want) {
			t.Fatalf("List() = %v, %v; want %v", hosts(got), err, want)
		}
		got, err = f.r.Domains.List(t.Context(), o.ID, s.ID, "", 2)
		if err != nil || !slices.Equal(hosts(got), want[:2]) {
			t.Fatalf("List(limit 2) = %v, %v", hosts(got), err)
		}
		got, err = f.r.Domains.List(t.Context(), o.ID, s.ID, want[1], 10)
		if err != nil || !slices.Equal(hosts(got), want[2:]) {
			t.Fatalf("List(after %s) = %v, %v", want[1], hosts(got), err)
		}
		got, err = f.r.Domains.List(t.Context(), f.owner().ID, s.ID, "", 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("another owner's List() = %v, %v; want nothing", hosts(got), err)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.validDomain(s.ID)
		f.must(f.r.Domains.Create(t.Context(), o.ID, d))
		wantErr(t, f.r.Domains.Delete(t.Context(), f.owner().ID, s.ID, d.Hostname), store.ErrNotFound)
		wantErr(t, f.r.Domains.Delete(t.Context(), o.ID, f.site(o.ID).ID, d.Hostname), store.ErrNotFound)
		f.must(f.r.Domains.Delete(t.Context(), o.ID, s.ID, d.Hostname))
		_, err := f.r.Domains.Get(t.Context(), o.ID, s.ID, d.Hostname)
		wantErr(t, err, store.ErrNotFound)
		wantErr(t, f.r.Domains.Delete(t.Context(), o.ID, s.ID, d.Hostname), store.ErrNotFound)
		// The hostname is free again, for any site.
		other := f.owner()
		again := f.validDomain(f.site(other.ID).ID)
		again.Hostname = d.Hostname
		f.must(f.r.Domains.Create(t.Context(), other.ID, again))
	})
	t.Run("RecordCheck", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.validDomain(s.ID)
		f.must(f.r.Domains.Create(t.Context(), o.ID, d))

		later := now.Add(time.Minute)
		failed := d
		failed.LastCheckedAt, failed.FailingSince = later, later
		failed.LastFailureReason, failed.FailureDetail = store.DomainNoRecord, "No CNAME or TXT record found."
		f.must(f.r.Domains.RecordCheck(t.Context(), d, failed))
		got, err := f.r.Domains.Get(t.Context(), o.ID, s.ID, d.Hostname)
		if err != nil || got != failed {
			t.Fatalf("after a failed check Get() = %+v, %v; want %+v", got, err, failed)
		}

		passed := failed
		passed.Status, passed.VerifiedAt = store.DomainActive, later.Add(time.Minute)
		passed.LastCheckedAt = passed.VerifiedAt
		passed.FailingSince, passed.LastFailureReason, passed.FailureDetail = time.Time{}, "", ""
		f.must(f.r.Domains.RecordCheck(t.Context(), failed, passed))
		got, err = f.r.Domains.Get(t.Context(), o.ID, s.ID, d.Hostname)
		if err != nil || got != passed {
			t.Fatalf("after a passed check Get() = %+v, %v; want %+v", got, err, passed)
		}
		if _, err := f.r.Domains.GetActiveByHostname(t.Context(), d.Hostname); err != nil {
			t.Fatalf("an active domain is not routed: %v", err)
		}

		// A check of a snapshot that is no longer current changes nothing:
		// the status or the last check differ from what was read.
		stale := failed
		stale.LastCheckedAt = later.Add(time.Hour)
		stale.LastFailureReason, stale.FailureDetail = store.DomainDNSError, "timeout"
		wantErr(t, f.r.Domains.RecordCheck(t.Context(), failed, stale), store.ErrNotFound)
		sameStatus := passed
		sameStatus.LastCheckedAt = later // read before the passing check
		wantErr(t, f.r.Domains.RecordCheck(t.Context(), sameStatus, stale), store.ErrNotFound)
		wantErr(t, f.r.Domains.RecordCheck(t.Context(), d, stale), store.ErrNotFound) // never checked
		got, err = f.r.Domains.Get(t.Context(), o.ID, s.ID, d.Hostname)
		if err != nil || got != passed {
			t.Fatalf("a stale check changed the domain: %+v, %v", got, err)
		}

		// The row's constraints still hold.
		bad := passed
		bad.LastCheckedAt = later.Add(2 * time.Hour)
		bad.FailingSince = later
		wantErr(t, f.r.Domains.RecordCheck(t.Context(), passed, bad), store.ErrConstraint)
		bad = passed
		bad.Status = "verified"
		wantErr(t, f.r.Domains.RecordCheck(t.Context(), passed, bad), store.ErrConstraint)

		// A check for a removed claim changes nothing, even when the
		// hostname has been claimed again under a new ID.
		f.must(f.r.Domains.Delete(t.Context(), o.ID, s.ID, d.Hostname))
		again := f.validDomain(s.ID)
		again.Hostname = d.Hostname
		f.must(f.r.Domains.Create(t.Context(), o.ID, again))
		wantErr(t, f.r.Domains.RecordCheck(t.Context(), passed, stale), store.ErrNotFound)
		got, err = f.r.Domains.Get(t.Context(), o.ID, s.ID, d.Hostname)
		if err != nil || got != again {
			t.Fatalf("a stale check changed the new claim: %+v, %v", got, err)
		}
	})
	t.Run("ListToCheck", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		windowStart := now.Add(-48 * time.Hour)
		recheckBefore := now.Add(-24 * time.Hour)
		add := func(mutate func(d *store.Domain)) store.Domain {
			d := f.validDomain(s.ID)
			mutate(&d)
			f.must(f.r.Domains.Create(t.Context(), o.ID, d))
			return d
		}
		var want []string
		// Pending inside the window, never checked and checked.
		want = append(want, add(func(d *store.Domain) { d.CreatedAt = windowStart }).ID)
		want = append(want, add(func(d *store.Domain) {
			d.CreatedAt, d.LastCheckedAt = now.Add(-time.Hour), now
			d.FailingSince, d.LastFailureReason = now.Add(-time.Hour), store.DomainNoRecord
		}).ID)
		// Active and lapsed, last checked at the cut-off or never.
		want = append(want, add(func(d *store.Domain) {
			d.Status, d.CreatedAt, d.LastCheckedAt, d.VerifiedAt = store.DomainActive, windowStart.Add(-time.Hour), recheckBefore, recheckBefore
		}).ID)
		want = append(want, add(func(d *store.Domain) {
			d.Status, d.CreatedAt, d.LastCheckedAt = store.DomainLapsed, windowStart.Add(-time.Hour), recheckBefore.Add(-time.Hour)
			d.FailingSince, d.LastFailureReason = windowStart.Add(-time.Hour), store.DomainCNAMEElsewhere
		}).ID)
		want = append(want, add(func(d *store.Domain) { d.Status, d.CreatedAt = store.DomainActive, windowStart.Add(-time.Hour) }).ID)
		// Not candidates: pending past its window, recently checked active
		// and lapsed domains.
		add(func(d *store.Domain) { d.CreatedAt = windowStart.Add(-time.Millisecond) })
		add(func(d *store.Domain) {
			d.Status, d.CreatedAt, d.LastCheckedAt, d.VerifiedAt = store.DomainActive, windowStart, recheckBefore.Add(time.Millisecond), windowStart
		})
		add(func(d *store.Domain) {
			d.Status, d.CreatedAt, d.LastCheckedAt = store.DomainLapsed, windowStart, now
			d.FailingSince, d.LastFailureReason = windowStart, store.DomainDNSError
		})
		slices.Sort(want)

		var got []string
		after := ""
		for {
			page, err := f.r.Domains.ListToCheck(t.Context(), windowStart, recheckBefore, after, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) > 2 {
				t.Fatalf("page of %d, limit 2", len(page))
			}
			for _, d := range page {
				got = append(got, d.ID)
			}
			if len(page) < 2 {
				break
			}
			after = page[len(page)-1].ID
		}
		if !slices.Equal(got, want) {
			t.Fatalf("ListToCheck IDs = %v, want %v", got, want)
		}
	})
}

func domainsCreateContract(t *testing.T, open Opener) {
	type owned struct {
		ownerID string
		d       store.Domain
	}
	runInsertCases(t, open,
		func(f *fixture) owned {
			o := f.owner()
			return owned{o.ID, f.validDomain(f.site(o.ID).ID)}
		},
		func(f *fixture, v owned) error { return f.r.Domains.Create(f.t.Context(), v.ownerID, v.d) },
		[]insertCase[owned]{
			{name: "valid"},
			{name: "failing with reason", mutate: func(_ *fixture, v *owned) {
				v.d.Status, v.d.LastCheckedAt, v.d.FailingSince, v.d.LastFailureReason = store.DomainActive, now, now, store.DomainCNAMEElsewhere
			}},
			{name: "verified with failure detail", mutate: func(_ *fixture, v *owned) {
				v.d.Status, v.d.LastCheckedAt, v.d.VerifiedAt = store.DomainActive, now, now
				v.d.FailingSince, v.d.LastFailureReason, v.d.FailureDetail = now, store.DomainDNSError, "SERVFAIL from 192.0.2.53"
			}},
			{name: "253 characters", mutate: func(_ *fixture, v *owned) { v.d.Hostname = strings.Repeat("a", 249) + ".com" }},
			{name: "duplicate hostname on another owner's site", mutate: func(f *fixture, v *owned) {
				o := f.owner()
				other := f.validDomain(f.site(o.ID).ID)
				other.Hostname = v.d.Hostname
				f.must(f.r.Domains.Create(f.t.Context(), o.ID, other))
			}, want: store.ErrConflict},
			{name: "duplicate verification token", mutate: func(f *fixture, v *owned) {
				other := f.validDomain(v.d.SiteID)
				other.VerificationToken = v.d.VerificationToken
				f.must(f.r.Domains.Create(f.t.Context(), v.ownerID, other))
			}, want: store.ErrConflict},
			{name: "another owner's site", mutate: func(f *fixture, v *owned) { v.ownerID = f.owner().ID }, want: store.ErrNotFound},
			{name: "unknown site", mutate: func(_ *fixture, v *owned) { v.d.SiteID = store.NewID() }, want: store.ErrNotFound},
			{name: "uppercase", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "Docs.example.test" }, want: store.ErrConstraint},
			{name: "trailing dot", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "docs.example.test." }, want: store.ErrConstraint},
			{name: "port", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "docs.example.test:443" }, want: store.ErrConstraint},
			{name: "254 characters", mutate: func(_ *fixture, v *owned) { v.d.Hostname = strings.Repeat("a", 250) + ".com" }, want: store.ErrConstraint},
			{name: "empty", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "" }, want: store.ErrConstraint},
			{name: "unknown status", mutate: func(_ *fixture, v *owned) { v.d.Status = "verified" }, want: store.ErrConstraint},
			{name: "short token", mutate: func(_ *fixture, v *owned) { v.d.VerificationToken = "short" }, want: store.ErrConstraint},
			{name: "failing without reason", mutate: func(_ *fixture, v *owned) { v.d.FailingSince = now }, want: store.ErrConstraint},
			{name: "unknown reason", mutate: func(_ *fixture, v *owned) { v.d.LastFailureReason = "timeout" }, want: store.ErrConstraint},
		})
}
