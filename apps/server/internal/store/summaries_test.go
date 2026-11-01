// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"fmt"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// TestSummariesCostBoundedQueries proves Sites.Summaries runs the same
// number of SQL statements for one site as for hundreds, each with tags,
// a custom domain, a link, a policy and requests (V5.5).
func TestSummariesCostBoundedQueries(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	now := time.Now().UTC()
	owner := store.Owner{ID: store.NewID(), Email: "summaries@example.test", CreatedAt: now}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	tag, err := db.Organisation().CreateTag(ctx, store.ActorFor(store.Tag{ID: store.NewID(), OwnerID: owner.ID, Name: "preview", Colour: store.AutoColour, CreatedAt: now}.OwnerID), store.Tag{ID: store.NewID(), OwnerID: owner.ID, Name: "preview", Colour: store.AutoColour, CreatedAt: now}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Traffic().Backfill(ctx, func(string) store.Browser { return store.BrowserOther }, now); err != nil {
		t.Fatal(err)
	}
	w := store.SummaryWindow{Now: now, ExpiringBy: now.Add(48 * time.Hour), MinLifetime: time.Hour, DaysFrom: now.Truncate(24*time.Hour).AddDate(0, 0, -6), Days: 7, ActiveFrom: now.AddDate(0, 0, -30)}

	count := db.CountQueriesForTest()
	made := 0
	for _, n := range []int{1, 20, 150} {
		for ; made < n; made++ {
			s := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: fmt.Sprintf("site-%03d", made), CreatedAt: now}
			must(t, db.Sites().Create(ctx, store.ActorFor(s.OwnerID), s, store.Quotas{}))
			must(t, db.Policies().Create(ctx, store.ActorFor(owner.ID), store.Policy{ID: store.NewID(), SiteID: s.ID, PathPrefix: "/", Visibility: store.Protected, LinksEnabled: true, Version: 1, CreatedAt: now, UpdatedAt: now}))
			must(t, db.Links().Create(ctx, store.ActorFor(owner.ID), store.ShareLink{ID: store.NewID(), SiteID: s.ID, PublicID: fmt.Sprintf("q%09d", made), TokenHash: "hash-" + s.ID, PathPrefix: "/", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}))
			must(t, db.Domains().Create(ctx, store.ActorFor(owner.ID), store.Domain{ID: store.NewID(), SiteID: s.ID, Hostname: fmt.Sprintf("d%d.example.test", made), Status: store.DomainPending, VerificationToken: fmt.Sprintf("verify-token-%06d", made), CreatedAt: now}))
			_, err := db.Organisation().Organise(ctx, store.ActorFor(owner.ID), owner.ID, []string{s.Name}, store.OrganiseChange{AddTagIDs: []string{tag.ID}})
			must(t, err)
			must(t, db.AccessLog().Append(ctx, []store.AccessEntry{{ID: store.NewID(), OccurredAt: now, SiteID: s.ID, Host: "h", Path: "/", Status: 200, Via: store.ViaPublic}}))
		}
		count.Store(0)
		got, err := db.Sites().Summaries(ctx, store.ActorFor(owner.ID), owner.ID, w)
		must(t, err)
		if len(got) != n || got[n-1].WorkingLinks != 1 || got[n-1].Requests[6] != 1 || len(got[n-1].TagIDs) != 1 || len(got[n-1].Domains) != 1 {
			t.Fatalf("%d sites: got %d summaries, last %+v", n, len(got), got[len(got)-1])
		}
		if q := count.Load(); q != 5 { // sites, pins, tags, domains, requests
			t.Fatalf("%d sites cost %d queries; want 5, as for any number", n, q)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
