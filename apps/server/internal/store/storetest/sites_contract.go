// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func sitesContract(t *testing.T, open Opener) {
	t.Run("CreateGet", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		want := f.validSite(f.owner().ID)
		want.SPAFallback = true
		f.must(f.r.Sites.Create(t.Context(), want))
		got, err := f.r.Sites.Get(t.Context(), want.OwnerID, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("Get() = %+v, want %+v", got, want)
		}
	})
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.Site { return f.validSite(f.owner().ID) },
			func(f *fixture, s store.Site) error { return f.r.Sites.Create(f.t.Context(), s) },
			[]insertCase[store.Site]{
				{name: "valid"},
				{name: "one character", mutate: func(_ *fixture, s *store.Site) { s.Name = "a" }},
				{name: "63 characters", mutate: func(_ *fixture, s *store.Site) { s.Name = strings.Repeat("a", 63) }},
				{name: "duplicate name across owners", mutate: func(f *fixture, s *store.Site) {
					s.Name = f.site(f.owner().ID).Name
				}, want: store.ErrConflict},
				{name: "empty name", mutate: func(_ *fixture, s *store.Site) { s.Name = "" }, want: store.ErrConstraint},
				{name: "64 characters", mutate: func(_ *fixture, s *store.Site) { s.Name = strings.Repeat("a", 64) }, want: store.ErrConstraint},
				{name: "uppercase", mutate: func(_ *fixture, s *store.Site) { s.Name = "Docs" }, want: store.ErrConstraint},
				{name: "leading hyphen", mutate: func(_ *fixture, s *store.Site) { s.Name = "-docs" }, want: store.ErrConstraint},
				{name: "trailing hyphen", mutate: func(_ *fixture, s *store.Site) { s.Name = "docs-" }, want: store.ErrConstraint},
				{name: "unknown owner", mutate: func(_ *fixture, s *store.Site) { s.OwnerID = store.NewID() }, want: store.ErrConstraint},
			})
	})
	t.Run("GetOtherOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		s := f.site(f.owner().ID)
		_, err := f.r.Sites.Get(t.Context(), f.owner().ID, s.ID)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Sites.Get(t.Context(), s.OwnerID, store.NewID())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("SetActiveDeploy", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.deploy(o.ID, s.ID)
		otherSite := f.site(o.ID)
		otherDeploy := f.deploy(o.ID, otherSite.ID)

		wantErr(t, f.r.Sites.SetActiveDeploy(t.Context(), f.owner().ID, s.ID, d.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.SetActiveDeploy(t.Context(), o.ID, s.ID, otherDeploy.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.SetActiveDeploy(t.Context(), o.ID, s.ID, store.NewID()), store.ErrNotFound)
		wantErr(t, f.r.Sites.SetActiveDeploy(t.Context(), o.ID, s.ID, d.ID), nil)
		got, err := f.r.Sites.Get(t.Context(), o.ID, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveDeployID != d.ID {
			t.Fatalf("ActiveDeployID = %q, want %q", got.ActiveDeployID, d.ID)
		}
	})
	t.Run("DeleteCascades", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.deploy(o.ID, s.ID)
		f.must(f.r.Sites.SetActiveDeploy(t.Context(), o.ID, s.ID, d.ID))
		p := f.policy(o.ID, s.ID)
		l := f.link(o.ID, s.ID)
		dom := f.validDomain(s.ID)
		f.must(f.r.Domains.Create(t.Context(), o.ID, dom))
		sess := store.SiteSession{
			ID: store.NewID(), SiteID: s.ID, TokenHash: f.hash(), Method: store.SessionLink, ShareLinkID: l.ID,
			PolicyID: p.ID, PolicyVersion: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}
		f.must(f.r.Sessions.CreateSite(t.Context(), sess))

		wantErr(t, f.r.Sites.Delete(t.Context(), f.owner().ID, s.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.Delete(t.Context(), o.ID, s.ID), nil)
		wantErr(t, f.r.Sites.Delete(t.Context(), o.ID, s.ID), store.ErrNotFound)

		// Every unique value the site held is free again, which shows its
		// child rows were deleted with it.
		s2 := s
		s2.ID = store.NewID()
		f.must(f.r.Sites.Create(t.Context(), s2))
		dom.ID, dom.SiteID = store.NewID(), s2.ID
		f.must(f.r.Domains.Create(t.Context(), o.ID, dom))
		l.ID, l.SiteID = store.NewID(), s2.ID
		f.must(f.r.Links.Create(t.Context(), o.ID, l))
		p.ID, p.SiteID = store.NewID(), s2.ID
		f.must(f.r.Policies.Create(t.Context(), o.ID, p))
		sess.ID, sess.SiteID, sess.ShareLinkID, sess.PolicyID = store.NewID(), s2.ID, l.ID, p.ID
		f.must(f.r.Sessions.CreateSite(t.Context(), sess))
		d.SiteID = s2.ID
		f.must(f.r.Deploys.Create(t.Context(), o.ID, d, []store.DeployFile{{Path: "index.html", SHA256: sha('a'), Size: 5}}))
	})
}
