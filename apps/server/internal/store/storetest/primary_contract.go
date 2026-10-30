// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// primaryContract covers Sites.SetPrimaryBase, SetPrimaryDomain and
// ClearPrimary (C1.18) and the primary fields every site read returns.
func primaryContract(t *testing.T, open Opener) {
	t.Run("ChooseAndClear", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.activeDomain(o.ID, s.ID)

		wantPrimary(t, f, o.ID, s, false, "", false)
		f.must(f.r.Sites.SetPrimaryBase(t.Context(), o.ID, s.ID))
		wantPrimary(t, f, o.ID, s, true, "", false)
		f.must(f.r.Sites.SetPrimaryDomain(t.Context(), o.ID, s.ID, active.ID))
		wantPrimary(t, f, o.ID, s, false, active.Hostname, true)
		f.must(f.r.Sites.SetPrimaryBase(t.Context(), o.ID, s.ID))
		wantPrimary(t, f, o.ID, s, true, "", false)
		f.must(f.r.Sites.ClearPrimary(t.Context(), o.ID, s.ID))
		wantPrimary(t, f, o.ID, s, false, "", false)
		// Clearing a site with no primary is no error.
		f.must(f.r.Sites.ClearPrimary(t.Context(), o.ID, s.ID))
	})
	t.Run("EveryReadReturnsIt", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.activeDomain(o.ID, s.ID)
		f.must(f.r.Sites.SetPrimaryDomain(t.Context(), o.ID, s.ID, active.ID))
		plain := f.activeDomain(o.ID, s.ID)
		for _, d := range []store.Domain{active, plain} {
			got, err := f.r.Domains.Get(t.Context(), o.ID, s.ID, d.Hostname)
			if err != nil || got.Primary != (d.ID == active.ID) {
				t.Fatalf("Domains.Get(%s) = %+v, %v; want Primary %t", d.Hostname, got, err, d.ID == active.ID)
			}
		}
		domains, err := f.r.Domains.List(t.Context(), o.ID, s.ID, "", 10)
		if err != nil || len(domains) != 2 {
			t.Fatalf("Domains.List() = %+v, %v", domains, err)
		}
		for _, d := range domains {
			if d.Primary != (d.ID == active.ID) {
				t.Fatalf("Domains.List() has %s Primary %t", d.Hostname, d.Primary)
			}
		}
		check := func(what string, got store.Site, err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
			if got.PrimaryBase || got.PrimaryDomain != active.Hostname || !got.PrimaryDomainActive {
				t.Fatalf("%s = %+v, want primary %s", what, got, active.Hostname)
			}
		}
		got, err := f.r.Sites.Get(t.Context(), o.ID, s.ID)
		check("Get", got, err)
		got, err = f.r.Sites.GetByOwnerName(t.Context(), o.ID, s.Name)
		check("GetByOwnerName", got, err)
		got, err = f.r.Sites.GetByOwnerDomain(t.Context(), o.ID, active.Hostname)
		check("GetByOwnerDomain", got, err)
		got, err = f.r.Sites.GetByName(t.Context(), s.Name)
		check("GetByName", got, err)
		got, err = f.r.Sites.GetByID(t.Context(), s.ID)
		check("GetByID", got, err)
		list, err := f.r.Sites.List(t.Context(), o.ID, "", 10)
		if len(list) != 1 {
			t.Fatalf("List() = %+v, %v; want the site", list, err)
		}
		check("List", list[0], err)
		all, err := f.r.Sites.ListAll(t.Context(), "", 10)
		if len(all) != 1 {
			t.Fatalf("ListAll() = %+v, %v; want the site", all, err)
		}
		check("ListAll", all[0].Site, err)
	})
	t.Run("RefusedDomains", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		f.must(f.r.Sites.SetPrimaryBase(t.Context(), o.ID, s.ID))
		pending := f.validDomain(s.ID)
		f.must(f.r.Domains.Create(t.Context(), o.ID, pending))
		lapsed := f.validDomain(s.ID)
		lapsed.Status, lapsed.LastCheckedAt, lapsed.VerifiedAt, lapsed.FailingSince = store.DomainLapsed, now, now, now
		lapsed.LastFailureReason, lapsed.FailureDetail = store.DomainNoRecord, "No CNAME or TXT record found."
		f.must(f.r.Domains.Create(t.Context(), o.ID, lapsed))
		sibling := f.site(o.ID)
		siblings := f.activeDomain(o.ID, sibling.ID)
		other := f.owner()
		others := f.activeDomain(other.ID, f.site(other.ID).ID)

		for name, id := range map[string]string{
			"pending": pending.ID, "lapsed": lapsed.ID, "another site's": siblings.ID,
			"another owner's": others.ID, "unknown": store.NewID(),
		} {
			if err := f.r.Sites.SetPrimaryDomain(t.Context(), o.ID, s.ID, id); err == nil {
				t.Errorf("SetPrimaryDomain(%s domain) = nil, want ErrNotFound", name)
			} else {
				wantErr(t, err, store.ErrNotFound)
			}
		}
		// Nothing changed.
		wantPrimary(t, f, o.ID, s, true, "", false)
	})
	t.Run("OwnerIsolation", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.activeDomain(o.ID, s.ID)
		other := f.owner()
		wantErr(t, f.r.Sites.SetPrimaryBase(t.Context(), other.ID, s.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.SetPrimaryDomain(t.Context(), other.ID, s.ID, active.ID), store.ErrNotFound)
		f.must(f.r.Sites.SetPrimaryBase(t.Context(), o.ID, s.ID))
		wantErr(t, f.r.Sites.ClearPrimary(t.Context(), other.ID, s.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.SetPrimaryBase(t.Context(), o.ID, store.NewID()), store.ErrNotFound)
		wantPrimary(t, f, o.ID, s, true, "", false)
	})
	t.Run("TakenDownIsFrozen", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.activeDomain(o.ID, s.ID)
		f.must(f.r.Sites.SetPrimaryBase(t.Context(), o.ID, s.ID))
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, now))
		wantErr(t, f.r.Sites.SetPrimaryDomain(t.Context(), o.ID, s.ID, active.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.ClearPrimary(t.Context(), o.ID, s.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.SetPrimaryBase(t.Context(), o.ID, s.ID), store.ErrNotFound)
		wantPrimary(t, f, o.ID, s, true, "", false)
	})
	t.Run("LapseSuspendsRecoveryResumes", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.activeDomain(o.ID, s.ID)
		f.must(f.r.Sites.SetPrimaryDomain(t.Context(), o.ID, s.ID, active.ID))

		lapsed := active
		lapsed.Status, lapsed.LastCheckedAt = store.DomainLapsed, now.Add(time.Hour)
		lapsed.FailingSince, lapsed.LastFailureReason, lapsed.FailureDetail = now, store.DomainNoRecord, "No CNAME or TXT record found."
		f.must(f.r.Domains.RecordCheck(t.Context(), active, lapsed))
		wantPrimary(t, f, o.ID, s, false, active.Hostname, false)
		// The domain still says it is the chosen primary.
		got, err := f.r.Domains.Get(t.Context(), o.ID, s.ID, active.Hostname)
		if err != nil || !got.Primary {
			t.Fatalf("Get() of the lapsed primary = %+v, %v; want Primary", got, err)
		}

		again := lapsed
		again.Status, again.LastCheckedAt, again.VerifiedAt = store.DomainActive, now.Add(2*time.Hour), now.Add(2*time.Hour)
		again.FailingSince, again.LastFailureReason, again.FailureDetail = time.Time{}, "", ""
		f.must(f.r.Domains.RecordCheck(t.Context(), lapsed, again))
		wantPrimary(t, f, o.ID, s, false, active.Hostname, true)
	})
	t.Run("RemovingTheDomainClearsIt", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.activeDomain(o.ID, s.ID)
		f.must(f.r.Sites.SetPrimaryDomain(t.Context(), o.ID, s.ID, active.ID))
		f.must(f.r.Domains.Delete(t.Context(), o.ID, s.ID, active.Hostname))
		wantPrimary(t, f, o.ID, s, false, "", false)

		// The same hostname claimed again is a new domain: not primary.
		again := f.validDomain(s.ID)
		again.Hostname, again.Status, again.LastCheckedAt, again.VerifiedAt = active.Hostname, store.DomainActive, now, now
		f.must(f.r.Domains.Create(t.Context(), o.ID, again))
		wantPrimary(t, f, o.ID, s, false, "", false)
	})
	t.Run("DeletingTheSite", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.activeDomain(o.ID, s.ID)
		f.must(f.r.Sites.SetPrimaryDomain(t.Context(), o.ID, s.ID, active.ID))
		f.must(f.r.Sites.Delete(t.Context(), o.ID, s.ID))
		_, err := f.r.Sites.Get(t.Context(), o.ID, s.ID)
		wantErr(t, err, store.ErrNotFound)
	})
}

// activeDomain inserts an active custom domain on siteID.
func (f *fixture) activeDomain(ownerID, siteID string) store.Domain {
	f.t.Helper()
	d := f.validDomain(siteID)
	d.Status, d.LastCheckedAt, d.VerifiedAt = store.DomainActive, now, now
	f.must(f.r.Domains.Create(f.t.Context(), ownerID, d))
	return d
}

func wantPrimary(t *testing.T, f *fixture, ownerID string, s store.Site, base bool, domain string, active bool) {
	t.Helper()
	got, err := f.r.Sites.Get(t.Context(), ownerID, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PrimaryBase != base || got.PrimaryDomain != domain || got.PrimaryDomainActive != active {
		t.Fatalf("primary = base %t, domain %q (active %t); want base %t, domain %q (active %t)",
			got.PrimaryBase, got.PrimaryDomain, got.PrimaryDomainActive, base, domain, active)
	}
}
