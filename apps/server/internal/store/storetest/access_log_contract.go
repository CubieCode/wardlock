// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func accessLogContract(t *testing.T, open Opener) {
	valid := func() store.AccessEntry {
		return store.AccessEntry{
			ID: store.NewID(), OccurredAt: now, SiteID: store.NewID(), Host: "docs.wardlock.localhost", Path: "/",
			Status: 200, Via: store.ViaLink, LinkPublicID: "p000000001", IP: "192.0.2.0", UserAgent: "curl/8",
		}
	}
	t.Run("Append", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(*fixture) store.AccessEntry { return valid() },
			func(f *fixture, e store.AccessEntry) error {
				return f.r.AccessLog.Append(f.t.Context(), []store.AccessEntry{e})
			},
			[]insertCase[store.AccessEntry]{
				{name: "link for a site that no longer exists"},
				{name: "denied", mutate: func(_ *fixture, e *store.AccessEntry) { e.Via, e.LinkPublicID, e.Status = store.ViaNone, "", 401 }},
				{name: "link without public ID", mutate: func(_ *fixture, e *store.AccessEntry) { e.LinkPublicID = "" }, want: store.ErrConstraint},
				{name: "public ID without link", mutate: func(_ *fixture, e *store.AccessEntry) { e.Via = store.ViaPublic }, want: store.ErrConstraint},
				{name: "unknown via", mutate: func(_ *fixture, e *store.AccessEntry) { e.Via = "cookie" }, want: store.ErrConstraint},
				{name: "bad status", mutate: func(_ *fixture, e *store.AccessEntry) { e.Status = 0 }, want: store.ErrConstraint},
			})
	})
	t.Run("AppendIsAtomic", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		good, bad := valid(), valid()
		bad.Via = "cookie"
		wantErr(t, f.r.AccessLog.Append(t.Context(), []store.AccessEntry{good, bad}), store.ErrConstraint)
		// The good entry was rolled back with the batch, so its ID is free.
		wantErr(t, f.r.AccessLog.Append(t.Context(), []store.AccessEntry{good, valid()}), nil)
	})
	t.Run("ListBySite", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s, other := f.site(o.ID), f.site(o.ID)
		var want []store.AccessEntry
		// Two entries share a timestamp, so paging must break ties by ID.
		for i, at := range []time.Duration{0, time.Second, time.Second, 3 * time.Second} {
			e := valid()
			e.SiteID, e.OccurredAt, e.LinkPublicID = s.ID, now.Add(at), f.publicID()
			if i == 0 {
				e.Via, e.LinkPublicID, e.IP, e.UserAgent = store.ViaPublic, "", "", ""
			}
			want = append(want, e)
		}
		noise := valid()
		noise.SiteID = other.ID
		f.must(f.r.AccessLog.Append(t.Context(), append(slices.Clone(want), noise)))
		slices.SortFunc(want, newestFirst)

		all, err := f.r.AccessLog.ListBySite(t.Context(), o.ID, s.ID, store.AccessCursor{}, 10)
		f.must(err)
		if !slices.EqualFunc(all, want, sameEntry) {
			t.Fatalf("ListBySite() = %+v, want %+v", all, want)
		}
		var paged []store.AccessEntry
		for cursor := (store.AccessCursor{}); ; {
			page, err := f.r.AccessLog.ListBySite(t.Context(), o.ID, s.ID, cursor, 3)
			f.must(err)
			paged = append(paged, page...)
			if len(page) < 3 {
				break
			}
			last := page[len(page)-1]
			cursor = store.AccessCursor{OccurredAt: last.OccurredAt, ID: last.ID}
		}
		if !slices.EqualFunc(paged, want, sameEntry) {
			t.Fatalf("paged ListBySite() = %+v, want %+v", paged, want)
		}
		// Another owner and a deleted site see nothing, as for a missing site.
		for name, list := range map[string]func() ([]store.AccessEntry, error){
			"other owner": func() ([]store.AccessEntry, error) {
				return f.r.AccessLog.ListBySite(t.Context(), f.owner().ID, s.ID, store.AccessCursor{}, 10)
			},
			"missing site": func() ([]store.AccessEntry, error) {
				return f.r.AccessLog.ListBySite(t.Context(), o.ID, store.NewID(), store.AccessCursor{}, 10)
			},
		} {
			if got, err := list(); err != nil || len(got) != 0 {
				t.Fatalf("ListBySite(%s) = %v, %v; want none", name, got, err)
			}
		}
	})
	t.Run("ListByLink", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s, other := f.site(o.ID), f.site(o.ID)
		link := f.publicID()
		entry := func(siteID, publicID string, at time.Duration) store.AccessEntry {
			e := valid()
			e.SiteID, e.LinkPublicID, e.OccurredAt = siteID, publicID, now.Add(at)
			return e
		}
		want := []store.AccessEntry{entry(s.ID, link, 0), entry(s.ID, link, time.Second)}
		f.must(f.r.AccessLog.Append(t.Context(), append(slices.Clone(want),
			entry(s.ID, f.publicID(), 2*time.Second), // another link
			entry(other.ID, link, 3*time.Second),     // the same public ID on another site
		)))
		slices.SortFunc(want, newestFirst)
		got, err := f.r.AccessLog.ListByLink(t.Context(), o.ID, s.ID, link, store.AccessCursor{}, 10)
		f.must(err)
		if !slices.EqualFunc(got, want, sameEntry) {
			t.Fatalf("ListByLink() = %+v, want %+v", got, want)
		}
		first, err := f.r.AccessLog.ListByLink(t.Context(), o.ID, s.ID, link, store.AccessCursor{}, 1)
		f.must(err)
		rest, err := f.r.AccessLog.ListByLink(t.Context(), o.ID, s.ID, link, store.AccessCursor{OccurredAt: first[0].OccurredAt, ID: first[0].ID}, 10)
		f.must(err)
		if !slices.EqualFunc(append(first, rest...), want, sameEntry) {
			t.Fatalf("paged ListByLink() = %+v then %+v, want %+v", first, rest, want)
		}
		if got, err := f.r.AccessLog.ListByLink(t.Context(), f.owner().ID, s.ID, link, store.AccessCursor{}, 10); err != nil || len(got) != 0 {
			t.Fatalf("ListByLink(other owner) = %v, %v; want none", got, err)
		}
	})
	t.Run("Prune", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		at := func(siteID string, d time.Duration) store.AccessEntry {
			e := valid()
			e.SiteID, e.OccurredAt = siteID, now.Add(d)
			return e
		}
		cutoff := now.Add(time.Hour)
		kept := []store.AccessEntry{at(s.ID, 2*time.Hour), at(s.ID, time.Hour)} // at the cutoff is kept
		f.must(f.r.AccessLog.Append(t.Context(), append(slices.Clone(kept),
			at(s.ID, time.Hour-time.Millisecond),
			at(s.ID, 0),
			at(store.NewID(), 0), // a deleted site's entries are pruned too
		)))
		n, err := f.r.AccessLog.Prune(t.Context(), cutoff, 100)
		f.must(err)
		if n != 3 {
			t.Fatalf("Prune() = %d, want 3", n)
		}
		got, err := f.r.AccessLog.ListBySite(t.Context(), o.ID, s.ID, store.AccessCursor{}, 10)
		f.must(err)
		if !slices.EqualFunc(got, kept, sameEntry) {
			t.Fatalf("after Prune() = %+v, want %+v", got, kept)
		}
		if n, err := f.r.AccessLog.Prune(t.Context(), cutoff, 100); err != nil || n != 0 {
			t.Fatalf("second Prune() = %d, %v; want 0", n, err)
		}
	})
	t.Run("PruneKeepsHeldSites", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, suspended := f.owner(), f.owner()
		down, held, free := f.site(o.ID), f.site(suspended.ID), f.site(o.ID)
		f.must(f.r.Sites.TakeDown(t.Context(), down.ID, now))
		f.must(f.r.Owners.Suspend(t.Context(), suspended.ID, now))
		var entries []store.AccessEntry
		for _, id := range []string{down.ID, held.ID, free.ID} {
			e := valid()
			e.SiteID, e.OccurredAt = id, now.Add(-time.Hour)
			entries = append(entries, e)
		}
		f.must(f.r.AccessLog.Append(t.Context(), entries))
		n, err := f.r.AccessLog.Prune(t.Context(), now, 100)
		if err != nil || n != 1 {
			t.Fatalf("Prune() = %d, %v; want only the free site's entry", n, err)
		}
		for owner, site := range map[string]string{o.ID: down.ID, suspended.ID: held.ID} {
			got, err := f.r.AccessLog.ListBySite(t.Context(), owner, site, store.AccessCursor{}, 10)
			if err != nil || len(got) != 1 {
				t.Fatalf("held site %s entries = %v, %v", site, got, err)
			}
		}
		// Once restored, retention applies again.
		f.must(f.r.Sites.Restore(t.Context(), down.ID))
		if n, err := f.r.AccessLog.Prune(t.Context(), now, 100); err != nil || n != 1 {
			t.Fatalf("Prune() after restore = %d, %v; want 1", n, err)
		}
	})
	t.Run("PruneInChunks", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		var old []store.AccessEntry
		for range 5 {
			e := valid()
			e.OccurredAt = now.Add(-time.Hour)
			old = append(old, e)
		}
		kept := valid()
		f.must(f.r.AccessLog.Append(t.Context(), append(old, kept)))
		var chunks []int64
		for {
			n, err := f.r.AccessLog.Prune(t.Context(), now, 2)
			f.must(err)
			chunks = append(chunks, n)
			if n < 2 {
				break
			}
		}
		if !slices.Equal(chunks, []int64{2, 2, 1}) {
			t.Fatalf("Prune() chunks = %v, want [2 2 1]", chunks)
		}
		// A limit below 1 still deletes one row at a time.
		f.must(f.r.AccessLog.Append(t.Context(), old[:2]))
		for _, limit := range []int{0, -5} {
			if n, err := f.r.AccessLog.Prune(t.Context(), now, limit); err != nil || n != 1 {
				t.Fatalf("Prune(limit %d) = %d, %v; want 1", limit, n, err)
			}
		}
	})
	t.Run("ListLimitIsClamped", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		batch := make([]store.AccessEntry, store.MaxAccessListLimit+5)
		for i := range batch {
			batch[i] = valid()
			batch[i].SiteID = s.ID
		}
		f.must(f.r.AccessLog.Append(t.Context(), batch))
		for _, tc := range []struct{ limit, want int }{
			{0, 1}, {-1, 1}, {3, 3}, {store.MaxAccessListLimit + 1, store.MaxAccessListLimit}, {1 << 40, store.MaxAccessListLimit},
		} {
			got, err := f.r.AccessLog.ListBySite(t.Context(), o.ID, s.ID, store.AccessCursor{}, tc.limit)
			f.must(err)
			if len(got) != tc.want {
				t.Errorf("ListBySite(limit %d) returned %d, want %d", tc.limit, len(got), tc.want)
			}
			got, err = f.r.AccessLog.ListByLink(t.Context(), o.ID, s.ID, batch[0].LinkPublicID, store.AccessCursor{}, tc.limit)
			f.must(err)
			if len(got) != tc.want {
				t.Errorf("ListByLink(limit %d) returned %d, want %d", tc.limit, len(got), tc.want)
			}
		}
	})
}

func newestFirst(a, b store.AccessEntry) int {
	if c := b.OccurredAt.Compare(a.OccurredAt); c != 0 {
		return c
	}
	switch {
	case a.ID > b.ID:
		return -1
	case a.ID < b.ID:
		return 1
	}
	return 0
}

func sameEntry(a, b store.AccessEntry) bool {
	return a.ID == b.ID && a.OccurredAt.Equal(b.OccurredAt) && a.SiteID == b.SiteID && a.Host == b.Host &&
		a.Path == b.Path && a.Status == b.Status && a.Via == b.Via && a.LinkPublicID == b.LinkPublicID &&
		a.IP == b.IP && a.UserAgent == b.UserAgent
}
