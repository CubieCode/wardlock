// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
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
	t.Run("GetByEmail", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		want := f.owner()
		f.owner()
		got, err := f.r.Owners.GetByEmail(t.Context(), want.Email)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("GetByEmail() = %+v, want %+v", got, want)
		}
		_, err = f.r.Owners.GetByEmail(t.Context(), "nobody@example.test")
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("Count", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		for want := range 3 {
			got, err := f.r.Owners.Count(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("Count() = %d, want %d", got, want)
			}
			f.owner()
		}
	})
	t.Run("MarkAccepted", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		first := now.Add(time.Minute)
		f.must(f.r.Owners.MarkAccepted(t.Context(), o.ID, first))
		f.must(f.r.Owners.MarkAccepted(t.Context(), o.ID, first.Add(time.Hour)))
		got, err := f.r.Owners.Get(t.Context(), o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.AcceptedAt.Equal(first) {
			t.Fatalf("AcceptedAt = %v, want the first time %v", got.AcceptedAt, first)
		}
		f.must(f.r.Owners.MarkAccepted(t.Context(), store.NewID(), first))
	})
	t.Run("CreateWithLoginCode", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.validOwner()
		c := f.validLoginCode(o.ID, store.LoginCodeSignInURL)
		f.must(f.r.Owners.CreateWithLoginCode(t.Context(), o, c))
		if _, err := f.r.Owners.Get(t.Context(), o.ID); err != nil {
			t.Fatal(err)
		}
		got, err := f.r.Owners.GetLoginCodeByHash(t.Context(), c.Purpose, c.CodeHash)
		if err != nil {
			t.Fatal(err)
		}
		if got != c {
			t.Fatalf("GetLoginCodeByHash() = %+v, want %+v", got, c)
		}

		// A failing code insert leaves no owner behind.
		o2 := f.validOwner()
		bad := f.validLoginCode(o2.ID, store.LoginCodeSignInURL)
		bad.ExpiresAt = bad.CreatedAt
		wantErr(t, f.r.Owners.CreateWithLoginCode(t.Context(), o2, bad), store.ErrConstraint)
		_, err = f.r.Owners.Get(t.Context(), o2.ID)
		wantErr(t, err, store.ErrNotFound)
		dup := f.validOwner()
		dup.Email = o.Email
		wantErr(t, f.r.Owners.CreateWithLoginCode(t.Context(), dup, f.validLoginCode(dup.ID, store.LoginCodeSignInURL)), store.ErrConflict)
	})
	t.Run("CompleteSetup", func(t *testing.T) {
		t.Parallel()
		// setupCode inserts a setup code valid from createdAt to expiresAt.
		setupCode := func(f *fixture, createdAt, expiresAt time.Time) store.OwnerLoginCode {
			c := f.validLoginCode("", store.LoginCodeSetup)
			c.CreatedAt, c.ExpiresAt = createdAt, expiresAt
			f.must(f.r.Owners.CreateLoginCode(f.t.Context(), c))
			return c
		}
		valid := func(f *fixture) store.OwnerLoginCode { return setupCode(f, now.Add(-time.Minute), now.Add(time.Hour)) }
		tests := []struct {
			name string
			code func(f *fixture) store.OwnerLoginCode
			want error
		}{
			{name: "valid", code: valid},
			{name: "used code", code: func(f *fixture) store.OwnerLoginCode {
				c := valid(f)
				f.must(f.r.Owners.UseLoginCode(f.t.Context(), c.ID, now.Add(-time.Second)))
				return c
			}, want: store.ErrNotFound},
			{name: "expired code", code: func(f *fixture) store.OwnerLoginCode { return setupCode(f, now.Add(-time.Hour), now) }, want: store.ErrNotFound},
			{name: "unknown code", code: func(f *fixture) store.OwnerLoginCode {
				c := valid(f)
				c.ID = store.NewID()
				return c
			}, want: store.ErrNotFound},
			{name: "owner exists", code: func(f *fixture) store.OwnerLoginCode { f.owner(); return valid(f) }, want: store.ErrConflict},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, open)
				c := tt.code(f)
				before, err := f.r.Owners.Count(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				o := f.validOwner()
				o.AcceptedAt = now
				wantErr(t, f.r.Owners.CompleteSetup(t.Context(), c.ID, o), tt.want)
				if tt.want == nil {
					if got, err := f.r.Owners.Get(t.Context(), o.ID); err != nil || got != o {
						t.Fatalf("Get() = %+v, %v; want %+v", got, err, o)
					}
					wantErr(t, f.r.Owners.CompleteSetup(t.Context(), c.ID, f.validOwner()), store.ErrNotFound)
					return
				}
				if after, err := f.r.Owners.Count(t.Context()); err != nil || after != before {
					t.Fatalf("owners %d -> %d (%v) after failed setup", before, after, err)
				}
				if errors.Is(tt.want, store.ErrConflict) {
					got, err := f.r.Owners.GetLoginCodeByHash(t.Context(), store.LoginCodeSetup, c.CodeHash)
					if err != nil {
						t.Fatal(err)
					}
					if !got.UsedAt.IsZero() {
						t.Fatal("failed setup spent the code")
					}
				}
			})
		}
	})
	t.Run("GetLoginCodeByHash", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		c := f.loginCode(o.ID, store.LoginCodeSignInURL)
		_, err := f.r.Owners.GetLoginCodeByHash(t.Context(), store.LoginCodeEmail, c.CodeHash)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Owners.GetLoginCodeByHash(t.Context(), store.LoginCodeSignInURL, f.hash())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("LatestLoginCode", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		other := f.owner()
		_, err := f.r.Owners.LatestLoginCode(t.Context(), o.ID, store.LoginCodeEmail)
		wantErr(t, err, store.ErrNotFound)
		f.loginCode(o.ID, store.LoginCodeEmail)
		later := f.validLoginCode(o.ID, store.LoginCodeEmail)
		later.CreatedAt = now.Add(time.Second)
		later.ExpiresAt = later.CreatedAt.Add(10 * time.Minute)
		f.must(f.r.Owners.CreateLoginCode(t.Context(), later))
		f.loginCode(o.ID, store.LoginCodeSignInURL)
		f.loginCode(other.ID, store.LoginCodeEmail)
		got, err := f.r.Owners.LatestLoginCode(t.Context(), o.ID, store.LoginCodeEmail)
		if err != nil {
			t.Fatal(err)
		}
		if got != later {
			t.Fatalf("LatestLoginCode() = %+v, want %+v", got, later)
		}
	})
	t.Run("AddLoginCodeAttempt", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		c := f.loginCode(f.owner().ID, store.LoginCodeEmail)
		for want := 1; want <= 3; want++ {
			got, err := f.r.Owners.AddLoginCodeAttempt(t.Context(), c.ID, 3)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("AddLoginCodeAttempt() = %d, want %d", got, want)
			}
		}
		_, err := f.r.Owners.AddLoginCodeAttempt(t.Context(), c.ID, 3)
		wantErr(t, err, store.ErrNotFound) // exhausted
		got, err := f.r.Owners.GetLoginCodeByHash(t.Context(), c.Purpose, c.CodeHash)
		if err != nil || got.Attempts != 3 {
			t.Fatalf("attempts = %d (%v) after exhaustion, want 3", got.Attempts, err)
		}
		used := f.loginCode(f.owner().ID, store.LoginCodeEmail)
		f.must(f.r.Owners.UseLoginCode(t.Context(), used.ID, now))
		_, err = f.r.Owners.AddLoginCodeAttempt(t.Context(), used.ID, 3)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Owners.AddLoginCodeAttempt(t.Context(), store.NewID(), 3)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("UseLoginCode", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		c := f.loginCode(f.owner().ID, store.LoginCodeSignInURL)
		at := now.Add(time.Minute)
		f.must(f.r.Owners.UseLoginCode(t.Context(), c.ID, at))
		wantErr(t, f.r.Owners.UseLoginCode(t.Context(), c.ID, at), store.ErrNotFound)
		got, err := f.r.Owners.GetLoginCodeByHash(t.Context(), c.Purpose, c.CodeHash)
		if err != nil {
			t.Fatal(err)
		}
		if !got.UsedAt.Equal(at) {
			t.Fatalf("UsedAt = %v, want %v", got.UsedAt, at)
		}
		expiring := f.loginCode(f.owner().ID, store.LoginCodeEmail)
		wantErr(t, f.r.Owners.UseLoginCode(t.Context(), expiring.ID, expiring.ExpiresAt), store.ErrNotFound)
		wantErr(t, f.r.Owners.UseLoginCode(t.Context(), store.NewID(), at), store.ErrNotFound)
	})
	t.Run("ReplaceLoginCode", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		old := f.loginCode(o.ID, store.LoginCodeEmail)
		link := f.loginCode(o.ID, store.LoginCodeSignInURL)
		othersCode := f.loginCode(other.ID, store.LoginCodeEmail)
		next := f.validLoginCode(o.ID, store.LoginCodeEmail)
		next.CreatedAt = now.Add(time.Minute)
		next.ExpiresAt = next.CreatedAt.Add(10 * time.Minute)
		f.must(f.r.Owners.ReplaceLoginCode(t.Context(), next))
		get := func(c store.OwnerLoginCode) store.OwnerLoginCode {
			t.Helper()
			got, err := f.r.Owners.GetLoginCodeByHash(t.Context(), c.Purpose, c.CodeHash)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		if !get(old).UsedAt.Equal(next.CreatedAt) {
			t.Fatal("previous code not retired at the new code's creation")
		}
		if got := get(next); got != next {
			t.Fatalf("new code = %+v, want %+v", got, next)
		}
		if !get(link).UsedAt.IsZero() || !get(othersCode).UsedAt.IsZero() {
			t.Fatal("retired another purpose's or owner's code")
		}
		// A failing insert retires nothing.
		bad := f.validLoginCode(o.ID, store.LoginCodeEmail)
		bad.ExpiresAt = bad.CreatedAt
		wantErr(t, f.r.Owners.ReplaceLoginCode(t.Context(), bad), store.ErrConstraint)
		if !get(next).UsedAt.IsZero() {
			t.Fatal("failed replace retired the current code")
		}
		setup := f.loginCode("", store.LoginCodeSetup)
		f.must(f.r.Owners.ReplaceLoginCode(t.Context(), f.validLoginCode("", store.LoginCodeSetup)))
		if get(setup).UsedAt.IsZero() {
			t.Fatal("setup code not retired")
		}
	})
	t.Run("RetireLoginCodes", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		other := f.owner()
		email1 := f.loginCode(o.ID, store.LoginCodeEmail)
		email2 := f.loginCode(o.ID, store.LoginCodeEmail)
		link := f.loginCode(o.ID, store.LoginCodeSignInURL)
		othersEmail := f.loginCode(other.ID, store.LoginCodeEmail)
		setup := f.loginCode("", store.LoginCodeSetup)
		at := now.Add(time.Minute)
		f.must(f.r.Owners.RetireLoginCodes(t.Context(), o.ID, store.LoginCodeEmail, at))
		used := func(c store.OwnerLoginCode) bool {
			t.Helper()
			got, err := f.r.Owners.GetLoginCodeByHash(t.Context(), c.Purpose, c.CodeHash)
			if err != nil {
				t.Fatal(err)
			}
			return !got.UsedAt.IsZero()
		}
		if !used(email1) || !used(email2) {
			t.Fatal("owner's email codes not retired")
		}
		if used(link) || used(othersEmail) || used(setup) {
			t.Fatal("retired codes of another purpose or owner")
		}
		f.must(f.r.Owners.RetireLoginCodes(t.Context(), "", store.LoginCodeSetup, at))
		if !used(setup) || used(link) {
			t.Fatal("setup retirement wrong")
		}
	})
	ownersOperatorContract(t, open)
}

// validLoginCode returns an unused code for ownerID ("" for setup), valid
// for ten minutes from now.
func (f *fixture) validLoginCode(ownerID string, purpose store.LoginCodePurpose) store.OwnerLoginCode {
	return store.OwnerLoginCode{
		ID: store.NewID(), OwnerID: ownerID, Purpose: purpose, CodeHash: f.hash(),
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
}

func (f *fixture) loginCode(ownerID string, purpose store.LoginCodePurpose) store.OwnerLoginCode {
	f.t.Helper()
	c := f.validLoginCode(ownerID, purpose)
	f.must(f.r.Owners.CreateLoginCode(f.t.Context(), c))
	return c
}
