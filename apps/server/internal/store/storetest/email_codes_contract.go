// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// emailPolicy stores a protected policy on siteID with the email method and
// an allowlist.
func (f *fixture) emailPolicy(ownerID, siteID, prefix string) store.Policy {
	p := f.validPolicy(siteID)
	p.PathPrefix, p.EmailEnabled = prefix, true
	p.Allowlist = store.Allowlist{Emails: []string{"ana@example.test"}, Domains: []string{"*.example.test", "example.test"}}
	f.must(f.r.Policies.Create(f.t.Context(), store.ActorFor(ownerID), p))
	return p
}

// policiesSetAllowlist covers Policies' allowlist handling (A2.4).
func policiesSetAllowlist(t *testing.T, open Opener) {
	t.Run("Allowlist", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		a, b := f.owner(), f.owner()
		s := f.site(a.ID)
		p := f.emailPolicy(a.ID, s.ID, "/")
		docs := f.validPolicy(s.ID)
		docs.PathPrefix = "/docs/"
		docs.Allowlist = store.Allowlist{GitHubOrgs: []string{"example-org"}}
		f.must(f.r.Policies.Create(ctx, store.ActorFor(a.ID), docs))
		other := f.site(a.ID)
		f.emailPolicy(a.ID, other.ID, "/")

		// ForSite and List carry each policy's own allowlist.
		got, err := f.r.Policies.ForSite(ctx, s.ID)
		f.must(err)
		if len(got) != 2 || !reflect.DeepEqual(got[0], p) || !reflect.DeepEqual(got[1], docs) {
			t.Fatalf("ForSite = %+v, want %+v and %+v", got, p, docs)
		}
		listed, err := f.r.Policies.List(ctx, store.ActorFor(a.ID), s.ID, "", 10)
		f.must(err)
		if !reflect.DeepEqual(listed, got) {
			t.Fatalf("List = %+v, want %+v", listed, got)
		}
		if page, err := f.r.Policies.List(ctx, store.ActorFor(b.ID), s.ID, "", 10); err != nil || len(page) != 0 {
			t.Fatalf("owner B lists %+v, %v", page, err)
		}

		session := func(m store.SessionMethod, linkID string) store.SiteSession {
			ss := store.SiteSession{
				ID: store.NewID(), SiteID: s.ID, TokenHash: f.hash(), Method: m, ShareLinkID: linkID, Subject: "ana@example.test",
				PolicyID: p.ID, PolicyVersion: 1, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
			}
			if m != store.SessionEmail && m != store.SessionOAuth {
				ss.Subject = ""
			}
			f.must(f.r.Sessions.CreateSite(ctx, ss))
			return ss
		}
		email, oauth := session(store.SessionEmail, ""), session(store.SessionOAuth, "")
		link := session(store.SessionLink, f.link(a.ID, s.ID).ID)
		revoked := func(ss store.SiteSession) bool {
			got, err := f.r.Sessions.GetSiteByTokenHash(ctx, ss.TokenHash)
			f.must(err)
			return !got.RevokedAt.IsZero()
		}
		at := now.Add(time.Hour)
		set := func(mutate func(*store.Policy)) (store.Policy, error) {
			q := p
			q.ID, q.UpdatedAt, q.Allowlist = store.NewID(), at, store.Allowlist{}
			mutate(&q)
			return f.r.Policies.Set(ctx, store.ActorFor(a.ID), q)
		}

		// Keeping the allowlist, or setting the same entries in another
		// order, changes nothing.
		kept, err := set(func(q *store.Policy) { q.KeepAllowlist = true })
		f.must(err)
		if kept.Version != 1 || !kept.Allowlist.Equal(p.Allowlist) || kept.KeepAllowlist {
			t.Fatalf("keep: %+v", kept)
		}
		same, err := set(func(q *store.Policy) {
			q.Allowlist = store.Allowlist{Emails: []string{"ana@example.test"}, Domains: []string{"example.test", "*.example.test"}}
		})
		f.must(err)
		if same.Version != 1 || revoked(email) {
			t.Fatalf("same entries: %+v", same)
		}

		// A changed allowlist bumps the version and revokes the email and
		// OAuth sessions, not the others.
		changed, err := set(func(q *store.Policy) { q.Allowlist = store.Allowlist{Emails: []string{"bo@example.test"}} })
		f.must(err)
		want := store.Allowlist{Emails: []string{"bo@example.test"}}
		if changed.Version != 2 || !changed.Allowlist.Equal(want) {
			t.Fatalf("change: %+v", changed)
		}
		if !revoked(email) || !revoked(oauth) || revoked(link) {
			t.Fatalf("after change: email %v, oauth %v, link %v revoked", revoked(email), revoked(oauth), revoked(link))
		}
		stored, err := f.r.Policies.ForSite(ctx, s.ID)
		f.must(err)
		if !reflect.DeepEqual(stored[0].Allowlist, want) || !reflect.DeepEqual(stored[1].Allowlist, docs.Allowlist) {
			t.Fatalf("stored %+v", stored)
		}

		// Turning the email method off keeps the allowlist when asked to,
		// and is still a change.
		off, err := set(func(q *store.Policy) { q.EmailEnabled, q.KeepAllowlist = false, true })
		f.must(err)
		if off.Version != 3 || off.EmailEnabled || !off.Allowlist.Equal(want) {
			t.Fatalf("email off: %+v", off)
		}

		// The email method needs allowed addresses or domains: GitHub
		// organisations alone are not enough, and nothing is stored.
		for name, mutate := range map[string]func(*store.Policy){
			"empty":       func(q *store.Policy) {},
			"orgs only":   func(q *store.Policy) { q.Allowlist = store.Allowlist{GitHubOrgs: []string{"example-org"}} },
			"keep none":   func(q *store.Policy) { q.PathPrefix, q.KeepAllowlist = "/docs/", true },
			"new, keep":   func(q *store.Policy) { q.PathPrefix, q.KeepAllowlist = "/new/", true },
			"new, empty":  func(q *store.Policy) { q.PathPrefix = "/new/" },
			"empty again": func(q *store.Policy) { q.Allowlist = store.Allowlist{Domains: nil} },
		} {
			if _, err := set(mutate); !errors.Is(err, store.ErrEmailNeedsAllowlist) {
				t.Errorf("%s: Set = %v, want ErrEmailNeedsAllowlist", name, err)
			}
		}
		after, err := f.r.Policies.ForSite(ctx, s.ID)
		f.must(err)
		if len(after) != 2 || after[0].Version != 3 || !after[0].Allowlist.Equal(want) {
			t.Fatalf("after refused sets: %+v", after)
		}

		// A new policy stores its allowlist; owner B cannot set one.
		created, err := set(func(q *store.Policy) {
			q.PathPrefix, q.Allowlist = "/new/", store.Allowlist{Domains: []string{"example.test"}}
		})
		f.must(err)
		if created.Version != 1 || !created.Allowlist.Equal(store.Allowlist{Domains: []string{"example.test"}}) {
			t.Fatalf("created %+v", created)
		}
		_, err = f.r.Policies.Set(ctx, store.ActorFor(b.ID), store.Policy{
			ID: store.NewID(), SiteID: s.ID, PathPrefix: "/", Visibility: store.Protected, EmailEnabled: true,
			Allowlist: store.Allowlist{Emails: []string{"eve@example.test"}}, UpdatedAt: at,
		})
		wantErr(t, err, store.ErrNotFound)

		// Deleting a policy deletes its allowlist.
		f.must(f.r.Policies.Delete(ctx, store.ActorFor(a.ID), s.ID, created.ID))
		after, err = f.r.Policies.ForSite(ctx, s.ID)
		f.must(err)
		if len(after) != 2 {
			t.Fatalf("after delete: %+v", after)
		}
	})
}

func emailCodesContract(t *testing.T, open Opener) {
	setup := func(t *testing.T) (*fixture, store.Owner, store.Policy, func(func(*store.VisitorEmailCode)) store.VisitorEmailCode) {
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		p := f.emailPolicy(o.ID, s.ID, "/")
		code := func(mutate func(*store.VisitorEmailCode)) store.VisitorEmailCode {
			c := store.VisitorEmailCode{
				ID: store.NewID(), SiteID: s.ID, PolicyID: p.ID, PolicyVersion: p.Version,
				AddressHash: "address-1", CodeHash: f.hash(), CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
			}
			if mutate != nil {
				mutate(&c)
			}
			return c
		}
		return f, o, p, code
	}
	t.Run("Put", func(t *testing.T) {
		t.Parallel()
		f, o, p, code := setup(t)
		ctx := t.Context()
		links := f.validPolicy(p.SiteID)
		links.PathPrefix = "/links/"
		f.must(f.r.Policies.Create(ctx, store.ActorFor(o.ID), links))
		other := f.site(o.ID)
		for name, c := range map[string]store.VisitorEmailCode{
			"another version":           code(func(c *store.VisitorEmailCode) { c.PolicyVersion = p.Version + 1 }),
			"policy without the method": code(func(c *store.VisitorEmailCode) { c.PolicyID = links.ID }),
			"another site":              code(func(c *store.VisitorEmailCode) { c.SiteID = other.ID }),
			"unknown policy":            code(func(c *store.VisitorEmailCode) { c.PolicyID = store.NewID() }),
		} {
			if err := f.r.EmailCodes.Put(ctx, c); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: Put = %v, want ErrNotFound", name, err)
			}
		}
		if _, err := f.r.EmailCodes.Get(ctx, p.SiteID, p.ID, "address-1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a refused code was stored: %v", err)
		}

		first := code(nil)
		f.must(f.r.EmailCodes.Put(ctx, first))
		got, err := f.r.EmailCodes.Get(ctx, p.SiteID, p.ID, "address-1")
		f.must(err)
		if got != first {
			t.Fatalf("Get = %+v, want %+v", got, first)
		}
		// A second code replaces the first, attempts and all.
		_, err = f.r.EmailCodes.AddAttempt(ctx, first.ID, 5, now)
		f.must(err)
		second := code(func(c *store.VisitorEmailCode) {
			c.CreatedAt, c.ExpiresAt = now.Add(time.Minute), now.Add(11*time.Minute)
		})
		f.must(f.r.EmailCodes.Put(ctx, second))
		got, err = f.r.EmailCodes.Get(ctx, p.SiteID, p.ID, "address-1")
		f.must(err)
		if got != second {
			t.Fatalf("after replace: %+v, want %+v", got, second)
		}
		if _, err := f.r.EmailCodes.AddAttempt(ctx, first.ID, 5, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("the replaced code still counts attempts: %v", err)
		}
		// Another address is its own code; a later Put sweeps expired ones.
		f.must(f.r.EmailCodes.Put(ctx, code(func(c *store.VisitorEmailCode) { c.AddressHash = "address-2" })))
		f.must(f.r.EmailCodes.Put(ctx, code(func(c *store.VisitorEmailCode) {
			c.AddressHash, c.CreatedAt, c.ExpiresAt = "address-3", now.Add(11*time.Minute), now.Add(21*time.Minute)
		})))
		for addr, live := range map[string]bool{"address-1": false, "address-2": false, "address-3": true} {
			_, err := f.r.EmailCodes.Get(ctx, p.SiteID, p.ID, addr)
			if live != (err == nil) {
				t.Errorf("%s: Get = %v, want live %v", addr, err, live)
			}
		}
		// Deleting the policy deletes its codes.
		docs := f.emailPolicy(o.ID, p.SiteID, "/docs/")
		f.must(f.r.EmailCodes.Put(ctx, code(func(c *store.VisitorEmailCode) {
			c.PolicyID, c.CreatedAt, c.ExpiresAt = docs.ID, now.Add(11*time.Minute), now.Add(21*time.Minute)
		})))
		f.must(f.r.Policies.Delete(ctx, store.ActorFor(o.ID), p.SiteID, docs.ID))
		if _, err := f.r.EmailCodes.Get(ctx, p.SiteID, docs.ID, "address-1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a deleted policy's code remains: %v", err)
		}
	})
	t.Run("AddAttempt", func(t *testing.T) {
		t.Parallel()
		f, _, _, code := setup(t)
		ctx := t.Context()
		c := code(nil)
		f.must(f.r.EmailCodes.Put(ctx, c))
		var wg sync.WaitGroup
		var mu sync.Mutex
		counted := 0
		for range 12 {
			wg.Go(func() {
				if _, err := f.r.EmailCodes.AddAttempt(ctx, c.ID, 5, now); err == nil {
					mu.Lock()
					counted++
					mu.Unlock()
				} else if !errors.Is(err, store.ErrNotFound) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if counted != 5 {
			t.Fatalf("%d concurrent attempts counted, want 5", counted)
		}
		got, err := f.r.EmailCodes.Get(ctx, c.SiteID, c.PolicyID, c.AddressHash)
		f.must(err)
		if got.Attempts != 5 {
			t.Fatalf("attempts = %d", got.Attempts)
		}
		fresh := code(func(c *store.VisitorEmailCode) { c.AddressHash = "address-2" })
		f.must(f.r.EmailCodes.Put(ctx, fresh))
		if n, err := f.r.EmailCodes.AddAttempt(ctx, fresh.ID, 5, now); err != nil || n != 1 {
			t.Fatalf("AddAttempt = %d, %v", n, err)
		}
		if _, err := f.r.EmailCodes.AddAttempt(ctx, fresh.ID, 5, fresh.ExpiresAt); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("an expired code counts attempts: %v", err)
		}
		if _, err := f.r.EmailCodes.AddAttempt(ctx, store.NewID(), 5, now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("an unknown code counts attempts: %v", err)
		}
	})
	t.Run("Use", func(t *testing.T) {
		t.Parallel()
		f, _, _, code := setup(t)
		ctx := t.Context()
		c := code(nil)
		f.must(f.r.EmailCodes.Put(ctx, c))
		wantErr(t, f.r.EmailCodes.Use(ctx, c.ID, c.ExpiresAt), store.ErrNotFound)
		f.must(f.r.EmailCodes.Use(ctx, c.ID, now))
		wantErr(t, f.r.EmailCodes.Use(ctx, c.ID, now), store.ErrNotFound)
		if _, err := f.r.EmailCodes.Get(ctx, c.SiteID, c.PolicyID, c.AddressHash); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a used code remains: %v", err)
		}
	})
	t.Run("CreateSession", func(t *testing.T) {
		t.Parallel()
		f, o, p, _ := setup(t)
		ctx := t.Context()
		links := f.validPolicy(p.SiteID)
		links.PathPrefix = "/links/"
		f.must(f.r.Policies.Create(ctx, store.ActorFor(o.ID), links))
		other := f.site(o.ID)
		session := func(mutate func(*store.SiteSession)) store.SiteSession {
			ss := store.SiteSession{
				ID: store.NewID(), SiteID: p.SiteID, TokenHash: f.hash(), Method: store.SessionEmail, Subject: "ana@example.test",
				PolicyID: p.ID, PolicyVersion: p.Version, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
			}
			if mutate != nil {
				mutate(&ss)
			}
			return ss
		}
		for name, ss := range map[string]store.SiteSession{
			"another version":           session(func(ss *store.SiteSession) { ss.PolicyVersion = p.Version + 1 }),
			"policy without the method": session(func(ss *store.SiteSession) { ss.PolicyID = links.ID }),
			"another site":              session(func(ss *store.SiteSession) { ss.SiteID = other.ID }),
			"unknown policy":            session(func(ss *store.SiteSession) { ss.PolicyID = store.NewID() }),
		} {
			wantErr(t, f.r.EmailCodes.CreateSession(ctx, ss), store.ErrNotFound)
			if _, err := f.r.Sessions.GetSiteByTokenHash(ctx, ss.TokenHash); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: a session was stored: %v", name, err)
			}
		}
		ok := session(nil)
		f.must(f.r.EmailCodes.CreateSession(ctx, ok))
		got, err := f.r.Sessions.GetSiteByTokenHash(ctx, ok.TokenHash)
		f.must(err)
		if got != ok {
			t.Fatalf("stored %+v, want %+v", got, ok)
		}
		wantErr(t, f.r.EmailCodes.CreateSession(ctx, session(func(ss *store.SiteSession) { ss.TokenHash = ok.TokenHash })), store.ErrConflict)
	})
}
