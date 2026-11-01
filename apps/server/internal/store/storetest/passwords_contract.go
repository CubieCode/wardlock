// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func passwordSessionsContract(t *testing.T, open Opener) {
	t.Run("CreateSession", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		s := f.site(o.ID)
		p := f.validPolicy(s.ID)
		p.PasswordEnabled, p.PasswordHash = true, "$argon2id$stand-in"
		f.must(f.r.Policies.Create(ctx, store.ActorFor(o.ID), p))
		links := f.validPolicy(s.ID)
		links.PathPrefix = "/links/"
		f.must(f.r.Policies.Create(ctx, store.ActorFor(o.ID), links))
		other := f.site(o.ID)

		session := func(mutate func(*store.SiteSession)) store.SiteSession {
			ss := store.SiteSession{
				ID: store.NewID(), SiteID: s.ID, TokenHash: f.hash(), Method: store.SessionPassword,
				PolicyID: p.ID, PolicyVersion: p.Version, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
			}
			if mutate != nil {
				mutate(&ss)
			}
			return ss
		}
		for _, c := range []struct {
			name string
			s    store.SiteSession
			hash string
		}{
			{"another password", session(nil), "$argon2id$other"},
			{"no password", session(nil), ""},
			{"another version", session(func(ss *store.SiteSession) { ss.PolicyVersion = p.Version + 1 }), p.PasswordHash},
			{"policy without the method", session(func(ss *store.SiteSession) { ss.PolicyID = links.ID }), ""},
			{"another site", session(func(ss *store.SiteSession) { ss.SiteID = other.ID }), p.PasswordHash},
			{"unknown policy", session(func(ss *store.SiteSession) { ss.PolicyID = store.NewID() }), p.PasswordHash},
		} {
			wantErr(t, f.r.PasswordSessions.CreateSession(ctx, c.s, c.hash), store.ErrNotFound)
			if _, err := f.r.Sessions.GetSiteByTokenHash(ctx, c.s.TokenHash); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: a session was stored: %v", c.name, err)
			}
		}
		ok := session(nil)
		f.must(f.r.PasswordSessions.CreateSession(ctx, ok, p.PasswordHash))
		got, err := f.r.Sessions.GetSiteByTokenHash(ctx, ok.TokenHash)
		f.must(err)
		if got.ID != ok.ID || got.Method != store.SessionPassword || got.PolicyVersion != p.Version || got.ShareLinkID != "" || !got.ExpiresAt.Equal(ok.ExpiresAt) || !got.RevokedAt.IsZero() {
			t.Fatalf("stored %+v, want %+v", got, ok)
		}
		wantErr(t, f.r.PasswordSessions.CreateSession(ctx, ok, p.PasswordHash), store.ErrConflict)
	})
}

// policiesSetPassword covers Policies.Set's password handling (A2.3).
func policiesSetPassword(t *testing.T, open Opener) {
	t.Run("SetPassword", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		s := f.site(o.ID)
		p := f.validPolicy(s.ID)
		p.PasswordEnabled, p.PasswordHash = true, "$argon2id$stand-in"
		f.must(f.r.Policies.Create(ctx, store.ActorFor(o.ID), p))
		docs := f.validPolicy(s.ID)
		docs.PathPrefix, docs.PasswordEnabled, docs.PasswordHash = "/docs/", true, "$argon2id$stand-in"
		f.must(f.r.Policies.Create(ctx, store.ActorFor(o.ID), docs))
		l := f.link(o.ID, s.ID)
		other := f.site(o.ID)
		otherPolicy := f.validPolicy(other.ID)
		otherPolicy.PasswordEnabled, otherPolicy.PasswordHash = true, "$argon2id$stand-in"
		f.must(f.r.Policies.Create(ctx, store.ActorFor(o.ID), otherPolicy))

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
		set := func(hash string) (store.Policy, error) {
			q := p
			q.ID, q.PasswordEnabled, q.PasswordHash, q.UpdatedAt = store.NewID(), hash != "", hash, at
			return f.r.Policies.Set(ctx, store.ActorFor(o.ID), q)
		}
		revokedAt := func(ss store.SiteSession) time.Time {
			got, err := f.r.Sessions.GetSiteByTokenHash(ctx, ss.TokenHash)
			f.must(err)
			return got.RevokedAt
		}

		// Keeping the password changes nothing and revokes nothing.
		kept, err := set(store.KeepPasswordHash)
		f.must(err)
		if kept.PasswordHash != p.PasswordHash || kept.Version != p.Version || !revokedAt(live).IsZero() {
			t.Fatalf("keep: %+v, live revoked %v", kept, revokedAt(live))
		}
		// A new hash bumps the version and revokes the policy's password
		// sessions, at every version, and nothing else.
		changed, err := set("$argon2id$changed")
		f.must(err)
		if changed.PasswordHash != "$argon2id$changed" || changed.Version != p.Version+1 {
			t.Fatalf("change: %+v", changed)
		}
		for _, c := range []struct {
			name string
			s    store.SiteSession
			want time.Time
		}{
			{"live", live, at}, {"older version", oldVersion, at}, {"already revoked", already, earlier},
			{"link", link, time.Time{}}, {"other prefix", otherPrefix, time.Time{}}, {"other site", otherSite, time.Time{}},
		} {
			if got := revokedAt(c.s); !got.Equal(c.want) {
				t.Errorf("%s: revoked at %v, want %v", c.name, got, c.want)
			}
		}
		// A keep that was decided before that change keeps the new hash:
		// it can never revive the old password.
		kept, err = set(store.KeepPasswordHash)
		f.must(err)
		if kept.PasswordHash != "$argon2id$changed" || kept.Version != changed.Version {
			t.Fatalf("stale keep: %+v", kept)
		}
		// Removing the password revokes too, and then there is nothing to keep.
		fresh := session(s.ID, p.ID, store.SessionPassword, "", changed.Version, time.Time{})
		q := p
		q.ID, q.PasswordEnabled, q.PasswordHash, q.UpdatedAt = store.NewID(), false, "", at.Add(time.Minute)
		if _, err := f.r.Policies.Set(ctx, store.ActorFor(o.ID), q); err != nil {
			t.Fatal(err)
		}
		if !revokedAt(fresh).Equal(at.Add(time.Minute)) {
			t.Fatalf("removal left the session: %v", revokedAt(fresh))
		}
		if _, err := set(store.KeepPasswordHash); !errors.Is(err, store.ErrNoPasswordToKeep) {
			t.Fatalf("keep with no password: %v", err)
		}
		missing := p
		missing.ID, missing.PathPrefix, missing.PasswordHash = store.NewID(), "/new/", store.KeepPasswordHash
		if _, err := f.r.Policies.Set(ctx, store.ActorFor(o.ID), missing); !errors.Is(err, store.ErrNoPasswordToKeep) {
			t.Fatalf("keep on a new policy: %v", err)
		}
	})
}
