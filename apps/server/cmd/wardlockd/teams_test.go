// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// moveToTeam moves every published site to a new team, and has p act as that
// team from here on, as a team token does.
func (p *policyEdge) moveToTeam() {
	p.t.Helper()
	ctx := p.t.Context()
	team := store.Team{ID: store.NewID(), Name: "Web team", CreatedAt: p.now()}
	if err := p.db.Teams().Create(ctx, store.ActorFor(p.owner.ID), team, 0); err != nil {
		p.t.Fatal(err)
	}
	actor := store.Actor{OwnerID: p.owner.ID, Memberships: []store.Membership{{TeamID: team.ID, Role: store.RoleAdmin}}}
	all, err := p.db.Sites().ListAll(ctx, "", 100)
	if err != nil {
		p.t.Fatal(err)
	}
	for _, s := range all {
		if err := p.db.Sites().Transfer(ctx, actor, s.ID, team.ID, store.Quotas{}, p.now()); err != nil {
			p.t.Fatal(err)
		}
	}
	p.owner = store.Owner{ID: team.ID}
}

// A team's site belongs to no one person: suspending a member ends their
// access and leaves it serving, unlike a suspended owner's own site.
func TestSuspendedMemberLeavesTeamSiteServing(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	p.publish("docs")
	member := p.owner
	p.moveToTeam()
	p.do(http.MethodGet, host, "/", "", nil).expect(t, http.StatusOK, "Home page", "")
	if err := p.db.Owners().Suspend(ctx, member.ID, p.now()); err != nil {
		t.Fatal(err)
	}
	ids, err := p.db.Sites().UnavailableIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("unavailable sites: %v", ids)
	}
	p.notifier.Notify(ctx, p.siteID("docs"))
	p.do(http.MethodGet, host, "/", "", nil).expect(t, http.StatusOK, "Home page", "")
	// Taking it down works as for any site.
	if err := p.db.Sites().TakeDown(ctx, p.siteID("docs"), p.now()); err != nil {
		t.Fatal(err)
	}
	p.notifier.Notify(ctx, p.siteID("docs"))
	p.do(http.MethodGet, host, "/", "", nil).unavailable(t)
}

func TestAdminTeams(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"WARDLOCK_DATA_DIR": dir, "WARDLOCK_SECRET": testSecret,
		"WARDLOCK_MAX_OWNER_SITES": "5", "WARDLOCK_QUOTA_TEAMS": "2",
	}
	getenv := func(k string) string { return env[k] }
	var logs bytes.Buffer
	admin := func(args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		err := run(t.Context(), append([]string{"admin"}, args...), getenv, &out, &logs)
		return out.String(), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := admin(args...)
		if err != nil {
			t.Fatalf("admin %v: %v\n%s", args, err, logs.String())
		}
		return out
	}
	must("create", "--email", "owner@example.test")
	db, err := store.Open(t.Context(), filepath.Join(dir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	o, err := db.Owners().GetByEmail(t.Context(), "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	tm := store.Team{ID: store.NewID(), Name: "Web team", CreatedAt: time.Now()}
	if err := db.Teams().Create(t.Context(), store.ActorFor(o.ID), tm, 0); err != nil {
		t.Fatal(err)
	}
	actor := store.Actor{OwnerID: o.ID, Memberships: []store.Membership{{TeamID: tm.ID, Role: store.RoleAdmin}}}
	if err := db.Sites().Create(t.Context(), actor, store.Site{ID: store.NewID(), OwnerID: tm.ID, Name: "shared", CreatedAt: time.Now()}, store.Quotas{}); err != nil {
		t.Fatal(err)
	}

	if out := must("list-teams"); !strings.Contains(out, "Web team") || !strings.Contains(out, "owner@example.test") || !strings.Contains(out, "admin") {
		t.Fatalf("list-teams:\n%s", out)
	}
	if out := must("list-sites"); !strings.Contains(out, "team Web team") || strings.Contains(out, ".invalid") {
		t.Fatalf("list-sites:\n%s", out)
	}
	if out := must("list-owners"); strings.Contains(out, ".invalid") || strings.Contains(out, tm.ID) {
		t.Fatalf("a team in list-owners:\n%s", out)
	}

	out := must("team-quota", tm.ID)
	if !strings.Contains(out, "Web team") || !strings.Contains(out, "instance") {
		t.Fatalf("team-quota:\n%s", out)
	}
	out = must("team-quota", tm.ID, "--sites", "9")
	if !strings.Contains(out, "this team") {
		t.Fatalf("team-quota --sites:\n%s", out)
	}
	over, _, err := db.Owners().Quotas(t.Context(), tm.ID, time.Now())
	if err != nil || over.Sites == nil || *over.Sites != 9 {
		t.Fatalf("override = %+v, %v", over, err)
	}
	if _, err := admin("team-quota", store.NewID()); err == nil {
		t.Fatal("team-quota of a missing team worked")
	}

	out = must("quota", "owner@example.test")
	if !strings.Contains(out, "teams") {
		t.Fatalf("quota has no teams row:\n%s", out)
	}
	must("quota", "owner@example.test", "--teams", "1")
	if max, err := db.Teams().TeamQuota(t.Context(), o.ID); err != nil || max == nil || *max != 1 {
		t.Fatalf("team quota = %v, %v", max, err)
	}
	must("quota", "owner@example.test", "--teams", "default")
	if max, err := db.Teams().TeamQuota(t.Context(), o.ID); err != nil || max != nil {
		t.Fatalf("team quota = %v, %v", max, err)
	}

	// Repairing a team, once its only admin is suspended.
	must("create", "--email", "second@example.test")
	must("suspend-owner", "owner@example.test")
	if out := must("set-team-role", tm.ID, "second@example.test", "admin"); !strings.Contains(out, "admin of team") {
		t.Fatalf("set-team-role:\n%s", out)
	}
	if _, err := admin("set-team-role", tm.ID, "second@example.test", "emperor"); err == nil {
		t.Fatal("an unknown role worked")
	}
	if _, err := admin("set-team-role", tm.ID, "second@example.test", "viewer"); err == nil {
		t.Fatal("the last working admin was demoted")
	}
	if out := must("revoke-team-tokens", tm.ID); !strings.Contains(out, "Revoked 0 tokens") {
		t.Fatalf("revoke-team-tokens:\n%s", out)
	}
	if _, err := admin("revoke-team-tokens", store.NewID()); err == nil {
		t.Fatal("revoking a missing team's tokens worked")
	}

	// A team is never treated as a person, by its address or its ID.
	for _, args := range [][]string{
		{"suspend-owner", store.TeamAddress(tm.ID)},
		{"quota", store.TeamAddress(tm.ID)},
		{"reinstate-owner", store.TeamAddress(tm.ID)},
		{"moderation", "disable", "--owner", store.TeamAddress(tm.ID)},
	} {
		if out, err := admin(args...); err == nil {
			t.Errorf("admin %v worked:\n%s", args, out)
		}
	}
}
