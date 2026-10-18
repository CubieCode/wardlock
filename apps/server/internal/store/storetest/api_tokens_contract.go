// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func apiTokensContract(t *testing.T, open Opener) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.APIToken {
				return store.APIToken{
					ID: store.NewID(), OwnerID: f.owner().ID, PublicID: f.publicID(), TokenHash: f.hash(),
					Name: "CI", ScopeDeploy: true, CreatedAt: now,
				}
			},
			func(f *fixture, tok store.APIToken) error { return f.r.APITokens.Create(f.t.Context(), tok) },
			[]insertCase[store.APIToken]{
				{name: "valid"},
				{name: "expiring, manage only", mutate: func(_ *fixture, tok *store.APIToken) {
					tok.ScopeDeploy, tok.ScopeManage, tok.ExpiresAt = false, true, now.Add(time.Hour)
				}},
				{name: "duplicate public ID across owners", mutate: func(f *fixture, tok *store.APIToken) {
					other := *tok
					other.ID, other.OwnerID, other.TokenHash = store.NewID(), f.owner().ID, f.hash()
					f.must(f.r.APITokens.Create(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "short public ID", mutate: func(_ *fixture, tok *store.APIToken) { tok.PublicID = "abc" }, want: store.ErrConstraint},
				{name: "uppercase public ID", mutate: func(_ *fixture, tok *store.APIToken) { tok.PublicID = "ABCDEFGHIJ" }, want: store.ErrConstraint},
				{name: "no scopes", mutate: func(_ *fixture, tok *store.APIToken) { tok.ScopeDeploy = false }, want: store.ErrConstraint},
				{name: "empty name", mutate: func(_ *fixture, tok *store.APIToken) { tok.Name = "" }, want: store.ErrConstraint},
				{name: "long name", mutate: func(_ *fixture, tok *store.APIToken) { tok.Name = strings.Repeat("n", 101) }, want: store.ErrConstraint},
				{name: "expires at creation", mutate: func(_ *fixture, tok *store.APIToken) { tok.ExpiresAt = tok.CreatedAt }, want: store.ErrConstraint},
				{name: "unknown owner", mutate: func(_ *fixture, tok *store.APIToken) { tok.OwnerID = store.NewID() }, want: store.ErrConstraint},
			})
	})
	t.Run("CreateCLILogin", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.CLILoginRequest {
				return store.CLILoginRequest{
					ID: store.NewID(), DeviceCodeHash: f.hash(), UserCode: "WDJB-MJHT", Status: store.CLILoginPending,
					ScopeDeploy: true, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
				}
			},
			func(f *fixture, l store.CLILoginRequest) error { return f.r.APITokens.CreateCLILogin(f.t.Context(), l) },
			[]insertCase[store.CLILoginRequest]{
				{name: "pending"},
				{name: "approved", mutate: func(f *fixture, l *store.CLILoginRequest) {
					l.Status, l.OwnerID, l.DecidedAt = store.CLILoginApproved, f.owner().ID, now
				}},
				{name: "redeemed", mutate: func(f *fixture, l *store.CLILoginRequest) {
					o := f.owner()
					tok := store.APIToken{ID: store.NewID(), OwnerID: o.ID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "cli", ScopeDeploy: true, CreatedAt: now}
					f.must(f.r.APITokens.Create(f.t.Context(), tok))
					l.Status, l.OwnerID, l.APITokenID = store.CLILoginRedeemed, o.ID, tok.ID
				}},
				{name: "pending with owner", mutate: func(f *fixture, l *store.CLILoginRequest) { l.OwnerID = f.owner().ID }, want: store.ErrConstraint},
				{name: "approved without owner", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.Status = store.CLILoginApproved }, want: store.ErrConstraint},
				{name: "redeemed without token", mutate: func(f *fixture, l *store.CLILoginRequest) {
					l.Status, l.OwnerID = store.CLILoginRedeemed, f.owner().ID
				}, want: store.ErrConstraint},
				{name: "unknown status", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.Status = "expired" }, want: store.ErrConstraint},
				{name: "short user code", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.UserCode = "AB" }, want: store.ErrConstraint},
				{name: "duplicate user code", mutate: func(f *fixture, l *store.CLILoginRequest) {
					other := *l
					other.ID, other.DeviceCodeHash = store.NewID(), f.hash()
					f.must(f.r.APITokens.CreateCLILogin(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "duplicate device code", mutate: func(f *fixture, l *store.CLILoginRequest) {
					other := *l
					other.ID, other.UserCode = store.NewID(), "XXXX-YYYY"
					f.must(f.r.APITokens.CreateCLILogin(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "expires at creation", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.ExpiresAt = l.CreatedAt }, want: store.ErrConstraint},
			})
	})
	t.Run("GetByPublicID", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		want := f.validAPIToken(f.owner().ID, now)
		want.ScopeManage, want.ExpiresAt, want.LastUsedAt = true, now.Add(time.Hour), now.Add(time.Minute)
		f.must(f.r.APITokens.Create(t.Context(), want))
		got, err := f.r.APITokens.GetByPublicID(t.Context(), want.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("GetByPublicID() = %+v, want %+v", got, want)
		}
		_, err = f.r.APITokens.GetByPublicID(t.Context(), f.publicID())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("Touch", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		tok := f.apiToken(f.owner().ID, now)
		later := now.Add(time.Hour)
		f.must(f.r.APITokens.Touch(t.Context(), tok.ID, later))
		f.must(f.r.APITokens.Touch(t.Context(), tok.ID, now.Add(time.Minute)))
		got, err := f.r.APITokens.GetByPublicID(t.Context(), tok.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.LastUsedAt.Equal(later) {
			t.Fatalf("LastUsedAt = %v, want %v", got.LastUsedAt, later)
		}
	})
	t.Run("Revoke", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		tok := f.apiToken(a.ID, now)
		wantErr(t, f.r.APITokens.Revoke(t.Context(), b.ID, tok.ID, now), store.ErrNotFound)
		wantErr(t, f.r.APITokens.Revoke(t.Context(), a.ID, store.NewID(), now), store.ErrNotFound)
		first := now.Add(time.Minute)
		f.must(f.r.APITokens.Revoke(t.Context(), a.ID, tok.ID, first))
		f.must(f.r.APITokens.Revoke(t.Context(), a.ID, tok.ID, first.Add(time.Minute)))
		got, err := f.r.APITokens.GetByPublicID(t.Context(), tok.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.RevokedAt.Equal(first) {
			t.Fatalf("RevokedAt = %v, want %v", got.RevokedAt, first)
		}
	})
	t.Run("List", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		// Five usable tokens created a minute apart; two share a creation
		// time so the ID breaks the tie.
		var usable []store.APIToken
		for i := range 5 {
			at := now.Add(time.Duration(i) * time.Minute)
			if i == 4 {
				at = usable[3].CreatedAt
			}
			usable = append(usable, f.apiToken(a.ID, at))
		}
		revoked := f.apiToken(a.ID, now)
		f.must(f.r.APITokens.Revoke(t.Context(), a.ID, revoked.ID, now))
		expired := f.validAPIToken(a.ID, now)
		expired.ExpiresAt = now.Add(time.Minute)
		f.must(f.r.APITokens.Create(t.Context(), expired))
		f.apiToken(b.ID, now)

		q := store.APITokenQuery{OwnerID: a.ID, Now: now.Add(10 * time.Minute), Limit: 2}
		want := []string{usable[4].ID, usable[3].ID, usable[2].ID, usable[1].ID, usable[0].ID}
		if usable[3].ID > usable[4].ID {
			want[0], want[1] = usable[3].ID, usable[4].ID
		}
		var got []string
		for range 4 {
			page, err := f.r.APITokens.List(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			for _, tok := range page {
				got = append(got, tok.ID)
			}
			if len(page) < q.Limit {
				break
			}
			last := page[len(page)-1]
			q.BeforeCreated, q.BeforeID = last.CreatedAt, last.ID
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("List pages = %v, want %v", got, want)
		}
		page, err := f.r.APITokens.List(t.Context(), store.APITokenQuery{OwnerID: b.ID, Now: now, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 1 || page[0].OwnerID != b.ID {
			t.Fatalf("owner B sees %+v", page)
		}
	})
	t.Run("GetCLILoginByDeviceCodeHash", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		want := f.validCLILogin()
		want.ClientName = "wardlock dev on build-host"
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), want))
		got, err := f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), want.DeviceCodeHash)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("GetCLILoginByDeviceCodeHash() = %+v, want %+v", got, want)
		}
		_, err = f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), f.hash())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("DecideCLILogin", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		l := f.validCLILogin()
		l.ClientName = "from the CLI"
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), l))
		at := now.Add(time.Minute)
		approve := store.CLILoginDecision{
			UserCode: l.UserCode, Status: store.CLILoginApproved, OwnerID: o.ID, ScopeDeploy: true, ScopeManage: true,
			TokenName: "laptop", At: at,
		}
		f.must(f.r.APITokens.DecideCLILogin(t.Context(), approve))
		got, err := f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), l.DeviceCodeHash)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != store.CLILoginApproved || got.OwnerID != o.ID || !got.ScopeDeploy || !got.ScopeManage ||
			got.ClientName != "laptop" || !got.DecidedAt.Equal(at) {
			t.Fatalf("after approval: %+v", got)
		}
		// Decided requests cannot be decided again, by anyone.
		wantErr(t, f.r.APITokens.DecideCLILogin(t.Context(), approve), store.ErrNotFound)
		deny := store.CLILoginDecision{UserCode: l.UserCode, Status: store.CLILoginDenied, OwnerID: f.owner().ID, At: at}
		wantErr(t, f.r.APITokens.DecideCLILogin(t.Context(), deny), store.ErrNotFound)
		// Unknown and expired codes.
		deny.UserCode = "ZZZZ-ZZZZ"
		wantErr(t, f.r.APITokens.DecideCLILogin(t.Context(), deny), store.ErrNotFound)
		old := f.validCLILogin()
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), old))
		deny.UserCode, deny.At = old.UserCode, old.ExpiresAt
		wantErr(t, f.r.APITokens.DecideCLILogin(t.Context(), deny), store.ErrNotFound)
		// Denial keeps the CLI's name.
		deny.At = now
		f.must(f.r.APITokens.DecideCLILogin(t.Context(), deny))
		got, err = f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), old.DeviceCodeHash)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != store.CLILoginDenied || got.ScopeDeploy || got.ScopeManage {
			t.Fatalf("after denial: %+v", got)
		}
	})
	t.Run("RedeemCLILogin", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		pending := f.validCLILogin()
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), pending))
		tok := f.validAPIToken(o.ID, now)
		wantErr(t, f.r.APITokens.RedeemCLILogin(t.Context(), pending.ID, tok, now), store.ErrNotFound)
		_, err := f.r.APITokens.GetByPublicID(t.Context(), tok.PublicID)
		wantErr(t, err, store.ErrNotFound) // rolled back

		f.must(f.r.APITokens.DecideCLILogin(t.Context(), store.CLILoginDecision{
			UserCode: pending.UserCode, Status: store.CLILoginApproved, OwnerID: o.ID, ScopeDeploy: true, At: now,
		}))
		wantErr(t, f.r.APITokens.RedeemCLILogin(t.Context(), pending.ID, tok, pending.ExpiresAt), store.ErrNotFound)
		f.must(f.r.APITokens.RedeemCLILogin(t.Context(), pending.ID, tok, now))
		got, err := f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), pending.DeviceCodeHash)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != store.CLILoginRedeemed || got.APITokenID != tok.ID {
			t.Fatalf("after redeem: %+v", got)
		}
		if _, err := f.r.APITokens.GetByPublicID(t.Context(), tok.PublicID); err != nil {
			t.Fatal(err)
		}
		again := f.validAPIToken(o.ID, now)
		wantErr(t, f.r.APITokens.RedeemCLILogin(t.Context(), pending.ID, again, now), store.ErrNotFound)
		_, err = f.r.APITokens.GetByPublicID(t.Context(), again.PublicID)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("DeleteCLILoginsExpiredBefore", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		old, current := f.validCLILogin(), f.validCLILogin()
		current.ExpiresAt = old.ExpiresAt.Add(time.Hour)
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), old))
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), current))
		f.must(f.r.APITokens.DeleteCLILoginsExpiredBefore(t.Context(), old.ExpiresAt.Add(time.Minute)))
		_, err := f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), old.DeviceCodeHash)
		wantErr(t, err, store.ErrNotFound)
		if _, err := f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), current.DeviceCodeHash); err != nil {
			t.Fatal(err)
		}
	})
}

// validAPIToken returns a deploy token for ownerID created at.
func (f *fixture) validAPIToken(ownerID string, at time.Time) store.APIToken {
	return store.APIToken{
		ID: store.NewID(), OwnerID: ownerID, PublicID: f.publicID(), TokenHash: f.hash(),
		Name: "CI", ScopeDeploy: true, CreatedAt: at,
	}
}

func (f *fixture) apiToken(ownerID string, at time.Time) store.APIToken {
	f.t.Helper()
	t := f.validAPIToken(ownerID, at)
	f.must(f.r.APITokens.Create(f.t.Context(), t))
	return t
}

// validCLILogin returns a pending CLI login with a unique user code.
func (f *fixture) validCLILogin() store.CLILoginRequest {
	return store.CLILoginRequest{
		ID: store.NewID(), DeviceCodeHash: f.hash(), UserCode: fmt.Sprintf("CODE-%04d", f.seq()), Status: store.CLILoginPending,
		CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute),
	}
}
