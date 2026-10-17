// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// TestPoliciesThroughTheEdge wires the edge, the access service and the
// store as serve does, then checks policies end to end: the default for a
// new site, precedence, sessions, version bumps, cookie isolation, logout,
// the reserved path and the protected-site headers.
func TestPoliciesThroughTheEdge(t *testing.T) {
	ctx := t.Context()
	db := storetest.New(t)
	blobs := deploys.NewFSBlobStore(t.TempDir())
	notifier := deploys.NewLocalNotifier()
	now := func() time.Time { return time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC) }
	logs := &bytes.Buffer{}
	acc, err := access.New(access.Config{
		Sites: db.Sites(), Policies: db.Policies(), Sessions: db.Sessions(), Notifier: notifier,
		Secret: []byte(testSecret), Now: time.Now, Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(acc.Close)
	deploySvc := deploys.NewService(deploys.Config{
		Sites: db.Sites(), Deploys: db.Deploys(), Blobs: blobs, Notifier: notifier,
		Limits: deploys.DefaultLimits(), Now: now,
	})
	dashboardHits := 0
	e := edge.New(edge.Config{
		DashboardHost: "wardlock.localhost",
		Dashboard:     http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dashboardHits++ }),
		Health:        http.NotFoundHandler(),
		Sites:         siteSource{baseDomain: "wardlock.localhost", sites: db.Sites(), domains: db.Domains(), deploys: db.Deploys()},
		Blobs:         blobs,
		Invalidator:   notifier,
		Access:        acc,
		Log:           slog.New(slog.NewTextHandler(logs, nil)),
	})
	t.Cleanup(e.Close)

	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	publish := func(name string, files map[string]string) store.Site {
		t.Helper()
		site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: name, CreatedAt: now()}
		if err := db.Sites().Create(ctx, site); err != nil {
			t.Fatal(err)
		}
		var m deploys.Manifest
		for p, body := range files {
			sum := sha256.Sum256([]byte(body))
			m.Files = append(m.Files, store.DeployFile{Path: p, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))})
		}
		d, _, err := deploySvc.Create(ctx, owner.ID, name, m)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m.Files {
			if err := deploySvc.Upload(ctx, owner.ID, name, d.ID, f.SHA256, strings.NewReader(files[f.Path])); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := deploySvc.Activate(ctx, owner.ID, name, d.ID); err != nil {
			t.Fatal(err)
		}
		return site
	}
	files := map[string]string{
		"index.html":          "<p>Home page</p>",
		"404.html":            "<p>Custom not found</p>",
		"assets/site.css":     "body { color: teal }",
		"internal/index.html": "<p>Internal plans</p>",
	}
	docs := publish("docs", files)
	publish("other", files)

	type result struct {
		code int
		body string
		h    http.Header
	}
	do := func(method, host, target, cookie string, browser bool) result {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		req.Host = host
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: access.SessionCookieName, Value: cookie})
		}
		if browser {
			req.Header.Set("Accept", "text/html")
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return result{rec.Code, rec.Body.String(), rec.Result().Header}
	}
	const host, otherHost = "docs.wardlock.localhost", "other.wardlock.localhost"
	expect := func(r result, code int, body, hidden string) {
		t.Helper()
		if r.code != code || !strings.Contains(r.body, body) || (hidden != "" && strings.Contains(r.body, hidden)) {
			t.Fatalf("%d %q, want %d with %q and never %q", r.code, r.body, code, body, hidden)
		}
	}
	protectedHeaders := func(r result, cache string) {
		t.Helper()
		for k, want := range map[string]string{"X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer", "Cache-Control": cache} {
			if got := r.h.Get(k); got != want {
				t.Fatalf("%s = %q, want %q", k, got, want)
			}
		}
	}
	cleared := func(r result) bool {
		return strings.HasPrefix(r.h.Get("Set-Cookie"), access.SessionCookieName+"=;") && strings.Contains(r.h.Get("Set-Cookie"), "Max-Age=0")
	}

	// A new site is protected with share links only.
	r := do(http.MethodGet, host, "/", "", true)
	expect(r, 401, "This page is private", "Home page")
	protectedHeaders(r, "no-store")
	expect(do(http.MethodGet, host, "/", "", false), 401, "protected", "Home page")
	expect(do(http.MethodGet, host, "/no/such/page", "", false), 401, "protected", "Custom not found")
	expect(do(http.MethodGet, host, "/.wardlock/health", "", false), 200, `"ok"`, "")
	expect(do(http.MethodGet, host, "/.wardlock/login", "", true), 200, "share link", "")

	// Precedence: public default, protected /internal/, public /assets/
	// under it would be public again.
	set := func(site, prefix string, vis store.Visibility, links bool) store.Policy {
		t.Helper()
		p, err := acc.Set(ctx, owner.ID, site, access.Rule{PathPrefix: prefix, Visibility: vis, Links: links})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	set("docs", "/", store.Public, false)
	internal := set("docs", "/internal/", store.Protected, true)
	r = do(http.MethodGet, host, "/", "", true)
	expect(r, 200, "Home page", "")
	if r.h.Get("X-Robots-Tag") != "" || r.h.Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatalf("public page headers %v", r.h)
	}
	expect(do(http.MethodGet, host, "/internal/", "", true), 401, "This page is private", "Internal plans")
	expect(do(http.MethodGet, host, "/internal", "", false), 401, "protected", "")
	expect(do(http.MethodGet, host, "/.wardlock/health", "", false), 200, `"ok"`, "")

	// A share-link session (as A2.2 will create) for /internal/ lets the
	// visitor in, with the protected headers.
	links := 0
	issue := func(site store.Site, p store.Policy) string {
		t.Helper()
		links++
		l := store.ShareLink{
			ID: store.NewID(), SiteID: site.ID, PublicID: "link" + strings.Repeat("0", 5) + string(rune('a'+links)),
			TokenHash: store.NewID(), PathPrefix: "/", CreatedAt: now(),
		}
		if err := db.Links().Create(ctx, owner.ID, l); err != nil {
			t.Fatal(err)
		}
		c, err := acc.IssueSession(ctx, access.NewSession{SiteID: site.ID, Policy: p, Method: store.SessionLink, ShareLinkID: l.ID})
		if err != nil {
			t.Fatal(err)
		}
		return c.Value
	}
	cookie := issue(docs, internal)
	r = do(http.MethodGet, host, "/internal/", cookie, true)
	expect(r, 200, "Internal plans", "")
	protectedHeaders(r, "private, max-age=0, must-revalidate")
	expect(do(http.MethodHead, host, "/internal/", cookie, false), 200, "", "")

	// Cookie isolation: the same cookie grants nothing on another site and
	// is cleared there; the dashboard host never reads it.
	set("other", "/", store.Protected, true)
	r = do(http.MethodGet, otherHost, "/", cookie, false)
	expect(r, 401, "protected", "Home page")
	if !cleared(r) {
		t.Fatalf("foreign cookie not cleared: %v", r.h)
	}
	do(http.MethodGet, "wardlock.localhost", "/api/v0/me", cookie, false)
	if dashboardHits != 1 {
		t.Fatalf("dashboard host not routed to the dashboard")
	}

	// A fallback file behind a stricter policy is not served on a public
	// path: protect the default, open /assets/, and ask for a missing asset.
	set("docs", "/", store.Protected, true)
	set("docs", "/assets/", store.Public, false)
	expect(do(http.MethodGet, host, "/assets/site.css", "", false), 200, "teal", "")
	r = do(http.MethodGet, host, "/assets/missing.css", "", false)
	expect(r, 404, "404 page not found", "Custom not found")

	// Changing the /internal/ policy bumps its version: the session ends and
	// its cookie is cleared.
	set("docs", "/internal/", store.Protected, true) // identical: no bump
	expect(do(http.MethodGet, host, "/internal/", cookie, false), 200, "Internal plans", "")
	set("docs", "/internal/", store.Public, false)
	set("docs", "/internal/", store.Protected, true)
	r = do(http.MethodGet, host, "/internal/", cookie, false)
	expect(r, 401, "protected", "Internal plans")
	if !cleared(r) {
		t.Fatalf("stale cookie not cleared: %v", r.h)
	}

	// Logout ends a fresh session.
	ps, err := acc.List(ctx, owner.ID, "docs", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var current store.Policy
	for _, p := range ps {
		if p.PathPrefix == "/internal/" {
			current = p
		}
	}
	cookie = issue(docs, current)
	expect(do(http.MethodGet, host, "/internal/", cookie, false), 200, "Internal plans", "")
	r = do(http.MethodPost, host, "/.wardlock/logout", cookie, false)
	if r.code != 303 || !cleared(r) {
		t.Fatalf("logout = %d %v", r.code, r.h)
	}
	expect(do(http.MethodGet, host, "/internal/", cookie, false), 401, "protected", "Internal plans")

	// Logs name policies and sites, never cookie values.
	enc, sig, _ := strings.Cut(cookie, ".")
	if out := logs.String(); strings.Contains(out, enc) || strings.Contains(out, sig) {
		t.Fatalf("a cookie reached the logs:\n%s", out)
	}
}
