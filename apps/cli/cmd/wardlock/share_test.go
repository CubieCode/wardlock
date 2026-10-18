// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"wardlock.nz/apiclient"
)

// handleLinks adds the share-link operations to the fake, as the real
// server answers them. Tokens are synthetic and differ on every create and
// rotate, so tests can tell where one leaked.
func (f *fakeAPI) handleLinks(handle func(pattern, scope string, h func(w http.ResponseWriter, r *http.Request))) {
	f.links = map[string][]*apiclient.ShareLink{}
	handle("GET /api/v0/sites/{site}/links", "", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		items := []apiclient.ShareLink{}
		for _, l := range f.links[s.site.Name] {
			items = append(items, *l)
		}
		page, next := paginate(r, items)
		reply(w, http.StatusOK, apiclient.ShareLinkPage{Items: page, NextCursor: next})
	}))
	handle("POST /api/v0/sites/{site}/links", "manage", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		var body apiclient.CreateShareLinkRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusBadRequest, apiErr("invalid_request", "The request is malformed."))
			return
		}
		if body.Path != nil && !strings.HasSuffix(*body.Path, "/") {
			reply(w, http.StatusBadRequest, apiErr("invalid_share_link", "path must start and end with /, like /docs/."))
			return
		}
		f.nextID++
		l := &apiclient.ShareLink{
			Id: f.id(), PublicId: fmt.Sprintf("lnk%07d", f.nextID)[:10], Path: "/", Status: apiclient.ShareLinkStatusActive,
			CreatedAt: 1_800_000_000_000 + int64(f.nextID), Label: body.Label, ExpiresAt: body.ExpiresAt, MaxUses: body.MaxUses,
		}
		l.PublicId = strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return 'a' + (r - '0')
			}
			return r
		}, l.PublicId)
		if body.Path != nil {
			l.Path = *body.Path
		}
		f.links[s.site.Name] = append([]*apiclient.ShareLink{l}, f.links[s.site.Name]...)
		reply(w, http.StatusCreated, f.createdLink(s, l))
	}))
	withLink := func(h func(w http.ResponseWriter, s *fakeSite, i int)) func(w http.ResponseWriter, r *http.Request) {
		return f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
			for i, l := range f.links[s.site.Name] {
				if l.PublicId == r.PathValue("linkId") {
					h(w, s, i)
					return
				}
			}
			reply(w, http.StatusNotFound, notFound)
		})
	}
	handle("DELETE /api/v0/sites/{site}/links/{linkId}", "manage", withLink(func(w http.ResponseWriter, s *fakeSite, i int) {
		ls := f.links[s.site.Name]
		f.links[s.site.Name] = append(ls[:i:i], ls[i+1:]...)
		w.WriteHeader(http.StatusNoContent)
	}))
	handle("POST /api/v0/sites/{site}/links/{linkId}/rotate", "manage", withLink(func(w http.ResponseWriter, s *fakeSite, i int) {
		l := f.links[s.site.Name][i]
		f.nextID++
		l.RotatedAt = ptr(int64(1_800_000_100_000))
		reply(w, http.StatusOK, f.createdLink(s, l))
	}))
}

// createdLink is l with a fresh synthetic token, as create and rotate answer.
func (f *fakeAPI) createdLink(s *fakeSite, l *apiclient.ShareLink) apiclient.CreatedShareLink {
	token := fmt.Sprintf("wl_%s_%s%06d", l.PublicId, strings.Repeat("S", 37), f.nextID)
	return apiclient.CreatedShareLink{
		ShareLink: *l, Token: token,
		Url: strings.TrimSuffix(s.site.Url, "/") + l.Path + "?t=" + token,
	}
}

func TestShareCreate(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityProtected)
	a, out, errOut := ciApp(t, srv)
	a.now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }

	err := run(t.Context(), a, []string{"share", "create", "docs.wardlock.example.test", "--label", "Linear ENG-123", "--path", "/docs/", "--expires", "7d", "--uses", "1"})
	if err != nil {
		t.Fatalf("create: %v\n%s", err, errOut)
	}
	url := strings.TrimSpace(out.String())
	if !strings.HasPrefix(url, "https://docs."+fakeBase+"/docs/?t=wl_") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("stdout %q, want just the URL", out)
	}
	if msg := errOut.String(); !strings.Contains(msg, "shown only now") || !strings.Contains(msg, `"Linear ENG-123"`) || !strings.Contains(msg, "1 use") || strings.Contains(msg, "wl_") {
		t.Fatalf("stderr %q", msg)
	}
	l := srv.links["docs"][0]
	if *l.ExpiresAt != 1_800_000_000_000+7*24*3600*1000 || *l.MaxUses != 1 || l.Path != "/docs/" {
		t.Fatalf("sent %+v", l)
	}

	// Defaults: whole site, never expires, unlimited.
	out.Reset()
	if err := run(t.Context(), a, []string{"share", "create", "docs", "--json"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "shareLink", "token", "url")
	var created apiclient.CreatedShareLink
	if err := json.Unmarshal(out.Bytes(), &created); err != nil || created.ShareLink.ExpiresAt != nil || created.ShareLink.MaxUses != nil || created.ShareLink.Path != "/" {
		t.Fatalf("%s (%v)", m["shareLink"], err)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"share", "create", "docs", "--expires", "soon"}, "--expires"},
		{[]string{"share", "create", "docs", "--expires", "0d"}, "--expires"},
		{[]string{"share", "create", "docs", "--uses", "-1"}, "--uses"},
		{[]string{"share", "create"}, ""},
		{[]string{"share", "create", "docs", "extra"}, ""},
		{[]string{"share", "create", "Not A Site"}, "not a site name"},
		{[]string{"share"}, ""},
		{[]string{"share", "frobnicate", "docs"}, "unknown share command"},
	} {
		errOut.Reset()
		err := run(t.Context(), a, tc.args)
		if code := exitCode(errOut, err); code != 2 || !strings.Contains(errOut.String(), tc.want) {
			t.Fatalf("%v: exit %d, stderr %q", tc.args, code, errOut)
		}
	}

	// A missing site and a server-side refusal are failures (exit 1).
	errOut.Reset()
	err = run(t.Context(), a, []string{"share", "create", "nope"})
	if exitCode(errOut, err) != 1 || !strings.Contains(errOut.String(), "no site nope") {
		t.Fatalf("missing site: %q", errOut)
	}
	errOut.Reset()
	err = run(t.Context(), a, []string{"share", "create", "docs", "--path", "/docs"})
	if exitCode(errOut, err) != 1 || !strings.Contains(errOut.String(), "start and end with /") {
		t.Fatalf("refused: %q", errOut)
	}
}

func TestShareListRevokeRotate(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", apiclient.PolicyVisibilityProtected)
	a, out, errOut := ciApp(t, srv)
	var tokens []string
	for i, args := range [][]string{
		{"--label", "sent to a reviewer", "--uses", "3"},
		{"--path", "/docs/", "--expires", "1h"},
		{},
	} {
		out.Reset()
		if err := run(t.Context(), a, append([]string{"share", "create", "docs", "--json"}, args...)); err != nil {
			t.Fatalf("create %d: %v\n%s", i, err, errOut)
		}
		var c apiclient.CreatedShareLink
		if err := json.Unmarshal(out.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, c.Token)
	}
	noTokens := func(t *testing.T, s string) {
		t.Helper()
		for _, tok := range tokens {
			if strings.Contains(s, tok) {
				t.Fatalf("a token leaked: %s", s)
			}
		}
	}

	// list pages through every link (the fake serves two per page).
	out.Reset()
	if err := run(t.Context(), a, []string{"share", "list", "docs"}); err != nil {
		t.Fatalf("list: %v\n%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(out.String(), "0 of 3") ||
		!strings.Contains(out.String(), "sent to a reviewer") || !strings.Contains(out.String(), "/docs/") {
		t.Fatalf("list:\n%s", out)
	}
	noTokens(t, out.String())
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "share", "list", "docs"}); err != nil {
		t.Fatal(err)
	}
	var listed shareListResult
	assertJSONKeys(t, out.Bytes(), "site", "links")
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed.Links) != 3 || listed.Site.Name != "docs" {
		t.Fatalf("%s (%v)", out, err)
	}
	noTokens(t, out.String())
	id := listed.Links[0].PublicId

	// rotate prints the new URL once.
	out.Reset()
	errOut.Reset()
	if err := run(t.Context(), a, []string{"share", "rotate", "docs", strings.ToUpper(id)}); err != nil {
		t.Fatalf("rotate: %v\n%s", err, errOut)
	}
	if !strings.Contains(out.String(), "?t=wl_"+id+"_") || !strings.Contains(errOut.String(), "Rotated share link "+id) {
		t.Fatalf("rotate: %q %q", out, errOut)
	}
	noTokens(t, errOut.String())

	// revoke, then revoking again explains the 404 without a token.
	out.Reset()
	if err := run(t.Context(), a, []string{"share", "revoke", "docs", id, "--json"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "revoked", "site", "publicId")
	errOut.Reset()
	err := run(t.Context(), a, []string{"share", "revoke", "docs", id})
	if exitCode(errOut, err) != 1 || !strings.Contains(errOut.String(), "has no share link "+id) {
		t.Fatalf("second revoke: %q", errOut)
	}
	errOut.Reset()
	err = run(t.Context(), a, []string{"share", "rotate", "docs", "not-an-id"})
	if exitCode(errOut, err) != 2 || !strings.Contains(errOut.String(), "not a share link ID") {
		t.Fatalf("bad ID: %q", errOut)
	}

	// Without the manage scope, writes fail and say why.
	srv.mu.Lock()
	srv.scopes = []string{"deploy"}
	srv.mu.Unlock()
	errOut.Reset()
	err = run(t.Context(), a, []string{"share", "revoke", "docs", listed.Links[1].PublicId})
	if exitCode(errOut, err) != 1 || !strings.Contains(errOut.String(), "scope") {
		t.Fatalf("deploy-only revoke: %q", errOut)
	}
	if err := run(t.Context(), a, []string{"share", "list", "docs"}); err != nil {
		t.Fatalf("deploy-only list: %v", err)
	}

	out.Reset()
	srv.links["docs"] = nil
	if err := run(t.Context(), a, []string{"share", "list", "docs"}); err != nil || !strings.Contains(out.String(), "no share links") {
		t.Fatalf("empty list: %v %q", err, out)
	}
}

func TestParseExpiry(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for raw, want := range map[string]time.Duration{"30m": 30 * time.Minute, "1h": time.Hour, "7d": 7 * 24 * time.Hour, "2W": 14 * 24 * time.Hour} {
		if got, err := parseExpiry(raw, now); err != nil || !got.Equal(now.Add(want)) {
			t.Errorf("%s: %v %v", raw, got, err)
		}
	}
	if got, err := parseExpiry("never", now); err != nil || !got.IsZero() {
		t.Errorf("never: %v %v", got, err)
	}
	for _, bad := range []string{"", "d", "-1d", "1y", "1.5h", "100001d"} {
		if _, err := parseExpiry(bad, now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestShareUsageErrorsNeedNoServer: bad flags exit 2 before any request,
// even when the server cannot be reached.
func TestShareUsageErrorsNeedNoServer(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	srv.Close()
	for _, args := range [][]string{
		{"share", "create", "docs", "--expires", "tomorrow"},
		{"share", "create", "docs", "--uses", "-3"},
		{"share", "revoke", "docs", "nope"},
	} {
		errOut.Reset()
		err := run(t.Context(), a, args)
		if code := exitCode(errOut, err); code != 2 || strings.Contains(errOut.String(), "could not reach") {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}
