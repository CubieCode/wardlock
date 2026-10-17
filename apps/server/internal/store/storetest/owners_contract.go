// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func ownersContract(t *testing.T, open Opener) {
	t.Run("CreateGet", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		inviter := f.owner()
		want := f.validOwner()
		want.InvitedBy = inviter.ID
		want.AcceptedAt = now.Add(time.Minute)
		f.must(f.r.Owners.Create(t.Context(), want))
		got, err := f.r.Owners.Get(t.Context(), want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("Get() = %+v, want %+v", got, want)
		}
	})
	t.Run("GetMissing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		_, err := f.r.Owners.Get(t.Context(), store.NewID())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.Owner { return f.validOwner() },
			func(f *fixture, o store.Owner) error { return f.r.Owners.Create(f.t.Context(), o) },
			[]insertCase[store.Owner]{
				{name: "valid"},
				{name: "duplicate email", mutate: func(f *fixture, o *store.Owner) { o.Email = f.owner().Email }, want: store.ErrConflict},
				{name: "duplicate id", mutate: func(f *fixture, o *store.Owner) { o.ID = f.owner().ID }, want: store.ErrConflict},
				{name: "uppercase email", mutate: func(_ *fixture, o *store.Owner) { o.Email = "Owner@example.test" }, want: store.ErrConstraint},
				{name: "email without at", mutate: func(_ *fixture, o *store.Owner) { o.Email = "owner.example.test" }, want: store.ErrConstraint},
				{name: "email too long", mutate: func(_ *fixture, o *store.Owner) { o.Email = strings.Repeat("a", 250) + "@x.nz" }, want: store.ErrConstraint},
				{name: "unknown inviter", mutate: func(_ *fixture, o *store.Owner) { o.InvitedBy = store.NewID() }, want: store.ErrConstraint},
			})
	})
	t.Run("CreateLoginCode", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.OwnerLoginCode {
				return store.OwnerLoginCode{
					ID: store.NewID(), OwnerID: f.owner().ID, Purpose: store.LoginCodeEmail, CodeHash: f.hash(),
					CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
				}
			},
			func(f *fixture, c store.OwnerLoginCode) error { return f.r.Owners.CreateLoginCode(f.t.Context(), c) },
			[]insertCase[store.OwnerLoginCode]{
				{name: "email code"},
				{name: "sign-in URL", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.Purpose = store.LoginCodeSignInURL }},
				{name: "setup without owner", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.Purpose, c.OwnerID = store.LoginCodeSetup, "" }},
				{name: "setup with owner", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.Purpose = store.LoginCodeSetup }, want: store.ErrConstraint},
				{name: "email code without owner", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.OwnerID = "" }, want: store.ErrConstraint},
				{name: "unknown purpose", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.Purpose = "password" }, want: store.ErrConstraint},
				{name: "unknown owner", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.OwnerID = store.NewID() }, want: store.ErrConstraint},
				{name: "negative attempts", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.Attempts = -1 }, want: store.ErrConstraint},
				{name: "expires at creation", mutate: func(_ *fixture, c *store.OwnerLoginCode) { c.ExpiresAt = c.CreatedAt }, want: store.ErrConstraint},
			})
	})
}
