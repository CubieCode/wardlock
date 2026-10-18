// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"wardlock.nz/apiclient"
	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
)

var shareURL = regexp.MustCompile(`^http://docs\.wardlock\.example\.test:8080(/[^?]*)\?t=(wl_[a-z2-7]{10}_[A-Za-z0-9_-]{43})$`)

func (hs *harness) createLink(owner, site string, body any) api.CreatedShareLink {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPost, "/sites/"+site+"/links", body)
	res.want(hs.t, http.StatusCreated, "")
	if cc := res.header.Get("Cache-Control"); cc != "no-store" {
		hs.t.Fatalf("create Cache-Control %q", cc)
	}
	var c api.CreatedShareLink
	res.decode(hs.t, &c)
	return c
}

func (hs *harness) links(owner, site, query string) api.ShareLinkPage {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/sites/"+site+"/links"+query, nil)
	res.want(hs.t, http.StatusOK, "")
	var page api.ShareLinkPage
	res.decode(hs.t, &page)
	return page
}

func TestShareLinks(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")

	expires := time.Now().Add(7 * 24 * time.Hour).UnixMilli()
	c := hs.createLink(hs.ownerA, "docs", map[string]any{"label": "Linear ENG-123", "path": "/docs/", "expiresAt": expires, "maxUses": 3})
	m := shareURL.FindStringSubmatch(c.Url)
	if m == nil || m[1] != "/docs/" || m[2] != c.Token || !strings.HasPrefix(c.Token, "wl_"+c.ShareLink.PublicId+"_") {
		t.Fatalf("created %+v", c)
	}
	l := c.ShareLink
	if *l.Label != "Linear ENG-123" || l.Path != "/docs/" || *l.ExpiresAt != expires || *l.MaxUses != 3 || l.Uses != 0 ||
		l.Status != api.ShareLinkStatusActive || l.LastAccessedAt != nil || l.RotatedAt != nil {
		t.Fatalf("link %+v", l)
	}
	// An empty body is a whole-site link that never expires.
	never := hs.createLink(hs.ownerA, "docs", map[string]any{})
	if never.ShareLink.Path != "/" || never.ShareLink.ExpiresAt != nil || never.ShareLink.MaxUses != nil || never.ShareLink.Label != nil {
		t.Fatalf("default link %+v", never.ShareLink)
	}
	if m := shareURL.FindStringSubmatch(never.Url); m == nil || m[1] != "/" {
		t.Fatalf("default URL %s", never.Url)
	}
	unicode := hs.createLink(hs.ownerA, "docs", map[string]any{"path": "/über uns/"})
	if !strings.Contains(unicode.Url, "/%C3%BCber%20uns/?t=") {
		t.Fatalf("URL %s is not escaped", unicode.Url)
	}

	// Listing never shows tokens, newest first, in pages.
	first := hs.links(hs.ownerA, "docs", "?limit=2")
	if len(first.Items) != 2 || first.NextCursor == nil || first.Items[0].Id != unicode.ShareLink.Id {
		t.Fatalf("first page %+v", first)
	}
	second := hs.links(hs.ownerA, "docs", "?limit=2&cursor="+*first.NextCursor)
	if len(second.Items) != 1 || second.Items[0].Id != l.Id || second.NextCursor != nil {
		t.Fatalf("second page %+v", second)
	}
	listed := hs.do(hs.ownerA, http.MethodGet, "/sites/docs/links", nil)
	if strings.Contains(string(listed.body), "wl_") {
		t.Fatalf("listing leaks a token: %s", listed.body)
	}

	// Rotation keeps everything but the secret.
	res := hs.do(hs.ownerA, http.MethodPost, "/sites/docs/links/"+l.PublicId+"/rotate", nil)
	res.want(t, http.StatusOK, "")
	if cc := res.header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("rotate Cache-Control %q", cc)
	}
	var rotated api.CreatedShareLink
	res.decode(t, &rotated)
	if rotated.Token == c.Token || rotated.ShareLink.PublicId != l.PublicId || rotated.ShareLink.Id != l.Id ||
		*rotated.ShareLink.Label != *l.Label || rotated.ShareLink.RotatedAt == nil || !strings.HasSuffix(rotated.Url, rotated.Token) {
		t.Fatalf("rotated %+v", rotated)
	}

	// Revocation removes it from the list; a second revoke is 404.
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/links/"+l.PublicId, nil).want(t, http.StatusNoContent, "")
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/links/"+l.PublicId, nil).want(t, http.StatusNotFound, "not_found")
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/links/"+l.PublicId+"/rotate", nil).want(t, http.StatusNotFound, "not_found")
	if page := hs.links(hs.ownerA, "docs", ""); len(page.Items) != 2 {
		t.Fatalf("after revoke %+v", page)
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"long label", http.MethodPost, "/sites/docs/links", map[string]any{"label": strings.Repeat("l", 201)}, 400, "invalid_share_link"},
		{"control character", http.MethodPost, "/sites/docs/links", map[string]any{"label": "a\tb"}, 400, "invalid_share_link"},
		{"past expiry", http.MethodPost, "/sites/docs/links", map[string]any{"expiresAt": time.Now().Add(-time.Minute).UnixMilli()}, 400, "invalid_share_link"},
		{"zero uses", http.MethodPost, "/sites/docs/links", map[string]any{"maxUses": 0}, 400, "invalid_share_link"},
		{"relative path", http.MethodPost, "/sites/docs/links", map[string]any{"path": "docs/"}, 400, "invalid_share_link"},
		{"reserved path", http.MethodPost, "/sites/docs/links", map[string]any{"path": "/.wardlock/"}, 400, "invalid_share_link"},
		{"bad cursor", http.MethodGet, "/sites/docs/links?cursor=bm9wZQ", nil, 400, "invalid_request"},
		{"missing site", http.MethodGet, "/sites/nope/links", nil, 404, "not_found"},
		{"missing link", http.MethodDelete, "/sites/docs/links/aaaaaaaaaa", nil, 404, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs.do(hs.ownerA, tc.method, tc.path, tc.body).want(t, tc.status, tc.code)
		})
	}
	hs.do("", http.MethodGet, "/sites/docs/links", nil).want(t, http.StatusUnauthorized, "unauthenticated")
}

func TestShareLinksOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "alpha")
	hs.createSite(hs.ownerB, "beta")
	l := hs.createLink(hs.ownerA, "alpha", map[string]any{"label": "A's"}).ShareLink
	missing := hs.do(hs.ownerB, http.MethodGet, "/sites/missing/links", nil)
	missing.want(t, http.StatusNotFound, "not_found")

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"list", http.MethodGet, "/sites/alpha/links", nil},
		{"create", http.MethodPost, "/sites/alpha/links", map[string]any{}},
		{"revoke", http.MethodDelete, "/sites/alpha/links/" + l.PublicId, nil},
		{"rotate", http.MethodPost, "/sites/alpha/links/" + l.PublicId + "/rotate", nil},
		{"revoke through own site", http.MethodDelete, "/sites/beta/links/" + l.PublicId, nil},
		{"rotate through own site", http.MethodPost, "/sites/beta/links/" + l.PublicId + "/rotate", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := hs.do(hs.ownerB, tc.method, tc.path, tc.body)
			res.want(t, http.StatusNotFound, "not_found")
			if string(res.body) != string(missing.body) {
				t.Fatalf("body %s differs from a missing site's %s", res.body, missing.body)
			}
		})
	}
	page := hs.links(hs.ownerA, "alpha", "")
	if len(page.Items) != 1 || page.Items[0].RotatedAt != nil || page.Items[0].Id != l.Id {
		t.Fatalf("owner B changed owner A's links: %+v", page)
	}
}

// TestShareLinksThroughGoClient proves the generated Go client (which
// `wardlock share` uses) covers every share-link operation.
func TestShareLinksThroughGoClient(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	srv := httptest.NewServer(hs.h)
	t.Cleanup(srv.Close)
	c, err := apiclient.NewClientWithResponses(srv.URL, apiclient.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set(ownerHeader, hs.ownerA)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	label, uses := "sent to a reviewer", int32(1)
	created, err := c.CreateShareLinkWithResponse(ctx, "docs", apiclient.CreateShareLinkJSONRequestBody{Label: &label, MaxUses: &uses})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create: %v %s", err, created.Body)
	}
	id := created.JSON201.ShareLink.PublicId
	list, err := c.ListShareLinksWithResponse(ctx, "docs", nil)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Items) != 1 || list.JSON200.Items[0].PublicId != id {
		t.Fatalf("list: %v %s", err, list.Body)
	}
	rotated, err := c.RotateShareLinkWithResponse(ctx, "docs", id)
	if err != nil || rotated.JSON200 == nil || rotated.JSON200.Token == created.JSON201.Token {
		t.Fatalf("rotate: %v %s", err, rotated.Body)
	}
	revoked, err := c.RevokeShareLinkWithResponse(ctx, "docs", id)
	if err != nil || revoked.StatusCode() != http.StatusNoContent {
		t.Fatalf("revoke: %v %d", err, revoked.StatusCode())
	}
}
