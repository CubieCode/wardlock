// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestBackdater runs the demo's statements against a database migrated by
// internal/store and reads the result back through the store, so a schema
// change that breaks them fails here.
func TestBackdater(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "wardlock.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, time.Now); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(time.Now().UnixMilli())
	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: now}
	site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "backdated", CreatedAt: now}
	deploy := store.Deploy{ID: store.NewID(), SiteID: site.ID, FileCount: 1, TotalSize: 5, CreatedAt: now}
	policy := store.Policy{ID: store.NewID(), SiteID: site.ID, PathPrefix: "/", Visibility: store.Protected, LinksEnabled: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	link := store.ShareLink{ID: store.NewID(), SiteID: site.ID, PublicID: "abcdefgh23", TokenHash: "hash", PathPrefix: "/", CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	must(t, db.Owners().Create(ctx, owner))
	must(t, db.Sites().Create(ctx, store.ActorFor(site.OwnerID), site, store.Quotas{}))
	must(t, db.Deploys().Create(ctx, store.ActorFor(owner.ID), deploy, []store.DeployFile{{Path: "index.html", SHA256: sha256Of('a'), Size: 5}}, store.Quotas{}))
	must(t, db.Deploys().RecordUpload(ctx, store.ActorFor(owner.ID), owner.ID, store.Blob{SHA256: sha256Of('a'), Size: 5, CreatedAt: now}))
	_, err = db.Deploys().Activate(ctx, store.ActorFor(owner.ID), site.ID, deploy.ID, now)
	must(t, err)
	pending := store.Deploy{ID: store.NewID(), SiteID: site.ID, FileCount: 1, TotalSize: 5, CreatedAt: now}
	must(t, db.Deploys().Create(ctx, store.ActorFor(owner.ID), pending, []store.DeployFile{{Path: "index.html", SHA256: sha256Of('a'), Size: 5}}, store.Quotas{}))
	must(t, db.Policies().Create(ctx, store.ActorFor(owner.ID), policy))
	must(t, db.Links().Create(ctx, store.ActorFor(owner.ID), link))

	bd, err := openBackdater(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bd.Close() })
	ownerAt, siteAt, deployAt, linkAt := now.Add(-90*24*time.Hour), now.Add(-60*24*time.Hour), now.Add(-59*24*time.Hour), now.Add(-20*24*time.Hour)
	must(t, bd.owner(ctx, site.ID, ownerAt))
	must(t, bd.site(ctx, site.ID, siteAt))
	must(t, bd.deploy(ctx, deploy.ID, deployAt))
	must(t, bd.deploy(ctx, pending.ID, deployAt))
	must(t, bd.link(ctx, link.ID, linkAt))
	if err := bd.link(ctx, store.NewID(), linkAt); err == nil {
		t.Error("backdating a missing link succeeded")
	}

	owners, err := db.Owners().ListAll(ctx, "", 10)
	must(t, err)
	equal(t, "owner", owners[0].CreatedAt, ownerAt)
	gotSite, err := db.Sites().Get(ctx, store.ActorFor(owner.ID), site.ID)
	must(t, err)
	equal(t, "site", gotSite.CreatedAt, siteAt)
	equal(t, "site activation", gotSite.ActivatedAt, deployAt.Add(4*time.Second))
	gotDeploy, err := db.Deploys().Get(ctx, store.ActorFor(owner.ID), site.ID, deploy.ID)
	must(t, err)
	equal(t, "deploy", gotDeploy.CreatedAt, deployAt)
	equal(t, "deploy activation", gotDeploy.ActivatedAt, deployAt.Add(4*time.Second))
	gotPending, err := db.Deploys().Get(ctx, store.ActorFor(owner.ID), site.ID, pending.ID)
	must(t, err)
	if !gotPending.ActivatedAt.IsZero() {
		t.Error("backdating a pending deploy activated it")
	}
	policies, err := db.Policies().ForSite(ctx, site.ID)
	must(t, err)
	equal(t, "policy", policies[0].CreatedAt, siteAt)
	gotLink, err := db.Links().Get(ctx, site.ID, link.ID)
	must(t, err)
	equal(t, "link", gotLink.CreatedAt, linkAt)
	equal(t, "link expiry", gotLink.ExpiresAt, link.ExpiresAt)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func equal(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func sha256Of(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

// parseExpires is the CLI's --expires as a duration from now; 0 is never.
func parseExpires(s string) (time.Duration, error) {
	if s == "never" {
		return 0, nil
	}
	units := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	n, err := strconv.Atoi(s[:len(s)-1])
	unit, ok := units[s[len(s)-1]]
	if err != nil || !ok || n <= 0 {
		return 0, fmt.Errorf("--expires %q", s)
	}
	return time.Duration(n) * unit, nil
}
