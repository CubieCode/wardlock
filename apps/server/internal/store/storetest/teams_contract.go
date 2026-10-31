// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func wantQuota(t testing.TB, err error, want store.QuotaError) {
	t.Helper()
	var qe *store.QuotaError
	if !errors.As(err, &qe) || !errors.Is(err, store.ErrQuotaExceeded) || *qe != want {
		t.Fatalf("error = %v, want %+v", err, want)
	}
}

// team starts a team with o as its admin.
func (f *fixture) team(o store.Owner) store.Team {
	f.t.Helper()
	tm := store.Team{ID: store.NewID(), Name: fmt.Sprintf("Team %d", f.seq()), CreatedAt: now}
	f.must(f.r.Teams.Create(f.t.Context(), store.ActorFor(o.ID), tm, 0))
	f.emails[tm.ID] = tm.Name
	return tm
}

// member invites o to the team as role and accepts, as they would.
func (f *fixture) member(admin store.Owner, teamID string, o store.Owner, role store.Role) {
	f.t.Helper()
	inv := store.TeamInvitation{
		ID: store.NewID(), TeamID: teamID, Email: o.Email, Role: role, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	f.must(f.r.Teams.Invite(f.t.Context(), f.actor(admin), inv))
	_, err := f.r.Teams.Accept(f.t.Context(), o.ID, inv.ID, now)
	f.must(err)
}

// actor is o as a request would see them, with their memberships.
func (f *fixture) actor(o store.Owner) store.Actor {
	f.t.Helper()
	ms, err := f.r.Teams.Memberships(f.t.Context(), o.ID)
	f.must(err)
	return store.Actor{OwnerID: o.ID, Memberships: ms}
}

// teamSite creates a site under the team, made by admin.
func (f *fixture) teamSite(admin store.Owner, teamID string) store.Site {
	f.t.Helper()
	s := f.validSite(teamID)
	f.must(f.r.Sites.Create(f.t.Context(), f.actor(admin), s, store.Quotas{}))
	return s
}

func teamsContract(t *testing.T, open Opener) {
	t.Run("ActorRequire", func(t *testing.T) {
		t.Parallel()
		a := store.Actor{OwnerID: "me", Memberships: []store.Membership{
			{TeamID: "ta", Role: store.RoleAdmin}, {TeamID: "te", Role: store.RoleEditor}, {TeamID: "tv", Role: store.RoleViewer},
		}}
		tok := store.TeamTokenActor("te", "tk")
		for _, c := range []struct {
			actor   store.Actor
			account string
			need    store.Role
			want    error
		}{
			{a, "me", store.RoleAdmin, nil},
			{a, "ta", store.RoleAdmin, nil},
			{a, "te", store.RoleEditor, nil},
			{a, "te", store.RoleAdmin, store.ErrForbiddenRole},
			{a, "tv", store.RoleViewer, nil},
			{a, "tv", store.RoleEditor, store.ErrForbiddenRole},
			{a, "other", store.RoleViewer, store.ErrNotFound},
			{a, "", store.RoleViewer, store.ErrNotFound},
			{tok, "te", store.RoleEditor, nil},
			{tok, "te", store.RoleAdmin, store.ErrForbiddenRole},
			{tok, "ta", store.RoleViewer, store.ErrNotFound},
			{store.ActorFor("me"), "ta", store.RoleViewer, store.ErrNotFound},
		} {
			if err := c.actor.Require(c.account, c.need); !errors.Is(err, c.want) {
				t.Errorf("%+v Require(%q, %s) = %v, want %v", c.actor, c.account, c.need, err, c.want)
			}
		}
	})

	t.Run("CreateAndRead", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		tm := f.team(o)
		got, err := f.r.Teams.Get(t.Context(), f.actor(o), tm.ID)
		if err != nil || got.Name != tm.Name || got.Role != store.RoleAdmin {
			t.Fatalf("Get = %+v, %v", got, err)
		}
		_, err = f.r.Teams.Get(t.Context(), f.actor(other), tm.ID)
		wantErr(t, err, store.ErrNotFound)
		list, err := f.r.Teams.List(t.Context(), f.actor(other))
		if err != nil || len(list) != 0 {
			t.Fatalf("other's List = %v, %v", list, err)
		}
		list, err = f.r.Teams.List(t.Context(), store.TeamTokenActor(tm.ID, "t"))
		if err != nil || len(list) != 1 || list[0].Role != store.RoleEditor {
			t.Fatalf("token's List = %+v, %v", list, err)
		}
		wantErr(t, f.r.Teams.Create(t.Context(), store.TeamTokenActor(tm.ID, "t"),
			store.Team{ID: store.NewID(), Name: "x", CreatedAt: now}, 0), store.ErrForbiddenRole)
	})

	t.Run("RolesAndLastAdmin", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, editor, viewer := f.owner(), f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, editor, store.RoleEditor)
		f.member(admin, tm.ID, viewer, store.RoleViewer)
		a := f.actor(admin)
		// The only admin can neither be demoted nor removed, even by themselves.
		wantErr(t, f.r.Teams.SetRole(t.Context(), a, tm.ID, admin.ID, store.RoleEditor), store.ErrLastAdmin)
		wantErr(t, f.r.Teams.RemoveMember(t.Context(), a, tm.ID, admin.ID), store.ErrLastAdmin)
		// Keeping admin is no change.
		wantErr(t, f.r.Teams.SetRole(t.Context(), a, tm.ID, admin.ID, store.RoleAdmin), nil)
		// Others cannot administer.
		wantErr(t, f.r.Teams.SetRole(t.Context(), f.actor(editor), tm.ID, viewer.ID, store.RoleEditor), store.ErrForbiddenRole)
		wantErr(t, f.r.Teams.RemoveMember(t.Context(), f.actor(viewer), tm.ID, editor.ID), store.ErrForbiddenRole)
		wantErr(t, f.r.Teams.Rename(t.Context(), f.actor(editor), tm.ID, "x"), store.ErrForbiddenRole)
		wantErr(t, f.r.Teams.Delete(t.Context(), f.actor(editor), tm.ID), store.ErrForbiddenRole)
		wantErr(t, f.r.Teams.Rename(t.Context(), store.TeamTokenActor(tm.ID, "t"), tm.ID, "x"), store.ErrForbiddenRole)
		// With a second admin the first may step down or leave.
		wantErr(t, f.r.Teams.SetRole(t.Context(), a, tm.ID, editor.ID, store.RoleAdmin), nil)
		wantErr(t, f.r.Teams.SetRole(t.Context(), a, tm.ID, admin.ID, store.RoleViewer), nil)
		wantErr(t, f.r.Teams.RemoveMember(t.Context(), f.actor(admin), tm.ID, admin.ID), nil)
		// A viewer may leave.
		wantErr(t, f.r.Teams.RemoveMember(t.Context(), f.actor(viewer), tm.ID, viewer.ID), nil)
		ms, err := f.r.Teams.Memberships(t.Context(), viewer.ID)
		if err != nil || len(ms) != 0 {
			t.Fatalf("memberships after leaving = %v, %v", ms, err)
		}
		wantErr(t, f.r.Teams.SetRole(t.Context(), f.actor(editor), tm.ID, store.NewID(), store.RoleViewer), store.ErrNotFound)
	})

	t.Run("Invitations", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, invitee, other := f.owner(), f.owner(), f.owner()
		tm := f.team(admin)
		inv := store.TeamInvitation{
			ID: store.NewID(), TeamID: tm.ID, Email: invitee.Email, Role: store.RoleEditor, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}
		wantErr(t, f.r.Teams.Invite(t.Context(), f.actor(other), inv), store.ErrNotFound)
		wantErr(t, f.r.Teams.Invite(t.Context(), f.actor(admin), inv), nil)

		open, err := f.r.Teams.Invitations(t.Context(), f.actor(admin), tm.ID, now)
		if err != nil || len(open) != 1 || open[0].TeamName != tm.Name {
			t.Fatalf("Invitations = %+v, %v", open, err)
		}
		mine, err := f.r.Teams.InvitationsFor(t.Context(), invitee.ID, now)
		if err != nil || len(mine) != 1 {
			t.Fatalf("InvitationsFor = %+v, %v", mine, err)
		}
		if theirs, err := f.r.Teams.InvitationsFor(t.Context(), other.ID, now); err != nil || len(theirs) != 0 {
			t.Fatalf("other's InvitationsFor = %+v, %v", theirs, err)
		}
		if late, err := f.r.Teams.InvitationsFor(t.Context(), invitee.ID, now.Add(2*time.Hour)); err != nil || len(late) != 0 {
			t.Fatalf("expired InvitationsFor = %+v, %v", late, err)
		}

		// Only the invited address, however the ID was learned.
		_, err = f.r.Teams.Accept(t.Context(), other.ID, inv.ID, now)
		wantErr(t, err, store.ErrNotFound)
		wantErr(t, f.r.Teams.Decline(t.Context(), other.ID, inv.ID), store.ErrNotFound)
		_, err = f.r.Teams.Accept(t.Context(), invitee.ID, inv.ID, now.Add(2*time.Hour))
		wantErr(t, err, store.ErrNotFound)

		got, err := f.r.Teams.Accept(t.Context(), invitee.ID, inv.ID, now)
		if err != nil || got.Role != store.RoleEditor || got.ID != tm.ID {
			t.Fatalf("Accept = %+v, %v", got, err)
		}
		// Twice is harmless, and keeps a role changed since.
		wantErr(t, f.r.Teams.SetRole(t.Context(), f.actor(admin), tm.ID, invitee.ID, store.RoleViewer), nil)
		got, err = f.r.Teams.Accept(t.Context(), invitee.ID, inv.ID, now.Add(2*time.Hour))
		if err != nil || got.Role != store.RoleViewer {
			t.Fatalf("Accept twice = %+v, %v", got, err)
		}
		// Once removed, the spent invitation grants nothing.
		wantErr(t, f.r.Teams.RemoveMember(t.Context(), f.actor(admin), tm.ID, invitee.ID), nil)
		_, err = f.r.Teams.Accept(t.Context(), invitee.ID, inv.ID, now)
		wantErr(t, err, store.ErrNotFound)

		// A team token administers nothing, and declined is gone.
		again := inv
		again.ID = store.NewID()
		wantErr(t, f.r.Teams.Invite(t.Context(), store.TeamTokenActor(tm.ID, "t"), again), store.ErrForbiddenRole)
		wantErr(t, f.r.Teams.Invite(t.Context(), f.actor(admin), again), nil)
		wantErr(t, f.r.Teams.Decline(t.Context(), invitee.ID, again.ID), nil)
		_, err = f.r.Teams.Accept(t.Context(), invitee.ID, again.ID, now)
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("TeamTokens", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, second, editor := f.owner(), f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, second, store.RoleAdmin)
		f.member(admin, tm.ID, editor, store.RoleEditor)
		tok := store.APIToken{
			ID: store.NewID(), OwnerID: tm.ID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "ci", ScopeDeploy: true, CreatedAt: now,
		}
		wantErr(t, f.r.Teams.CreateToken(t.Context(), f.actor(editor), tok), store.ErrForbiddenRole)
		wantErr(t, f.r.Teams.CreateToken(t.Context(), f.actor(admin), tok), nil)
		// A team's token cannot be made as a personal one, nor the reverse.
		wantErr(t, f.r.APITokens.Create(t.Context(), store.APIToken{
			ID: store.NewID(), OwnerID: tm.ID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "x", ScopeDeploy: true, CreatedAt: now,
		}), store.ErrConstraint)
		wantErr(t, f.r.Teams.CreateToken(t.Context(), f.actor(admin), store.APIToken{
			ID: store.NewID(), OwnerID: admin.ID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "x", ScopeDeploy: true, CreatedAt: now,
		}), store.ErrConstraint)

		// It outlives the admin who made it and is found as a team's.
		wantErr(t, f.r.Teams.RemoveMember(t.Context(), f.actor(admin), tm.ID, admin.ID), nil)
		got, err := f.r.APITokens.GetByPublicID(t.Context(), tok.PublicID)
		if err != nil || !got.Team || got.OwnerID != tm.ID || got.CreatedBy != admin.ID {
			t.Fatalf("GetByPublicID = %+v, %v", got, err)
		}
		q := store.APITokenQuery{OwnerID: tm.ID, Now: now, Limit: 10}
		_, err = f.r.Teams.Tokens(t.Context(), f.actor(editor), q)
		wantErr(t, err, store.ErrForbiddenRole)
		list, err := f.r.Teams.Tokens(t.Context(), f.actor(second), q)
		if err != nil || len(list) != 1 {
			t.Fatalf("Tokens = %+v, %v", list, err)
		}
		wantErr(t, f.r.Teams.RevokeToken(t.Context(), f.actor(editor), tm.ID, tok.ID, now), store.ErrForbiddenRole)
		wantErr(t, f.r.Teams.RevokeToken(t.Context(), f.actor(second), tm.ID, tok.ID, now), nil)
		if list, _ := f.r.Teams.Tokens(t.Context(), f.actor(second), q); len(list) != 0 {
			t.Fatalf("Tokens after revoking = %+v", list)
		}
	})

	// A demoted or removed admin cannot come back by an invitation they set
	// up while they could.
	t.Run("InvitationsDoNotOutliveAuthority", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		boss, rogue, puppet := f.owner(), f.owner(), f.owner()
		tm := f.team(boss)
		f.member(boss, tm.ID, rogue, store.RoleAdmin)
		invite := func(by store.Owner, to string) store.TeamInvitation {
			return store.TeamInvitation{ID: store.NewID(), TeamID: tm.ID, Email: to, Role: store.RoleAdmin, InvitedBy: by.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		}
		// Not to a current member, themselves included.
		self := invite(rogue, rogue.Email)
		wantErr(t, f.r.Teams.Invite(ctx, f.actor(rogue), self), store.ErrAlreadyMember)

		// Sent before they went, to someone else: gone with them.
		sent := invite(rogue, puppet.Email)
		wantErr(t, f.r.Teams.Invite(ctx, f.actor(rogue), sent), nil)
		wantErr(t, f.r.Teams.SetRole(ctx, f.actor(boss), tm.ID, rogue.ID, store.RoleViewer), nil)
		_, err := f.r.Teams.Accept(ctx, puppet.ID, sent.ID, now)
		wantErr(t, err, store.ErrNotFound)

		wantErr(t, f.r.Teams.SetRole(ctx, f.actor(boss), tm.ID, rogue.ID, store.RoleAdmin), nil)
		again := invite(rogue, puppet.Email)
		wantErr(t, f.r.Teams.Invite(ctx, f.actor(rogue), again), nil)
		wantErr(t, f.r.Teams.RemoveMember(ctx, f.actor(boss), tm.ID, rogue.ID), nil)
		_, err = f.r.Teams.Accept(ctx, puppet.ID, again.ID, now)
		wantErr(t, err, store.ErrNotFound)

		// A former member is invited as anyone is, and joins as invited.
		f.member(boss, tm.ID, rogue, store.RoleViewer)
		if ms, _ := f.r.Teams.Memberships(ctx, rogue.ID); len(ms) != 1 || ms[0].Role != store.RoleViewer {
			t.Fatalf("memberships = %+v", ms)
		}
	})

	t.Run("LastWorkingAdmin", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		a, b, c := f.owner(), f.owner(), f.owner()
		tm := f.team(a)
		f.member(a, tm.ID, b, store.RoleAdmin)
		f.member(a, tm.ID, c, store.RoleAdmin)
		bActor, cActor := f.actor(b), f.actor(c)
		f.must(f.r.Owners.Suspend(ctx, a.ID, now))
		wantErr(t, f.r.Teams.RemoveMember(ctx, cActor, tm.ID, c.ID), nil)
		// A suspended admin keeps nothing running, so the last working one
		// stays; removing the suspended one is fine.
		wantErr(t, f.r.Teams.RemoveMember(ctx, bActor, tm.ID, b.ID), store.ErrLastAdmin)
		wantErr(t, f.r.Teams.SetRole(ctx, bActor, tm.ID, b.ID, store.RoleViewer), store.ErrLastAdmin)
		wantErr(t, f.r.Teams.RemoveMember(ctx, bActor, tm.ID, a.ID), nil)
	})

	t.Run("OperatorRepairs", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		a, b := f.owner(), f.owner()
		tm := f.team(a)
		tok := store.APIToken{ID: store.NewID(), OwnerID: tm.ID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "ci", ScopeDeploy: true, CreatedAt: now}
		f.must(f.r.Teams.CreateToken(ctx, f.actor(a), tok))
		n, err := f.r.Teams.RevokeAllTokens(ctx, tm.ID, now)
		if err != nil || n != 1 {
			t.Fatalf("RevokeAllTokens = %d, %v", n, err)
		}
		got, err := f.r.APITokens.GetByPublicID(ctx, tok.PublicID)
		f.must(err)
		if got.RevokedAt.IsZero() {
			t.Fatal("token not revoked")
		}
		_, err = f.r.Teams.RevokeAllTokens(ctx, a.ID, now)
		wantErr(t, err, store.ErrNotFound)

		f.must(f.r.Owners.Suspend(ctx, a.ID, now))
		wantErr(t, f.r.Teams.ForceRole(ctx, tm.ID, b.ID, store.RoleAdmin, now), nil)
		if role, ok := f.actor(b).Role(tm.ID); !ok || role != store.RoleAdmin {
			t.Fatalf("role = %q, %v", role, ok)
		}
		wantErr(t, f.r.Teams.ForceRole(ctx, tm.ID, b.ID, store.RoleViewer, now), store.ErrLastAdmin)
		wantErr(t, f.r.Teams.ForceRole(ctx, b.ID, a.ID, store.RoleViewer, now), store.ErrNotFound)
	})

	t.Run("DeleteOnlyWithoutSites", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin := f.owner()
		tm := f.team(admin)
		s := f.teamSite(admin, tm.ID)
		wantErr(t, f.r.Teams.Delete(t.Context(), f.actor(admin), tm.ID), store.ErrTeamHasSites)
		wantErr(t, f.r.Sites.Delete(t.Context(), f.actor(admin), s.ID), nil)
		wantErr(t, f.r.Teams.Delete(t.Context(), f.actor(admin), tm.ID), nil)
		_, err := f.r.Teams.Get(t.Context(), f.actor(admin), tm.ID)
		wantErr(t, err, store.ErrNotFound)
		if ms, _ := f.r.Teams.Memberships(t.Context(), admin.ID); len(ms) != 0 {
			t.Fatalf("memberships of a deleted team: %v", ms)
		}
	})

	// A team's row in owners is an account, never a principal.
	t.Run("TeamIsNeverAPrincipal", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		tm := f.team(o)
		address := store.TeamAddress(tm.ID)
		ctx := t.Context()

		_, err := f.r.Owners.Get(ctx, tm.ID)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Owners.GetByEmail(ctx, address)
		wantErr(t, err, store.ErrNotFound)
		all, err := f.r.Owners.ListAll(ctx, "", 100)
		if err != nil || slices.ContainsFunc(all, func(a store.Owner) bool { return a.ID == tm.ID }) {
			t.Fatalf("ListAll = %+v, %v", all, err)
		}
		if n, err := f.r.Owners.Count(ctx); err != nil || n != 1 {
			t.Fatalf("Count = %d, %v; want the one person", n, err)
		}
		wantErr(t, f.r.Owners.Suspend(ctx, tm.ID, now), store.ErrNotFound)
		wantErr(t, f.r.Owners.Reinstate(ctx, tm.ID), store.ErrNotFound)

		code := store.OwnerLoginCode{
			ID: store.NewID(), OwnerID: tm.ID, Purpose: store.LoginCodeEmail, CodeHash: f.hash(), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}
		wantErr(t, f.r.Owners.CreateLoginCode(ctx, code), store.ErrConstraint)
		wantErr(t, f.r.Owners.ReplaceLoginCode(ctx, code), store.ErrConstraint)
		_, err = f.r.Owners.LatestLoginCode(ctx, tm.ID, store.LoginCodeEmail)
		wantErr(t, err, store.ErrNotFound)

		wantErr(t, f.r.Sessions.CreateOwner(ctx, store.OwnerSession{
			ID: store.NewID(), OwnerID: tm.ID, TokenHash: f.hash(), CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		}), store.ErrNotFound)

		wantErr(t, f.r.Passkeys.Create(ctx, store.Passkey{
			ID: store.NewID(), OwnerID: tm.ID, CredentialID: store.NewID(), Credential: []byte("{}"), Name: "key", CreatedAt: now,
		}, 10), store.ErrPasskeyLimit)

		wantErr(t, f.r.APITokens.Create(ctx, store.APIToken{
			ID: store.NewID(), OwnerID: tm.ID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "x", ScopeDeploy: true, CreatedAt: now,
		}), store.ErrConstraint)

		login := store.CLILoginRequest{
			ID: store.NewID(), DeviceCodeHash: f.hash(), UserCode: "ABCD-EFGH", Status: store.CLILoginPending, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}
		f.must(f.r.APITokens.CreateCLILogin(ctx, login))
		wantErr(t, f.r.APITokens.DecideCLILogin(ctx, store.CLILoginDecision{
			UserCode: login.UserCode, Status: store.CLILoginApproved, OwnerID: tm.ID, ScopeDeploy: true, At: now,
		}), store.ErrNotFound)

		if _, err := f.r.Notifications.Recipient(ctx, tm.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Recipient(team) = %v", err)
		}
		if !store.IsTeamAddress(address) || store.IsTeamAddress(o.Email) {
			t.Fatalf("IsTeamAddress wrong for %q or %q", address, o.Email)
		}
	})

	t.Run("PinsArePersonal", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, viewer := f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, viewer, store.RoleViewer)
		s := f.teamSite(admin, tm.ID)
		_, err := f.r.Organisation.Organise(t.Context(), f.actor(viewer), tm.ID, []string{s.Name}, store.OrganiseChange{SetPinned: true, PinnedAt: now})
		f.must(err)
		mine, err := f.r.Sites.Get(t.Context(), f.actor(viewer), s.ID)
		f.must(err)
		theirs, err := f.r.Sites.Get(t.Context(), f.actor(admin), s.ID)
		f.must(err)
		if mine.PinnedAt.IsZero() || !theirs.PinnedAt.IsZero() {
			t.Fatalf("viewer's pin %v, admin's %v; want only the viewer's", mine.PinnedAt, theirs.PinnedAt)
		}
		// Anything but a pin needs an editor.
		g := store.Group{ID: store.NewID(), OwnerID: tm.ID, Name: "g", NameKey: "g", CreatedAt: now}
		f.must(f.r.Organisation.CreateGroup(t.Context(), f.actor(admin), g, 0))
		_, err = f.r.Organisation.Organise(t.Context(), f.actor(viewer), tm.ID, []string{s.Name}, store.OrganiseChange{SetGroup: true, GroupID: g.ID})
		wantErr(t, err, store.ErrForbiddenRole)
	})

	t.Run("GroupsAndTagsBelongToTheTeam", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, viewer, other := f.owner(), f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, viewer, store.RoleViewer)
		g := store.Group{ID: store.NewID(), OwnerID: tm.ID, Name: "g", NameKey: "g", CreatedAt: now}
		wantErr(t, f.r.Organisation.CreateGroup(t.Context(), f.actor(viewer), g, 0), store.ErrForbiddenRole)
		wantErr(t, f.r.Organisation.CreateGroup(t.Context(), f.actor(other), g, 0), store.ErrNotFound)
		f.must(f.r.Organisation.CreateGroup(t.Context(), f.actor(admin), g, 0))
		list, err := f.r.Organisation.ListGroups(t.Context(), f.actor(viewer), tm.ID)
		if err != nil || len(list) != 1 {
			t.Fatalf("viewer's ListGroups = %v, %v", list, err)
		}
		_, err = f.r.Organisation.ListGroups(t.Context(), f.actor(other), tm.ID)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Organisation.GetGroup(t.Context(), f.actor(other), g.ID)
		wantErr(t, err, store.ErrNotFound)
		wantErr(t, f.r.Organisation.DeleteGroup(t.Context(), f.actor(viewer), g.ID), store.ErrForbiddenRole)
		wantErr(t, f.r.Organisation.DeleteGroup(t.Context(), f.actor(other), g.ID), store.ErrNotFound)
		// A personal site cannot join a team's group.
		mine := f.site(admin.ID)
		_, err = f.r.Organisation.Organise(t.Context(), f.actor(admin), admin.ID, []string{mine.Name}, store.OrganiseChange{SetGroup: true, GroupID: g.ID})
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("CreatedBy", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, editor := f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, editor, store.RoleEditor)
		s := f.teamSite(admin, tm.ID)
		token := store.TeamTokenActor(tm.ID, "tok-1")
		ctx := t.Context()

		byEditor := f.validDeploy(s.ID)
		f.must(f.r.Deploys.Create(ctx, f.actor(editor), byEditor, nil, store.Quotas{}))
		byToken := f.validDeploy(s.ID)
		f.must(f.r.Deploys.Create(ctx, token, byToken, nil, store.Quotas{}))
		d, err := f.r.Deploys.Get(ctx, f.actor(admin), s.ID, byEditor.ID)
		f.must(err)
		if d.CreatedBy != editor.ID || d.CreatedByToken != "" {
			t.Fatalf("deploy by editor: %q, %q", d.CreatedBy, d.CreatedByToken)
		}
		d, err = f.r.Deploys.Get(ctx, f.actor(admin), s.ID, byToken.ID)
		f.must(err)
		if d.CreatedBy != tm.ID || d.CreatedByToken != "tok-1" {
			t.Fatalf("deploy by token: %q, %q", d.CreatedBy, d.CreatedByToken)
		}

		// A caller cannot claim to be someone else, and a member who leaves
		// stays named.
		spoof := f.validLink(s.ID)
		spoof.CreatedBy = admin.ID
		f.must(f.r.Links.Create(ctx, f.actor(editor), spoof))
		l, err := f.r.Links.GetByPublicID(ctx, spoof.PublicID)
		f.must(err)
		wantErr(t, f.r.Teams.RemoveMember(ctx, f.actor(admin), tm.ID, editor.ID), nil)
		if l.CreatedBy != editor.ID {
			t.Fatalf("link created by %q, want the editor", l.CreatedBy)
		}

		// A policy records whoever last changed it.
		p := f.validPolicy(s.ID)
		p.PathPrefix = "/docs/"
		got, err := f.r.Policies.Set(ctx, f.actor(admin), p)
		f.must(err)
		if got.CreatedBy != admin.ID {
			t.Fatalf("policy set by %q", got.CreatedBy)
		}
		p.Visibility, p.LinksEnabled, p.UpdatedAt = store.Public, false, now.Add(time.Minute)
		got, err = f.r.Policies.Set(ctx, token, p)
		f.must(err)
		if got.CreatedBy != tm.ID || got.CreatedByToken != "tok-1" {
			t.Fatalf("policy changed by %q, %q", got.CreatedBy, got.CreatedByToken)
		}
	})

	t.Run("TeamQuotasAreTheTeams", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, editor := f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, editor, store.RoleEditor)
		s := f.teamSite(admin, tm.ID)
		q := store.Quotas{DeploysPerDay: 1, Sites: 1, StorageBytes: 10}
		ctx := t.Context()
		d := f.validDeploy(s.ID)
		f.must(f.r.Deploys.Create(ctx, f.actor(editor), d, []store.DeployFile{{Path: "a", SHA256: sha('a'), Size: 5}}, q))
		_, usage, err := f.r.Owners.Quotas(ctx, tm.ID, now)
		f.must(err)
		if usage.StoredBytes != 5 || usage.DeploysToday != 1 || usage.Sites != 1 {
			t.Fatalf("team usage = %+v", usage)
		}
		for _, o := range []store.Owner{admin, editor} {
			if _, usage, err := f.r.Owners.Quotas(ctx, o.ID, now); err != nil || usage != (store.OwnerUsage{}) {
				t.Fatalf("member's usage = %+v, %v; want none", usage, err)
			}
		}
		// The team's limit refuses; the deploying member's own room is no help.
		wantQuota(t, f.r.Deploys.Create(ctx, f.actor(editor), f.validDeploy(s.ID), nil, q),
			store.QuotaError{Quota: store.QuotaDeploysPerDay, Limit: 1, Used: 1, Adding: 1})
		wantQuota(t, f.r.Sites.Create(ctx, f.actor(editor), f.validSite(tm.ID), q),
			store.QuotaError{Quota: store.QuotaSites, Limit: 1, Used: 1, Adding: 1})
		// An override applies to the team alone.
		more := int64(2)
		f.must(f.r.Owners.SetQuotas(ctx, tm.ID, store.QuotaOverrides{DeploysPerDay: &more}))
		wantErr(t, f.r.Deploys.Create(ctx, f.actor(editor), f.validDeploy(s.ID), nil, q), nil)
		// The member's personal quota is untouched by all this.
		wantErr(t, f.r.Sites.Create(ctx, f.actor(editor), f.validSite(editor.ID), q), nil)
	})

	t.Run("TeamsPerOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		create := func(limit int64) error {
			return f.r.Teams.Create(t.Context(), store.ActorFor(o.ID), store.Team{ID: store.NewID(), Name: "t", CreatedAt: now}, limit)
		}
		wantErr(t, create(1), nil)
		wantQuota(t, create(1), store.QuotaError{Quota: store.QuotaTeams, Limit: 1, Used: 1, Adding: 1})
		wantErr(t, create(0), nil) // unlimited
		three := int64(3)
		f.must(f.r.Teams.SetTeamQuota(t.Context(), o.ID, &three))
		wantErr(t, create(1), nil) // the override wins
		wantQuota(t, create(1), store.QuotaError{Quota: store.QuotaTeams, Limit: 3, Used: 3, Adding: 1})
		got, err := f.r.Teams.TeamQuota(t.Context(), o.ID)
		if err != nil || got == nil || *got != 3 {
			t.Fatalf("TeamQuota = %v, %v", got, err)
		}
		tm := f.team(f.owner())
		wantErr(t, f.r.Teams.SetTeamQuota(t.Context(), tm.ID, &three), store.ErrNotFound)
	})

	t.Run("OperatorReads", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, viewer := f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, viewer, store.RoleViewer)
		teams, err := f.r.Teams.ListAll(t.Context(), "", 10)
		if err != nil || len(teams) != 1 {
			t.Fatalf("ListAll = %+v, %v", teams, err)
		}
		members, err := f.r.Teams.AllMembers(t.Context(), tm.ID)
		if err != nil || len(members) != 2 {
			t.Fatalf("AllMembers = %+v, %v", members, err)
		}
		f.must(f.r.Owners.Suspend(t.Context(), viewer.ID, now))
		members, err = f.r.Teams.AllMembers(t.Context(), tm.ID)
		if err != nil || !slices.ContainsFunc(members, func(m store.TeamMember) bool { return m.OwnerID == viewer.ID && !m.SuspendedAt.IsZero() }) {
			t.Fatalf("a suspended member is listed as such: %+v, %v", members, err)
		}
	})

	// A team has no mailbox: its emails go to its admins who can read them,
	// each as a row of their own. Webhook rows are never expanded.
	t.Run("NotificationsGoToAdmins", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		admin, second, gone, viewer := f.owner(), f.owner(), f.owner(), f.owner()
		tm := f.team(admin)
		f.member(admin, tm.ID, second, store.RoleAdmin)
		f.member(admin, tm.ID, gone, store.RoleAdmin)
		f.member(admin, tm.ID, viewer, store.RoleViewer)
		f.must(f.r.Owners.Suspend(ctx, gone.ID, now))
		entry := func(channel store.NotificationChannel, owner, key string) store.OutboxEntry {
			return store.OutboxEntry{
				ID: store.NewID(), Channel: channel, EventKey: key, OwnerID: owner, Event: "e", Payload: "{}", CreatedAt: now,
			}
		}
		added, err := f.r.Notifications.Enqueue(ctx, entry(store.NotificationEmail, tm.ID, "k"), entry(store.NotificationEmail, viewer.ID, "own"))
		f.must(err)
		if added != 3 {
			t.Fatalf("added %d rows, want one per readable admin and the person's own", added)
		}
		// Reading the same event again adds nothing.
		added, err = f.r.Notifications.Enqueue(ctx, entry(store.NotificationEmail, tm.ID, "k"))
		f.must(err)
		if added != 0 {
			t.Fatalf("queued the same event again: %d", added)
		}
		due, err := f.r.Notifications.Due(ctx, store.NotificationEmail, now, 10)
		f.must(err)
		to := []string{}
		for _, e := range due {
			to = append(to, e.OwnerID)
		}
		want := []string{admin.ID, second.ID, viewer.ID}
		slices.Sort(to)
		slices.Sort(want)
		if !slices.Equal(to, want) {
			t.Fatalf("rows for %v, want %v", to, want)
		}
		web := entry(store.NotificationWebhook, admin.ID, "hook")
		added, err = f.r.Notifications.Enqueue(ctx, web)
		f.must(err)
		if added != 1 {
			t.Fatalf("webhook row added %d", added)
		}
	})

	t.Run("ListAcrossAccounts", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		admin, other := f.owner(), f.owner()
		tm, theirs := f.team(admin), f.team(other)
		mine := f.site(admin.ID)
		shared := f.teamSite(admin, tm.ID)
		f.teamSite(other, theirs.ID)
		f.site(other.ID)
		names := func(s []store.Site) []string {
			out := make([]string, len(s))
			for i := range s {
				out[i] = s[i].Name
			}
			slices.Sort(out)
			return out
		}
		all, err := f.r.Sites.List(t.Context(), f.actor(admin), "", "", 10)
		f.must(err)
		want := []string{mine.Name, shared.Name}
		slices.Sort(want)
		if !slices.Equal(names(all), want) {
			t.Fatalf("List = %v, want %v", names(all), want)
		}
		one, err := f.r.Sites.List(t.Context(), f.actor(admin), tm.ID, "", 10)
		f.must(err)
		if len(one) != 1 || one[0].OwnerKind != store.AccountTeam || one[0].OwnerName != tm.Name {
			t.Fatalf("team's List = %+v", one)
		}
		_, err = f.r.Sites.List(t.Context(), f.actor(admin), theirs.ID, "", 10)
		wantErr(t, err, store.ErrNotFound)
	})
}

func transferContract(t *testing.T, open Opener) {
	setup := func(t *testing.T) (*fixture, store.Owner, store.Team, store.Site) {
		f := newFixture(t, open)
		o := f.owner()
		tm := f.team(o)
		return f, o, tm, f.site(o.ID)
	}
	file := func(c byte, size int64) store.DeployFile {
		return store.DeployFile{Path: string(c) + ".html", SHA256: sha(c), Size: size}
	}

	t.Run("OwnerToTeamAndBack", func(t *testing.T) {
		t.Parallel()
		f, o, tm, s := setup(t)
		ctx := t.Context()
		d := f.deployFiles(o.ID, s.ID, file('a', 5), file('b', 7))
		f.uploadAll(o.ID, s.ID, d.ID)
		l := f.link(o.ID, s.ID)
		dom := f.validDomain(s.ID)
		f.must(f.r.Domains.Create(ctx, store.ActorFor(o.ID), dom))
		g := store.Group{ID: store.NewID(), OwnerID: o.ID, Name: "g", NameKey: "g", CreatedAt: now}
		f.must(f.r.Organisation.CreateGroup(ctx, f.actor(o), g, 0))
		tag, err := f.r.Organisation.CreateTag(ctx, f.actor(o), store.Tag{ID: store.NewID(), OwnerID: o.ID, Name: "t", CreatedAt: now}, 0)
		f.must(err)
		_, err = f.r.Organisation.Organise(ctx, f.actor(o), o.ID, []string{s.Name}, store.OrganiseChange{
			SetGroup: true, GroupID: g.ID, SetTags: true, TagIDs: []string{tag.ID}, SetPinned: true, PinnedAt: now,
		})
		f.must(err)

		f.must(f.r.Sites.Transfer(ctx, f.actor(o), s.ID, tm.ID, store.Quotas{}, now))

		got, err := f.r.Sites.Get(ctx, f.actor(o), s.ID)
		f.must(err)
		if got.OwnerID != tm.ID || got.OwnerKind != store.AccountTeam || got.OwnerName != tm.Name {
			t.Fatalf("after moving: %+v", got)
		}
		if got.GroupID != "" {
			t.Fatalf("group kept: %q", got.GroupID)
		}
		if got.PinnedAt.IsZero() {
			t.Fatal("the owner's pin was lost")
		}
		tags, err := f.r.Sites.TagIDs(ctx, f.actor(o), s.ID)
		f.must(err)
		if len(tags) != 0 {
			t.Fatalf("tags kept: %v", tags)
		}
		// What the site held is still there.
		if _, err := f.r.Deploys.Get(ctx, f.actor(o), s.ID, d.ID); err != nil {
			t.Fatalf("deploy lost: %v", err)
		}
		if _, err := f.r.Links.Get(ctx, s.ID, l.ID); err != nil {
			t.Fatalf("link lost: %v", err)
		}
		if _, err := f.r.Domains.Get(ctx, f.actor(o), s.ID, dom.Hostname); err != nil {
			t.Fatalf("domain lost: %v", err)
		}
		// The team holds the files; the account's usage moved.
		if size, err := f.r.Deploys.HeldSize(ctx, f.actor(o), s.ID, d.ID, sha('a')); err != nil || size != 5 {
			t.Fatalf("HeldSize after moving = %d, %v", size, err)
		}
		_, team, err := f.r.Owners.Quotas(ctx, tm.ID, now)
		f.must(err)
		_, mine, err := f.r.Owners.Quotas(ctx, o.ID, now)
		f.must(err)
		if team.StoredBytes != 12 || team.Sites != 1 || mine.StoredBytes != 0 || mine.Sites != 0 {
			t.Fatalf("team %+v, owner %+v", team, mine)
		}
		// Bytes the deploy log already counted stay with whoever made them.
		if mine.DeploysToday != 1 || team.DeploysToday != 0 {
			t.Fatalf("deploy log moved: team %+v, owner %+v", team, mine)
		}
		// And back.
		f.must(f.r.Sites.Transfer(ctx, f.actor(o), s.ID, o.ID, store.Quotas{}, now))
		got, err = f.r.Sites.Get(ctx, f.actor(o), s.ID)
		f.must(err)
		if got.OwnerID != o.ID || got.OwnerKind != store.AccountOwner {
			t.Fatalf("back: %+v", got)
		}
		_, team, err = f.r.Owners.Quotas(ctx, tm.ID, now)
		f.must(err)
		if team.StoredBytes != 0 {
			t.Fatalf("team still holds %d bytes", team.StoredBytes)
		}
	})

	t.Run("HoldingsAreOnlyWhatTheSourceHeld", func(t *testing.T) {
		t.Parallel()
		f, o, tm, s := setup(t)
		ctx := t.Context()
		// Listed but never uploaded: another owner's hash, guessed.
		d := f.deployFiles(o.ID, s.ID, file('c', 3))
		f.must(f.r.Sites.Transfer(ctx, f.actor(o), s.ID, tm.ID, store.Quotas{}, now))
		_, err := f.r.Deploys.HeldSize(ctx, f.actor(o), s.ID, d.ID, sha('c'))
		wantErr(t, err, store.ErrNotFound)
		missing, err := f.r.Deploys.MissingBlobs(ctx, f.actor(o), s.ID, d.ID)
		f.must(err)
		if len(missing) != 1 {
			t.Fatalf("MissingBlobs = %v", missing)
		}
	})

	t.Run("WhoMayMove", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		admin, other, editor, viewer := f.owner(), f.owner(), f.owner(), f.owner()
		tm, theirs := f.team(admin), f.team(other)
		f.member(admin, tm.ID, editor, store.RoleEditor)
		f.member(admin, tm.ID, viewer, store.RoleViewer)
		mine, teamSite := f.site(admin.ID), f.teamSite(admin, tm.ID)
		token := store.TeamTokenActor(tm.ID, "t")

		for _, c := range []struct {
			name  string
			actor store.Actor
			site  string
			to    string
			want  error
		}{
			{"a stranger cannot see the source", f.actor(other), mine.ID, theirs.ID, store.ErrNotFound},
			{"nor a team's", f.actor(other), teamSite.ID, theirs.ID, store.ErrNotFound},
			{"an editor is not admin of the source", f.actor(editor), teamSite.ID, editor.ID, store.ErrForbiddenRole},
			{"a viewer neither", f.actor(viewer), teamSite.ID, viewer.ID, store.ErrForbiddenRole},
			{"a team token administers nothing", token, teamSite.ID, tm.ID, store.ErrForbiddenRole},
			{"nor to a team they do not administer", f.actor(admin), mine.ID, theirs.ID, store.ErrNotFound},
			{"never to another person", f.actor(admin), teamSite.ID, other.ID, store.ErrNotFound},
			{"an unknown target", f.actor(admin), mine.ID, store.NewID(), store.ErrNotFound},
			{"an unknown site", f.actor(admin), store.NewID(), tm.ID, store.ErrNotFound},
		} {
			t.Run(c.name, func(t *testing.T) {
				wantErr(t, f.r.Sites.Transfer(ctx, c.actor, c.site, c.to, store.Quotas{}, now), c.want)
			})
		}
		// An editor of the target is not enough.
		f.member(other, theirs.ID, admin, store.RoleEditor)
		wantErr(t, f.r.Sites.Transfer(ctx, f.actor(admin), mine.ID, theirs.ID, store.Quotas{}, now), store.ErrForbiddenRole)
		// Its own account changes nothing.
		wantErr(t, f.r.Sites.Transfer(ctx, f.actor(admin), mine.ID, admin.ID, store.Quotas{}, now), nil)
	})

	t.Run("TeamToTeam", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		admin, other := f.owner(), f.owner()
		a, b := f.team(admin), f.team(other)
		f.member(other, b.ID, admin, store.RoleAdmin)
		s := f.teamSite(admin, a.ID)
		f.must(f.r.Sites.Transfer(ctx, f.actor(admin), s.ID, b.ID, store.Quotas{}, now))
		got, err := f.r.Sites.Get(ctx, f.actor(admin), s.ID)
		f.must(err)
		if got.OwnerID != b.ID {
			t.Fatalf("site in %s, want %s", got.OwnerID, b.ID)
		}
	})

	t.Run("RefusedWhileTakenDown", func(t *testing.T) {
		t.Parallel()
		f, o, tm, s := setup(t)
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, now))
		wantErr(t, f.r.Sites.Transfer(t.Context(), f.actor(o), s.ID, tm.ID, store.Quotas{}, now), store.ErrSiteTakenDown)
		got, err := f.r.Sites.Get(t.Context(), f.actor(o), s.ID)
		f.must(err)
		if got.OwnerID != o.ID {
			t.Fatalf("taken-down site moved to %s", got.OwnerID)
		}
	})

	t.Run("TargetQuotas", func(t *testing.T) {
		t.Parallel()
		f, o, tm, s := setup(t)
		ctx := t.Context()
		f.deployFiles(o.ID, s.ID, file('a', 8))
		other := f.teamSite(o, tm.ID)
		wantQuota(t, f.r.Sites.Transfer(ctx, f.actor(o), s.ID, tm.ID, store.Quotas{Sites: 1}, now),
			store.QuotaError{Quota: store.QuotaSites, Limit: 1, Used: 1, Adding: 1})
		f.must(f.r.Sites.Delete(ctx, f.actor(o), other.ID))
		var qe *store.QuotaError
		err := f.r.Sites.Transfer(ctx, f.actor(o), s.ID, tm.ID, store.Quotas{StorageBytes: 5}, now)
		if !errors.As(err, &qe) || qe.Quota != store.QuotaStorage {
			t.Fatalf("Transfer past storage = %v", err)
		}
		// Nothing moved, on either side.
		got, err := f.r.Sites.Get(ctx, f.actor(o), s.ID)
		f.must(err)
		_, mine, err2 := f.r.Owners.Quotas(ctx, o.ID, now)
		f.must(err2)
		_, team, err3 := f.r.Owners.Quotas(ctx, tm.ID, now)
		f.must(err3)
		if got.OwnerID != o.ID || mine.StoredBytes != 8 || team.StoredBytes != 0 {
			t.Fatalf("after refusal: site in %s, owner %+v, team %+v", got.OwnerID, mine, team)
		}
		f.must(f.r.Sites.Transfer(ctx, f.actor(o), s.ID, tm.ID, store.Quotas{StorageBytes: 8}, now))
	})
}
