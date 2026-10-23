// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
)

func (hs *harness) summaries(owner, query string) api.SiteSummaryPage {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/site-summaries"+query, nil)
	res.want(hs.t, http.StatusOK, "")
	var page api.SiteSummaryPage
	res.decode(hs.t, &page)
	return page
}

func (hs *harness) siteCounts(owner string) api.SiteCounts {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/site-counts", nil)
	res.want(hs.t, http.StatusOK, "")
	var c api.SiteCounts
	res.decode(hs.t, &c)
	return c
}

func summaryNames(items []api.SiteSummary) []string {
	names := make([]string, len(items))
	for i, s := range items {
		names[i] = s.Name
	}
	return names
}

// allSummaries pages through query with limit, checking every page but the
// last carries a cursor.
func (hs *harness) allSummaries(owner, query string, limit int) []string {
	hs.t.Helper()
	var names []string
	cursor := ""
	for range 20 {
		q := url.Values{}
		if query != "" {
			parsed, err := url.ParseQuery(query)
			if err != nil {
				hs.t.Fatal(err)
			}
			q = parsed
		}
		q.Set("limit", strconv.Itoa(limit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		page := hs.summaries(owner, "?"+q.Encode())
		if len(page.Items) > limit {
			hs.t.Fatalf("page of %d with limit %d", len(page.Items), limit)
		}
		names = append(names, summaryNames(page.Items)...)
		if page.NextCursor == nil {
			return names
		}
		cursor = *page.NextCursor
	}
	hs.t.Fatal("more than 20 pages")
	return nil
}

// summaryFixture is owner A's sites for the summary tests:
//
//	alpha    public, live, pinned, group "Client Work", tag preview, 3 requests today
//	bravo    protected with links (one expiring soon, one short-lived, one in a week), tag client, live, 1 today and 1 two days ago
//	charlie  protected, live, taken down, no requests
//	delta    public, never deployed, 1 request 10 days ago
//	echo     public, never deployed, custom domain, no requests
type summaryFixture struct {
	clients, empty       api.Group
	preview, client, odd api.Tag
}

func newSummaryFixture(t *testing.T, hs *harness) summaryFixture {
	a := hs.ownerA
	var fx summaryFixture
	for _, name := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		hs.createSite(a, name)
	}
	fx.clients = hs.createGroup(a, "Client Work")
	fx.empty = hs.createGroup(a, "Empty")
	fx.preview = hs.createTag(a, "preview")
	fx.client = hs.createTag(a, "client")
	fx.odd = hs.createTag(a, "unused")
	hs.do(a, http.MethodPut, "/sites/alpha/organisation", api.SetSiteOrganisationRequest{GroupId: &fx.clients.Id, TagIds: []string{fx.preview.Id}}).want(t, http.StatusOK, "")
	hs.do(a, http.MethodPut, "/sites/alpha/pin", nil).want(t, http.StatusOK, "")
	hs.do(a, http.MethodPut, "/sites/bravo/organisation", api.SetSiteOrganisationRequest{TagIds: []string{fx.client.Id}}).want(t, http.StatusOK, "")

	hs.deploy(a, "alpha", file{"index.html", "alpha"})
	hs.deploy(a, "charlie", file{"index.html", "charlie"})
	m := manifest(file{"index.html", "bravo"})
	m.Message = ptr("Fix typo")
	c := hs.createDeploy(a, "bravo", m)
	hs.upload(a, "bravo", c.Deploy.Id, file{"index.html", "bravo"}).want(t, http.StatusNoContent, "")
	hs.activate(a, "bravo", c.Deploy.Id)

	links := []api.PolicyMethod{api.PolicyMethodLinks}
	hs.setPolicy(a, "bravo", api.SetPolicyRequest{Path: "/", Visibility: api.PolicyVisibilityProtected, Methods: &links})
	hs.setPolicy(a, "charlie", api.SetPolicyRequest{Path: "/", Visibility: api.PolicyVisibilityProtected, Methods: &links})
	now := time.Now()
	for _, in := range []time.Duration{24 * time.Hour, 30 * time.Minute, 7 * 24 * time.Hour} {
		hs.createLink(a, "bravo", api.CreateShareLinkRequest{ExpiresAt: ptr(now.Add(in).UnixMilli())})
	}
	revoked := hs.createLink(a, "bravo", api.CreateShareLinkRequest{ExpiresAt: ptr(now.Add(time.Hour).UnixMilli())})
	hs.do(a, http.MethodDelete, "/sites/bravo/links/"+revoked.ShareLink.PublicId, nil).want(t, http.StatusNoContent, "")
	hs.do(a, http.MethodPost, "/sites/echo/domains", api.AddDomainRequest{Hostname: "www.kowhai.example"}).want(t, http.StatusCreated, "")

	charlie := hs.getSite(a, "charlie")
	if err := hs.db.Sites().TakeDown(t.Context(), charlie.Id, now); err != nil {
		t.Fatal(err)
	}

	today := now.UTC()
	hs.logTraffic(
		logEntry(today, 0, hs.getSite(a, "alpha").Id, store.ViaPublic, "", "/", http.StatusOK),
		logEntry(today, 0, hs.getSite(a, "alpha").Id, store.ViaPublic, "", "/", http.StatusOK),
		logEntry(today, 0, hs.getSite(a, "alpha").Id, store.ViaPublic, "", "/missing", http.StatusNotFound),
		logEntry(today, 0, hs.getSite(a, "bravo").Id, store.ViaNone, "", "/", http.StatusUnauthorized),
		logEntry(today.AddDate(0, 0, -2), 0, hs.getSite(a, "bravo").Id, store.ViaNone, "", "/", http.StatusUnauthorized),
		logEntry(today.AddDate(0, 0, -10), 0, hs.getSite(a, "delta").Id, store.ViaPublic, "", "/", http.StatusOK),
	)

	// Owner B's site, group and tag never show to A.
	hs.createSite(hs.ownerB, "theirs")
	hs.createGroup(hs.ownerB, "Client Work")
	hs.createTag(hs.ownerB, "preview")
	return fx
}

func TestSiteSummaryFields(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	fx := newSummaryFixture(t, hs)
	page := hs.summaries(hs.ownerA, "?sort=name")
	if got := summaryNames(page.Items); !slices.Equal(got, []string{"alpha", "bravo", "charlie", "delta", "echo"}) || page.NextCursor != nil {
		t.Fatalf("sites = %v (next %v)", got, page.NextCursor)
	}
	alpha, bravo, charlie, delta, echo := page.Items[0], page.Items[1], page.Items[2], page.Items[3], page.Items[4]
	todayIndex := 6

	if alpha.Visibility != api.PolicyVisibilityPublic || len(alpha.Methods) != 0 || !alpha.Pinned || alpha.GroupId == nil || *alpha.GroupId != fx.clients.Id ||
		!slices.Equal(alpha.TagIds, []string{fx.preview.Id}) || alpha.Unavailable || alpha.Quiet || alpha.Requests[todayIndex] != 3 ||
		alpha.Hostname != "alpha."+baseDomain || alpha.Url == "" {
		t.Errorf("alpha = %+v", alpha)
	}
	if alpha.LiveDeploy == nil || alpha.LiveDeploy.ShortId != alpha.LiveDeploy.Id[len(alpha.LiveDeploy.Id)-8:] || alpha.LiveDeploy.Message != nil || alpha.LiveDeploy.ActivatedAt == 0 {
		t.Errorf("alpha live deploy = %+v", alpha.LiveDeploy)
	}
	if bravo.Visibility != api.PolicyVisibilityProtected || !slices.Equal(bravo.Methods, []api.PolicyMethod{api.PolicyMethodLinks}) ||
		bravo.GroupId != nil || bravo.Pinned || bravo.WorkingLinks != 3 || bravo.ExpiringLinks != 1 || bravo.Quiet {
		t.Errorf("bravo = %+v", bravo)
	}
	if !slices.Equal(bravo.Requests, []int64{0, 0, 0, 0, 1, 0, 1}) {
		t.Errorf("bravo requests = %v, want one two days ago and one today", bravo.Requests)
	}
	if bravo.LiveDeploy == nil || bravo.LiveDeploy.Message == nil || *bravo.LiveDeploy.Message != "Fix typo" {
		t.Errorf("bravo live deploy = %+v", bravo.LiveDeploy)
	}
	if !charlie.Unavailable || !charlie.Quiet || charlie.Visibility != api.PolicyVisibilityProtected || charlie.WorkingLinks != 0 {
		t.Errorf("charlie = %+v", charlie)
	}
	if delta.Quiet || !slices.Equal(delta.Requests, make([]int64, 7)) || delta.LiveDeploy != nil {
		t.Errorf("delta = %+v; want not quiet, no requests in the seven days, nothing live", delta)
	}
	if !echo.Quiet || len(echo.TagIds) != 0 || echo.TagIds == nil {
		t.Errorf("echo = %+v", echo)
	}
}

func TestSiteSummaryFilters(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	fx := newSummaryFixture(t, hs)
	all := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for _, tc := range []struct {
		name  string
		query url.Values
		want  []string
	}{
		{"none", url.Values{}, all},
		{"group", url.Values{"group": {fx.clients.Id}}, []string{"alpha"}},
		{"empty group", url.Values{"group": {fx.empty.Id}}, nil},
		{"no group", url.Values{"group": {"none"}}, []string{"bravo", "charlie", "delta", "echo"}},
		{"tag", url.Values{"tag": {fx.client.Id}}, []string{"bravo"}},
		{"unused tag", url.Values{"tag": {fx.odd.Id}}, nil},
		{"pinned", url.Values{"view": {"pinned"}}, []string{"alpha"}},
		{"expiring", url.Values{"view": {"expiring"}}, []string{"bravo"}},
		{"public leaves out the taken-down", url.Values{"view": {"public"}}, []string{"alpha", "delta", "echo"}},
		{"quiet leaves out the taken-down", url.Values{"view": {"quiet"}}, []string{"echo"}},
		{"untagged", url.Values{"view": {"untagged"}}, []string{"charlie", "delta", "echo"}},
		{"down", url.Values{"view": {"down"}}, []string{"charlie"}},
		{"is:public", url.Values{"q": {"is:public"}}, []string{"alpha", "delta", "echo"}},
		{"is:protected", url.Values{"q": {"is:protected"}}, []string{"bravo"}},
		{"is:down", url.Values{"q": {"IS:DOWN"}}, []string{"charlie"}},
		{"is:pinned", url.Values{"q": {"is:pinned"}}, []string{"alpha"}},
		{"unknown is: matches nothing", url.Values{"q": {"is:quiet"}}, nil},
		{"tag:", url.Values{"q": {"tag:Client"}}, []string{"bravo"}},
		{"tag: is exact", url.Values{"q": {"tag:clie"}}, nil},
		{"two tags", url.Values{"q": {"tag:client tag:preview"}}, nil},
		{"group: prefix with hyphens for spaces", url.Values{"q": {"group:client-w"}}, []string{"alpha"}},
		{"group:none", url.Values{"q": {"group:none"}}, []string{"bravo", "charlie", "delta", "echo"}},
		{"group: unknown", url.Values{"q": {"group:board"}}, nil},
		{"text in a name", url.Values{"q": {"LPH"}}, []string{"alpha"}},
		{"text in a custom domain", url.Values{"q": {"kowhai"}}, []string{"echo"}},
		{"text in a hostname", url.Values{"q": {"bravo." + baseDomain}}, []string{"bravo"}},
		{"text in a tag name", url.Values{"q": {"previ"}}, []string{"alpha"}},
		{"text in a group name", url.Values{"q": {"work"}}, []string{"alpha"}},
		{"every word must match", url.Values{"q": {"client work"}}, []string{"alpha"}},
		{"a bare token is text", url.Values{"q": {"tag:"}}, nil},
		{"filters combine", url.Values{"group": {"none"}, "view": {"untagged"}, "q": {"is:public"}}, []string{"delta", "echo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.query.Set("sort", "name")
			page := hs.summaries(hs.ownerA, "?"+tc.query.Encode())
			if got := summaryNames(page.Items); !slices.Equal(got, tc.want) && len(got)+len(tc.want) > 0 {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSiteSummarySortsAndPaging(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	newSummaryFixture(t, hs)
	byName := hs.summaries(hs.ownerA, "?sort=name").Items

	// Deploy order: newest activation first, then the never-deployed by name.
	live := slices.DeleteFunc(slices.Clone(byName), func(s api.SiteSummary) bool { return s.LiveDeploy == nil })
	slices.SortStableFunc(live, func(a, b api.SiteSummary) int { return int(b.LiveDeploy.ActivatedAt - a.LiveDeploy.ActivatedAt) })
	byDeploy := append(summaryNames(live), "delta", "echo")

	for _, tc := range []struct {
		sort string
		want []string
	}{
		{"", byDeploy},
		{"deploy", byDeploy},
		{"name", []string{"alpha", "bravo", "charlie", "delta", "echo"}},
		{"requests", []string{"alpha", "bravo", "charlie", "delta", "echo"}},
		{"links", []string{"bravo", "alpha", "charlie", "delta", "echo"}},
	} {
		t.Run("sort "+tc.sort, func(t *testing.T) {
			query := ""
			if tc.sort != "" {
				query = "sort=" + tc.sort
			}
			if got := summaryNames(hs.summaries(hs.ownerA, "?"+query).Items); !slices.Equal(got, tc.want) {
				t.Fatalf("one page: %v, want %v", got, tc.want)
			}
			for _, limit := range []int{1, 2, 4, 5, 100} {
				if got := hs.allSummaries(hs.ownerA, query, limit); !slices.Equal(got, tc.want) {
					t.Fatalf("pages of %d: %v, want %v", limit, got, tc.want)
				}
			}
		})
	}
	// Paging a filtered list keeps the filter.
	if got := hs.allSummaries(hs.ownerA, "view=public&sort=links", 1); !slices.Equal(got, []string{"alpha", "delta", "echo"}) {
		t.Fatalf("filtered pages: %v", got)
	}

	page := hs.summaries(hs.ownerA, "?sort=name&limit=2")
	if page.NextCursor == nil {
		t.Fatal("no cursor after the first page")
	}
	for _, tc := range []struct{ name, query, target string }{
		{"cursor of another sort", "?sort=links&cursor=" + url.QueryEscape(*page.NextCursor), "cursor"},
		{"cursor not ours", "?cursor=bm90LW91cnM", "cursor"},
		{"cursor not base64", "?cursor=%21%21", "cursor"},
		{"limit 0", "?limit=0", "limit"},
		{"limit 101", "?limit=101", "limit"},
		{"unknown sort", "?sort=size", "sort"},
		{"unknown view", "?view=busy", "view"},
		{"long search", "?q=" + strings.Repeat("a", 201), "q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := hs.do(hs.ownerA, http.MethodGet, "/site-summaries"+tc.query, nil)
			res.want(t, http.StatusBadRequest, "invalid_request")
			if e := res.err(t); e.Target == nil || *e.Target != tc.target {
				t.Fatalf("target %v, want %q", e.Target, tc.target)
			}
		})
	}
	hs.do(hs.ownerA, http.MethodGet, "/site-summaries?q="+strings.Repeat("ā", 200), nil).want(t, http.StatusOK, "")
}

func TestSiteCountsMatchTheList(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	fx := newSummaryFixture(t, hs)
	c := hs.siteCounts(hs.ownerA)
	want := api.SiteCounts{
		All:     5,
		Views:   api.SiteViewCounts{Pinned: 1, Expiring: 1, Public: 3, Quiet: 1, Untagged: 3, Down: 1},
		Groups:  []api.GroupSiteCount{{GroupId: fx.clients.Id, Sites: 1}, {GroupId: fx.empty.Id, Sites: 0}},
		NoGroup: 4,
		Tags:    []api.TagSiteCount{{TagId: fx.client.Id, Sites: 1}, {TagId: fx.preview.Id, Sites: 1}, {TagId: fx.odd.Id, Sites: 0}},
	}
	if c.All != want.All || c.Views != want.Views || c.NoGroup != want.NoGroup || !slices.Equal(c.Groups, want.Groups) || !slices.Equal(c.Tags, want.Tags) {
		t.Fatalf("counts = %+v\nwant     %+v", c, want)
	}
	count := func(query string) api.SiteCount {
		return api.SiteCount(len(hs.allSummaries(hs.ownerA, query, 100)))
	}
	if count("") != c.All || count("group=none") != c.NoGroup {
		t.Error("all or no-group count differs from the list")
	}
	for view, n := range map[string]api.SiteCount{
		"pinned": c.Views.Pinned, "expiring": c.Views.Expiring, "public": c.Views.Public,
		"quiet": c.Views.Quiet, "untagged": c.Views.Untagged, "down": c.Views.Down,
	} {
		if got := count("view=" + view); got != n {
			t.Errorf("view %s lists %d, counts %d", view, got, n)
		}
	}
	for _, g := range c.Groups {
		if got := count("group=" + g.GroupId); got != g.Sites {
			t.Errorf("group %s lists %d, counts %d", g.GroupId, got, g.Sites)
		}
	}
	for _, tg := range c.Tags {
		if got := count("tag=" + tg.TagId); got != tg.Sites {
			t.Errorf("tag %s lists %d, counts %d", tg.TagId, got, tg.Sites)
		}
	}
}

func TestSiteSummariesOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	fx := newSummaryFixture(t, hs)
	b := hs.ownerB
	if got := summaryNames(hs.summaries(b, "").Items); !slices.Equal(got, []string{"theirs"}) {
		t.Fatalf("B lists %v", got)
	}
	c := hs.siteCounts(b)
	if c.All != 1 || len(c.Groups) != 1 || len(c.Tags) != 1 || c.Groups[0].GroupId == fx.clients.Id || c.Tags[0].TagId == fx.preview.Id {
		t.Fatalf("B counts %+v", c)
	}
	// A's group and tag in B's filters, and IDs no one has, answer the
	// same 404.
	var bodies []string
	for _, query := range []string{
		"?group=" + fx.clients.Id, "?tag=" + fx.preview.Id,
		"?group=" + store.NewID(), "?tag=" + store.NewID(), "?group=not-an-id",
	} {
		res := hs.do(b, http.MethodGet, "/site-summaries"+query, nil)
		res.want(t, http.StatusNotFound, "not_found")
		bodies = append(bodies, string(res.body))
	}
	for _, body := range bodies[1:] {
		if body != bodies[0] {
			t.Fatalf("404 bodies differ: %q and %q", bodies[0], body)
		}
	}
	// B's search never finds A's sites by their tags, group or names.
	for _, q := range []string{"alpha", "tag:client", "group:client", "is:pinned"} {
		if got := hs.summaries(b, "?q="+url.QueryEscape(q)).Items; len(got) != 0 {
			t.Errorf("B searching %q finds %v", q, summaryNames(got))
		}
	}
}

func TestSiteSummariesTokenScopes(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	for _, scope := range []api.Scope{api.ScopeDeploy, api.ScopeManage} {
		tok := createToken(t, h, session, string(scope), scope).Token
		for _, path := range []string{"/api/v0/site-summaries", "/api/v0/site-counts", "/api/v0/sign-in-providers"} {
			wantStatus(t, h.bearer(tok, req{method: http.MethodGet, path: path}), http.StatusOK)
		}
	}
	for _, path := range []string{"/api/v0/site-summaries", "/api/v0/site-counts", "/api/v0/sign-in-providers"} {
		wantError(t, h.bearer("wl_aaaaaaaaaa_"+strings.Repeat("A", 43), req{method: http.MethodGet, path: path}), http.StatusUnauthorized, "unauthenticated")
	}
}
