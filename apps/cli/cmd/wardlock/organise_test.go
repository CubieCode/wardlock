// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

// handleOrganisation adds groups, tags, pins and the bulk change to the
// fake, as V5.3's server answers them: reads with either scope, writes
// with manage, and setSiteOrganisation with either. A group or tag ID the
// owner does not have answers 404, as another owner's does.
func (f *fakeAPI) handleOrganisation(handle func(pattern, scope string, h func(w http.ResponseWriter, r *http.Request))) {
	locked := func(h func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			h(w, r)
		}
	}
	handle("GET /api/v0/groups", "", locked(func(w http.ResponseWriter, _ *http.Request) {
		items := slices.Clone(f.groups)
		slices.SortFunc(items, func(a, b apiclient.Group) int { return strings.Compare(groupKey(a.Name), groupKey(b.Name)) })
		reply(w, http.StatusOK, apiclient.GroupList{Items: append([]apiclient.Group{}, items...)})
	}))
	handle("POST /api/v0/groups", "manage", locked(func(w http.ResponseWriter, r *http.Request) {
		var body apiclient.CreateGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			reply(w, http.StatusBadRequest, apiclient.Error{Code: "invalid_request", Message: "Invalid name.", Target: ptr("name")})
			return
		}
		if f.groupIndex(body.Name) >= 0 {
			reply(w, http.StatusConflict, apiErr("group_name_taken", "You already have a group with that name."))
			return
		}
		g := apiclient.Group{Id: f.id(), Name: strings.TrimSpace(body.Name), CreatedAt: 1_800_000_000_000}
		if body.Note != nil && *body.Note != "" {
			g.Note = body.Note
		}
		f.groups = append(f.groups, g)
		reply(w, http.StatusCreated, g)
	}))
	withGroup := func(h func(w http.ResponseWriter, r *http.Request, i int)) func(w http.ResponseWriter, r *http.Request) {
		return locked(func(w http.ResponseWriter, r *http.Request) {
			i := slices.IndexFunc(f.groups, func(g apiclient.Group) bool { return g.Id == r.PathValue("groupId") })
			if i < 0 {
				reply(w, http.StatusNotFound, notFound)
				return
			}
			h(w, r, i)
		})
	}
	handle("PATCH /api/v0/groups/{groupId}", "manage", withGroup(func(w http.ResponseWriter, r *http.Request, i int) {
		var body apiclient.UpdateGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusBadRequest, apiErr("invalid_request", "The request is malformed."))
			return
		}
		g := &f.groups[i]
		if body.Name != nil {
			if j := f.groupIndex(*body.Name); j >= 0 && j != i {
				reply(w, http.StatusConflict, apiErr("group_name_taken", "You already have a group with that name."))
				return
			}
			g.Name = *body.Name
		}
		if body.Note != nil {
			g.Note = body.Note
			if *body.Note == "" {
				g.Note = nil
			}
		}
		reply(w, http.StatusOK, *g)
	}))
	handle("DELETE /api/v0/groups/{groupId}", "manage", withGroup(func(w http.ResponseWriter, _ *http.Request, i int) {
		id := f.groups[i].Id
		f.groups = slices.Delete(f.groups, i, i+1)
		for _, s := range f.sites {
			if s.site.GroupId != nil && *s.site.GroupId == id {
				s.site.GroupId = nil
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	handle("GET /api/v0/tags", "", locked(func(w http.ResponseWriter, _ *http.Request) {
		items := slices.Clone(f.tags)
		slices.SortFunc(items, func(a, b apiclient.Tag) int { return strings.Compare(a.Name, b.Name) })
		reply(w, http.StatusOK, apiclient.TagList{Items: append([]apiclient.Tag{}, items...)})
	}))
	handle("POST /api/v0/tags", "manage", locked(func(w http.ResponseWriter, r *http.Request) {
		var body apiclient.CreateTagRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !tagNamePattern.MatchString(body.Name) {
			reply(w, http.StatusBadRequest, apiclient.Error{Code: "invalid_request", Message: "Invalid name.", Target: ptr("name")})
			return
		}
		if slices.ContainsFunc(f.tags, func(t apiclient.Tag) bool { return t.Name == body.Name }) {
			reply(w, http.StatusConflict, apiErr("tag_name_taken", "You already have a tag with that name."))
			return
		}
		t := apiclient.Tag{Id: f.id(), Name: body.Name, CreatedAt: 1_800_000_000_000}
		if body.Colour != nil {
			t.Colour = *body.Colour
		} else {
			for c := int32(1); c <= 8; c++ {
				if !slices.ContainsFunc(f.tags, func(t apiclient.Tag) bool { return t.Colour == c }) {
					t.Colour = c
					break
				}
			}
		}
		f.tags = append(f.tags, t)
		reply(w, http.StatusCreated, t)
	}))
	withTag := func(h func(w http.ResponseWriter, r *http.Request, i int)) func(w http.ResponseWriter, r *http.Request) {
		return locked(func(w http.ResponseWriter, r *http.Request) {
			i := slices.IndexFunc(f.tags, func(t apiclient.Tag) bool { return t.Id == r.PathValue("tagId") })
			if i < 0 {
				reply(w, http.StatusNotFound, notFound)
				return
			}
			h(w, r, i)
		})
	}
	handle("PATCH /api/v0/tags/{tagId}", "manage", withTag(func(w http.ResponseWriter, r *http.Request, i int) {
		var body apiclient.UpdateTagRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusBadRequest, apiErr("invalid_request", "The request is malformed."))
			return
		}
		t := &f.tags[i]
		if body.Name != nil {
			if slices.ContainsFunc(f.tags, func(o apiclient.Tag) bool { return o.Name == *body.Name && o.Id != t.Id }) {
				reply(w, http.StatusConflict, apiErr("tag_name_taken", "You already have a tag with that name."))
				return
			}
			t.Name = *body.Name
		}
		if body.Colour != nil {
			t.Colour = *body.Colour
		}
		reply(w, http.StatusOK, *t)
	}))
	handle("DELETE /api/v0/tags/{tagId}", "manage", withTag(func(w http.ResponseWriter, _ *http.Request, i int) {
		id := f.tags[i].Id
		f.tags = slices.Delete(f.tags, i, i+1)
		for _, s := range f.sites {
			s.site.TagIds = slices.DeleteFunc(s.site.TagIds, func(t string) bool { return t == id })
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	handle("PUT /api/v0/sites/{site}/organisation", "", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		var body apiclient.SetSiteOrganisationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TagIds == nil {
			reply(w, http.StatusBadRequest, apiErr("invalid_request", "The request is malformed."))
			return
		}
		if !f.known(body.GroupId, body.TagIds) {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		if len(body.TagIds) > 20 {
			reply(w, http.StatusRequestEntityTooLarge, apiclient.Error{Code: "limit_exceeded", Message: "A site has at most 20 tags.", Target: ptr("tagIds")})
			return
		}
		f.filed = append(f.filed, body)
		s.site.GroupId, s.site.TagIds = body.GroupId, f.byTagName(body.TagIds)
		reply(w, http.StatusOK, s.site)
	}))
	pin := func(pinned bool) func(w http.ResponseWriter, r *http.Request) {
		return f.withSite(func(w http.ResponseWriter, _ *http.Request, s *fakeSite) {
			s.site.Pinned = pinned
			reply(w, http.StatusOK, s.site)
		})
	}
	handle("PUT /api/v0/sites/{site}/pin", "manage", pin(true))
	handle("DELETE /api/v0/sites/{site}/pin", "manage", pin(false))
	handle("POST /api/v0/organise", "manage", locked(f.organise))
}

func (f *fakeAPI) organise(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body apiclient.OrganiseSitesRequest
	if err := dec.Decode(&body); err != nil || len(body.Sites) == 0 || (body.GroupId != nil && body.Ungroup != nil && *body.Ungroup) {
		reply(w, http.StatusBadRequest, apiErr("invalid_request", "The request is malformed."))
		return
	}
	f.organised = append(f.organised, body)
	add, remove := deref2(body.AddTagIds), deref2(body.RemoveTagIds)
	if !f.known(body.GroupId, append(slices.Clone(add), remove...)) {
		reply(w, http.StatusNotFound, notFound)
		return
	}
	var sites []*fakeSite
	for _, n := range body.Sites {
		s, ok := f.sites[n]
		if !ok {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		sites = append(sites, s)
	}
	prior := []apiclient.SiteOrganisation{}
	for _, s := range sites {
		prior = append(prior, apiclient.SiteOrganisation{Site: s.site.Name, GroupId: s.site.GroupId, TagIds: slices.Clone(s.site.TagIds), Pinned: s.site.Pinned})
		if body.GroupId != nil {
			s.site.GroupId = body.GroupId
		}
		if body.Ungroup != nil && *body.Ungroup {
			s.site.GroupId = nil
		}
		ids := slices.DeleteFunc(slices.Clone(s.site.TagIds), func(id string) bool { return slices.Contains(remove, id) })
		for _, id := range add {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		s.site.TagIds = f.byTagName(ids)
		if body.Pinned != nil {
			s.site.Pinned = *body.Pinned
		}
	}
	reply(w, http.StatusOK, apiclient.OrganiseSitesResult{Prior: prior})
}

func deref2(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}

// groupIndex finds a group by name, as the server compares them.
func (f *fakeAPI) groupIndex(name string) int {
	return slices.IndexFunc(f.groups, func(g apiclient.Group) bool { return groupKey(g.Name) == groupKey(name) })
}

// known reports whether the owner has the group (when given) and every tag.
func (f *fakeAPI) known(groupID *string, tagIDs []string) bool {
	if groupID != nil && !slices.ContainsFunc(f.groups, func(g apiclient.Group) bool { return g.Id == *groupID }) {
		return false
	}
	for _, id := range tagIDs {
		if !slices.ContainsFunc(f.tags, func(t apiclient.Tag) bool { return t.Id == id }) {
			return false
		}
	}
	return true
}

// byTagName orders tag IDs by their tags' names, as sites list them.
func (f *fakeAPI) byTagName(ids []string) []string {
	out := slices.Clone(ids)
	name := func(id string) string {
		i := slices.IndexFunc(f.tags, func(t apiclient.Tag) bool { return t.Id == id })
		return f.tags[i].Name
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(name(a), name(b)) })
	return out
}

// addGroup and addTag create a group or tag directly and return its ID.
func (f *fakeAPI) addGroup(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := apiclient.Group{Id: f.id(), Name: name, CreatedAt: 1_800_000_000_000}
	f.groups = append(f.groups, g)
	return g.Id
}

func (f *fakeAPI) addTag(name string, colour int32) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := apiclient.Tag{Id: f.id(), Name: name, Colour: colour, CreatedAt: 1_800_000_000_000}
	f.tags = append(f.tags, t)
	return t.Id
}

// organisationOf is a site's group ID ("" for none), tag IDs and pin.
func (f *fakeAPI) organisationOf(name string) (string, []string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sites[name].site
	return deref(s.GroupId), slices.Clone(s.TagIds), s.Pinned
}

func TestSiteOrganise(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	srv.addSite("blog", "")
	clients := srv.addGroup("Client work")
	preview, internal := srv.addTag("preview", 1), srv.addTag("internal", 2)
	a, out, errOut := ciApp(t, srv)

	// A hostname names a site too; names are matched as the server does.
	err := run(t.Context(), a, []string{"site", "organise", "docs", "blog." + fakeBase, "--group", "client WORK", "--tag", "preview", "--tag", "internal", "--json"})
	if err != nil {
		t.Fatalf("organise: %v\n%s", err, errOut)
	}
	m := assertJSONKeys(t, out.Bytes(), "prior", "sites")
	var res organiseResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Prior) != 2 || res.Prior[1].Site != "blog" || len(res.Sites) != 2 {
		t.Fatalf("%s (%v)", m["prior"], err)
	}
	assertJSONKeys(t, mustJSON(t, res.Prior[0]), "site", "tagIds", "pinned")
	if g, tags, _ := srv.organisationOf("blog"); g != clients || !slices.Equal(tags, []string{internal, preview}) {
		t.Fatalf("blog: group %q tags %q", g, tags)
	}
	if got := res.Sites[0]; got.GroupId == nil || *got.GroupId != clients || len(got.TagIds) != 2 {
		t.Fatalf("sites in output %+v", res.Sites)
	}
	body := srv.organised[0]
	if !slices.Equal(body.Sites, []string{"docs", "blog"}) || body.Ungroup != nil || len(*body.AddTagIds) != 2 || body.RemoveTagIds != nil {
		t.Fatalf("request %+v", body)
	}

	out.Reset()
	if err := run(t.Context(), a, []string{"site", "organise", "blog", "--no-group", "--untag", "preview"}); err != nil {
		t.Fatalf("organise: %v\n%s", err, errOut)
	}
	if g, tags, _ := srv.organisationOf("blog"); g != "" || !slices.Equal(tags, []string{internal}) {
		t.Fatalf("blog: group %q tags %q", g, tags)
	}
	text := out.String()
	if !strings.Contains(text, "Organised 1 site.") || !strings.Contains(text, "GROUP") || !regexpLine(text, `blog\s+-\s+internal\s+no`) {
		t.Fatalf("output:\n%s", text)
	}

	// An unknown name changes nothing and is never sent.
	for _, args := range [][]string{
		{"site", "organise", "docs", "--tag", "nope"},
		{"site", "organise", "docs", "--group", "Nope"},
	} {
		out.Reset()
		errOut.Reset()
		err := run(t.Context(), a, args)
		if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), `"nope"`) && !strings.Contains(errOut.String(), `"Nope"`) || out.Len() != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	if len(srv.organised) != 2 {
		t.Fatalf("sent %d organise requests", len(srv.organised))
	}
	errOut.Reset()
	err = run(t.Context(), a, []string{"site", "organise", "docs", "ghost", "--tag", "preview", "--json"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "nothing was changed") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if _, tags, _ := srv.organisationOf("docs"); !slices.Equal(tags, []string{internal, preview}) {
		t.Fatalf("docs changed: %q", tags)
	}

	// A deploy token cannot make bulk changes.
	srv.scopes = []string{"deploy"}
	errOut.Reset()
	err = run(t.Context(), a, []string{"site", "organise", "docs", "--untag", "preview"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "scope") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestPinUnpin(t *testing.T) {
	srv := newFakeAPI(t)
	srv.addSite("docs", "")
	srv.addSite("blog", "")
	a, out, errOut := ciApp(t, srv)
	if err := run(t.Context(), a, []string{"pin", "docs", "blog", "docs", "--json"}); err != nil {
		t.Fatalf("pin: %v\n%s", err, errOut)
	}
	m := assertJSONKeys(t, out.Bytes(), "sites")
	var res pinResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Sites) != 2 || !res.Sites[0].Pinned || !res.Sites[1].Pinned {
		t.Fatalf("%s (%v)", m["sites"], err)
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"unpin", "blog"}); err != nil || out.String() != "Unpinned blog.\n" {
		t.Fatalf("unpin: %v %q", err, out)
	}
	if _, _, pinned := srv.organisationOf("blog"); pinned {
		t.Fatal("blog still pinned")
	}

	// Every site is looked up first, so a missing one pins none.
	errOut.Reset()
	err := run(t.Context(), a, []string{"pin", "blog", "ghost"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "no site ghost") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if _, _, pinned := srv.organisationOf("blog"); pinned {
		t.Fatal("blog pinned although ghost is missing")
	}

	srv.scopes = []string{"deploy"}
	errOut.Reset()
	err = run(t.Context(), a, []string{"pin", "blog"})
	if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "scope") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

// TestOrganiseUsageErrorsNeedNoServer: bad arguments exit 2 before any
// request, even when the server cannot be reached.
func TestOrganiseUsageErrorsNeedNoServer(t *testing.T) {
	srv := newFakeAPI(t)
	a, _, errOut := ciApp(t, srv)
	srv.Close()
	for _, args := range [][]string{
		{"site"},
		{"site", "rename"},
		{"site", "organise", "--tag", "preview"},
		{"site", "organise", "docs"},
		{"site", "organise", "docs", "--group", "G", "--no-group"},
		{"site", "organise", "docs", "--tag", "x", "--untag", "x"},
		{"site", "organise", "docs", "--tag", "Not A Tag"},
		{"site", "organise", "docs", "--group", " "},
		{"site", "organise", "not_a_site", "--tag", "x"},
		{"pin"},
		{"unpin", "docs", "--nope"},
		{"group"},
		{"group", "move"},
		{"group", "list", "extra"},
		{"group", "create"},
		{"group", "create", strings.Repeat("g", 41)},
		{"group", "rename", "G"},
		{"group", "delete", "G"},
		{"group", "delete", "G", "--json"},
		{"tag"},
		{"tag", "create", "UPPER"},
		{"tag", "create", "ok", "--colour", "9"},
		{"tag", "create", "ok", "--colour", "-1"},
		{"tag", "rename", "ok"},
		{"tag", "rename", "ok", "Bad"},
		{"tag", "delete", "ok"},
		{"list", "--tag", "Bad"},
		{"list", "docs", "--group", "G"},
		{"list", "docs", "--tag", "x"},
		{"deploy", ".", "docs", "--tag", "Bad"},
		{"deploy", ".", "docs", "--group", strings.Repeat("g", 41)},
	} {
		errOut.Reset()
		err := run(t.Context(), a, args)
		if code := exitCode(errOut, err); code != 2 || strings.Contains(errOut.String(), "could not reach") {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// regexpLine reports whether some line of text matches pattern in full.
func regexpLine(text, pattern string) bool {
	return regexp.MustCompile(`(?m)^` + pattern + `\s*$`).MatchString(text)
}
