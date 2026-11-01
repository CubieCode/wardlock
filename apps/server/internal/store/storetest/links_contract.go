// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func linksContract(t *testing.T, open Opener) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		linksCreate(t, open)
	})
	t.Run("GetByPublicID and Get", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		l := f.validLink(s.ID)
		l.MaxUses, l.ExpiresAt, l.PathPrefix = 3, now.Add(time.Hour), "/docs/"
		f.must(f.r.Links.Create(t.Context(), store.ActorFor(o.ID), l))
		for name, get := range map[string]func() (store.ShareLink, error){
			"by public ID": func() (store.ShareLink, error) { return f.r.Links.GetByPublicID(t.Context(), l.PublicID) },
			"by ID":        func() (store.ShareLink, error) { return f.r.Links.Get(t.Context(), s.ID, l.ID) },
		} {
			got, err := get()
			if err != nil || got != l {
				t.Fatalf("%s = %+v, %v; want %+v", name, got, err, l)
			}
		}
		_, err := f.r.Links.GetByPublicID(t.Context(), f.publicID())
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Links.Get(t.Context(), f.site(o.ID).ID, l.ID)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("List", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		var want []store.ShareLink
		for i := range 5 {
			l := f.validLink(s.ID)
			l.CreatedAt = now.Add(time.Duration(i%3) * time.Minute)
			f.must(f.r.Links.Create(t.Context(), store.ActorFor(o.ID), l))
			want = append(want, l)
		}
		revoked := f.link(o.ID, s.ID)
		f.must(f.r.Links.Revoke(t.Context(), store.ActorFor(o.ID), s.ID, revoked.PublicID, now))
		f.link(o.ID, f.site(o.ID).ID) // another site's
		slices.SortFunc(want, func(a, b store.ShareLink) int {
			if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
				return c
			}
			return strings.Compare(b.ID, a.ID)
		})
		var got []store.ShareLink
		q := store.ShareLinkQuery{Actor: store.ActorFor(o.ID), SiteID: s.ID, Limit: 2}
		for {
			page, err := f.r.Links.List(t.Context(), q)
			f.must(err)
			got = append(got, page...)
			if len(page) < q.Limit {
				break
			}
			q.BeforeCreated, q.BeforeID = page[len(page)-1].CreatedAt, page[len(page)-1].ID
		}
		if !slices.Equal(got, want) {
			t.Fatalf("listed %v, want %v", got, want)
		}
		other, err := f.r.Links.List(t.Context(), store.ShareLinkQuery{Actor: store.ActorFor(f.owner().ID), SiteID: s.ID, Limit: 10})
		if err != nil || len(other) != 0 {
			t.Fatalf("another owner listed %v, %v", other, err)
		}
	})
	t.Run("Revoke", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		p := f.policy(o.ID, s.ID)
		l := f.link(o.ID, s.ID)
		sess := f.linkSession(s.ID, p, l)
		wantErr(t, f.r.Links.Revoke(t.Context(), store.ActorFor(f.owner().ID), s.ID, l.PublicID, now), store.ErrNotFound)
		wantErr(t, f.r.Links.Revoke(t.Context(), store.ActorFor(o.ID), f.site(o.ID).ID, l.PublicID, now), store.ErrNotFound)
		f.must(f.r.Links.Revoke(t.Context(), store.ActorFor(o.ID), s.ID, l.PublicID, now.Add(time.Minute)))
		got, err := f.r.Links.Get(t.Context(), s.ID, l.ID)
		if err != nil || !got.RevokedAt.Equal(now.Add(time.Minute)) {
			t.Fatalf("after revoke: %+v, %v", got, err)
		}
		gotSess, err := f.r.Sessions.GetSiteByTokenHash(t.Context(), sess.TokenHash)
		if err != nil || !gotSess.RevokedAt.Equal(now.Add(time.Minute)) {
			t.Fatalf("session after revoke: %+v, %v", gotSess, err)
		}
		wantErr(t, f.r.Links.Revoke(t.Context(), store.ActorFor(o.ID), s.ID, l.PublicID, now.Add(time.Hour)), store.ErrNotFound)
	})
	t.Run("Rotate", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		p := f.policy(o.ID, s.ID)
		l := f.validLink(s.ID)
		l.MaxUses, l.UseCount = 5, 2
		f.must(f.r.Links.Create(t.Context(), store.ActorFor(o.ID), l))
		sess := f.linkSession(s.ID, p, l)
		hash := f.hash()
		_, err := f.r.Links.Rotate(t.Context(), store.ActorFor(f.owner().ID), s.ID, l.PublicID, hash, now)
		wantErr(t, err, store.ErrNotFound)
		at := now.Add(time.Minute)
		got, err := f.r.Links.Rotate(t.Context(), store.ActorFor(o.ID), s.ID, l.PublicID, hash, at)
		want := l
		want.TokenHash, want.RotatedAt = hash, at
		if err != nil || got != want {
			t.Fatalf("Rotate = %+v, %v; want %+v", got, err, want)
		}
		gotSess, err := f.r.Sessions.GetSiteByTokenHash(t.Context(), sess.TokenHash)
		if err != nil || !gotSess.RevokedAt.Equal(at) {
			t.Fatalf("session after rotate: %+v, %v", gotSess, err)
		}
		f.must(f.r.Links.Revoke(t.Context(), store.ActorFor(o.ID), s.ID, l.PublicID, at))
		_, err = f.r.Links.Rotate(t.Context(), store.ActorFor(o.ID), s.ID, l.PublicID, f.hash(), at)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("Use", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		l := f.validLink(s.ID)
		l.MaxUses, l.ExpiresAt = 2, now.Add(time.Hour)
		f.must(f.r.Links.Create(t.Context(), store.ActorFor(o.ID), l))
		wantErr(t, f.r.Links.Use(t.Context(), l.ID, f.hash(), now), store.ErrNotFound)
		wantErr(t, f.r.Links.Use(t.Context(), l.ID, l.TokenHash, now.Add(time.Hour)), store.ErrNotFound)
		f.must(f.r.Links.Use(t.Context(), l.ID, l.TokenHash, now))
		f.must(f.r.Links.Use(t.Context(), l.ID, l.TokenHash, now.Add(time.Second)))
		wantErr(t, f.r.Links.Use(t.Context(), l.ID, l.TokenHash, now.Add(2*time.Second)), store.ErrNotFound)
		got, err := f.r.Links.Get(t.Context(), s.ID, l.ID)
		if err != nil || got.UseCount != 2 || !got.LastAccessedAt.Equal(now.Add(time.Second)) {
			t.Fatalf("after uses: %+v, %v", got, err)
		}
		unlimited := f.link(o.ID, s.ID)
		for range 3 {
			f.must(f.r.Links.Use(t.Context(), unlimited.ID, unlimited.TokenHash, now))
		}
		f.must(f.r.Links.Revoke(t.Context(), store.ActorFor(o.ID), s.ID, unlimited.PublicID, now))
		wantErr(t, f.r.Links.Use(t.Context(), unlimited.ID, unlimited.TokenHash, now), store.ErrNotFound)
	})
	t.Run("CreateSession", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		p := f.policy(o.ID, s.ID)
		l := f.validLink(s.ID)
		l.ExpiresAt = now.Add(time.Hour)
		f.must(f.r.Links.Create(t.Context(), store.ActorFor(o.ID), l))
		sess := func() store.SiteSession {
			v := f.validSiteSession(s.ID, p)
			v.Method, v.ShareLinkID = store.SessionLink, l.ID
			return v
		}
		ok := sess()
		f.must(f.r.Links.CreateSession(t.Context(), ok, l.TokenHash))
		got, err := f.r.Sessions.GetSiteByTokenHash(t.Context(), ok.TokenHash)
		if err != nil || got.Method != store.SessionLink || got.ShareLinkID != l.ID || got.PolicyID != p.ID || !got.ExpiresAt.Equal(ok.ExpiresAt) || !got.RevokedAt.IsZero() {
			t.Fatalf("stored %+v, %v", got, err)
		}
		wantErr(t, f.r.Links.CreateSession(t.Context(), sess(), f.hash()), store.ErrNotFound)
		late := sess()
		late.CreatedAt, late.ExpiresAt = now.Add(time.Hour), now.Add(2*time.Hour)
		wantErr(t, f.r.Links.CreateSession(t.Context(), late, l.TokenHash), store.ErrNotFound)
		other := f.site(o.ID)
		foreign := f.validSiteSession(other.ID, f.policy(o.ID, other.ID))
		foreign.Method, foreign.ShareLinkID = store.SessionLink, l.ID
		wantErr(t, f.r.Links.CreateSession(t.Context(), foreign, l.TokenHash), store.ErrNotFound)
		// After a rotation the old hash creates nothing; the new one does.
		rotated, err := f.r.Links.Rotate(t.Context(), store.ActorFor(o.ID), s.ID, l.PublicID, f.hash(), now)
		f.must(err)
		raced := sess()
		wantErr(t, f.r.Links.CreateSession(t.Context(), raced, l.TokenHash), store.ErrNotFound)
		if _, err := f.r.Sessions.GetSiteByTokenHash(t.Context(), raced.TokenHash); err == nil {
			t.Fatal("a refused session was stored")
		}
		f.must(f.r.Links.CreateSession(t.Context(), sess(), rotated.TokenHash))
		f.must(f.r.Links.Revoke(t.Context(), store.ActorFor(o.ID), s.ID, l.PublicID, now))
		wantErr(t, f.r.Links.CreateSession(t.Context(), sess(), rotated.TokenHash), store.ErrNotFound)
	})
	t.Run("Touch", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		l := f.link(o.ID, s.ID)
		f.must(f.r.Links.Touch(t.Context(), l.ID, now.Add(time.Hour)))
		f.must(f.r.Links.Touch(t.Context(), l.ID, now.Add(time.Minute)))
		got, err := f.r.Links.Get(t.Context(), s.ID, l.ID)
		if err != nil || !got.LastAccessedAt.Equal(now.Add(time.Hour)) {
			t.Fatalf("after touch: %+v, %v", got, err)
		}
	})
}

// linkSession creates a session on siteID from link l, bound to p.
func (f *fixture) linkSession(siteID string, p store.Policy, l store.ShareLink) store.SiteSession {
	f.t.Helper()
	s := f.validSiteSession(siteID, p)
	s.Method, s.ShareLinkID = store.SessionLink, l.ID
	f.must(f.r.Sessions.CreateSite(f.t.Context(), s))
	return s
}

func linksCreate(t *testing.T, open Opener) {
	type owned struct {
		ownerID string
		l       store.ShareLink
	}
	runInsertCases(t, open,
		func(f *fixture) owned {
			o := f.owner()
			return owned{o.ID, f.validLink(f.site(o.ID).ID)}
		},
		func(f *fixture, v owned) error {
			return f.r.Links.Create(f.t.Context(), store.ActorFor(v.ownerID), v.l)
		},
		[]insertCase[owned]{
			{name: "never expires"},
			{name: "one-time, scoped, expiring", mutate: func(_ *fixture, v *owned) {
				v.l.MaxUses, v.l.PathPrefix, v.l.ExpiresAt = 1, "/docs/", now.Add(7*24*time.Hour)
			}},
			{name: "used up", mutate: func(_ *fixture, v *owned) { v.l.MaxUses, v.l.UseCount = 2, 2 }},
			{name: "200-character label", mutate: func(_ *fixture, v *owned) { v.l.Label = strings.Repeat("l", 200) }},
			{name: "duplicate public ID on another owner's site", mutate: func(f *fixture, v *owned) {
				o := f.owner()
				other := f.validLink(f.site(o.ID).ID)
				other.PublicID = v.l.PublicID
				f.must(f.r.Links.Create(f.t.Context(), store.ActorFor(o.ID), other))
			}, want: store.ErrConflict},
			{name: "another owner's site", mutate: func(f *fixture, v *owned) { v.ownerID = f.owner().ID }, want: store.ErrNotFound},
			{name: "short public ID", mutate: func(_ *fixture, v *owned) { v.l.PublicID = "abc" }, want: store.ErrConstraint},
			{name: "201-character label", mutate: func(_ *fixture, v *owned) { v.l.Label = strings.Repeat("l", 201) }, want: store.ErrConstraint},
			{name: "uses beyond limit", mutate: func(_ *fixture, v *owned) { v.l.MaxUses, v.l.UseCount = 1, 2 }, want: store.ErrConstraint},
			{name: "negative limit", mutate: func(_ *fixture, v *owned) { v.l.MaxUses = -1 }, want: store.ErrConstraint},
			{name: "relative prefix", mutate: func(_ *fixture, v *owned) { v.l.PathPrefix = "docs/" }, want: store.ErrConstraint},
			{name: "expires at creation", mutate: func(_ *fixture, v *owned) { v.l.ExpiresAt = v.l.CreatedAt }, want: store.ErrConstraint},
		})
}
