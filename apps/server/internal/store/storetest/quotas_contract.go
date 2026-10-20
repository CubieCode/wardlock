// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// Owner quotas (A2.14): Deploys.Create, Sites.Create, the deletes that hand
// storage back, and Owners.Quotas and SetQuotas.
func quotasContract(t *testing.T, open Opener) {
	file := func(path string, c byte, size int64) store.DeployFile {
		return store.DeployFile{Path: path, SHA256: sha(c), Size: size}
	}
	// create makes a deploy of files at now+at within quotas.
	create := func(f *fixture, ownerID, siteID string, at time.Duration, q store.Quotas, files ...store.DeployFile) (store.Deploy, error) {
		d := f.validDeploy(siteID)
		d.CreatedAt = now.Add(at)
		d.FileCount, d.TotalSize = len(files), 0
		for _, file := range files {
			d.TotalSize += file.Size
		}
		return d, f.r.Deploys.Create(f.t.Context(), ownerID, d, files, q)
	}
	usage := func(f *fixture, ownerID string, at time.Time) store.OwnerUsage {
		f.t.Helper()
		_, u, err := f.r.Owners.Quotas(f.t.Context(), ownerID, at)
		f.must(err)
		return u
	}
	stored := func(f *fixture, ownerID string) int64 { f.t.Helper(); return usage(f, ownerID, now).StoredBytes }
	wantQuota := func(t *testing.T, err error, want store.QuotaError) {
		t.Helper()
		var qe *store.QuotaError
		if !errors.As(err, &qe) || !errors.Is(err, store.ErrQuotaExceeded) || *qe != want {
			t.Fatalf("error = %v, want %+v", err, want)
		}
	}

	t.Run("StorageAtUnderOver", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		q := store.Quotas{StorageBytes: 100}
		// A hash listed twice in one deploy counts once.
		_, err := create(f, o.ID, s.ID, 0, q, file("a.txt", 'a', 40), file("copy.txt", 'a', 40), file("b.txt", 'b', 40))
		wantErr(t, err, nil)
		if got := stored(f, o.ID); got != 80 {
			t.Fatalf("stored = %d, want 80", got)
		}
		// Hashes the owner's deploys already list are free: this one adds
		// only c, reaching the limit exactly.
		_, err = create(f, o.ID, s.ID, time.Second, q, file("a.txt", 'a', 40), file("c.txt", 'c', 20))
		wantErr(t, err, nil)
		if got := stored(f, o.ID); got != 100 {
			t.Fatalf("stored = %d, want 100", got)
		}
		// One byte over is refused and writes nothing.
		before := usage(f, o.ID, now.Add(time.Hour))
		d, err := create(f, o.ID, s.ID, 2*time.Second, q, file("a.txt", 'a', 40), file("d.txt", 'd', 1))
		wantQuota(t, err, store.QuotaError{Quota: store.QuotaStorage, Limit: 100, Used: 100, Adding: 1})
		_, err = f.r.Deploys.Get(t.Context(), o.ID, s.ID, d.ID)
		wantErr(t, err, store.ErrNotFound)
		if after := usage(f, o.ID, now.Add(time.Hour)); after != before {
			t.Fatalf("usage after a refused deploy = %+v, want %+v", after, before)
		}
		// A deploy of only files already stored still fits.
		_, err = create(f, o.ID, s.ID, 3*time.Second, q, file("b.txt", 'b', 40))
		wantErr(t, err, nil)
		// Unlimited takes anything.
		_, err = create(f, o.ID, s.ID, 4*time.Second, store.Quotas{}, file("e.txt", 'e', 1000))
		wantErr(t, err, nil)
		if got := stored(f, o.ID); got != 1100 {
			t.Fatalf("stored = %d, want 1100", got)
		}
	})
	t.Run("StoragePerOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		s1, s2, sb := f.site(a.ID), f.site(a.ID), f.site(b.ID)
		q := store.Quotas{StorageBytes: 50}
		_, err := create(f, a.ID, s1.ID, 0, q, file("x", 'a', 50))
		wantErr(t, err, nil)
		// The same blob on the owner's other site counts once per owner.
		_, err = create(f, a.ID, s2.ID, 0, q, file("y", 'a', 50))
		wantErr(t, err, nil)
		// Another owner listing it counts it against their own quota.
		_, err = create(f, b.ID, sb.ID, 0, q, file("z", 'a', 50))
		wantErr(t, err, nil)
		if sa, sbb := stored(f, a.ID), stored(f, b.ID); sa != 50 || sbb != 50 {
			t.Fatalf("stored = %d and %d, want 50 each", sa, sbb)
		}
		_, err = create(f, b.ID, sb.ID, 0, q, file("w", 'b', 1))
		wantQuota(t, err, store.QuotaError{Quota: store.QuotaStorage, Limit: 50, Used: 50, Adding: 1})
	})
	t.Run("DeleteReleasesStorage", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d1, err := create(f, o.ID, s.ID, 0, store.Quotas{}, file("a", 'a', 10), file("b", 'b', 20))
		f.must(err)
		d2, err := create(f, o.ID, s.ID, time.Second, store.Quotas{}, file("a", 'a', 10), file("c", 'c', 40))
		f.must(err)
		f.uploadAll(o.ID, s.ID, d2.ID)
		_, err = f.r.Deploys.Activate(t.Context(), o.ID, s.ID, d2.ID, now)
		f.must(err)
		// The active deploy cannot go, and a refused delete releases nothing.
		wantErr(t, f.r.Deploys.Delete(t.Context(), o.ID, d2.ID), store.ErrConstraint)
		// Another owner cannot delete it, nor release its storage.
		wantErr(t, f.r.Deploys.Delete(t.Context(), f.owner().ID, d1.ID), store.ErrNotFound)
		if got := stored(f, o.ID); got != 70 {
			t.Fatalf("stored = %d, want 70", got)
		}
		// Deleting d1 releases b only: d2 still lists a.
		wantErr(t, f.r.Deploys.Delete(t.Context(), o.ID, d1.ID), nil)
		if got := stored(f, o.ID); got != 50 {
			t.Fatalf("stored after deleting d1 = %d, want 50", got)
		}
		// A deploy listing a again gets it free; b counts afresh.
		_, err = create(f, o.ID, s.ID, 2*time.Second, store.Quotas{StorageBytes: 70}, file("a", 'a', 10), file("b", 'b', 20))
		wantErr(t, err, nil)
		if got := stored(f, o.ID); got != 70 {
			t.Fatalf("stored = %d, want 70", got)
		}
	})
	t.Run("SiteDeleteReleasesStorage", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		keep, gone := f.site(o.ID), f.site(o.ID)
		_, err := create(f, o.ID, keep.ID, 0, store.Quotas{}, file("a", 'a', 10))
		f.must(err)
		for i := range 3 {
			_, err := create(f, o.ID, gone.ID, time.Duration(i)*time.Second, store.Quotas{}, file("a", 'a', 10), file("b", 'b', 20))
			f.must(err)
		}
		// Another owner's delete is a miss and changes nothing.
		wantErr(t, f.r.Sites.Delete(t.Context(), f.owner().ID, gone.ID), store.ErrNotFound)
		if got := stored(f, o.ID); got != 30 {
			t.Fatalf("stored = %d, want 30", got)
		}
		f.must(f.r.Sites.Delete(t.Context(), o.ID, gone.ID))
		if got := stored(f, o.ID); got != 10 {
			t.Fatalf("stored after deleting the site = %d, want 10", got)
		}
	})
	t.Run("TearDownReleasesStorage", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		_, err := create(f, o.ID, s.ID, 0, store.Quotas{}, file("a", 'a', 10))
		f.must(err)
		// An owner cannot delete a taken-down site, which keeps its storage.
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, now))
		wantErr(t, f.r.Sites.Delete(t.Context(), o.ID, s.ID), store.ErrNotFound)
		if got := stored(f, o.ID); got != 10 {
			t.Fatalf("stored = %d, want 10", got)
		}
		f.must(f.r.Sites.TearDown(t.Context(), s.ID))
		if got := stored(f, o.ID); got != 0 {
			t.Fatalf("stored after teardown = %d, want 0", got)
		}
	})
	t.Run("PruneReleasesStorage", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		old, err := create(f, o.ID, s.ID, 0, store.Quotas{}, file("a", 'a', 10))
		f.must(err)
		young, err := create(f, o.ID, s.ID, time.Hour, store.Quotas{}, file("b", 'b', 20))
		f.must(err)
		// A young pending deploy is kept, and keeps its storage.
		deleted, err := f.r.GC.DeletePrunableDeploy(t.Context(), young.ID, young.CreatedAt)
		if err != nil || deleted {
			t.Fatalf("DeletePrunableDeploy(young) = %v, %v; want false", deleted, err)
		}
		if got := stored(f, o.ID); got != 30 {
			t.Fatalf("stored = %d, want 30", got)
		}
		deleted, err = f.r.GC.DeletePrunableDeploy(t.Context(), old.ID, young.CreatedAt)
		if err != nil || !deleted {
			t.Fatalf("DeletePrunableDeploy(old) = %v, %v; want true", deleted, err)
		}
		if got := stored(f, o.ID); got != 20 {
			t.Fatalf("stored after pruning = %d, want 20", got)
		}
		deleted, err = f.r.GC.DeletePrunableDeploy(t.Context(), store.NewID(), young.CreatedAt)
		if err != nil || deleted {
			t.Fatalf("DeletePrunableDeploy(unknown) = %v, %v; want false", deleted, err)
		}
	})
	t.Run("DeploysPerDayAtUnderOver", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		q := store.Quotas{DeploysPerDay: 2}
		for _, at := range []time.Duration{0, time.Hour} {
			_, err := create(f, o.ID, s.ID, at, q, file("a", 'a', 1))
			wantErr(t, err, nil)
		}
		if got := usage(f, o.ID, now.Add(time.Hour)).DeploysToday; got != 2 {
			t.Fatalf("DeploysToday = %d, want 2", got)
		}
		_, err := create(f, o.ID, s.ID, 2*time.Hour, q, file("a", 'a', 1))
		wantQuota(t, err, store.QuotaError{Quota: store.QuotaDeploysPerDay, Limit: 2, Used: 2, Adding: 1})
		// Deleting the site does not hand deploys back.
		f.must(f.r.Sites.Delete(t.Context(), o.ID, s.ID))
		s = f.site(o.ID)
		_, err = create(f, o.ID, s.ID, 23*time.Hour, q, file("a", 'a', 1))
		wantQuota(t, err, store.QuotaError{Quota: store.QuotaDeploysPerDay, Limit: 2, Used: 2, Adding: 1})
		// 24 hours after the first, it no longer counts.
		_, err = create(f, o.ID, s.ID, 24*time.Hour, q, file("a", 'a', 1))
		wantErr(t, err, nil)
		// Another owner's deploys are their own.
		other := f.owner()
		_, err = create(f, other.ID, f.site(other.ID).ID, 24*time.Hour, q, file("a", 'a', 1))
		wantErr(t, err, nil)
	})
	t.Run("SitesAtUnderOver", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		q := store.Quotas{Sites: 2}
		first := f.validSite(o.ID)
		wantErr(t, f.r.Sites.Create(t.Context(), first, q), nil)
		wantErr(t, f.r.Sites.Create(t.Context(), f.validSite(o.ID), q), nil)
		third := f.validSite(o.ID)
		wantQuota(t, f.r.Sites.Create(t.Context(), third, q), store.QuotaError{Quota: store.QuotaSites, Limit: 2, Used: 2, Adding: 1})
		_, err := f.r.Sites.Get(t.Context(), o.ID, third.ID)
		wantErr(t, err, store.ErrNotFound)
		// Deleting one frees a place.
		f.must(f.r.Sites.Delete(t.Context(), o.ID, first.ID))
		wantErr(t, f.r.Sites.Create(t.Context(), third, q), nil)
		// Another owner has their own allowance; an unknown owner is refused.
		wantErr(t, f.r.Sites.Create(t.Context(), f.validSite(f.owner().ID), q), nil)
		wantErr(t, f.r.Sites.Create(t.Context(), f.validSite(store.NewID()), q), store.ErrConstraint)
	})
	t.Run("Overrides", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		defaults := store.Quotas{StorageBytes: 10, Sites: 1, DeploysPerDay: 1}
		over, _, err := f.r.Owners.Quotas(t.Context(), o.ID, now)
		wantErr(t, err, nil)
		if over != (store.QuotaOverrides{}) {
			t.Fatalf("new owner's overrides = %+v, want none", over)
		}
		three, zero := int64(3), int64(0)
		set := store.QuotaOverrides{Sites: &three, StorageBytes: &zero}
		f.must(f.r.Owners.SetQuotas(t.Context(), o.ID, set))
		over, _, err = f.r.Owners.Quotas(t.Context(), o.ID, now)
		wantErr(t, err, nil)
		if over.Sites == nil || *over.Sites != 3 || over.StorageBytes == nil || *over.StorageBytes != 0 || over.DeploysPerDay != nil {
			t.Fatalf("overrides = %+v, want sites 3, storage 0, deploys default", over)
		}
		if got, want := defaults.Apply(over), (store.Quotas{StorageBytes: 0, Sites: 3, DeploysPerDay: 1}); got != want {
			t.Fatalf("Apply = %+v, want %+v", got, want)
		}
		// Three sites under the override; the other owner keeps the default.
		var sites []store.Site
		for range 3 {
			s := f.validSite(o.ID)
			wantErr(t, f.r.Sites.Create(t.Context(), s, defaults), nil)
			sites = append(sites, s)
		}
		wantQuota(t, f.r.Sites.Create(t.Context(), f.validSite(o.ID), defaults), store.QuotaError{Quota: store.QuotaSites, Limit: 3, Used: 3, Adding: 1})
		wantErr(t, f.r.Sites.Create(t.Context(), f.validSite(other.ID), defaults), nil)
		wantQuota(t, f.r.Sites.Create(t.Context(), f.validSite(other.ID), defaults), store.QuotaError{Quota: store.QuotaSites, Limit: 1, Used: 1, Adding: 1})
		// Storage 0 is unlimited; deploys per day follows the default.
		_, err = create(f, o.ID, sites[0].ID, 0, defaults, file("big", 'a', 1000))
		wantErr(t, err, nil)
		_, err = create(f, o.ID, sites[0].ID, time.Second, defaults, file("big", 'a', 1000))
		wantQuota(t, err, store.QuotaError{Quota: store.QuotaDeploysPerDay, Limit: 1, Used: 1, Adding: 1})
		// Over a lowered limit, a deploy of files already stored still goes
		// through; one adding a byte does not.
		ten := int64(10)
		f.must(f.r.Owners.SetQuotas(t.Context(), o.ID, store.QuotaOverrides{Sites: &three, StorageBytes: &ten, DeploysPerDay: &zero}))
		_, err = create(f, o.ID, sites[0].ID, 2*time.Second, defaults, file("again", 'a', 1000))
		wantErr(t, err, nil)
		_, err = create(f, o.ID, sites[0].ID, 3*time.Second, defaults, file("again", 'a', 1000), file("new", 'b', 1))
		wantQuota(t, err, store.QuotaError{Quota: store.QuotaStorage, Limit: 10, Used: 1000, Adding: 1})
		// Clearing the overrides returns to the defaults.
		f.must(f.r.Owners.SetQuotas(t.Context(), o.ID, store.QuotaOverrides{}))
		over, u, err := f.r.Owners.Quotas(t.Context(), o.ID, now)
		wantErr(t, err, nil)
		if over != (store.QuotaOverrides{}) || u != (store.OwnerUsage{StoredBytes: 1000, Sites: 3, DeploysToday: 2}) {
			t.Fatalf("after clearing: overrides %+v, usage %+v", over, u)
		}
		wantErr(t, f.r.Owners.SetQuotas(t.Context(), store.NewID(), set), store.ErrNotFound)
		_, _, err = f.r.Owners.Quotas(t.Context(), store.NewID(), now)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("ConcurrentCreates", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		const workers, limit = 8, 3
		s := f.site(o.ID)
		// Each deploy adds 30 new bytes; only three fit under 100.
		storage := make([]error, workers)
		sites := make([]error, workers)
		daily := make([]error, workers)
		var wg sync.WaitGroup
		for i := range workers {
			wg.Go(func() {
				d := f.validDeploy(s.ID)
				d.ID = store.NewID()
				storage[i] = f.r.Deploys.Create(t.Context(), o.ID, d, []store.DeployFile{file("f", byte('a'+i), 30)}, store.Quotas{StorageBytes: 100})
			})
			wg.Go(func() {
				sites[i] = f.r.Sites.Create(t.Context(), store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "race-" + string(rune('a'+i)), CreatedAt: now}, store.Quotas{Sites: limit + 1})
			})
		}
		wg.Wait()
		other := f.owner()
		otherSite := f.site(other.ID)
		for i := range workers {
			wg.Go(func() {
				d := f.validDeploy(otherSite.ID)
				d.ID = store.NewID()
				daily[i] = f.r.Deploys.Create(t.Context(), other.ID, d, []store.DeployFile{file("f", 'a', 1)}, store.Quotas{DeploysPerDay: limit})
			})
		}
		wg.Wait()
		for name, errs := range map[string][]error{"storage": storage, "sites": sites, "deploys per day": daily} {
			ok := 0
			for _, err := range errs {
				switch {
				case err == nil:
					ok++
				case !errors.Is(err, store.ErrQuotaExceeded):
					t.Errorf("%s: unexpected error %v", name, err)
				}
			}
			if ok != limit {
				t.Errorf("%s: %d of %d concurrent creates succeeded, want %d", name, ok, workers, limit)
			}
		}
		if got := stored(f, o.ID); got != 90 {
			t.Errorf("stored = %d, want 90", got)
		}
	})
}
