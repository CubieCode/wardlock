// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

const day = 24 * time.Hour

// previewsContract covers Previews (C1.19) and the preview path of
// Deploys.Activate and Rollback.
func previewsContract(t *testing.T, open Opener) {
	// previewDeploy creates and uploads a deploy for preview name, with
	// days as its lifetime (nil for none given), and activates it at.
	previewDeploy := func(f *fixture, ownerID, siteID, name string, days *int, at time.Time) store.Deploy {
		f.t.Helper()
		d := f.validDeploy(siteID)
		d.Preview, d.PreviewDays, d.CreatedAt = name, days, at
		f.must(f.r.Deploys.Create(f.t.Context(), store.ActorFor(ownerID), d, []store.DeployFile{{Path: "index.html", SHA256: sha('b'), Size: 5}}, store.Quotas{}))
		f.uploadAll(ownerID, siteID, d.ID)
		d, err := f.r.Deploys.Activate(f.t.Context(), store.ActorFor(ownerID), siteID, d.ID, at)
		f.must(err)
		return d
	}
	ptr := func(n int) *int { return &n }
	names := previewNames

	t.Run("ActivateGoesToThePreview", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		live := f.activated(o.ID, s.ID, now)
		at := now.Add(time.Hour)
		d := previewDeploy(f, o.ID, s.ID, "pr-1", nil, at)
		if !d.ActivatedAt.IsZero() || !d.PreviewActivatedAt.Equal(at) || d.Preview != "pr-1" || d.PreviewDays != nil {
			t.Fatalf("activated preview deploy = %+v", d)
		}
		site, err := f.r.Sites.Get(t.Context(), store.ActorFor(o.ID), s.ID)
		wantErr(t, err, nil)
		if site.ActiveDeployID != live.ID {
			t.Fatalf("site serves %s, want the live deploy %s", site.ActiveDeployID, live.ID)
		}
		want := store.Preview{SiteID: s.ID, Name: "pr-1", DeployID: d.ID, Days: store.DefaultPreviewDays, CreatedAt: at, DeployedAt: at, ExpiresAt: at.Add(store.DefaultPreviewDays * day)}
		got, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "pr-1", at)
		if err != nil || got != want {
			t.Fatalf("Get() = %+v, %v; want %+v", got, err, want)
		}
		served, err := f.r.Previews.Served(t.Context(), s.ID, "pr-1", at)
		if err != nil || served != want {
			t.Fatalf("Served() = %+v, %v; want %+v", served, err, want)
		}
		stored, err := f.r.Deploys.Get(t.Context(), store.ActorFor(o.ID), s.ID, d.ID)
		if err != nil || stored.Preview != "pr-1" || !stored.PreviewActivatedAt.Equal(at) || !stored.ActivatedAt.IsZero() {
			t.Fatalf("Deploys.Get() = %+v, %v", stored, err)
		}
		_, err = f.r.Deploys.Activate(t.Context(), store.ActorFor(o.ID), s.ID, d.ID, at)
		wantErr(t, err, store.ErrDeployNotPending)
	})

	t.Run("ReplaceKeepsOrSetsTheLifetime", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		first := previewDeploy(f, o.ID, s.ID, "redesign", ptr(0), now)
		got, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "redesign", now.Add(1000*day))
		if err != nil || got.Days != 0 || !got.ExpiresAt.IsZero() || got.DeployID != first.ID {
			t.Fatalf("never-expiring preview = %+v, %v", got, err)
		}
		// No lifetime given: the preview keeps never.
		second := previewDeploy(f, o.ID, s.ID, "redesign", nil, now.Add(day))
		got, err = f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "redesign", now.Add(day))
		if err != nil || got.Days != 0 || !got.ExpiresAt.IsZero() || got.DeployID != second.ID || !got.CreatedAt.Equal(now) || !got.DeployedAt.Equal(now.Add(day)) {
			t.Fatalf("replaced preview = %+v, %v", got, err)
		}
		third := previewDeploy(f, o.ID, s.ID, "redesign", ptr(30), now.Add(2*day))
		got, err = f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "redesign", now.Add(2*day))
		if err != nil || got.Days != 30 || !got.ExpiresAt.Equal(now.Add(32*day)) || got.DeployID != third.ID {
			t.Fatalf("preview with 30 days = %+v, %v", got, err)
		}
		// The next deploy without a lifetime keeps 30 days, from its own time.
		previewDeploy(f, o.ID, s.ID, "redesign", nil, now.Add(3*day))
		got, err = f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "redesign", now.Add(3*day))
		if err != nil || got.Days != 30 || !got.ExpiresAt.Equal(now.Add(33*day)) {
			t.Fatalf("preview keeping 30 days = %+v, %v", got, err)
		}
	})

	t.Run("Expiry", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		previewDeploy(f, o.ID, s.ID, "pr-2", ptr(1), now)
		before, after := now.Add(day-time.Millisecond), now.Add(day)
		if _, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "pr-2", before); err != nil {
			t.Fatalf("Get() just before expiry: %v", err)
		}
		_, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "pr-2", after)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Previews.Served(t.Context(), s.ID, "pr-2", after)
		wantErr(t, err, store.ErrNotFound)
		list, err := f.r.Previews.List(t.Context(), store.ActorFor(o.ID), s.ID, "", 10, after)
		if err != nil || len(list) != 0 {
			t.Fatalf("List() after expiry = %+v, %v", list, err)
		}
		wantErr(t, f.r.Previews.Delete(t.Context(), store.ActorFor(o.ID), s.ID, "pr-2", after), store.ErrNotFound)
		_, _, err = f.r.Previews.Promote(t.Context(), store.ActorFor(o.ID), s.ID, "pr-2", after)
		wantErr(t, err, store.ErrNotFound)
		// Deploying to it again starts afresh, with the default lifetime.
		again := now.Add(2 * day)
		d := previewDeploy(f, o.ID, s.ID, "pr-2", nil, again)
		got, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "pr-2", again)
		if err != nil || got.DeployID != d.ID || got.Days != store.DefaultPreviewDays || !got.CreatedAt.Equal(again) {
			t.Fatalf("preview deployed again after expiry = %+v, %v", got, err)
		}
	})

	t.Run("ListByName", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		for _, n := range []string{"c", "a", "b"} {
			previewDeploy(f, o.ID, s.ID, n, nil, now)
		}
		other := f.site(o.ID)
		previewDeploy(f, o.ID, other.ID, "z", nil, now)
		page, err := f.r.Previews.List(t.Context(), store.ActorFor(o.ID), s.ID, "", 2, now)
		wantErr(t, err, nil)
		next, err := f.r.Previews.List(t.Context(), store.ActorFor(o.ID), s.ID, "b", 2, now)
		wantErr(t, err, nil)
		if !slices.Equal(names(page), []string{"a", "b"}) || !slices.Equal(names(next), []string{"c"}) {
			t.Fatalf("pages = %v, %v", names(page), names(next))
		}
	})

	t.Run("OwnerIsolation", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		s := f.site(a.ID)
		f.activated(a.ID, s.ID, now)
		previewDeploy(f, a.ID, s.ID, "pr-3", nil, now)
		_, err := f.r.Previews.Get(t.Context(), store.ActorFor(b.ID), s.ID, "pr-3", now)
		wantErr(t, err, store.ErrNotFound)
		list, err := f.r.Previews.List(t.Context(), store.ActorFor(b.ID), s.ID, "", 10, now)
		if err != nil || len(list) != 0 {
			t.Fatalf("List() by another owner = %+v, %v", list, err)
		}
		wantErr(t, f.r.Previews.Delete(t.Context(), store.ActorFor(b.ID), s.ID, "pr-3", now), store.ErrNotFound)
		_, _, err = f.r.Previews.Promote(t.Context(), store.ActorFor(b.ID), s.ID, "pr-3", now)
		wantErr(t, err, store.ErrNotFound)
		if _, err := f.r.Previews.Get(t.Context(), store.ActorFor(a.ID), s.ID, "pr-3", now); err != nil {
			t.Fatalf("the owner's preview after another owner's attempts: %v", err)
		}
	})

	t.Run("Promote", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		live := f.activated(o.ID, s.ID, now)
		p := previewDeploy(f, o.ID, s.ID, "pr-4", nil, now.Add(time.Hour))
		// A preview deploy never promoted is no rollback target.
		_, _, err := f.r.Deploys.Rollback(t.Context(), store.ActorFor(o.ID), s.ID, p.ID, now.Add(2*time.Hour))
		wantErr(t, err, store.ErrDeployNotActivated)
		_, _, err = f.r.Deploys.Rollback(t.Context(), store.ActorFor(o.ID), s.ID, "", now.Add(2*time.Hour))
		wantErr(t, err, store.ErrNoPreviousDeploy)

		at := now.Add(3 * time.Hour)
		d, changed, err := f.r.Previews.Promote(t.Context(), store.ActorFor(o.ID), s.ID, "pr-4", at)
		if err != nil || !changed || d.ID != p.ID || !d.ActivatedAt.Equal(at) {
			t.Fatalf("Promote() = %+v, %t, %v", d, changed, err)
		}
		site, err := f.r.Sites.Get(t.Context(), store.ActorFor(o.ID), s.ID)
		if err != nil || site.ActiveDeployID != p.ID {
			t.Fatalf("site after promotion = %+v, %v", site, err)
		}
		if got, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "pr-4", at); err != nil || got.DeployID != p.ID {
			t.Fatalf("preview after promotion = %+v, %v", got, err)
		}
		_, changed, err = f.r.Previews.Promote(t.Context(), store.ActorFor(o.ID), s.ID, "pr-4", at.Add(time.Hour))
		if err != nil || changed {
			t.Fatalf("Promote() again = %t, %v; want no change", changed, err)
		}
		// Promoted, it is ordinary history: roll back to live and forward again.
		back, _, err := f.r.Deploys.Rollback(t.Context(), store.ActorFor(o.ID), s.ID, "", at.Add(2*time.Hour))
		if err != nil || back.ID != live.ID {
			t.Fatalf("Rollback() = %+v, %v; want the earlier live deploy", back, err)
		}
		forward, _, err := f.r.Deploys.Rollback(t.Context(), store.ActorFor(o.ID), s.ID, "", at.Add(3*time.Hour))
		if err != nil || forward.ID != p.ID {
			t.Fatalf("Rollback() again = %+v, %v; want the promoted deploy", forward, err)
		}
	})

	t.Run("PromoteStaleBase", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		base := f.activated(o.ID, s.ID, now)
		d := f.validDeploy(s.ID)
		d.Preview, d.PathPrefix, d.BaseDeployID = "docs", "/docs/", base.ID
		f.must(f.r.Deploys.Create(t.Context(), store.ActorFor(o.ID), d, []store.DeployFile{{Path: "docs/index.html", SHA256: sha('c'), Size: 5}}, store.Quotas{}))
		f.uploadAll(o.ID, s.ID, d.ID)
		_, err := f.r.Deploys.Activate(t.Context(), store.ActorFor(o.ID), s.ID, d.ID, now.Add(time.Hour))
		wantErr(t, err, nil)
		f.activated(o.ID, s.ID, now.Add(2*time.Hour))
		_, _, err = f.r.Previews.Promote(t.Context(), store.ActorFor(o.ID), s.ID, "docs", now.Add(3*time.Hour))
		wantErr(t, err, store.ErrStaleBase)
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := previewDeploy(f, o.ID, s.ID, "gone", nil, now)
		wantErr(t, f.r.Previews.Delete(t.Context(), store.ActorFor(o.ID), s.ID, "gone", now), nil)
		_, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), s.ID, "gone", now)
		wantErr(t, err, store.ErrNotFound)
		wantErr(t, f.r.Previews.Delete(t.Context(), store.ActorFor(o.ID), s.ID, "gone", now), store.ErrNotFound)
		// The deploy waits for garbage collection.
		if _, err := f.r.Deploys.Get(t.Context(), store.ActorFor(o.ID), s.ID, d.ID); err != nil {
			t.Fatalf("deploy after its preview was deleted: %v", err)
		}
		// A taken-down site's previews stay as they are.
		down := f.site(o.ID)
		previewDeploy(f, o.ID, down.ID, "kept", nil, now)
		f.must(f.r.Sites.TakeDown(t.Context(), down.ID, now))
		wantErr(t, f.r.Previews.Delete(t.Context(), store.ActorFor(o.ID), down.ID, "kept", now), store.ErrNotFound)
		if _, err := f.r.Previews.Get(t.Context(), store.ActorFor(o.ID), down.ID, "kept", now); err != nil {
			t.Fatalf("taken-down site's preview: %v", err)
		}
	})

	t.Run("SiteTeardownDeletesPreviews", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		previewDeploy(f, o.ID, s.ID, "pr-5", nil, now)
		f.must(f.r.Sites.Delete(t.Context(), store.ActorFor(o.ID), s.ID))
		_, err := f.r.Previews.Served(t.Context(), s.ID, "pr-5", now)
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("SiteNameExists", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		for name, want := range map[string]bool{s.Name: true, s.Name + "--x": false} {
			got, err := f.r.Previews.SiteNameExists(t.Context(), name)
			if err != nil || got != want {
				t.Fatalf("SiteNameExists(%q) = %t, %v; want %t", name, got, err, want)
			}
		}
	})
}

func previewNames(ps []store.Preview) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}
