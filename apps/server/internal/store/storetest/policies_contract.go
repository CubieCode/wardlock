// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func policiesContract(t *testing.T, open Opener) {
	policiesSetPassword(t, open)
	policiesSetAllowlist(t, open)
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		policiesCreate(t, open)
	})
	t.Run("ForSite", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner().ID
		s, other := f.site(o).ID, f.site(o).ID
		docs := f.validPolicy(s)
		docs.PathPrefix = "/docs/"
		f.must(f.r.Policies.Create(t.Context(), o, docs))
		def := f.policy(o, s)
		f.policy(o, other)
		got, err := f.r.Policies.ForSite(t.Context(), s)
		if err != nil {
			t.Fatal(err)
		}
		if want := []store.Policy{def, docs}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ForSite() = %+v, want %+v", got, want)
		}
		got, err = f.r.Policies.ForSite(t.Context(), store.NewID())
		if err != nil || len(got) != 0 {
			t.Fatalf("ForSite(unknown) = %+v, %v", got, err)
		}
	})
	t.Run("EnsureDefault", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner().ID
		s := f.site(o).ID
		p := f.validPolicy(s)
		f.must(f.r.Policies.EnsureDefault(t.Context(), p))
		again := f.validPolicy(s)
		again.Visibility, again.LinksEnabled = store.Public, false
		f.must(f.r.Policies.EnsureDefault(t.Context(), again))
		got, err := f.r.Policies.ForSite(t.Context(), s)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !reflect.DeepEqual(got[0], p) {
			t.Fatalf("after EnsureDefault twice: %+v, want only %+v", got, p)
		}
		wantErr(t, f.r.Policies.EnsureDefault(t.Context(), f.validPolicy(store.NewID())), store.ErrConstraint)
	})
	t.Run("List", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner().ID, f.owner().ID
		s := f.site(a).ID
		var want []string
		for _, prefix := range []string{"/", "/a/", "/b/", "/c/", "/d/"} {
			p := f.validPolicy(s)
			p.PathPrefix = prefix
			f.must(f.r.Policies.Create(t.Context(), a, p))
			want = append(want, p.ID)
		}
		f.policy(a, f.site(a).ID)
		var got []string
		after := ""
		for range 4 {
			page, err := f.r.Policies.List(t.Context(), a, s, after, 2)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range page {
				got = append(got, p.ID)
			}
			if len(page) < 2 {
				break
			}
			after = page[len(page)-1].PathPrefix
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("List pages = %v, want %v", got, want)
		}
		page, err := f.r.Policies.List(t.Context(), b, s, "", 10)
		if err != nil || len(page) != 0 {
			t.Fatalf("owner B lists %+v, %v", page, err)
		}
	})
	t.Run("Set", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner().ID, f.owner().ID
		s := f.site(a).ID
		p := f.validPolicy(s)
		p.PathPrefix, p.Version, p.CreatedAt = "/docs/", 7, time.Time{}
		created, err := f.r.Policies.Set(t.Context(), a, p)
		if err != nil {
			t.Fatal(err)
		}
		if created.ID != p.ID || created.Version != 1 || !created.CreatedAt.Equal(p.UpdatedAt) {
			t.Fatalf("created %+v", created)
		}

		same := p
		same.ID, same.UpdatedAt = store.NewID(), now.Add(time.Hour)
		got, err := f.r.Policies.Set(t.Context(), a, same)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, created) {
			t.Fatalf("Set(same settings) = %+v, want unchanged %+v", got, created)
		}

		changed := same
		changed.Visibility, changed.LinksEnabled = store.Public, false
		got, err = f.r.Policies.Set(t.Context(), a, changed)
		if err != nil {
			t.Fatal(err)
		}
		want := changed
		want.ID, want.CreatedAt, want.Version = created.ID, created.CreatedAt, 2
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Set(changed) = %+v, want %+v", got, want)
		}
		stored, err := f.r.Policies.ForSite(t.Context(), s)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) != 1 || !reflect.DeepEqual(stored[0], want) {
			t.Fatalf("stored %+v, want %+v", stored, want)
		}

		_, err = f.r.Policies.Set(t.Context(), b, changed)
		wantErr(t, err, store.ErrNotFound)
		bad := changed
		bad.Visibility = store.Public
		bad.LinksEnabled = true
		_, err = f.r.Policies.Set(t.Context(), a, bad)
		wantErr(t, err, store.ErrConstraint)
	})
	t.Run("SetOAuthAllowlist", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a := f.owner().ID
		s := f.site(a).ID
		ctx := t.Context()
		at := now
		set := func(mutate func(*store.Policy)) (store.Policy, error) {
			at = at.Add(time.Minute)
			q := f.validPolicy(s)
			q.LinksEnabled, q.OAuthEnabled, q.UpdatedAt = false, true, at
			mutate(&q)
			return f.r.Policies.Set(ctx, a, q)
		}
		// The OAuth method needs an entry of some kind; nothing is stored.
		for name, mutate := range map[string]func(*store.Policy){
			"empty":           func(q *store.Policy) {},
			"keep none":       func(q *store.Policy) { q.KeepAllowlist = true },
			"keep orgs, none": func(q *store.Policy) { q.KeepGitHubOrgs = true },
		} {
			if _, err := set(mutate); !errors.Is(err, store.ErrOAuthNeedsAllowlist) {
				t.Errorf("%s: Set = %v, want ErrOAuthNeedsAllowlist", name, err)
			}
		}
		if ps, err := f.r.Policies.ForSite(ctx, s); err != nil || len(ps) != 0 {
			t.Fatalf("after refused sets: %+v, %v", ps, err)
		}
		// Organisations alone are enough.
		orgs, err := set(func(q *store.Policy) { q.Allowlist = store.Allowlist{GitHubOrgs: []string{"example-org"}} })
		f.must(err)
		if !orgs.OAuthEnabled || !orgs.Allowlist.Equal(store.Allowlist{GitHubOrgs: []string{"example-org"}}) {
			t.Fatalf("orgs only: %+v", orgs)
		}
		// KeepGitHubOrgs keeps them while the addresses and domains are
		// replaced.
		kept, err := set(func(q *store.Policy) {
			q.Allowlist, q.KeepGitHubOrgs = store.Allowlist{Emails: []string{"ana@example.test"}, GitHubOrgs: []string{"ignored"}}, true
		})
		f.must(err)
		want := store.Allowlist{Emails: []string{"ana@example.test"}, GitHubOrgs: []string{"example-org"}}
		if kept.Version != 2 || !kept.Allowlist.Equal(want) || kept.KeepGitHubOrgs {
			t.Fatalf("kept orgs: %+v", kept)
		}
		stored, err := f.r.Policies.ForSite(ctx, s)
		f.must(err)
		if len(stored) != 1 || !stored[0].Allowlist.Equal(want) {
			t.Fatalf("stored %+v", stored)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner().ID, f.owner().ID
		s := f.site(a).ID
		p := f.policy(a, s)
		sess := f.validSiteSession(s, p)
		f.must(f.r.Sessions.CreateSite(t.Context(), sess))
		wantErr(t, f.r.Policies.Delete(t.Context(), b, s, p.ID), store.ErrNotFound)
		wantErr(t, f.r.Policies.Delete(t.Context(), a, f.site(a).ID, p.ID), store.ErrNotFound)
		f.must(f.r.Policies.Delete(t.Context(), a, s, p.ID))
		wantErr(t, f.r.Policies.Delete(t.Context(), a, s, p.ID), store.ErrNotFound)
		_, err := f.r.Sessions.GetSiteByTokenHash(t.Context(), sess.TokenHash)
		wantErr(t, err, store.ErrNotFound)
	})
}

func policiesCreate(t *testing.T, open Opener) {
	type owned struct {
		ownerID string
		p       store.Policy
	}
	runInsertCases(t, open,
		func(f *fixture) owned {
			o := f.owner()
			return owned{o.ID, f.validPolicy(f.site(o.ID).ID)}
		},
		func(f *fixture, v owned) error { return f.r.Policies.Create(f.t.Context(), v.ownerID, v.p) },
		[]insertCase[owned]{
			{name: "valid"},
			{name: "public", mutate: func(_ *fixture, v *owned) { v.p.Visibility, v.p.LinksEnabled = store.Public, false }},
			{name: "password", mutate: func(_ *fixture, v *owned) { v.p.PasswordEnabled, v.p.PasswordHash = true, "$argon2id$stand-in" }},
			{name: "same prefix on another site", mutate: func(f *fixture, v *owned) { f.policy(v.ownerID, f.site(v.ownerID).ID) }},
			{name: "path policy beside default", mutate: func(f *fixture, v *owned) {
				f.policy(v.ownerID, v.p.SiteID)
				v.p.PathPrefix = "/docs/"
			}},
			{name: "duplicate prefix", mutate: func(f *fixture, v *owned) { f.policy(v.ownerID, v.p.SiteID) }, want: store.ErrConflict},
			{name: "another owner's site", mutate: func(f *fixture, v *owned) { v.ownerID = f.owner().ID }, want: store.ErrNotFound},
			{name: "public with methods", mutate: func(_ *fixture, v *owned) { v.p.Visibility = store.Public }, want: store.ErrConstraint},
			{name: "password without hash", mutate: func(_ *fixture, v *owned) { v.p.PasswordEnabled = true }, want: store.ErrConstraint},
			{name: "relative prefix", mutate: func(_ *fixture, v *owned) { v.p.PathPrefix = "docs/" }, want: store.ErrConstraint},
			{name: "unknown visibility", mutate: func(_ *fixture, v *owned) { v.p.Visibility = "private" }, want: store.ErrConstraint},
			{name: "version zero", mutate: func(_ *fixture, v *owned) { v.p.Version = 0 }, want: store.ErrConstraint},
		})
}
