// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
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
}
