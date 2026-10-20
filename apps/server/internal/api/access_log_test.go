// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

func (hs *harness) siteAccessLog(owner, site, query string) api.AccessLogPage {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/sites/"+site+"/access-log"+query, nil)
	res.want(hs.t, http.StatusOK, "")
	if tokenShape.Match(res.body) {
		hs.t.Fatalf("access log leaks a token: %s", res.body)
	}
	var page api.AccessLogPage
	res.decode(hs.t, &page)
	return page
}

// logEntry is an access-log row for site, at offset from base.
func logEntry(base time.Time, at time.Duration, siteID string, via store.AccessVia, link, path string, status int) store.AccessEntry {
	return store.AccessEntry{
		ID: store.NewID(), OccurredAt: base.Add(at), SiteID: siteID, Host: "docs." + baseDomain,
		Path: path, Status: status, Via: via, LinkPublicID: link,
		IP: "203.0.113.0", UserAgent: "Mozilla/5.0 (test)",
	}
}

func TestSiteAccessLog(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	docs := hs.createSite(hs.ownerA, "docs")
	other := hs.createSite(hs.ownerA, "other")
	theirs := hs.createSite(hs.ownerB, "theirs")
	labelled := hs.createLink(hs.ownerA, "docs", map[string]any{"label": "sent to a reviewer"})
	unlabelled := hs.createLink(hs.ownerA, "docs", map[string]any{}).ShareLink
	id := labelled.ShareLink.PublicId

	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond).UTC()
	// Every way in, two entries sharing a millisecond, one without IP or
	// User-Agent; plus another site's and another owner's entries, which
	// must not appear.
	mine := []store.AccessEntry{
		logEntry(base, 0, docs.Id, store.ViaPublic, "", "/", http.StatusOK),
		logEntry(base, time.Minute, docs.Id, store.ViaLink, id, "/docs/", http.StatusOK),
		logEntry(base, time.Minute, docs.Id, store.ViaLink, unlabelled.PublicId, "/docs/a", http.StatusOK),
		logEntry(base, 2*time.Minute, docs.Id, store.ViaPassword, "", "/b", http.StatusOK),
		logEntry(base, 3*time.Minute, docs.Id, store.ViaEmail, "", "/c", http.StatusNotModified),
		logEntry(base, 4*time.Minute, docs.Id, store.ViaOAuth, "", "/d", http.StatusOK),
		logEntry(base, 5*time.Minute, docs.Id, store.ViaNone, "", "/e", http.StatusUnauthorized),
		logEntry(base, 6*time.Minute, docs.Id, store.ViaLink, id, "/docs/f", http.StatusOK),
	}
	mine[3].IP, mine[3].UserAgent = "", ""
	noise := []store.AccessEntry{
		logEntry(base, 7*time.Minute, other.Id, store.ViaPublic, "", "/other-site", http.StatusOK),
		logEntry(base, 7*time.Minute, theirs.Id, store.ViaPublic, "", "/their-docs", http.StatusOK),
	}
	if err := hs.db.AccessLog().Append(t.Context(), append(slices.Clone(mine), noise...)); err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(mine)
	slices.SortFunc(want, func(a, b store.AccessEntry) int {
		if c := b.OccurredAt.Compare(a.OccurredAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	// A revoked link's entries keep its label.
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/links/"+id, nil).want(t, http.StatusNoContent, "")

	var got []api.AccessLogEntry
	query := "?limit=3"
	for pages := 0; ; pages++ {
		page := hs.siteAccessLog(hs.ownerA, "docs", query)
		got = append(got, page.Items...)
		if page.NextCursor == nil {
			if pages != 2 {
				t.Fatalf("%d pages, want 3", pages+1)
			}
			break
		}
		if len(page.Items) != 3 {
			t.Fatalf("a page with a cursor has %d items", len(page.Items))
		}
		query = "?limit=3&cursor=" + *page.NextCursor
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, g := range got {
		w := want[i]
		if g.Id != w.ID || g.OccurredAt != w.OccurredAt.UnixMilli() || g.Path != w.Path || g.Host != w.Host ||
			int(g.Status) != w.Status || string(g.Via) != string(w.Via) {
			t.Fatalf("entry %d: %+v, want %+v", i, g, w)
		}
		switch w.LinkPublicID {
		case "":
			if g.LinkPublicId != nil || g.LinkLabel != nil {
				t.Fatalf("entry %d names a link: %+v", i, g)
			}
		case id:
			if g.LinkPublicId == nil || *g.LinkPublicId != id || g.LinkLabel == nil || *g.LinkLabel != "sent to a reviewer" {
				t.Fatalf("entry %d: %+v", i, g)
			}
		default:
			if g.LinkPublicId == nil || *g.LinkPublicId != w.LinkPublicID || g.LinkLabel != nil {
				t.Fatalf("entry %d: unlabelled link %+v", i, g)
			}
		}
		if w.UserAgent == "" {
			if g.UserAgent != nil || g.Ip != nil {
				t.Fatalf("entry %d: absent values sent: %+v", i, g)
			}
		} else if g.UserAgent == nil || *g.UserAgent != w.UserAgent || g.Ip == nil || *g.Ip != w.IP {
			t.Fatalf("entry %d: %+v", i, g)
		}
	}
	all := hs.do(hs.ownerA, http.MethodGet, "/sites/docs/access-log", nil)
	all.want(t, http.StatusOK, "")
	if strings.Contains(string(all.body), labelled.Token) || strings.Contains(string(all.body), "null") {
		t.Fatalf("body %s", all.body)
	}

	// The link access log carries the label too.
	if page := hs.accessLog(hs.ownerA, "docs", id, ""); len(page.Items) != 2 || page.Items[0].LinkLabel == nil || *page.Items[0].LinkLabel != "sent to a reviewer" {
		t.Fatalf("link log %+v", page)
	}
	// Owner B reads only their own site.
	if page := hs.siteAccessLog(hs.ownerB, "theirs", ""); len(page.Items) != 1 || page.Items[0].Path != "/their-docs" {
		t.Fatalf("owner B's site %+v", page)
	}
	// A site with no visits lists an empty page.
	hs.createSite(hs.ownerA, "quiet")
	if page := hs.siteAccessLog(hs.ownerA, "quiet", ""); len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("quiet site %+v", page)
	}

	missing := hs.do(hs.ownerA, http.MethodGet, "/sites/missing/access-log", nil)
	missing.want(t, http.StatusNotFound, "not_found")
	for _, tc := range []struct {
		name, owner, path string
		status            int
		code              string
		sameAsAbsent      bool
	}{
		{"another owner's site", hs.ownerB, "/sites/docs/access-log", http.StatusNotFound, "not_found", true},
		{"own site through another owner", hs.ownerA, "/sites/theirs/access-log", http.StatusNotFound, "not_found", true},
		{"limit too small", hs.ownerA, "/sites/docs/access-log?limit=0", http.StatusBadRequest, "invalid_request", false},
		{"limit too large", hs.ownerA, "/sites/docs/access-log?limit=101", http.StatusBadRequest, "invalid_request", false},
		{"forged cursor", hs.ownerA, "/sites/docs/access-log?cursor=bm90LWEtY3Vyc29y", http.StatusBadRequest, "invalid_request", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := hs.do(tc.owner, http.MethodGet, tc.path, nil)
			res.want(t, tc.status, tc.code)
			if tc.sameAsAbsent && string(res.body) != string(missing.body) {
				t.Fatalf("body %s differs from a missing site's %s", res.body, missing.body)
			}
		})
	}
	hs.do("", http.MethodGet, "/sites/docs/access-log", nil).want(t, http.StatusUnauthorized, "unauthenticated")
}

// TestSiteAccessLogAfterTeardown proves a torn-down site's entries, which
// outlive it, are not reachable by name, even once another owner reuses
// the name.
func TestSiteAccessLogAfterTeardown(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	gone := hs.createSite(hs.ownerA, "gone")
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond).UTC()
	if err := hs.db.AccessLog().Append(t.Context(), []store.AccessEntry{
		logEntry(base, 0, gone.Id, store.ViaPublic, "", "/before-teardown", http.StatusOK),
	}); err != nil {
		t.Fatal(err)
	}
	if page := hs.siteAccessLog(hs.ownerA, "gone", ""); len(page.Items) != 1 {
		t.Fatalf("before teardown %+v", page)
	}
	missing := hs.do(hs.ownerA, http.MethodGet, "/sites/missing/access-log", nil)
	hs.do(hs.ownerA, http.MethodDelete, "/sites/gone", nil).want(t, http.StatusNoContent, "")

	res := hs.do(hs.ownerA, http.MethodGet, "/sites/gone/access-log", nil)
	res.want(t, http.StatusNotFound, "not_found")
	if string(res.body) != string(missing.body) {
		t.Fatalf("body %s differs from a missing site's %s", res.body, missing.body)
	}
	hs.createSite(hs.ownerB, "gone")
	if page := hs.siteAccessLog(hs.ownerB, "gone", ""); len(page.Items) != 0 {
		t.Fatalf("a new site named gone lists the old one's entries: %+v", page)
	}
	hs.do(hs.ownerA, http.MethodGet, "/sites/gone/access-log", nil).want(t, http.StatusNotFound, "not_found")
}
