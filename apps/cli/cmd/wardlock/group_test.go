// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

func TestGroupCommands(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"group", "list"}); err != nil || !strings.Contains(out.String(), "No groups yet") {
		t.Fatalf("%v: %s", err, out)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"group", "create", "  Client work ", "--note", "Previews for clients", "--json"}); err != nil {
		t.Fatalf("create: %v\n%s", err, errOut)
	}
	assertJSONKeys(t, out.Bytes(), "id", "name", "note", "createdAt")
	out.Reset()
	if err := run(t.Context(), a, []string{"group", "create", "Board"}); err != nil || out.String() != "Created group Board.\n" {
		t.Fatalf("create: %v %q", err, out)
	}
	// Names collide ignoring case: the server's message is shown.
	errOut.Reset()
	err := run(t.Context(), a, []string{"group", "create", "board"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "already have a group") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"group", "list"}); err != nil {
		t.Fatal(err)
	}
	if !regexpLine(out.String(), `Board\s*`) || !regexpLine(out.String(), `Client work\s+Previews for clients`) || strings.Index(out.String(), "Board") > strings.Index(out.String(), "Client") {
		t.Fatalf("output:\n%s", out)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"--json", "group", "list"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "groups")
	var groups []apiclient.Group
	if err := json.Unmarshal(m["groups"], &groups); err != nil || len(groups) != 2 || groups[1].Name != "Client work" {
		t.Fatalf("%s (%v)", m["groups"], err)
	}

	// Rename finds the group in any case; --note alone changes the note.
	out.Reset()
	if err := run(t.Context(), a, []string{"group", "rename", "CLIENT WORK", "Clients"}); err != nil || out.String() != "Renamed group Client work to Clients.\n" {
		t.Fatalf("rename: %v %q %s", err, out, errOut)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"group", "rename", "clients", "--note", "", "--json"}); err != nil {
		t.Fatalf("rename: %v\n%s", err, errOut)
	}
	assertJSONKeys(t, out.Bytes(), "id", "name", "createdAt")
	errOut.Reset()
	err = run(t.Context(), a, []string{"group", "rename", "Nope", "Other"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), `no group named "Nope"`) {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}

	// Deleting keeps the group's sites, ungrouped.
	srv.addSite("docs", "")
	srv.mu.Lock()
	srv.sites["docs"].site.GroupId = &groups[1].Id
	srv.mu.Unlock()
	out.Reset()
	if err := run(t.Context(), a, []string{"group", "delete", "clients", "--yes", "--json"}); err != nil {
		t.Fatalf("delete: %v\n%s", err, errOut)
	}
	m = assertJSONKeys(t, out.Bytes(), "deleted", "group")
	if string(m["deleted"]) != "true" || !strings.Contains(string(m["group"]), `"Clients"`) {
		t.Fatalf("output %s", out)
	}
	if g, _, _ := srv.organisationOf("docs"); g != "" || len(srv.groups) != 1 {
		t.Fatalf("docs group %q, %d groups", g, len(srv.groups))
	}

	// A deploy token can list groups but not change them.
	srv.scopes = []string{"deploy"}
	out.Reset()
	if err := run(t.Context(), a, []string{"group", "list"}); err != nil || !strings.Contains(out.String(), "Board") {
		t.Fatalf("%v: %s", err, out)
	}
	errOut.Reset()
	err = run(t.Context(), a, []string{"group", "create", "CI"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "scope") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestGroupDeleteAsks(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addGroup("Board")
	a, out, errOut := ciApp(t, srv)
	a.canPrompt = true
	a.stdin = strings.NewReader("board\n")
	err := run(t.Context(), a, []string{"group", "delete", "Board"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "not confirmed") || len(srv.groups) != 1 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	a.stdin = strings.NewReader("Board\n")
	if err := run(t.Context(), a, []string{"group", "delete", "board"}); err != nil || len(srv.groups) != 0 || !strings.Contains(out.String(), "Deleted group Board.") {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestTagCommands(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"tag", "create", "preview", "--colour", "3", "--json"}); err != nil {
		t.Fatalf("create: %v\n%s", err, errOut)
	}
	m := assertJSONKeys(t, out.Bytes(), "id", "name", "colour", "createdAt")
	if string(m["colour"]) != "3" {
		t.Fatalf("colour %s", m["colour"])
	}
	// Without --colour the server picks the first free slot, and 0 is
	// sent only when asked for.
	out.Reset()
	if err := run(t.Context(), a, []string{"tag", "create", "internal"}); err != nil || out.String() != "Created tag internal (colour 1).\n" {
		t.Fatalf("create: %v %q", err, out)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"tag", "create", "draft", "--colour=0"}); err != nil || out.String() != "Created tag draft (colour 0).\n" {
		t.Fatalf("create: %v %q", err, out)
	}
	errOut.Reset()
	err := run(t.Context(), a, []string{"tag", "create", "draft"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "already have a tag") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"tag", "list"}); err != nil {
		t.Fatal(err)
	}
	if !regexpLine(out.String(), `preview\s+3`) || strings.Index(out.String(), "draft") > strings.Index(out.String(), "internal") {
		t.Fatalf("output:\n%s", out)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"tag", "list", "--json"}); err != nil {
		t.Fatal(err)
	}
	m = assertJSONKeys(t, out.Bytes(), "tags")
	var tags []apiclient.Tag
	if err := json.Unmarshal(m["tags"], &tags); err != nil || len(tags) != 3 {
		t.Fatalf("%s (%v)", m["tags"], err)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"tag", "rename", "preview", "review"}); err != nil || out.String() != "Renamed tag preview to review (colour 3).\n" {
		t.Fatalf("rename: %v %q %s", err, out, errOut)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"tag", "rename", "review", "--colour", "8", "--json"}); err != nil || !strings.Contains(out.String(), `"colour": 8`) {
		t.Fatalf("recolour: %v %s", err, out)
	}
	errOut.Reset()
	err = run(t.Context(), a, []string{"tag", "rename", "preview", "other"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), `no tag named "preview"`) {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}

	// Deleting removes the tag from its sites.
	srv.addSite("docs", "")
	srv.mu.Lock()
	srv.sites["docs"].site.TagIds = []string{tags[0].Id, tags[1].Id}
	srv.mu.Unlock()
	out.Reset()
	if err := run(t.Context(), a, []string{"tag", "delete", "draft", "--yes", "--json"}); err != nil {
		t.Fatalf("delete: %v\n%s", err, errOut)
	}
	m = assertJSONKeys(t, out.Bytes(), "deleted", "tag")
	if !strings.Contains(string(m["tag"]), `"draft"`) {
		t.Fatalf("output %s", out)
	}
	if _, ids, _ := srv.organisationOf("docs"); len(ids) != 1 || ids[0] != tags[1].Id {
		t.Fatalf("docs tags %q", ids)
	}

	srv.scopes = []string{"deploy"}
	errOut.Reset()
	err = run(t.Context(), a, []string{"tag", "delete", "internal", "--yes"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "scope") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestListGroupsAndTags(t *testing.T) {
	srv := newFakeAPI(t)
	for _, n := range []string{"docs", "blog", "shop"} {
		srv.addSite(n, "")
	}
	board := srv.addGroup("Board")
	preview, internal := srv.addTag("preview", 1), srv.addTag("internal", 2)
	srv.mu.Lock()
	srv.sites["docs"].site.GroupId = &board
	srv.sites["docs"].site.TagIds = []string{internal, preview}
	srv.sites["blog"].site.TagIds = []string{preview}
	srv.mu.Unlock()
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"list"}); err != nil {
		t.Fatalf("list: %v\n%s", err, errOut)
	}
	text := out.String()
	if !regexpLine(text, `SITE\s+POLICY\s+GROUP\s+TAGS\s+LAST DEPLOYED\s+URL`) || !regexpLine(text, `docs\s+public\s+Board\s+internal, preview\s+not deployed yet\s+\S+`) ||
		!regexpLine(text, `shop\s+public\s+-\s+-\s+not deployed yet\s+\S+`) {
		t.Fatalf("output:\n%s", text)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"list", "--tag", "preview", "--json"}); err != nil {
		t.Fatal(err)
	}
	var res siteList
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Sites) != 2 || res.Sites[0].Name != "blog" || res.Sites[1].Group != "Board" ||
		strings.Join(res.Sites[1].Tags, ",") != "internal,preview" {
		t.Fatalf("%+v (%v)", res, err)
	}
	var raw struct{ Sites []json.RawMessage }
	_ = json.Unmarshal(out.Bytes(), &raw)
	assertJSONKeys(t, raw.Sites[0], "id", "name", "hostname", "url", "createdAt", "unavailable", "tagIds", "pinned", "policy", "tags")
	assertJSONKeys(t, raw.Sites[1], "id", "name", "hostname", "url", "createdAt", "unavailable", "groupId", "tagIds", "pinned", "policy", "group", "tags")

	for args, want := range map[string]string{
		"--group board":                "docs",
		"--tag preview --tag internal": "docs",
		"--group Board --tag preview":  "docs",
	} {
		out.Reset()
		if err := run(t.Context(), a, append([]string{"list", "--json"}, strings.Fields(args)...)); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		var res siteList
		if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Sites) != 1 || res.Sites[0].Name != want {
			t.Fatalf("%s: %s", args, out)
		}
	}
	srv.addGroup("Empty")
	out.Reset()
	if err := run(t.Context(), a, []string{"list", "--group", "empty"}); err != nil || out.String() != "No sites match.\n" {
		t.Fatalf("%v: %q", err, out)
	}

	for _, args := range [][]string{{"list", "--group", "Nope"}, {"list", "--tag", "nope"}} {
		out.Reset()
		errOut.Reset()
		err := run(t.Context(), a, args)
		if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "named") || out.Len() != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}
