// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
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

// Suspending a team takes its sites away as suspending an owner does, within
// the watch's interval, and reinstating brings them back.
func TestSuspendedTeamSitesAreUnavailable(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	p.publish("docs")
	p.moveToTeam()
	team := p.owner.ID
	p.do(http.MethodGet, host, "/", "", nil).expect(t, http.StatusOK, "Home page", "")

	watchCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		(&unavailableWatch{log: p.log, sites: p.db.Sites(), domains: p.db.Domains(), notify: p.notifier}).run(watchCtx, 5*time.Millisecond)
	})
	t.Cleanup(func() { cancel(); wg.Wait() })
	eventually := func(what string, code int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for p.do(http.MethodGet, host, "/", "", nil).code != code {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// The watch reads its baseline first; suspending before that goes unseen.
	time.Sleep(20 * time.Millisecond)

	if err := p.db.Teams().Suspend(ctx, team, p.now()); err != nil {
		t.Fatal(err)
	}
	eventually("the suspended team's site to go", http.StatusForbidden)
	p.do(http.MethodGet, host, "/", "", nil).unavailable(t)
	if err := p.db.Teams().Reinstate(ctx, team); err != nil {
		t.Fatal(err)
	}
	eventually("the reinstated team's site to serve", http.StatusOK)
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
	refused := func(want string, args ...string) {
		t.Helper()
		out, err := admin(args...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("admin %v: %v\n%s\nwant an error with %q", args, err, out, want)
		}
	}
	gone := store.NewID()
	noTeam := fmt.Sprintf("no team %q on this server", gone)
	refused(noTeam, "team-quota", gone)
	refused(noTeam, "team-quota", gone, "--sites", "1")
	refused(noTeam, "suspend-team", gone)
	refused(noTeam, "reinstate-team", gone)
	refused(noTeam, "set-team-role", gone, "owner@example.test", "viewer")
	// An owner's ID names no team, and a role goes to someone who exists.
	notTeam := fmt.Sprintf("no team %q on this server", o.ID)
	refused(notTeam, "team-quota", o.ID)
	refused(notTeam, "suspend-team", o.ID)
	refused(notTeam, "reinstate-team", o.ID)
	refused(`no owner "nobody@example.test" on this server`, "set-team-role", tm.ID, "nobody@example.test", "viewer")
	refused("is not a plain email address", "set-team-role", tm.ID, store.TeamAddress(tm.ID), "viewer")

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
	must("create", "--email", "third@example.test")
	must("suspend-owner", "owner@example.test")
	if out := must("set-team-role", tm.ID, "second@example.test", "admin"); !strings.Contains(out, "admin of team") {
		t.Fatalf("set-team-role:\n%s", out)
	}
	refused(`"emperor" is not a role`, "set-team-role", tm.ID, "second@example.test", "emperor")
	refused("would have no working admin", "set-team-role", tm.ID, "second@example.test", "viewer")
	// A suspended owner can repair nothing, as a new member or an old one.
	must("suspend-owner", "third@example.test")
	refused("third@example.test is suspended", "set-team-role", tm.ID, "third@example.test", "admin")
	refused("owner@example.test is suspended", "set-team-role", tm.ID, "owner@example.test", "viewer")
	if ms, err := db.Teams().Memberships(t.Context(), func() string {
		third, err := db.Owners().GetByEmail(t.Context(), "third@example.test")
		if err != nil {
			t.Fatal(err)
		}
		return third.ID
	}()); err != nil || len(ms) != 0 {
		t.Fatalf("a suspended owner joined: %v, %v", ms, err)
	}
	must("reinstate-owner", "third@example.test")

	// Suspending a team, end to end.
	second, err := db.Owners().GetByEmail(t.Context(), "second@example.test")
	if err != nil {
		t.Fatal(err)
	}
	tok := store.APIToken{ID: store.NewID(), OwnerID: tm.ID, PublicID: "aaaaaaaaaa", TokenHash: "h", Name: "ci", ScopeDeploy: true, CreatedAt: time.Now()}
	if err := db.Teams().CreateToken(t.Context(), store.Actor{OwnerID: second.ID, Memberships: []store.Membership{{TeamID: tm.ID, Role: store.RoleAdmin}}}, tok); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	out = must("suspend-team", tm.ID)
	if !strings.Contains(out, "Suspended team Web team") || !strings.Contains(out, "reinstate-team "+tm.ID) {
		t.Fatalf("suspend-team:\n%s", out)
	}
	if strings.Contains(logs.String(), "Web team") || !strings.Contains(logs.String(), tm.ID) {
		t.Fatalf("logs name the team, not its ID:\n%s", logs.String())
	}
	if _, err := db.APITokens().GetByPublicID(t.Context(), tok.PublicID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a suspended team's token: %v", err)
	}
	if out := must("list-sites"); !strings.Contains(out, "team suspended") {
		t.Fatalf("list-sites:\n%s", out)
	}
	if out := must("list-teams"); !strings.Contains(out, "suspended "+day(time.Now())) {
		t.Fatalf("list-teams:\n%s", out)
	}
	if out := must("list-owners"); strings.Contains(out, tm.ID) {
		t.Fatalf("a suspended team in list-owners:\n%s", out)
	}
	if _, err := db.Owners().Get(t.Context(), second.ID); err != nil {
		t.Fatalf("a member's account: %v", err)
	}
	// Suspending twice is harmless, and a team is not an owner.
	must("suspend-team", tm.ID)
	refused("is not a plain email address", "reinstate-owner", tm.ID)
	if out := must("reinstate-team", tm.ID); !strings.Contains(out, "Reinstated team Web team") {
		t.Fatalf("reinstate-team:\n%s", out)
	}
	if _, err := db.APITokens().GetByPublicID(t.Context(), tok.PublicID); err != nil {
		t.Fatalf("token after reinstating: %v", err)
	}
	if out := must("list-sites"); strings.Contains(out, "suspended") {
		t.Fatalf("list-sites after reinstating:\n%s", out)
	}
	if _, err := admin("revoke-team-tokens", tm.ID); !errors.Is(err, errUsage) {
		t.Fatalf("revoke-team-tokens is gone: %v", err)
	}

	// A team with no members left is the one that most needs finding: its last
	// member's account went without the rules that keep an admin.
	raw, err := sql.Open("sqlite", filepath.Join(dir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(t.Context(), `DELETE FROM team_members WHERE team_id = ?`, tm.ID); err != nil {
		t.Fatal(err)
	}
	if out := must("list-teams"); !strings.Contains(out, "Web team") || !strings.Contains(out, tm.ID) {
		t.Fatalf("list-teams leaves out a team with no members:\n%s", out)
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
