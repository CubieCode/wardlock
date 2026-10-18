// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func passwordSessionsContract(t *testing.T, open Opener) {
	t.Run("RevokeForPolicy", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		s := f.site(o.ID)
		p := f.validPolicy(s.ID)
		p.PasswordEnabled, p.PasswordHash = true, "$argon2id$stand-in"
		f.must(f.r.Policies.Create(ctx, o.ID, p))
		docs := f.validPolicy(s.ID)
		docs.PathPrefix, docs.PasswordEnabled, docs.PasswordHash = "/docs/", true, "$argon2id$stand-in"
		f.must(f.r.Policies.Create(ctx, o.ID, docs))
		l := f.link(o.ID, s.ID)
		other := f.site(o.ID)
		otherPolicy := f.validPolicy(other.ID)
		otherPolicy.PasswordEnabled, otherPolicy.PasswordHash = true, "$argon2id$stand-in"
		f.must(f.r.Policies.Create(ctx, o.ID, otherPolicy))

		session := func(siteID, policyID string, m store.SessionMethod, linkID string, version int, revoked time.Time) store.SiteSession {
			ss := store.SiteSession{
				ID: store.NewID(), SiteID: siteID, TokenHash: f.hash(), Method: m, ShareLinkID: linkID,
				PolicyID: policyID, PolicyVersion: version, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour), RevokedAt: revoked,
			}
			f.must(f.r.Sessions.CreateSite(ctx, ss))
			return ss
		}
		earlier := now.Add(-time.Minute)
		live := session(s.ID, p.ID, store.SessionPassword, "", 1, time.Time{})
		oldVersion := session(s.ID, p.ID, store.SessionPassword, "", 2, time.Time{})
		already := session(s.ID, p.ID, store.SessionPassword, "", 1, earlier)
		link := session(s.ID, p.ID, store.SessionLink, l.ID, 1, time.Time{})
		otherPrefix := session(s.ID, docs.ID, store.SessionPassword, "", 1, time.Time{})
		otherSite := session(other.ID, otherPolicy.ID, store.SessionPassword, "", 1, time.Time{})

		at := now.Add(time.Hour)
		// Another site's ID never reaches this site's sessions.
		if n, err := f.r.PasswordSessions.RevokeForPolicy(ctx, other.ID, p.ID, at); err != nil || n != 0 {
			t.Fatalf("wrong site: %d, %v; want 0", n, err)
		}
		n, err := f.r.PasswordSessions.RevokeForPolicy(ctx, s.ID, p.ID, at)
		if err != nil || n != 2 {
			t.Fatalf("RevokeForPolicy = %d, %v; want 2", n, err)
		}
		for _, c := range []struct {
			name string
			s    store.SiteSession
			want time.Time
		}{
			{"live", live, at}, {"older version", oldVersion, at}, {"already revoked", already, earlier},
			{"link", link, time.Time{}}, {"other prefix", otherPrefix, time.Time{}}, {"other site", otherSite, time.Time{}},
		} {
			got, err := f.r.Sessions.GetSiteByTokenHash(ctx, c.s.TokenHash)
			f.must(err)
			if !got.RevokedAt.Equal(c.want) {
				t.Errorf("%s: revoked at %v, want %v", c.name, got.RevokedAt, c.want)
			}
		}
		if n, err := f.r.PasswordSessions.RevokeForPolicy(ctx, s.ID, p.ID, at.Add(time.Hour)); err != nil || n != 0 {
			t.Fatalf("second revoke: %d, %v; want 0", n, err)
		}
	})
}
