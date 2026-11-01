// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

const (
	admin  = apiclient.TeamRoleAdmin
	editor = apiclient.TeamRoleEditor
	viewer = apiclient.TeamRoleViewer
)

// wantFailure runs args and checks the exit status and that stderr's message
// has want in it, with nothing on stdout.
func wantFailure(t *testing.T, a *app, status int, want string, args ...string) {
	t.Helper()
	var msg strings.Builder
	got := exitCode(&msg, run(t.Context(), a, args))
	if got != status || !strings.Contains(msg.String(), want) {
		t.Fatalf("%v: exit %d, %q; want exit %d with %q", args, got, msg.String(), status, want)
	}
	if out := a.stdout.(interface{ Len() int }); out.Len() != 0 {
		t.Fatalf("%v: wrote to stdout on failure: %v", args, a.stdout)
	}
}

func TestTeamList(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"team", "list"}); err != nil || !strings.Contains(out.String(), "You are in no teams.") {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "team", "list"}); err != nil {
		t.Fatal(err)
	}
	if m := assertJSONKeys(t, out.Bytes(), "teams"); string(m["teams"]) == "null" {
		t.Fatalf("no teams must be [], not null:\n%s", out)
	}

	web := srv.addTeam("Web team", admin)
	srv.addTeam("Ops", viewer)
	out.Reset()
	if err := run(t.Context(), a, []string{"team", "list"}); err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !regexpLine(text, `TEAM\s+ROLE\s+ID`) || !regexpLine(text, `Web team\s+admin\s+`+web) || !regexpLine(text, `Ops\s+viewer\s+\S+`) {
		t.Fatalf("output:\n%s", text)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"team", "list", "--json"}); err != nil {
		t.Fatal(err)
	}
	var res teamListResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Teams) != 2 {
		t.Fatalf("%v\n%s", err, out)
	}
	assertJSONKeys(t, mustJSON(t, res.Teams[0]), "id", "name", "role", "createdAt")

	// A team token sees its own team, as an editor.
	srv.tokenTeam = web
	out.Reset()
	if err := run(t.Context(), a, []string{"team", "list", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Teams) != 1 || res.Teams[0].Id != web || res.Teams[0].Role != editor {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestTeamMembers(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", viewer)
	srv.members[web] = []apiclient.TeamMember{
		{OwnerId: srv.id(), Email: "ana@example.test", Role: admin, CreatedAt: 1_800_000_000_000},
		{OwnerId: srv.id(), Email: "ben@example.test", Role: editor, CreatedAt: 1_800_000_000_000},
		{OwnerId: srv.id(), Email: "cat@example.test", Role: viewer, CreatedAt: 1_800_000_000_000, Suspended: true},
	}
	a, out, _ := ciApp(t, srv)
	// All three pages' worth, by name in any case.
	if err := run(t.Context(), a, []string{"team", "members", "web TEAM"}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !regexpLine(text, `MEMBER\s+ROLE\s+STATUS\s+JOINED`) || !regexpLine(text, `ana@example.test\s+admin\s+active\s+.+`) || !regexpLine(text, `cat@example.test\s+viewer\s+suspended\s+.+`) {
		t.Fatalf("output:\n%s", text)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"team", "members", web, "--json"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "team", "members")
	var res teamMembersResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Team.Id != web || len(res.Members) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
	assertJSONKeys(t, mustJSON(t, res.Members[0]), "ownerId", "email", "role", "createdAt", "suspended")

	// A team token lists its own team's members.
	srv.tokenTeam = web
	out.Reset()
	if err := run(t.Context(), a, []string{"team", "members", "Web team"}); err != nil || !strings.Contains(out.String(), "ben@example.test") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestFindTeamNames(t *testing.T) {
	srv := newFakeAPI(t)
	first := srv.addTeam("Design", admin)
	second := srv.addTeam("Design", viewer)
	third := srv.addTeam("design", viewer)
	srv.addTeam("Ops", viewer)
	a, out, _ := ciApp(t, srv)

	// Team names are not unique: the IDs choose.
	wantFailure(t, a, 1, first+" or "+second, "team", "members", "Design")
	wantFailure(t, a, 1, `no team named "Nope"`, "team", "members", "Nope")
	// An exact name beats one differing in case.
	if err := run(t.Context(), a, []string{"team", "members", "design", "--json"}); err != nil || !strings.Contains(out.String(), third) {
		t.Fatalf("%v\n%s", err, out)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"team", "members", second, "--json"}); err != nil || !strings.Contains(out.String(), second) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestTeamUsageErrors(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", admin)
	srv.addTeamSite("docs", web)
	for _, args := range [][]string{
		{"team"},
		{"team", "nope"},
		{"team", "invite", "ana@example.test"}, // dashboard-only
		{"team", "remove", "ana@example.test"},
		{"team", "list", "extra"},
		{"team", "list", "--nope"},
		{"team", "members"},
		{"team", "members", "a", "b"},
		{"team", "members", " "},
		{"site", "move"},
		{"site", "move", "docs"},
		{"site", "move", "docs", "--team", "Web team", "--personal", "--yes"},
		{"site", "move", "docs", "--team", "", "--yes"},
		{"site", "move", "docs", "other", "--personal", "--yes"},
		{"site", "move", "-bad-", "--personal", "--yes"},
		{"site", "move", "docs", "--personal"},           // no terminal to ask in
		{"site", "move", "docs", "--personal", "--json"}, // never prompts
		{"list", "docs", "--team", "Web team"},           // filters the list of sites
		{"deploy", "--team"},                             // needs a value
		{"group", "list", "--team"},
		{"tag", "list", "--team"},
	} {
		a, _, _ := ciApp(t, srv)
		if got := exitCode(io.Discard, run(t.Context(), a, args)); got != 2 {
			t.Errorf("%v: exit %d, want 2", args, got)
		}
	}
	if srv.ownerOf("docs").Kind != apiclient.SiteOwnerKindTeam {
		t.Fatal("a usage error moved the site")
	}
}

func TestTeamHelp(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"team", "help"}); err != nil || !strings.Contains(out.String(), "members TEAM") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, unbuilt := range []string{"invite  ", "remove  "} {
		if strings.Contains(out.String(), unbuilt) {
			t.Fatalf("help lists %q:\n%s", unbuilt, out)
		}
	}
	for _, args := range [][]string{{"team", "list", "--help"}, {"team", "members", "--help"}, {"site", "move", "--help"}} {
		errOut.Reset()
		if err := run(t.Context(), a, args); err != nil || !strings.Contains(errOut.String(), "Usage: wardlock "+strings.Join(args[:2], " ")) {
			t.Fatalf("%v: %v\n%s", args, err, errOut)
		}
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"site", "help"}); err != nil || !strings.Contains(out.String(), "move SITE") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDeployTeam(t *testing.T) {
	deploy := func(t *testing.T, srv *fakeAPI, config string, args ...string) (*app, error) {
		t.Helper()
		a, _, _ := ciApp(t, srv)
		files := map[string]string{"dist/index.html": "hi"}
		if config != "" {
			files["wardlock.json"] = config
		}
		writeFiles(t, workDir(t, a), files)
		return a, run(t.Context(), a, append([]string{"deploy", "dist", "--json"}, args...))
	}
	t.Run("flag", func(t *testing.T) {
		srv := newFakeAPI(t)
		web := srv.addTeam("Web team", editor)
		a, err := deploy(t, srv, "", "docs", "--team", "web team")
		if err != nil {
			t.Fatal(err)
		}
		if o := srv.ownerOf("docs"); o.Kind != apiclient.SiteOwnerKindTeam || o.Id != web || o.Name != "Web team" {
			t.Fatalf("owner %+v", o)
		}
		var res deployResult
		if err := json.Unmarshal(a.stdout.(interface{ Bytes() []byte }).Bytes(), &res); err != nil || res.Site.Owner.Id != web || !res.SiteCreated {
			t.Fatalf("%v %+v", err, res)
		}
	})
	t.Run("wardlock.json", func(t *testing.T) {
		srv := newFakeAPI(t)
		web := srv.addTeam("Web team", editor)
		if _, err := deploy(t, srv, `{"team": "Web team"}`, "docs"); err != nil {
			t.Fatal(err)
		}
		if srv.ownerOf("docs").Id != web {
			t.Fatalf("owner %+v", srv.ownerOf("docs"))
		}
	})
	t.Run("flag overrides wardlock.json", func(t *testing.T) {
		srv := newFakeAPI(t)
		srv.addTeam("Web team", editor)
		ops := srv.addTeam("Ops", editor)
		if _, err := deploy(t, srv, `{"team": "Web team"}`, "docs", "--team", "Ops"); err != nil {
			t.Fatal(err)
		}
		if srv.ownerOf("docs").Id != ops {
			t.Fatalf("owner %+v", srv.ownerOf("docs"))
		}
		if _, err := deploy(t, srv, `{"team": "Web team"}`, "mine", "--team", ""); err != nil {
			t.Fatal(err)
		}
		if srv.ownerOf("mine") != personalOwner {
			t.Fatalf("owner %+v", srv.ownerOf("mine"))
		}
	})
	t.Run("no team stays personal", func(t *testing.T) {
		srv := newFakeAPI(t)
		srv.addTeam("Web team", editor)
		if _, err := deploy(t, srv, "", "docs"); err != nil {
			t.Fatal(err)
		}
		if srv.ownerOf("docs") != personalOwner {
			t.Fatalf("owner %+v", srv.ownerOf("docs"))
		}
	})
	t.Run("team token", func(t *testing.T) {
		srv := newFakeAPI(t)
		web := srv.addTeam("Web team", admin)
		srv.tokenTeam = web
		if _, err := deploy(t, srv, "", "plain"); err != nil {
			t.Fatal(err)
		}
		if _, err := deploy(t, srv, "", "named", "--team", "Web team"); err != nil {
			t.Fatal(err)
		}
		if srv.ownerOf("plain").Id != web || srv.ownerOf("named").Id != web {
			t.Fatalf("owners %+v %+v", srv.ownerOf("plain"), srv.ownerOf("named"))
		}
	})
	t.Run("existing site stays where it is", func(t *testing.T) {
		srv := newFakeAPI(t)
		srv.addTeam("Web team", editor)
		ops := srv.addTeam("Ops", editor)
		srv.addTeamSite("docs", ops)
		a, err := deploy(t, srv, "", "docs", "--team", "Web team")
		if err != nil {
			t.Fatal(err)
		}
		if srv.ownerOf("docs").Id != ops {
			t.Fatalf("owner %+v", srv.ownerOf("docs"))
		}
		if msg := a.stderr.(interface{ String() string }).String(); !strings.Contains(msg, "docs already exists in the team Ops") {
			t.Fatalf("stderr:\n%s", msg)
		}
		// Its own team's site needs no note.
		a, err = deploy(t, srv, "", "docs", "--team", "Ops")
		if msg := a.stderr.(interface{ String() string }).String(); err != nil || strings.Contains(msg, "already exists") {
			t.Fatalf("%v\n%s", err, msg)
		}
	})
	t.Run("files a new site under the team's group and tags", func(t *testing.T) {
		srv := newFakeAPI(t)
		web := srv.addTeam("Web team", editor)
		srv.addGroup("Board") // the owner's, not the team's
		board := srv.addTeamGroup(web, "Board")
		tag := srv.addTeamTag(web, "live")
		if _, err := deploy(t, srv, "", "docs", "--team", "Web team", "--group", "Board", "--tag", "live"); err != nil {
			t.Fatal(err)
		}
		if g, tags, _ := srv.organisationOf("docs"); g != board || !slices.Equal(tags, []string{tag}) {
			t.Fatalf("group %q tags %q", g, tags)
		}
	})
	t.Run("files an existing team site under its own team's", func(t *testing.T) {
		srv := newFakeAPI(t)
		web := srv.addTeam("Web team", editor)
		srv.addTeamSite("docs", web)
		board := srv.addTeamGroup(web, "Board")
		if _, err := deploy(t, srv, "", "docs", "--group", "Board"); err != nil {
			t.Fatal(err)
		}
		if g, _, _ := srv.organisationOf("docs"); g != board {
			t.Fatalf("group %q", g)
		}
	})
	t.Run("a personal group does not file a team site", func(t *testing.T) {
		srv := newFakeAPI(t)
		web := srv.addTeam("Web team", editor)
		srv.addGroup("Board")
		a, _, _ := ciApp(t, srv)
		writeFiles(t, workDir(t, a), map[string]string{"dist/index.html": "hi"})
		wantFailure(t, a, 1, `no group named "Board"`, "deploy", "dist", "docs", "--team", web, "--group", "Board")
		if len(srv.sites) != 0 {
			t.Fatal("created the site before finding the group")
		}
	})
	for name, tc := range map[string]struct {
		role    apiclient.TeamRole
		team    string
		suspend bool
		want    string
	}{
		"unknown team":   {editor, "Nope", false, `no team named "Nope"`},
		"viewer":         {viewer, "Web team", false, "Your role in this team does not allow this."},
		"suspended team": {editor, "Web team", true, "suspended this team"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newFakeAPI(t)
			web := srv.addTeam("Web team", tc.role)
			srv.suspendedTeams[web] = tc.suspend
			a, _, _ := ciApp(t, srv)
			writeFiles(t, workDir(t, a), map[string]string{"dist/index.html": "hi"})
			wantFailure(t, a, 1, tc.want, "deploy", "dist", "docs", "--team", tc.team)
			if len(srv.sites) != 0 || len(srv.created) != 0 {
				t.Fatal("deployed anyway")
			}
		})
	}
}

func TestConfigTeamField(t *testing.T) {
	cfg, err := parseConfig([]byte(`{"team": "Web team"}`))
	if err != nil || cfg.Team != "Web team" {
		t.Fatalf("%+v, %v", cfg, err)
	}
	srv := newFakeAPI(t)
	a, _, _ := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"wardlock.json": `{"team": 7}`, "index.html": "hi"})
	wantFailure(t, a, 2, "wardlock.json", "deploy", ".", "docs")
}

func TestListShowsTeams(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("mine", "")
	a, out, _ := ciApp(t, srv)

	// An owner in no team sees nothing change.
	if err := run(t.Context(), a, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "TEAM") {
		t.Fatalf("output:\n%s", out)
	}

	web := srv.addTeam("Web team", viewer)
	ops := srv.addTeam("Ops", viewer)
	docs := srv.addTeamSite("docs", web)
	srv.addTeamSite("runbook", ops)
	board := srv.addTeamGroup(web, "Board")
	live := srv.addTeamTag(web, "live")
	docs.site.GroupId, docs.site.TagIds = &board, []string{live}

	out.Reset()
	if err := run(t.Context(), a, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !regexpLine(text, `SITE\s+TEAM\s+POLICY\s+GROUP\s+TAGS\s+LAST DEPLOYED\s+URL`) || !regexpLine(text, `docs\s+Web team\s+public\s+Board\s+live\s+not deployed yet\s+\S+`) ||
		!regexpLine(text, `mine\s+-\s+public\s+-\s+-\s+not deployed yet\s+\S+`) || !regexpLine(text, `runbook\s+Ops\s+public.*`) {
		t.Fatalf("output:\n%s", text)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"list", "--json"}); err != nil {
		t.Fatal(err)
	}
	var all struct {
		Sites []listedSite `json:"sites"`
	}
	if err := json.Unmarshal(out.Bytes(), &all); err != nil || len(all.Sites) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
	if s := all.Sites[0]; s.Owner.Kind != apiclient.SiteOwnerKindTeam || s.Owner.Id != web || s.Owner.Name != "Web team" || s.Group != "Board" || !slices.Equal(s.Tags, []string{"live"}) {
		t.Fatalf("site %+v", s)
	}
	if s := all.Sites[1]; s.Owner.Kind != apiclient.SiteOwnerKindOwner {
		t.Fatalf("site %+v", s)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"list", "--team", "web team", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &all); err != nil || len(all.Sites) != 1 || all.Sites[0].Name != "docs" {
		t.Fatalf("%v\n%s", err, out)
	}

	// --group and --tag are the team's with --team, and the owner's without.
	out.Reset()
	if err := run(t.Context(), a, []string{"list", "--team", "Web team", "--group", "Board", "--tag", "live"}); err != nil || !strings.Contains(out.String(), "docs") {
		t.Fatalf("%v\n%s", err, out)
	}
	out.Reset()
	wantFailure(t, a, 1, `no group named "Board"`, "list", "--group", "Board")
	out.Reset()
	wantFailure(t, a, 1, `no team named "Nope"`, "list", "--team", "Nope")
	out.Reset()
	empty := srv.addTeam("Empty", viewer)
	if err := run(t.Context(), a, []string{"list", "--team", empty}); err != nil || !strings.Contains(out.String(), "No sites match.") {
		t.Fatalf("%v\n%s", err, out)
	}

	// A team token sees only its team, with its groups and tags.
	srv.tokenTeam = web
	out.Reset()
	if err := run(t.Context(), a, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !regexpLine(text, `docs\s+Web team\s+public\s+Board\s+live.*`) || strings.Contains(text, "mine") || strings.Contains(text, "runbook") {
		t.Fatalf("output:\n%s", text)
	}
}

func TestPinTeamSites(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", viewer) // a pin is the caller's own, so a viewer may
	srv.addTeamSite("docs", web)
	srv.addTeamSite("blog", web)
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"pin", "docs", "blog." + fakeBase, "--json"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	assertJSONKeys(t, out.Bytes(), "sites")
	var res pinResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Sites) != 2 || !res.Sites[0].Pinned || res.Sites[0].Owner.Id != web {
		t.Fatalf("%v\n%s", err, out)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"unpin", "docs"}); err != nil || !strings.Contains(out.String(), "Unpinned docs.") {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if _, _, pinned := srv.organisationOf("docs"); pinned {
		t.Fatal("docs is still pinned")
	}

	// A personal site works as ever, beside teams.
	srv.addSite("mine", "")
	out.Reset()
	if err := run(t.Context(), a, []string{"pin", "mine"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}

	// organiseSites works in one account, and each change is all or nothing.
	out.Reset()
	wantFailure(t, a, 1, "name the sites of one account at a time", "pin", "docs", "mine")
	if n := len(srv.organised); n != 3 {
		t.Fatalf("%d requests, want the 3 before the refused one", n)
	}
	// A site that does not exist is the server's to refuse, for all of them.
	wantFailure(t, a, 1, "nothing was changed", "pin", "docs", "nope")
	if _, _, pinned := srv.organisationOf("docs"); pinned {
		t.Fatal("pinned docs anyway")
	}

	// A pin is personal, so a team token cannot change one.
	srv.tokenTeam = web
	wantFailure(t, a, 1, "a team token cannot change one", "pin", "docs")
	msg := &strings.Builder{}
	if exitCode(msg, run(t.Context(), a, []string{"unpin", "docs"})) != 1 || strings.Contains(msg.String(), "scope") {
		t.Fatalf("message %q blames the token's scope", msg)
	}
}

func TestOrganiseTeamSites(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", editor)
	docs := srv.addTeamSite("docs", web)
	srv.addTeamSite("blog", web)
	srv.addGroup("Board") // the owner's, not the team's
	srv.addSite("mine", "")
	board := srv.addTeamGroup(web, "Board")
	live := srv.addTeamTag(web, "live")
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"site", "organise", "docs", "blog", "--group", "Board", "--tag", "live", "--json"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	assertJSONKeys(t, out.Bytes(), "prior", "sites")
	if g, tags, _ := srv.organisationOf("blog"); g != board || !slices.Equal(tags, []string{live}) {
		t.Fatalf("group %q tags %q", g, tags)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"site", "organise", "docs", "--untag", "live"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if text := out.String(); !strings.Contains(text, "Organised 1 site.") || !regexpLine(text, `docs\s+Board\s+-\s+no`) {
		t.Fatalf("output:\n%s", text)
	}

	out.Reset()
	wantFailure(t, a, 1, "name the sites of one account at a time", "site", "organise", "docs", "mine", "--no-group")
	wantFailure(t, a, 1, `no tag named "other"`, "site", "organise", "docs", "--tag", "other")

	// A team token changes its own team's groups and tags.
	srv.tokenTeam = web
	if err := run(t.Context(), a, []string{"site", "organise", "docs", "--tag", "live", "--json"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !slices.Equal(docs.site.TagIds, []string{live}) {
		t.Fatalf("tags %q", docs.site.TagIds)
	}

	// A viewer sees the site but cannot file it, and it is no scope's fault.
	srv.tokenTeam = ""
	srv.teams[0].Role = viewer
	out.Reset()
	msg := &strings.Builder{}
	if exitCode(msg, run(t.Context(), a, []string{"site", "organise", "docs", "--no-group"})) != 1 ||
		!strings.Contains(msg.String(), "Your role in this team does not allow this.") || strings.Contains(msg.String(), "scope") {
		t.Fatalf("message %q", msg)
	}
}

func TestGroupsAndTagsOfATeam(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", editor)
	mine := srv.addGroup("Personal")
	srv.addTag("private", 1)
	a, out, errOut := ciApp(t, srv)

	ok := func(args ...string) {
		t.Helper()
		out.Reset()
		if err := run(t.Context(), a, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, errOut)
		}
	}
	ok("group", "create", "Board", "--team", "Web team", "--json")
	var grp apiclient.Group
	if err := json.Unmarshal(out.Bytes(), &grp); err != nil || srv.account[grp.Id] != web {
		t.Fatalf("group %+v in %q: %v", grp, srv.account[grp.Id], err)
	}
	ok("group", "list", "--team", "Web team", "--json")
	var groups groupListResult
	if err := json.Unmarshal(out.Bytes(), &groups); err != nil || len(groups.Groups) != 1 || groups.Groups[0].Id != grp.Id {
		t.Fatalf("%v\n%s", err, out)
	}
	ok("group", "list")
	if !strings.Contains(out.String(), "Personal") || strings.Contains(out.String(), "Board") {
		t.Fatalf("output:\n%s", out)
	}
	ok("group", "rename", "Board", "Boards", "--team", "Web team")
	ok("group", "delete", "Boards", "--team", "Web team", "--yes")
	if len(srv.groups) != 1 || srv.groups[0].Id != mine {
		t.Fatalf("groups %+v", srv.groups)
	}

	ok("tag", "create", "live", "--team", "Web team", "--json")
	var tag apiclient.Tag
	if err := json.Unmarshal(out.Bytes(), &tag); err != nil || srv.account[tag.Id] != web {
		t.Fatalf("tag %+v: %v", tag, err)
	}
	ok("tag", "list", "--team", web)
	if !strings.Contains(out.String(), "live") || strings.Contains(out.String(), "private") {
		t.Fatalf("output:\n%s", out)
	}
	ok("tag", "rename", "live", "--colour", "3", "--team", "Web team")
	ok("tag", "delete", "live", "--team", "Web team", "--yes")

	out.Reset()
	wantFailure(t, a, 1, `no team named "Nope"`, "group", "list", "--team", "Nope")
	wantFailure(t, a, 1, `no team named "Nope"`, "tag", "create", "x", "--team", "Nope")
	wantFailure(t, a, 1, `no group named "Personal"`, "group", "delete", "Personal", "--team", "Web team", "--yes")

	// A viewer can read them, and is refused by role, not scope.
	srv.teams[0].Role = viewer
	ok("group", "list", "--team", "Web team")
	out.Reset()
	wantFailure(t, a, 1, "Your role in this team does not allow this.", "group", "create", "Board", "--team", "Web team")

	// A team token's own account is the team, with or without --team.
	srv.tokenTeam = web
	ok("tag", "create", "ci")
	if i := slices.IndexFunc(srv.tags, func(x apiclient.Tag) bool { return x.Name == "ci" }); i < 0 || srv.account[srv.tags[i].Id] != web {
		t.Fatalf("tags %+v", srv.tags)
	}
	ok("tag", "list", "--team", "Web team")
	if !strings.Contains(out.String(), "ci") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSiteMove(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", admin)
	ops := srv.addTeam("Ops", admin)
	docs := srv.addSite("docs", "")
	grp, tag := srv.addGroup("Board"), srv.addTag("live", 1)
	docs.site.GroupId, docs.site.TagIds = &grp, []string{tag}
	a, out, errOut := ciApp(t, srv)

	// Owner to team; the output is the API's Site.
	if err := run(t.Context(), a, []string{"site", "move", "docs", "--team", "Web team", "--yes", "--json"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	var site apiclient.Site
	if err := json.Unmarshal(out.Bytes(), &site); err != nil || site.Owner.Id != web || site.Owner.Name != "Web team" || site.GroupId != nil || len(site.TagIds) != 0 {
		t.Fatalf("%v\n%s", err, out)
	}
	assertJSONKeys(t, out.Bytes(), "id", "name", "hostname", "url", "createdAt", "owner", "tagIds", "pinned", "unavailable")
	if to := srv.transfers[0].To; to.Kind != apiclient.TransferSiteRequestToKindTeam || deref(to.Id) != web {
		t.Fatalf("request %+v", to)
	}

	// Team to team, by hostname and team ID.
	out.Reset()
	if err := run(t.Context(), a, []string{"site", "move", "docs." + fakeBase, "--team", ops, "--yes"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out.String(), "Moved docs to the team Ops.") || srv.ownerOf("docs").Id != ops {
		t.Fatalf("output:\n%s", out)
	}

	// Team to the caller's own account: no ID goes with it.
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "site", "move", "docs", "--personal", "--yes"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if srv.ownerOf("docs") != personalOwner {
		t.Fatalf("owner %+v", srv.ownerOf("docs"))
	}
	if to := srv.transfers[2].To; to.Kind != apiclient.TransferSiteRequestToKindOwner || to.Id != nil {
		t.Fatalf("request %+v", to)
	}
}

func TestSiteMoveConfirmation(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addTeam("Web team", admin)
	srv.addSite("docs", "")
	a, out, errOut := ciApp(t, srv)
	a.canPrompt = true

	a.stdin = strings.NewReader("no\n")
	wantFailure(t, a, 1, "not confirmed: nothing was moved", "site", "move", "docs", "--team", "Web team")
	if len(srv.transfers) != 0 {
		t.Fatal("moved without confirmation")
	}
	if text := errOut.String(); !strings.Contains(text, "moves docs from a personal account to the team Web team") || !strings.Contains(text, "group and tags are cleared") {
		t.Fatalf("question:\n%s", text)
	}

	a.stdin = strings.NewReader("docs\n")
	if err := run(t.Context(), a, []string{"site", "move", "docs", "--team", "Web team"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Moved docs to the team Web team.") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSiteMoveFailures(t *testing.T) {
	const roleHint = "a team token cannot move one"
	for name, tc := range map[string]struct {
		setup func(srv *fakeAPI, web, ops string)
		args  []string
		want  string
	}{
		"missing site":                  {nil, []string{"nope", "--personal"}, "no site nope on"},
		"unknown team":                  {nil, []string{"docs", "--team", "Nope"}, `no team named "Nope"`},
		"editor of the source":          {func(srv *fakeAPI, _, _ string) { srv.teams[0].Role = editor }, []string{"docs", "--team", "Ops"}, roleHint},
		"editor of the target":          {func(srv *fakeAPI, _, _ string) { srv.teams[1].Role = editor }, []string{"docs", "--team", "Ops"}, roleHint},
		"team token to its own account": {func(srv *fakeAPI, web, _ string) { srv.tokenTeam = web }, []string{"docs", "--personal"}, roleHint},
		"team token to its own team":    {func(srv *fakeAPI, web, _ string) { srv.tokenTeam = web }, []string{"docs", "--team", "Web team"}, roleHint},
		"a deploy-scoped token":         {func(srv *fakeAPI, _, _ string) { srv.scopes = []string{"deploy"} }, []string{"docs", "--personal"}, "lack the scope"},
		"taken down":                    {func(srv *fakeAPI, _, _ string) { srv.takenDown["docs"] = true }, []string{"docs", "--personal"}, "taken this site down"},
		"suspended target":              {func(srv *fakeAPI, _, ops string) { srv.suspendedTeams[ops] = true }, []string{"docs", "--team", "Ops"}, "suspended this team"},
		"target over quota":             {func(srv *fakeAPI, _, _ string) { srv.quotaFull = true }, []string{"docs", "--team", "Ops"}, "as many sites"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newFakeAPI(t)
			web, ops := srv.addTeam("Web team", admin), srv.addTeam("Ops", admin)
			srv.addTeamSite("docs", web)
			if tc.setup != nil {
				tc.setup(srv, web, ops)
			}
			a, _, _ := ciApp(t, srv)
			wantFailure(t, a, 1, tc.want, append(append([]string{"site", "move"}, tc.args...), "--yes", "--json")...)
			if len(srv.transfers) != 0 || srv.ownerOf("docs").Id != web {
				t.Fatal("moved anyway")
			}
		})
	}
}

func TestMCPTeamRoleRefusal(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", viewer)
	srv.addTeamSite("docs", web)
	srv.refuseDeploy = &refusal{status: 403, body: forbiddenRole}
	a, _, _ := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"dist/index.html": "hi"})
	cs := mcpSession(t, a)
	// The token has the scope: the role is what is refused, and the tool says so.
	mustFail(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "dist"}, "Your role in this team does not allow this.")
}

func TestMCPDeployUsesConfigTeam(t *testing.T) {
	srv := newFakeAPI(t)
	web := srv.addTeam("Web team", editor)
	a, _, _ := ciApp(t, srv)
	writeFiles(t, workDir(t, a), map[string]string{"dist/index.html": "hi", "wardlock.json": `{"team": "Web team"}`})
	cs := mcpSession(t, a)
	var res deployResult
	mustCall(t, cs, "deploy_directory", map[string]any{"site": "docs", "directory": "dist"}, &res)
	if res.Site.Owner.Id != web {
		t.Fatalf("owner %+v", res.Site.Owner)
	}
}
