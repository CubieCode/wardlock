// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// countingSites and countingOrganisation count every repository call.
type countingSites struct {
	store.Sites
	n *int
}

func (c countingSites) Summaries(ctx context.Context, actor store.Actor, ownerID string, w store.SummaryWindow) ([]store.SiteSummary, error) {
	*c.n++
	return c.Sites.Summaries(ctx, store.ActorFor(ownerID), ownerID, w)
}

type countingOrganisation struct {
	store.Organisation
	n *int
}

func (c countingOrganisation) ListGroups(ctx context.Context, actor store.Actor, ownerID string) ([]store.Group, error) {
	*c.n++
	return c.Organisation.ListGroups(ctx, store.ActorFor(ownerID), ownerID)
}

func (c countingOrganisation) ListTags(ctx context.Context, actor store.Actor, ownerID string) ([]store.Tag, error) {
	*c.n++
	return c.Organisation.ListTags(ctx, store.ActorFor(ownerID), ownerID)
}

// TestSummaryPageCostsBoundedCalls proves a page of summaries and the
// counts make the same three repository calls whatever the page size and
// number of sites; store's TestSummariesCostBoundedQueries bounds each
// call's queries.
func TestSummaryPageCostsBoundedCalls(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	owner := store.Owner{ID: store.NewID(), Email: "pages@example.test", CreatedAt: time.Now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc := sites.NewService(sites.Config{
		Sites: countingSites{db.Sites(), &calls}, Organisation: countingOrganisation{db.Organisation(), &calls},
		BaseDomain: "sites.example.test", DashboardHost: "dash.example.test", Now: time.Now,
	})
	for i := range 120 {
		if _, err := svc.Create(ctx, store.ActorFor(owner.ID), owner.ID, fmt.Sprintf("site-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{1, 50, 100} {
		calls = 0
		page, err := svc.SiteSummaries(ctx, store.ActorFor(owner.ID), owner.ID, sites.SummaryFilter{Query: "site"}, sites.SortName, nil, limit)
		if err != nil || len(page.Items) != limit || page.Next == nil {
			t.Fatalf("limit %d: %d items, next %v, %v", limit, len(page.Items), page.Next, err)
		}
		if calls != 3 {
			t.Fatalf("a page of %d made %d repository calls, want 3", limit, calls)
		}
	}
	calls = 0
	if c, err := svc.SiteCounts(ctx, store.ActorFor(owner.ID), owner.ID); err != nil || c.All != 120 || calls != 3 {
		t.Fatalf("SiteCounts() = %+v, %v after %d calls; want 120 sites after 3", c, err, calls)
	}
}
