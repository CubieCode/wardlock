// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// Operator controls (A2.13): the Owners, Sites and GC cases for suspension
// and takedown, run from those interfaces' suites.

func ownersOperatorContract(t *testing.T, open Opener) {
	t.Run("Suspend", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		sess := f.ownerSession(o.ID, now)
		otherSess := f.ownerSession(other.ID, now)
		tok := f.apiToken(o.ID, now)
		approved, pending, unredeemed := f.validCLILogin(), f.validCLILogin(), f.validCLILogin()
		for _, l := range []store.CLILoginRequest{approved, pending, unredeemed} {
			f.must(f.r.APITokens.CreateCLILogin(t.Context(), l))
		}
		for _, code := range []string{approved.UserCode, unredeemed.UserCode} {
			f.must(f.r.APITokens.DecideCLILogin(t.Context(), store.CLILoginDecision{
				UserCode: code, Status: store.CLILoginApproved, OwnerID: o.ID, ScopeDeploy: true, At: now,
			}))
		}

		at := now.Add(time.Hour)
		f.must(f.r.Owners.Suspend(t.Context(), o.ID, at))
		// Suspending again keeps the first time.
		f.must(f.r.Owners.Suspend(t.Context(), o.ID, at.Add(time.Hour)))
		got, err := f.r.Owners.Get(t.Context(), o.ID)
		wantErr(t, err, nil)
		if !got.SuspendedAt.Equal(at) {
			t.Fatalf("SuspendedAt = %v, want %v", got.SuspendedAt, at)
		}
		byEmail, err := f.r.Owners.GetByEmail(t.Context(), o.Email)
		wantErr(t, err, nil)
		if !byEmail.SuspendedAt.Equal(at) {
			t.Fatalf("GetByEmail SuspendedAt = %v", byEmail.SuspendedAt)
		}

		// Every credential kind stops working; the other owner's do not.
		_, err = f.r.Sessions.GetOwnerByTokenHash(t.Context(), sess.TokenHash)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.APITokens.GetByPublicID(t.Context(), tok.PublicID)
		wantErr(t, err, store.ErrNotFound)
		l, err := f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), approved.DeviceCodeHash)
		wantErr(t, err, nil)
		if l.Status != store.CLILoginDenied {
			t.Fatalf("approved login after suspension: %s", l.Status)
		}
		l, err = f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), pending.DeviceCodeHash)
		wantErr(t, err, nil)
		if l.Status != store.CLILoginPending {
			t.Fatalf("pending login after suspension: %s", l.Status)
		}
		if _, err := f.r.Sessions.GetOwnerByTokenHash(t.Context(), otherSess.TokenHash); err != nil {
			t.Fatalf("other owner's session: %v", err)
		}

		// Reinstating brings tokens back; sessions stay revoked.
		f.must(f.r.Owners.Reinstate(t.Context(), o.ID))
		got, err = f.r.Owners.Get(t.Context(), o.ID)
		wantErr(t, err, nil)
		if !got.SuspendedAt.IsZero() {
			t.Fatalf("SuspendedAt after reinstating = %v", got.SuspendedAt)
		}
		if _, err := f.r.APITokens.GetByPublicID(t.Context(), tok.PublicID); err != nil {
			t.Fatalf("token after reinstating: %v", err)
		}
		row, err := f.r.Sessions.GetOwnerByTokenHash(t.Context(), sess.TokenHash)
		wantErr(t, err, nil)
		if !row.RevokedAt.Equal(at) {
			t.Fatalf("session RevokedAt = %v, want %v", row.RevokedAt, at)
		}
		l, err = f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), approved.DeviceCodeHash)
		wantErr(t, err, nil)
		if l.Status != store.CLILoginDenied {
			t.Fatalf("approved login after reinstating: %s", l.Status)
		}
	})
	t.Run("SuspendedCannotRedeemCLILogin", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		l := f.validCLILogin()
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), l))
		f.must(f.r.APITokens.DecideCLILogin(t.Context(), store.CLILoginDecision{
			UserCode: l.UserCode, Status: store.CLILoginApproved, OwnerID: o.ID, ScopeDeploy: true, At: now,
		}))
		// Suspended between approval and collection, by a writer that
		// skipped the denial (a race with Suspend's transaction).
		f.must(f.r.Owners.Suspend(t.Context(), o.ID, now))
		tok := f.validAPIToken(o.ID, now)
		wantErr(t, f.r.APITokens.RedeemCLILogin(t.Context(), l.ID, tok, now), store.ErrNotFound)
	})
	t.Run("SuspendedCannotDecideCLILogin", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		l := f.validCLILogin()
		f.must(f.r.APITokens.CreateCLILogin(t.Context(), l))
		// Suspended between the session check and the approval.
		f.must(f.r.Owners.Suspend(t.Context(), o.ID, now))
		wantErr(t, f.r.APITokens.DecideCLILogin(t.Context(), store.CLILoginDecision{
			UserCode: l.UserCode, Status: store.CLILoginApproved, OwnerID: o.ID, ScopeDeploy: true, At: now,
		}), store.ErrNotFound)
		got, err := f.r.APITokens.GetCLILoginByDeviceCodeHash(t.Context(), l.DeviceCodeHash)
		wantErr(t, err, nil)
		if got.Status != store.CLILoginPending {
			t.Fatalf("login after a suspended owner's approval: %s", got.Status)
		}
		// Reinstated, nothing approved meanwhile survives; a new decision works.
		f.must(f.r.Owners.Reinstate(t.Context(), o.ID))
		f.must(f.r.APITokens.DecideCLILogin(t.Context(), store.CLILoginDecision{
			UserCode: l.UserCode, Status: store.CLILoginApproved, OwnerID: o.ID, ScopeDeploy: true, At: now,
		}))
	})
	t.Run("SuspendedGetsNoSession", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		f.must(f.r.Owners.Suspend(t.Context(), o.ID, now))
		s := f.validOwnerSession(o.ID, now)
		wantErr(t, f.r.Sessions.CreateOwner(t.Context(), s), store.ErrNotFound)
		// Nothing was stored that reinstatement could bring back.
		f.must(f.r.Owners.Reinstate(t.Context(), o.ID))
		_, err := f.r.Sessions.GetOwnerByTokenHash(t.Context(), s.TokenHash)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("SuspendUnknown", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		wantErr(t, f.r.Owners.Suspend(t.Context(), store.NewID(), now), store.ErrNotFound)
		wantErr(t, f.r.Owners.Reinstate(t.Context(), store.NewID()), store.ErrNotFound)
	})
	t.Run("ListAll", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		var want []string
		for range 3 {
			want = append(want, f.owner().Email)
		}
		slices.Sort(want)
		first, err := f.r.Owners.ListAll(t.Context(), "", 2)
		wantErr(t, err, nil)
		rest, err := f.r.Owners.ListAll(t.Context(), first[len(first)-1].Email, 2)
		wantErr(t, err, nil)
		var got []string
		for _, o := range append(first, rest...) {
			got = append(got, o.Email)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("ListAll = %v, want %v", got, want)
		}
	})
}

func sitesOperatorContract(t *testing.T, open Opener) {
	t.Run("TakeDown", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.activated(o.ID, s.ID, now)
		next := f.deploy(o.ID, s.ID)
		at := now.Add(time.Hour)
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, at))
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, at.Add(time.Hour)))
		for name, get := range map[string]func() (store.Site, error){
			"Get":       func() (store.Site, error) { return f.r.Sites.Get(t.Context(), store.ActorFor(o.ID), s.ID) },
			"GetByName": func() (store.Site, error) { return f.r.Sites.GetByName(t.Context(), s.Name) },
			"GetByID":   func() (store.Site, error) { return f.r.Sites.GetByID(t.Context(), s.ID) },
		} {
			got, err := get()
			wantErr(t, err, nil)
			if !got.TakenDownAt.Equal(at) {
				t.Fatalf("%s TakenDownAt = %v, want %v", name, got.TakenDownAt, at)
			}
		}
		// The owner can neither switch deploys nor delete the site.
		wantErr(t, f.r.Sites.SetActiveDeploy(t.Context(), store.ActorFor(o.ID), s.ID, next.ID), store.ErrNotFound)
		wantErr(t, f.r.Sites.Delete(t.Context(), store.ActorFor(o.ID), s.ID), store.ErrNotFound)
		got, err := f.r.Sites.GetByID(t.Context(), s.ID)
		wantErr(t, err, nil)
		if got.ActiveDeployID != d.ID {
			t.Fatalf("active deploy = %s, want %s", got.ActiveDeployID, d.ID)
		}

		f.must(f.r.Sites.Restore(t.Context(), s.ID))
		got, err = f.r.Sites.GetByID(t.Context(), s.ID)
		wantErr(t, err, nil)
		if !got.TakenDownAt.IsZero() {
			t.Fatalf("TakenDownAt after restore = %v", got.TakenDownAt)
		}
		f.must(f.r.Sites.SetActiveDeploy(t.Context(), store.ActorFor(o.ID), s.ID, next.ID))
	})
	t.Run("TakeDownUnknown", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		wantErr(t, f.r.Sites.TakeDown(t.Context(), store.NewID(), now), store.ErrNotFound)
		wantErr(t, f.r.Sites.Restore(t.Context(), store.NewID()), store.ErrNotFound)
		wantErr(t, f.r.Sites.TearDown(t.Context(), store.NewID()), store.ErrNotFound)
	})
	t.Run("TearDown", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.activated(o.ID, s.ID, now)
		// A site that is not taken down is never torn down, so a restore
		// racing the operator's command wins.
		wantErr(t, f.r.Sites.TearDown(t.Context(), s.ID), store.ErrNotFound)
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, now))
		f.must(f.r.Sites.TearDown(t.Context(), s.ID))
		_, err := f.r.Sites.GetByID(t.Context(), s.ID)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Deploys.Get(t.Context(), store.ActorFor(o.ID), s.ID, d.ID)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("UnavailableAndListAll", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, suspended := f.owner(), f.owner()
		open1, down := f.site(o.ID), f.site(o.ID)
		held := f.site(suspended.ID)
		f.must(f.r.Sites.TakeDown(t.Context(), down.ID, now))
		f.must(f.r.Owners.Suspend(t.Context(), suspended.ID, now))

		ids, err := f.r.Sites.UnavailableIDs(t.Context())
		wantErr(t, err, nil)
		want := []string{down.ID, held.ID}
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Fatalf("UnavailableIDs = %v, want %v", ids, want)
		}

		all, err := f.r.Sites.ListAll(t.Context(), "", 10)
		wantErr(t, err, nil)
		byID := map[string]store.OperatorSite{}
		for _, s := range all {
			byID[s.ID] = s
		}
		if len(all) != 3 || byID[open1.ID].OwnerEmail != o.Email || !byID[open1.ID].TakenDownAt.IsZero() ||
			byID[down.ID].TakenDownAt.IsZero() || byID[held.ID].OwnerSuspendedAt.IsZero() {
			t.Fatalf("ListAll = %+v", all)
		}
		page, err := f.r.Sites.ListAll(t.Context(), all[0].Name, 1)
		wantErr(t, err, nil)
		if len(page) != 1 || page[0].ID != all[1].ID {
			t.Fatalf("second page = %+v", page)
		}
	})
}

func gcOperatorContract(t *testing.T, open Opener) {
	t.Run("KeepsHeldSites", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, suspended := f.owner(), f.owner()
		down, held, free := f.site(o.ID), f.site(suspended.ID), f.site(o.ID)
		var replaced []store.Deploy
		for _, s := range []store.Site{down, held, free} {
			owner := o.ID
			if s.ID == held.ID {
				owner = suspended.ID
			}
			old := f.activated(owner, s.ID, now)
			f.activated(owner, s.ID, now.Add(time.Second))
			replaced = append(replaced, old)
		}
		f.must(f.r.Sites.TakeDown(t.Context(), down.ID, now))
		f.must(f.r.Owners.Suspend(t.Context(), suspended.ID, now))

		later := now.Add(time.Hour)
		got, err := f.r.GC.PrunableDeploys(t.Context(), 1, later, store.DeployCursor{}, 10)
		wantErr(t, err, nil)
		if len(got) != 1 || got[0].ID != replaced[2].ID {
			t.Fatalf("prunable = %+v, want only %s", got, replaced[2].ID)
		}
		for _, d := range replaced[:2] {
			deleted, err := f.r.GC.DeletePrunableDeploy(t.Context(), d.ID, later)
			wantErr(t, err, nil)
			if deleted {
				t.Fatalf("deleted held deploy %s", d.ID)
			}
		}
		// Restored, the site's history is pruned as usual.
		f.must(f.r.Sites.Restore(t.Context(), down.ID))
		deleted, err := f.r.GC.DeletePrunableDeploy(t.Context(), replaced[0].ID, later)
		wantErr(t, err, nil)
		if !deleted {
			t.Fatal("restored site's replaced deploy was kept")
		}
	})
}
