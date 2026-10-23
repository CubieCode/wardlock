// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
)

func (hs *harness) analytics(owner, query string) api.Analytics {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/analytics"+query, nil)
	res.want(hs.t, http.StatusOK, "")
	var a api.Analytics
	res.decode(hs.t, &a)
	return a
}

// logTraffic appends entries through the real store after the backfill,
// so they reach the rollup as the access-log writer's batches do.
func (hs *harness) logTraffic(entries ...store.AccessEntry) {
	hs.t.Helper()
	if err := accesslog.Backfill(hs.t.Context(), hs.db.Traffic(), time.Now, slog.New(slog.DiscardHandler)); err != nil {
		hs.t.Fatal(err)
	}
	for i := range entries {
		entries[i].Browser = accesslog.Classify(entries[i].UserAgent)
	}
	if err := hs.db.AccessLog().Append(hs.t.Context(), entries); err != nil {
		hs.t.Fatal(err)
	}
}

func analyticsTotal(a api.Analytics) int64 { return a.Total.Allowed + a.Total.Refused }

// TestAnalyticsScopesAndRanges counts one owner's requests over each range
// and scope, beside another owner whose requests never show.
func TestAnalyticsScopesAndRanges(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	docs := hs.createSite(a, "docs")
	blog := hs.createSite(a, "blog")
	shop := hs.createSite(a, "shop")
	theirs := hs.createSite(b, "theirs")
	clients := hs.createGroup(a, "Clients")
	preview := hs.createTag(a, "preview")
	hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{GroupId: &clients.Id, TagIds: []string{preview.Id}}).want(t, http.StatusOK, "")
	hs.do(a, http.MethodPut, "/sites/blog/organisation", api.SetSiteOrganisationRequest{TagIds: []string{preview.Id}}).want(t, http.StatusOK, "")
	link := hs.createLink(a, "docs", api.CreateShareLinkRequest{Label: ptr("Client review")})

	now := time.Now().UTC()
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }
	day := 24 * time.Hour
	entries := []store.AccessEntry{
		// docs: in every range.
		logEntry(at(time.Hour), 0, docs.Id, store.ViaLink, link.ShareLink.PublicId, "/", http.StatusOK),
		logEntry(at(time.Hour), time.Minute, docs.Id, store.ViaNone, "", "/admin/", http.StatusUnauthorized),
		// blog: 30 and 90 days.
		logEntry(at(10*day), 0, blog.Id, store.ViaPublic, "", "/post", http.StatusOK),
		// shop: 90 days only.
		logEntry(at(40*day), 0, shop.Id, store.ViaPublic, "", "/", http.StatusNotFound),
		// Past every range.
		logEntry(at(100*day), 0, docs.Id, store.ViaPublic, "", "/", http.StatusOK),
		// Another owner's.
		logEntry(at(time.Hour), 0, theirs.Id, store.ViaPublic, "", "/", http.StatusOK),
	}
	entries[1].UserAgent = "curl/8"
	hs.logTraffic(entries...)

	for _, tc := range []struct {
		name, query string
		want        int64
	}{
		{"default is 7 days", "", 2},
		{"7 days", "?days=7", 2},
		{"30 days", "?days=30", 3},
		{"90 days", "?days=90", 4},
		{"site", "?site=blog&days=90", 1},
		{"site over 7 days", "?site=blog", 0},
		{"group", "?group=" + clients.Id + "&days=90", 2},
		{"tag", "?tag=" + preview.Id + "&days=90", 3},
		{"tag over 7 days", "?tag=" + preview.Id, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hs.analytics(a, tc.query)
			if analyticsTotal(got) != tc.want {
				t.Fatalf("total = %+v, want %d requests", got.Total, tc.want)
			}
			var hours int64
			for _, h := range got.Hours {
				hours += h.Allowed + h.Refused
			}
			if hours != tc.want {
				t.Fatalf("hours add up to %d, want %d", hours, tc.want)
			}
			if span := time.Duration(got.To-got.From) * time.Millisecond; span != time.Duration(got.Days)*day || got.To%time.Hour.Milliseconds() != 0 || got.To < now.UnixMilli() {
				t.Fatalf("range %d to %d over %d days", got.From, got.To, got.Days)
			}
		})
	}

	got := hs.analytics(a, "?days=90")
	if got.Days != 90 || got.Total != (api.RequestCount{Allowed: 2, Refused: 2}) {
		t.Fatalf("90-day total = %+v", got.Total)
	}
	if want := fmt.Sprint([]api.AnalyticsSite{{Site: "blog", Allowed: 1}, {Site: "docs", Allowed: 1, Refused: 1}, {Site: "shop", Refused: 1}}); fmt.Sprint(got.Sites) != want {
		t.Errorf("Sites = %v, want %s", got.Sites, want)
	}
	if want := fmt.Sprint([]api.AnalyticsVia{{Via: api.AccessViaLink, Allowed: 1}, {Via: api.AccessViaNone, Refused: 1}, {Via: api.AccessViaPublic, Allowed: 1, Refused: 1}}); fmt.Sprint(got.Via) != want {
		t.Errorf("Via = %v, want %s", got.Via, want)
	}
	if len(got.Groups) != 2 || got.Groups[0].GroupId != nil || got.Groups[0].Allowed+got.Groups[0].Refused != 2 ||
		got.Groups[1].GroupId == nil || *got.Groups[1].GroupId != clients.Id {
		t.Errorf("Groups = %+v, want ungrouped (2) then Clients", got.Groups)
	}
	if len(got.Tags) != 1 || got.Tags[0].TagId != preview.Id || got.Tags[0].Allowed != 2 || got.Tags[0].Refused != 1 {
		t.Errorf("Tags = %+v", got.Tags)
	}
	if len(got.Links) != 1 || got.Links[0].PublicId != link.ShareLink.PublicId || got.Links[0].Site != "docs" || got.Links[0].Label == nil || *got.Links[0].Label != "Client review" {
		t.Errorf("Links = %+v", got.Links)
	}
	// Equally busy paths list by site, then path.
	if len(got.Paths) != 4 || got.Paths[0].Site != "blog" || got.Paths[1].Path != "/" || got.Paths[2].Path != "/admin/" || got.OtherPaths != (api.RequestCount{}) {
		t.Errorf("Paths = %+v, other %+v", got.Paths, got.OtherPaths)
	}
	if want := fmt.Sprint([]api.AnalyticsBrowser{{Browser: api.BrowserFamilyOther, Allowed: 2, Refused: 1}, {Browser: api.BrowserFamilyTool, Refused: 1}}); fmt.Sprint(got.Browsers) != want {
		t.Errorf("Browsers = %v, want %s", got.Browsers, want)
	}

	// The other owner sees only their own site.
	other := hs.do(b, http.MethodGet, "/analytics", nil)
	other.want(t, http.StatusOK, "")
	var ob api.Analytics
	other.decode(t, &ob)
	if analyticsTotal(ob) != 1 || len(ob.Sites) != 1 || ob.Sites[0].Site != "theirs" {
		t.Fatalf("owner B's analytics = %+v", ob)
	}
	// A deleted site's requests go with it; empty lists are [], not null.
	hs.do(b, http.MethodDelete, "/sites/theirs", nil).want(t, http.StatusNoContent, "")
	res := hs.do(b, http.MethodGet, "/analytics", nil)
	res.want(t, http.StatusOK, "")
	for _, s := range []string{`"total":{"allowed":0,"refused":0}`, `"hours":[]`, `"sites":[]`, `"links":[]`, `"browsers":[]`} {
		if !strings.Contains(string(res.body), s) {
			t.Fatalf("after delete %s, want %s", res.body, s)
		}
	}
}

// TestAnalyticsErrors checks validation and owner isolation: another
// owner's site, group or tag answers exactly as a missing one does.
func TestAnalyticsErrors(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	hs.createSite(a, "docs")
	hs.createSite(b, "theirs")
	group, tag := hs.createGroup(a, "Mine"), hs.createTag(a, "mine")
	theirGroup, theirTag := hs.createGroup(b, "Theirs"), hs.createTag(b, "theirs")
	missing := hs.do(a, http.MethodGet, "/analytics?site=missing", nil)
	missing.want(t, http.StatusNotFound, "not_found")

	for _, tc := range []struct {
		name, query  string
		status       int
		code         string
		sameAsAbsent bool
	}{
		{"another owner's site", "?site=theirs", http.StatusNotFound, "not_found", true},
		{"another owner's group", "?group=" + theirGroup.Id, http.StatusNotFound, "not_found", true},
		{"another owner's tag", "?tag=" + theirTag.Id, http.StatusNotFound, "not_found", true},
		{"missing group", "?group=" + store.NewID(), http.StatusNotFound, "not_found", true},
		{"missing tag", "?tag=" + store.NewID(), http.StatusNotFound, "not_found", true},
		{"a range not offered", "?days=14", http.StatusBadRequest, "invalid_request", false},
		{"a range that is not a number", "?days=week", http.StatusBadRequest, "invalid_request", false},
		{"site and group", "?site=docs&group=" + group.Id, http.StatusBadRequest, "invalid_request", false},
		{"group and tag", "?group=" + group.Id + "&tag=" + tag.Id, http.StatusBadRequest, "invalid_request", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := hs.do(a, http.MethodGet, "/analytics"+tc.query, nil)
			res.want(t, tc.status, tc.code)
			if tc.sameAsAbsent && string(res.body) != string(missing.body) {
				t.Fatalf("body %s differs from a missing site's %s", res.body, missing.body)
			}
		})
	}
	hs.do("", http.MethodGet, "/analytics", nil).want(t, http.StatusUnauthorized, "unauthenticated")
}

// TestAnalyticsTokenScopes checks either API token scope reads analytics.
func TestAnalyticsTokenScopes(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	for _, scope := range []api.Scope{api.ScopeDeploy, api.ScopeManage} {
		tok := createToken(t, h, session, string(scope), scope).Token
		wantStatus(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/analytics?days=30"}), http.StatusOK)
	}
}
