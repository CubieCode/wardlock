// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// TestEdgeServesStoreDeploys wires the edge to the store, BlobStore and
// Notifier as serve does, then deploys, activates and rolls back through the
// deploys service.
func TestEdgeServesStoreDeploys(t *testing.T) {
	ctx := t.Context()
	db := storetest.New(t)
	blobs := deploys.NewFSBlobStore(t.TempDir())
	notifier := deploys.NewLocalNotifier()
	clock := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	svc := deploys.NewService(deploys.Config{
		Sites: db.Sites(), Deploys: db.Deploys(), Blobs: blobs, Notifier: notifier,
		Limits: deploys.DefaultLimits(),
		Now:    func() time.Time { clock = clock.Add(time.Minute); return clock },
	})
	e := edge.New(edge.Config{
		DashboardHost: "wardlock.localhost",
		Dashboard:     http.NotFoundHandler(),
		Health:        http.NotFoundHandler(),
		Sites:         siteSource{baseDomain: "wardlock.localhost", sites: db.Sites(), domains: db.Domains(), deploys: db.Deploys()},
		Blobs:         blobs,
		Invalidator:   notifier,
	})
	t.Cleanup(e.Close)

	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: clock}
	site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "docs", CreatedAt: clock}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(ctx, site); err != nil {
		t.Fatal(err)
	}

	deploy := func(spa bool, files map[string]string) string {
		t.Helper()
		var m deploys.Manifest
		m.SPA = spa
		for p, body := range files {
			sum := sha256.Sum256([]byte(body))
			m.Files = append(m.Files, store.DeployFile{Path: p, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))})
		}
		d, missing, err := svc.Create(ctx, owner.ID, site.Name, m)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m.Files {
			for _, h := range missing {
				if h == f.SHA256 {
					if err := svc.Upload(ctx, owner.ID, site.Name, d.ID, h, strings.NewReader(files[f.Path])); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		return d.ID
	}
	activate := func(id string) {
		t.Helper()
		if _, _, err := svc.Activate(ctx, owner.ID, site.Name, id); err != nil {
			t.Fatal(err)
		}
	}
	get := func(host, target string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		return rec.Code, string(body)
	}
	expect := func(host, target string, status int, body string) {
		t.Helper()
		code, got := get(host, target)
		if code != status || !strings.Contains(got, body) {
			t.Fatalf("GET %s%s = %d %q, want %d containing %q", host, target, code, got, status, body)
		}
	}
	const host = "docs.wardlock.localhost"

	v1 := deploy(false, map[string]string{"index.html": "<p>First deploy</p>", "guide.html": "<p>Guide</p>"})
	expect(host, "/", 404, "No site") // nothing published yet; not cached
	activate(v1)
	expect(host, "/", 200, "First deploy")
	expect(host, "/guide", 200, "Guide")
	expect(host, "/app/route", 404, "404 page not found")

	// A pending custom domain is an unknown host; an active one serves the site.
	pending := store.Domain{ID: store.NewID(), SiteID: site.ID, Hostname: "pending.example.test", Status: store.DomainPending, VerificationToken: "verify-pending-000001", CreatedAt: clock}
	active := store.Domain{ID: store.NewID(), SiteID: site.ID, Hostname: "reports.example.test", Status: store.DomainActive, VerificationToken: "verify-active-0000001", CreatedAt: clock}
	for _, d := range []store.Domain{pending, active} {
		if err := db.Domains().Create(ctx, owner.ID, d); err != nil {
			t.Fatal(err)
		}
	}
	expect("pending.example.test", "/", 404, "No site")
	expect("reports.example.test", "/", 200, "First deploy")

	// Re-activation: the notifier drops the cached manifest on every host.
	v2 := deploy(true, map[string]string{"index.html": "<p>Second deploy</p>"})
	activate(v2)
	expect(host, "/", 200, "Second deploy")
	expect("reports.example.test", "/", 200, "Second deploy")
	expect(host, "/app/route", 200, "Second deploy")       // SPA from the deploy
	expect(host, "/guide.html", 404, "404 page not found") // gone with v1

	// Rollback restores the earlier deploy and its SPA setting.
	if _, _, err := svc.Rollback(ctx, owner.ID, site.Name, v1); err != nil {
		t.Fatal(err)
	}
	expect(host, "/", 200, "First deploy")
	expect(host, "/app/route", 404, "404 page not found")
	expect(host, "/.wardlock/health", 200, `"ok"`)
}
