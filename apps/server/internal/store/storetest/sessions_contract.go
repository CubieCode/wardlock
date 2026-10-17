// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func sessionsContract(t *testing.T, open Opener) {
	t.Run("CreateOwner", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.OwnerSession {
				return store.OwnerSession{
					ID: store.NewID(), OwnerID: f.owner().ID, TokenHash: f.hash(),
					CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(90 * 24 * time.Hour),
				}
			},
			func(f *fixture, s store.OwnerSession) error { return f.r.Sessions.CreateOwner(f.t.Context(), s) },
			[]insertCase[store.OwnerSession]{
				{name: "valid"},
				{name: "duplicate token hash", mutate: func(f *fixture, s *store.OwnerSession) {
					other := *s
					other.ID = store.NewID()
					f.must(f.r.Sessions.CreateOwner(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "unknown owner", mutate: func(_ *fixture, s *store.OwnerSession) { s.OwnerID = store.NewID() }, want: store.ErrConstraint},
				{name: "expires at creation", mutate: func(_ *fixture, s *store.OwnerSession) { s.ExpiresAt = s.CreatedAt }, want: store.ErrConstraint},
				{name: "seen before creation", mutate: func(_ *fixture, s *store.OwnerSession) { s.LastSeenAt = s.CreatedAt.Add(-time.Second) }, want: store.ErrConstraint},
			})
	})
	t.Run("CreateSite", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.SiteSession {
				o := f.owner()
				s := f.site(o.ID)
				p := f.policy(o.ID, s.ID)
				l := f.link(o.ID, s.ID)
				return store.SiteSession{
					ID: store.NewID(), SiteID: s.ID, TokenHash: f.hash(), Method: store.SessionLink, ShareLinkID: l.ID,
					PolicyID: p.ID, PolicyVersion: p.Version, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
				}
			},
			func(f *fixture, s store.SiteSession) error { return f.r.Sessions.CreateSite(f.t.Context(), s) },
			[]insertCase[store.SiteSession]{
				{name: "link"},
				{name: "email", mutate: func(_ *fixture, s *store.SiteSession) {
					s.Method, s.ShareLinkID, s.Subject = store.SessionEmail, "", "visitor@example.test"
				}},
				{name: "link method without link", mutate: func(_ *fixture, s *store.SiteSession) { s.ShareLinkID = "" }, want: store.ErrConstraint},
				{name: "password method with link", mutate: func(_ *fixture, s *store.SiteSession) { s.Method = store.SessionPassword }, want: store.ErrConstraint},
				{name: "unknown method", mutate: func(_ *fixture, s *store.SiteSession) { s.Method = "owner" }, want: store.ErrConstraint},
				{name: "unknown policy", mutate: func(_ *fixture, s *store.SiteSession) { s.PolicyID = store.NewID() }, want: store.ErrConstraint},
				{name: "another site's link", mutate: func(f *fixture, s *store.SiteSession) {
					o := f.owner().ID
					s.ShareLinkID = f.link(o, f.site(o).ID).ID
				}, want: store.ErrConstraint},
				{name: "another site's policy", mutate: func(f *fixture, s *store.SiteSession) {
					o := f.owner().ID
					s.PolicyID = f.policy(o, f.site(o).ID).ID
				}, want: store.ErrConstraint},
				{name: "unknown link", mutate: func(_ *fixture, s *store.SiteSession) { s.ShareLinkID = store.NewID() }, want: store.ErrConstraint},
				{name: "policy version zero", mutate: func(_ *fixture, s *store.SiteSession) { s.PolicyVersion = 0 }, want: store.ErrConstraint},
				{name: "expires at creation", mutate: func(_ *fixture, s *store.SiteSession) { s.ExpiresAt = s.CreatedAt }, want: store.ErrConstraint},
			})
	})
	t.Run("GetSiteByTokenHash", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner().ID
		s := f.site(o).ID
		want := f.validSiteSession(s, f.policy(o, s))
		want.Method, want.Subject = store.SessionEmail, "visitor@example.test"
		f.must(f.r.Sessions.CreateSite(t.Context(), want))
		got, err := f.r.Sessions.GetSiteByTokenHash(t.Context(), want.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("GetSiteByTokenHash() = %+v, want %+v", got, want)
		}
		_, err = f.r.Sessions.GetSiteByTokenHash(t.Context(), f.hash())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("RevokeSite", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner().ID
		s := f.site(o).ID
		sess := f.validSiteSession(s, f.policy(o, s))
		f.must(f.r.Sessions.CreateSite(t.Context(), sess))
		wantErr(t, f.r.Sessions.RevokeSite(t.Context(), f.site(o).ID, sess.ID, now), store.ErrNotFound)
		wantErr(t, f.r.Sessions.RevokeSite(t.Context(), s, store.NewID(), now), store.ErrNotFound)
		first := now.Add(time.Minute)
		f.must(f.r.Sessions.RevokeSite(t.Context(), s, sess.ID, first))
		f.must(f.r.Sessions.RevokeSite(t.Context(), s, sess.ID, first.Add(time.Minute)))
		got, err := f.r.Sessions.GetSiteByTokenHash(t.Context(), sess.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if !got.RevokedAt.Equal(first) {
			t.Fatalf("RevokedAt = %v, want %v", got.RevokedAt, first)
		}
	})
	t.Run("GetOwnerByTokenHash", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		want := f.validOwnerSession(f.owner().ID, now)
		want.UserAgent = "Mozilla/5.0 (X11; Linux x86_64)"
		f.must(f.r.Sessions.CreateOwner(t.Context(), want))
		got, err := f.r.Sessions.GetOwnerByTokenHash(t.Context(), want.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("GetOwnerByTokenHash() = %+v, want %+v", got, want)
		}
		_, err = f.r.Sessions.GetOwnerByTokenHash(t.Context(), f.hash())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("TouchOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		s := f.ownerSession(f.owner().ID, now)
		later := now.Add(time.Hour)
		f.must(f.r.Sessions.TouchOwner(t.Context(), s.ID, later))
		f.must(f.r.Sessions.TouchOwner(t.Context(), s.ID, now.Add(time.Minute)))
		got, err := f.r.Sessions.GetOwnerByTokenHash(t.Context(), s.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if !got.LastSeenAt.Equal(later) {
			t.Fatalf("LastSeenAt = %v, want %v", got.LastSeenAt, later)
		}
	})
	t.Run("RevokeOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		s := f.ownerSession(a.ID, now)
		wantErr(t, f.r.Sessions.RevokeOwner(t.Context(), b.ID, s.ID, now), store.ErrNotFound)
		wantErr(t, f.r.Sessions.RevokeOwner(t.Context(), a.ID, store.NewID(), now), store.ErrNotFound)
		first := now.Add(time.Minute)
		f.must(f.r.Sessions.RevokeOwner(t.Context(), a.ID, s.ID, first))
		f.must(f.r.Sessions.RevokeOwner(t.Context(), a.ID, s.ID, first.Add(time.Minute)))
		got, err := f.r.Sessions.GetOwnerByTokenHash(t.Context(), s.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if !got.RevokedAt.Equal(first) {
			t.Fatalf("RevokedAt = %v, want %v", got.RevokedAt, first)
		}
	})
	t.Run("ListOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		// Five active sessions seen a minute apart, newest last; two share
		// a last-seen time so the ID breaks the tie.
		var active []store.OwnerSession
		for i := range 5 {
			seen := now.Add(time.Duration(i) * time.Minute)
			if i == 4 {
				seen = active[3].LastSeenAt
			}
			active = append(active, f.ownerSession(a.ID, seen))
		}
		revoked := f.ownerSession(a.ID, now)
		f.must(f.r.Sessions.RevokeOwner(t.Context(), a.ID, revoked.ID, now))
		f.ownerSession(a.ID, now.Add(-15*24*time.Hour)) // idle
		expired := f.validOwnerSession(a.ID, now.Add(-time.Hour))
		expired.ExpiresAt = now.Add(time.Minute)
		f.must(f.r.Sessions.CreateOwner(t.Context(), expired))
		f.ownerSession(b.ID, now)

		q := store.OwnerSessionQuery{
			OwnerID: a.ID, Now: now.Add(10 * time.Minute), IdleSince: now.Add(-14 * 24 * time.Hour), Limit: 2,
		}
		want := []string{active[4].ID, active[3].ID, active[2].ID, active[1].ID, active[0].ID}
		if active[3].ID > active[4].ID {
			want[0], want[1] = active[3].ID, active[4].ID
		}
		var got []string
		for range 4 {
			page, err := f.r.Sessions.ListOwner(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range page {
				got = append(got, s.ID)
			}
			if len(page) < q.Limit {
				break
			}
			last := page[len(page)-1]
			q.AfterSeen, q.AfterID = last.LastSeenAt, last.ID
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("ListOwner pages = %v, want %v", got, want)
		}

		page, err := f.r.Sessions.ListOwner(t.Context(), store.OwnerSessionQuery{
			OwnerID: b.ID, Now: now, IdleSince: now.Add(-time.Hour), Limit: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 1 || page[0].OwnerID != b.ID {
			t.Fatalf("owner B sees %+v", page)
		}
	})
}

// validOwnerSession returns a session for ownerID created and last seen at
// seen, with a 90-day absolute expiry.
func (f *fixture) validOwnerSession(ownerID string, seen time.Time) store.OwnerSession {
	return store.OwnerSession{
		ID: store.NewID(), OwnerID: ownerID, TokenHash: f.hash(),
		CreatedAt: seen, LastSeenAt: seen, ExpiresAt: seen.Add(90 * 24 * time.Hour),
	}
}

func (f *fixture) ownerSession(ownerID string, seen time.Time) store.OwnerSession {
	f.t.Helper()
	s := f.validOwnerSession(ownerID, seen)
	f.must(f.r.Sessions.CreateOwner(f.t.Context(), s))
	return s
}

// validSiteSession returns a password session on siteID bound to p's
// current version.
func (f *fixture) validSiteSession(siteID string, p store.Policy) store.SiteSession {
	return store.SiteSession{
		ID: store.NewID(), SiteID: siteID, TokenHash: f.hash(), Method: store.SessionPassword,
		PolicyID: p.ID, PolicyVersion: p.Version, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
	}
}
