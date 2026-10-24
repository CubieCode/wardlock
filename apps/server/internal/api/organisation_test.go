// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
)

func (hs *harness) createGroup(owner, name string) api.Group {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPost, "/groups", api.CreateGroupRequest{Name: name})
	res.want(hs.t, http.StatusCreated, "")
	var g api.Group
	res.decode(hs.t, &g)
	return g
}

func (hs *harness) createTag(owner, name string) api.Tag {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPost, "/tags", api.CreateTagRequest{Name: name})
	res.want(hs.t, http.StatusCreated, "")
	var tg api.Tag
	res.decode(hs.t, &tg)
	return tg
}

func (hs *harness) getSite(owner, name string) api.Site {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/sites/"+name, nil)
	res.want(hs.t, http.StatusOK, "")
	var s api.Site
	res.decode(hs.t, &s)
	return s
}

func ptr[T any](v T) *T { return &v }

func TestGroupsCRUD(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA

	res := hs.do(a, http.MethodPost, "/groups", api.CreateGroupRequest{Name: "  Kōwhai Studio  ", Note: ptr("Client")})
	res.want(t, http.StatusCreated, "")
	var k api.Group
	res.decode(t, &k)
	if k.Name != "Kōwhai Studio" || k.Note == nil || *k.Note != "Client" || k.Id == "" || k.CreatedAt == 0 {
		t.Fatalf("created %+v", k)
	}
	b := hs.createGroup(a, "Board")

	res = hs.do(a, http.MethodGet, "/groups", nil)
	res.want(t, http.StatusOK, "")
	var list api.GroupList
	res.decode(t, &list)
	if len(list.Items) != 2 || list.Items[0].Id != b.Id || list.Items[1].Id != k.Id {
		t.Fatalf("list = %+v, want Board then Kōwhai Studio", list.Items)
	}

	// Rename keeps the note; an empty note removes it.
	res = hs.do(a, http.MethodPatch, "/groups/"+k.Id, api.UpdateGroupRequest{Name: ptr("Kōwhai")})
	res.want(t, http.StatusOK, "")
	res.decode(t, &k)
	if k.Name != "Kōwhai" || k.Note == nil || *k.Note != "Client" {
		t.Fatalf("renamed %+v", k)
	}
	res = hs.do(a, http.MethodPatch, "/groups/"+k.Id, api.UpdateGroupRequest{Note: ptr("")})
	res.want(t, http.StatusOK, "")
	k = api.Group{}
	res.decode(t, &k)
	if k.Note != nil {
		t.Fatalf("note after clearing = %q", *k.Note)
	}
	hs.do(a, http.MethodPatch, "/groups/"+k.Id, api.UpdateGroupRequest{Name: ptr("Board")}).want(t, http.StatusConflict, "group_name_taken")
	hs.do(a, http.MethodPost, "/groups", api.CreateGroupRequest{Name: "Board"}).want(t, http.StatusConflict, "group_name_taken")
	// Names are unique ignoring case, beyond ASCII and however an accented
	// letter is encoded.
	for _, variant := range []string{"board", "BOARD", "KŌWHAI", "kōwhai", "Ko\u0304whai"} {
		hs.do(a, http.MethodPost, "/groups", api.CreateGroupRequest{Name: variant}).want(t, http.StatusConflict, "group_name_taken")
	}
	hs.do(a, http.MethodPatch, "/groups/"+k.Id, api.UpdateGroupRequest{Name: ptr("bOARD")}).want(t, http.StatusConflict, "group_name_taken")
	// Renaming to another case of its own name is allowed, and kept as typed.
	res = hs.do(a, http.MethodPatch, "/groups/"+k.Id, api.UpdateGroupRequest{Name: ptr("KŌWHAI")})
	res.want(t, http.StatusOK, "")
	res.decode(t, &k)
	if k.Name != "KŌWHAI" {
		t.Fatalf("recased %+v", k)
	}
	// Listing ignores case too: "aardvark" comes before "Board".
	hs.createGroup(a, "aardvark")
	res = hs.do(a, http.MethodGet, "/groups", nil)
	list = api.GroupList{}
	res.decode(t, &list)
	if len(list.Items) != 3 || list.Items[0].Name != "aardvark" || list.Items[1].Name != "Board" || list.Items[2].Name != "KŌWHAI" {
		t.Fatalf("list = %+v, want aardvark, Board, KŌWHAI", list.Items)
	}
	// Another owner may use the same name.
	hs.createGroup(hs.ownerB, "Board")

	for name, body := range map[string]api.CreateGroupRequest{
		"empty":         {Name: ""},
		"spaces":        {Name: "   "},
		"41 characters": {Name: strings.Repeat("ā", 41)},
		"control":       {Name: "Tab\there"},
		"long note":     {Name: "Fine", Note: ptr(strings.Repeat("n", 201))},
		"control note":  {Name: "Fine", Note: ptr("line\nbreak")},
	} {
		res := hs.do(a, http.MethodPost, "/groups", body)
		res.want(t, http.StatusBadRequest, "invalid_request")
		want := "name"
		if body.Note != nil {
			want = "note"
		}
		if e := res.err(t); e.Target == nil || *e.Target != want {
			t.Errorf("%s: target %v, want %s", name, e.Target, want)
		}
	}
	hs.createGroup(a, strings.Repeat("ā", 40))

	// Deleting a group keeps its sites, ungrouped.
	hs.createSite(a, "docs")
	hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{GroupId: &b.Id, TagIds: []string{}}).want(t, http.StatusOK, "")
	hs.do(a, http.MethodDelete, "/groups/"+b.Id, nil).want(t, http.StatusNoContent, "")
	if s := hs.getSite(a, "docs"); s.GroupId != nil {
		t.Fatalf("site group after delete = %q", *s.GroupId)
	}
	hs.do(a, http.MethodDelete, "/groups/"+b.Id, nil).want(t, http.StatusNotFound, "not_found")
	hs.do(a, http.MethodPatch, "/groups/"+b.Id, api.UpdateGroupRequest{Name: ptr("Gone")}).want(t, http.StatusNotFound, "not_found")
}

func TestGroupAndTagLimits(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	for i := range 100 {
		hs.createGroup(a, fmt.Sprintf("Group %03d", i))
	}
	res := hs.do(a, http.MethodPost, "/groups", api.CreateGroupRequest{Name: "One more"})
	res.want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
	if e := res.err(t); e.Target == nil || *e.Target != "name" || !strings.Contains(e.Message, "100") {
		t.Fatalf("group limit error %+v", e)
	}
	for i := range 200 {
		hs.createTag(a, fmt.Sprintf("tag-%03d", i))
	}
	hs.do(a, http.MethodPost, "/tags", api.CreateTagRequest{Name: "one-more"}).want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
	// Limits are per owner.
	hs.createGroup(hs.ownerB, "One more")
	hs.createTag(hs.ownerB, "one-more")
}

func TestTagsCRUD(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	var colours []int32
	var tags []api.Tag
	for i := range 10 {
		tg := hs.createTag(a, fmt.Sprintf("t%d", i))
		tags = append(tags, tg)
		colours = append(colours, tg.Colour)
	}
	if want := []int32{1, 2, 3, 4, 5, 6, 7, 8, 0, 0}; !slices.Equal(colours, want) {
		t.Fatalf("colours %v, want %v", colours, want)
	}
	hs.do(a, http.MethodDelete, "/tags/"+tags[3].Id, nil).want(t, http.StatusNoContent, "")
	if got := hs.createTag(a, "refill").Colour; got != 4 {
		t.Fatalf("refilled colour %d, want 4", got)
	}
	res := hs.do(a, http.MethodPost, "/tags", api.CreateTagRequest{Name: "chosen", Colour: ptr[api.TagColour](0)})
	res.want(t, http.StatusCreated, "")
	var chosen api.Tag
	res.decode(t, &chosen)
	if chosen.Colour != 0 {
		t.Fatalf("chosen colour %d", chosen.Colour)
	}

	res = hs.do(a, http.MethodPatch, "/tags/"+chosen.Id, api.UpdateTagRequest{Colour: ptr[api.TagColour](7)})
	res.want(t, http.StatusOK, "")
	res.decode(t, &chosen)
	if chosen.Colour != 7 || chosen.Name != "chosen" {
		t.Fatalf("recoloured %+v", chosen)
	}
	res = hs.do(a, http.MethodPatch, "/tags/"+chosen.Id, api.UpdateTagRequest{Name: ptr("picked")})
	res.want(t, http.StatusOK, "")
	res.decode(t, &chosen)
	if chosen.Colour != 7 || chosen.Name != "picked" {
		t.Fatalf("renamed %+v", chosen)
	}
	hs.do(a, http.MethodPatch, "/tags/"+chosen.Id, api.UpdateTagRequest{Name: ptr("t0")}).want(t, http.StatusConflict, "tag_name_taken")
	hs.do(a, http.MethodPost, "/tags", api.CreateTagRequest{Name: "t0"}).want(t, http.StatusConflict, "tag_name_taken")

	for _, bad := range []api.CreateTagRequest{
		{Name: ""}, {Name: "Upper"}, {Name: "under_score"}, {Name: "space here"}, {Name: strings.Repeat("a", 31)},
		{Name: "ok", Colour: ptr[api.TagColour](9)}, {Name: "ok", Colour: ptr[api.TagColour](-1)},
	} {
		hs.do(a, http.MethodPost, "/tags", bad).want(t, http.StatusBadRequest, "invalid_request")
	}
	hs.do(a, http.MethodPatch, "/tags/"+chosen.Id, api.UpdateTagRequest{Colour: ptr[api.TagColour](9)}).want(t, http.StatusBadRequest, "invalid_request")
	hs.createTag(a, strings.Repeat("a", 30))

	res = hs.do(a, http.MethodGet, "/tags", nil)
	res.want(t, http.StatusOK, "")
	var list api.TagList
	res.decode(t, &list)
	names := []string{}
	for _, tg := range list.Items {
		names = append(names, tg.Name)
	}
	if !slices.IsSorted(names) || len(names) != 12 {
		t.Fatalf("tag names %v, want 12 in order", names)
	}

	// Deleting a tag removes it from its sites.
	hs.createSite(a, "docs")
	hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{TagIds: []string{tags[0].Id, tags[1].Id}}).want(t, http.StatusOK, "")
	hs.do(a, http.MethodDelete, "/tags/"+tags[0].Id, nil).want(t, http.StatusNoContent, "")
	if s := hs.getSite(a, "docs"); !slices.Equal(s.TagIds, []string{tags[1].Id}) {
		t.Fatalf("site tags after delete %v", s.TagIds)
	}
}

func TestSiteOrganisation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	created := hs.createSite(a, "docs")
	if created.GroupId != nil || created.TagIds == nil || len(created.TagIds) != 0 || created.Pinned {
		t.Fatalf("new site %+v", created)
	}
	if res := hs.do(a, http.MethodGet, "/sites/docs", nil); !strings.Contains(string(res.body), `"tagIds":[]`) || !strings.Contains(string(res.body), `"pinned":false`) {
		t.Fatalf("site body %s lacks tagIds [] or pinned false", res.body)
	}
	g := hs.createGroup(a, "Clients")
	zed, alpha := hs.createTag(a, "zed"), hs.createTag(a, "alpha")

	res := hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{GroupId: &g.Id, TagIds: []string{zed.Id, alpha.Id}})
	res.want(t, http.StatusOK, "")
	var s api.Site
	res.decode(t, &s)
	if s.GroupId == nil || *s.GroupId != g.Id || !slices.Equal(s.TagIds, []string{alpha.Id, zed.Id}) || s.Name != "docs" {
		t.Fatalf("organised %+v", s)
	}
	// Every read of the site shows it.
	res = hs.do(a, http.MethodGet, "/sites", nil)
	var page api.SitePage
	res.decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].GroupId == nil || len(page.Items[0].TagIds) != 2 {
		t.Fatalf("listed %+v", page.Items)
	}
	act := hs.deploy(a, "docs", file{"index.html", "hi"})
	if act.Site.GroupId == nil || len(act.Site.TagIds) != 2 {
		t.Fatalf("activation site %+v", act.Site)
	}

	// Pin and unpin; neither touches the group or tags.
	res = hs.do(a, http.MethodPut, "/sites/docs/pin", nil)
	res.want(t, http.StatusOK, "")
	res.decode(t, &s)
	if !s.Pinned || s.GroupId == nil || len(s.TagIds) != 2 {
		t.Fatalf("pinned %+v", s)
	}
	hs.do(a, http.MethodPut, "/sites/docs/pin", nil).want(t, http.StatusOK, "")
	res = hs.do(a, http.MethodDelete, "/sites/docs/pin", nil)
	res.want(t, http.StatusOK, "")
	res.decode(t, &s)
	if s.Pinned {
		t.Fatalf("unpinned %+v", s)
	}

	// Replacing with nothing clears the group and tags.
	res = hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{TagIds: []string{}})
	res.want(t, http.StatusOK, "")
	s = api.Site{}
	res.decode(t, &s)
	if s.GroupId != nil || len(s.TagIds) != 0 {
		t.Fatalf("cleared %+v", s)
	}

	// Validation and limits.
	hs.do(a, http.MethodPut, "/sites/docs/organisation", map[string]any{}).want(t, http.StatusBadRequest, "invalid_request")
	hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{TagIds: []string{zed.Id, zed.Id}}).want(t, http.StatusBadRequest, "invalid_request")
	var many []string
	for i := range 21 {
		many = append(many, hs.createTag(a, fmt.Sprintf("many-%d", i)).Id)
	}
	res = hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{TagIds: many})
	res.want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
	if e := res.err(t); e.Target == nil || *e.Target != "tagIds" {
		t.Fatalf("limit target %+v", e)
	}
	hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{TagIds: many[:20]}).want(t, http.StatusOK, "")
	hs.do(a, http.MethodPut, "/sites/docs/organisation", api.SetSiteOrganisationRequest{GroupId: ptr(store.NewID()), TagIds: []string{}}).want(t, http.StatusNotFound, "not_found")
	hs.do(a, http.MethodPut, "/sites/missing/organisation", api.SetSiteOrganisationRequest{TagIds: []string{}}).want(t, http.StatusNotFound, "not_found")
	hs.do(a, http.MethodPut, "/sites/missing/pin", nil).want(t, http.StatusNotFound, "not_found")
}

func TestOrganiseSitesBulk(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	for _, name := range []string{"one", "two", "three"} {
		hs.createSite(a, name)
	}
	g1, g2 := hs.createGroup(a, "First"), hs.createGroup(a, "Second")
	red, blue := hs.createTag(a, "red"), hs.createTag(a, "blue")
	hs.do(a, http.MethodPut, "/sites/two/organisation", api.SetSiteOrganisationRequest{GroupId: &g2.Id, TagIds: []string{red.Id}}).want(t, http.StatusOK, "")
	hs.do(a, http.MethodPut, "/sites/two/pin", nil).want(t, http.StatusOK, "")

	res := hs.do(a, http.MethodPost, "/organise", api.OrganiseSitesRequest{
		Sites: []string{"two", "one"}, AddTagIds: &[]string{blue.Id}, RemoveTagIds: &[]string{red.Id}, GroupId: &g1.Id, Pinned: ptr(true),
	})
	res.want(t, http.StatusOK, "")
	var out api.OrganiseSitesResult
	res.decode(t, &out)
	if len(out.Prior) != 2 ||
		out.Prior[0].Site != "two" || out.Prior[0].GroupId == nil || *out.Prior[0].GroupId != g2.Id || !slices.Equal(out.Prior[0].TagIds, []string{red.Id}) || !out.Prior[0].Pinned ||
		out.Prior[1].Site != "one" || out.Prior[1].GroupId != nil || len(out.Prior[1].TagIds) != 0 || out.Prior[1].Pinned {
		t.Fatalf("prior %+v", out.Prior)
	}
	if !strings.Contains(string(res.body), `"tagIds":[]`) {
		t.Fatalf("prior body %s: an untagged site must answer tagIds []", res.body)
	}
	for _, name := range []string{"one", "two"} {
		s := hs.getSite(a, name)
		if s.GroupId == nil || *s.GroupId != g1.Id || !slices.Equal(s.TagIds, []string{blue.Id}) || !s.Pinned {
			t.Fatalf("%s after bulk %+v", name, s)
		}
	}
	if s := hs.getSite(a, "three"); s.GroupId != nil || len(s.TagIds) != 0 || s.Pinned {
		t.Fatalf("three changed %+v", s)
	}

	// Undo by sending the inverse for "one".
	hs.do(a, http.MethodPost, "/organise", api.OrganiseSitesRequest{
		Sites: []string{"one"}, RemoveTagIds: &[]string{blue.Id}, Ungroup: ptr(true), Pinned: ptr(false),
	}).want(t, http.StatusOK, "")
	if s := hs.getSite(a, "one"); s.GroupId != nil || len(s.TagIds) != 0 || s.Pinned {
		t.Fatalf("one after undo %+v", s)
	}

	// A missing site anywhere refuses the whole request and changes nothing.
	hs.do(a, http.MethodPost, "/organise", api.OrganiseSitesRequest{
		Sites: []string{"three", "missing"}, AddTagIds: &[]string{red.Id},
	}).want(t, http.StatusNotFound, "not_found")
	if s := hs.getSite(a, "three"); len(s.TagIds) != 0 {
		t.Fatalf("three after a refused bulk %+v", s)
	}

	for name, body := range map[string]api.OrganiseSitesRequest{
		"no sites":            {Sites: []string{}, Pinned: ptr(true)},
		"duplicate site":      {Sites: []string{"one", "one"}, Pinned: ptr(true)},
		"no change":           {Sites: []string{"one"}},
		"group and ungroup":   {Sites: []string{"one"}, GroupId: &g1.Id, Ungroup: ptr(true)},
		"add and remove":      {Sites: []string{"one"}, AddTagIds: &[]string{red.Id}, RemoveTagIds: &[]string{red.Id}},
		"duplicate added tag": {Sites: []string{"one"}, AddTagIds: &[]string{red.Id, red.Id}},
	} {
		if res := hs.do(a, http.MethodPost, "/organise", body); res.status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400; body %s", name, res.status, res.body)
		}
	}
	tooMany := make([]string, 501)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("s%d", i)
	}
	hs.do(a, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: tooMany, Pinned: ptr(true)}).want(t, http.StatusBadRequest, "invalid_request")
}

// TestOrganiseSitesPinnedAt checks the prior pin time is answered and that
// sending it back re-pins a site where it was, never in the future.
func TestOrganiseSitesPinnedAt(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "first")
	hs.createSite(a, "later")
	hs.do(a, http.MethodPut, "/sites/first/pin", nil).want(t, http.StatusOK, "")
	hs.advance(time.Hour)
	hs.do(a, http.MethodPut, "/sites/later/pin", nil).want(t, http.StatusOK, "")
	prior := func(body api.OrganiseSitesRequest) []api.SiteOrganisation {
		t.Helper()
		res := hs.do(a, http.MethodPost, "/organise", body)
		res.want(t, http.StatusOK, "")
		var out api.OrganiseSitesResult
		res.decode(t, &out)
		return out.Prior
	}

	// Unpinning answers when each site was pinned.
	p := prior(api.OrganiseSitesRequest{Sites: []string{"first", "later"}, Pinned: ptr(false)})
	if p[0].PinnedAt == nil || p[1].PinnedAt == nil || *p[1].PinnedAt-*p[0].PinnedAt < time.Hour.Milliseconds() {
		t.Fatalf("prior pins %+v", p)
	}
	pinned, laterPinned := *p[0].PinnedAt, *p[1].PinnedAt
	hs.advance(time.Hour)
	prior(api.OrganiseSitesRequest{Sites: []string{"first"}, Pinned: ptr(true), PinnedAt: &pinned})
	prior(api.OrganiseSitesRequest{Sites: []string{"later"}, Pinned: ptr(true)})
	// Pinning again changes nothing and answers each site's time: first is
	// back at its own, later was pinned anew.
	p = prior(api.OrganiseSitesRequest{Sites: []string{"first", "later"}, Pinned: ptr(true)})
	if *p[0].PinnedAt != pinned || *p[1].PinnedAt <= laterPinned {
		t.Fatalf("after re-pinning %d, %d; want %d, then after %d", *p[0].PinnedAt, *p[1].PinnedAt, pinned, laterPinned)
	}
	// An unpinned site answers no time.
	prior(api.OrganiseSitesRequest{Sites: []string{"first"}, Pinned: ptr(false)})
	p = prior(api.OrganiseSitesRequest{Sites: []string{"first"}, Pinned: ptr(false)})
	if p[0].Pinned || p[0].PinnedAt != nil {
		t.Fatalf("unpinned prior %+v", p)
	}

	future := hs.now().Add(time.Minute).UnixMilli()
	for name, body := range map[string]api.OrganiseSitesRequest{
		"without pinned": {Sites: []string{"first"}, PinnedAt: &pinned, AddTagIds: &[]string{}},
		"with unpin":     {Sites: []string{"first"}, PinnedAt: &pinned, Pinned: ptr(false)},
		"in the future":  {Sites: []string{"first"}, PinnedAt: &future, Pinned: ptr(true)},
	} {
		if res := hs.do(a, http.MethodPost, "/organise", body); res.status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400; body %s", name, res.status, res.body)
		}
	}
	if s := hs.getSite(a, "first"); s.Pinned {
		t.Fatalf("a refused pin changed first %+v", s)
	}
}

// TestOrganiseSitesBulkLimits fills 500 sites in one request, and checks a
// site going past 20 tags refuses the whole request.
func TestOrganiseSitesBulkLimits(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	names := make([]string, 500)
	for i := range names {
		names[i] = fmt.Sprintf("site-%03d", i)
		if err := hs.db.Sites().Create(t.Context(), store.Site{ID: store.NewID(), OwnerID: a, Name: names[i], CreatedAt: hs.now()}, store.Quotas{}); err != nil {
			t.Fatal(err)
		}
	}
	tag := hs.createTag(a, "bulk")
	res := hs.do(a, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: names, AddTagIds: &[]string{tag.Id}})
	res.want(t, http.StatusOK, "")
	var out api.OrganiseSitesResult
	res.decode(t, &out)
	if len(out.Prior) != 500 || out.Prior[499].Site != names[499] {
		t.Fatalf("prior has %d entries", len(out.Prior))
	}
	if s := hs.getSite(a, names[499]); !slices.Equal(s.TagIds, []string{tag.Id}) {
		t.Fatalf("last site tags %v", s.TagIds)
	}

	// names[1] already has 20 tags: adding one more refuses the lot.
	var twenty []string
	for i := range 19 {
		twenty = append(twenty, hs.createTag(a, fmt.Sprintf("full-%02d", i)).Id)
	}
	hs.do(a, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{names[1]}, AddTagIds: &twenty}).want(t, http.StatusOK, "")
	extra := hs.createTag(a, "extra")
	res = hs.do(a, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{names[0], names[1]}, AddTagIds: &[]string{extra.Id}})
	res.want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
	if e := res.err(t); e.Target == nil || *e.Target != "addTagIds" {
		t.Fatalf("limit target %+v", e)
	}
	if s := hs.getSite(a, names[0]); slices.Contains(s.TagIds, extra.Id) {
		t.Fatal("first site kept the tag from a refused request")
	}
}

// TestOrganisationOwnerIsolation checks that owner B cannot read, change,
// delete or refer to owner A's groups, tags and sites, and that each
// answers the same 404 as a missing one.
func TestOrganisationOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	hs.createSite(a, "secret")
	hs.createSite(b, "mine")
	g := hs.createGroup(a, "Private")
	tg := hs.createTag(a, "private")
	hs.do(a, http.MethodPut, "/sites/secret/organisation", api.SetSiteOrganisationRequest{GroupId: &g.Id, TagIds: []string{tg.Id}}).want(t, http.StatusOK, "")
	hs.do(a, http.MethodPut, "/sites/secret/pin", nil).want(t, http.StatusOK, "")

	missing := store.NewID()
	notFound := func(name string, foreign, absent response) {
		t.Helper()
		foreign.want(t, http.StatusNotFound, "not_found")
		absent.want(t, http.StatusNotFound, "not_found")
		if string(foreign.body) != string(absent.body) {
			t.Errorf("%s: foreign body %s, missing body %s", name, foreign.body, absent.body)
		}
	}
	notFound("update group",
		hs.do(b, http.MethodPatch, "/groups/"+g.Id, api.UpdateGroupRequest{Name: ptr("Mine now")}),
		hs.do(b, http.MethodPatch, "/groups/"+missing, api.UpdateGroupRequest{Name: ptr("Mine now")}))
	notFound("delete group", hs.do(b, http.MethodDelete, "/groups/"+g.Id, nil), hs.do(b, http.MethodDelete, "/groups/"+missing, nil))
	notFound("update tag",
		hs.do(b, http.MethodPatch, "/tags/"+tg.Id, api.UpdateTagRequest{Colour: ptr[api.TagColour](3)}),
		hs.do(b, http.MethodPatch, "/tags/"+missing, api.UpdateTagRequest{Colour: ptr[api.TagColour](3)}))
	notFound("delete tag", hs.do(b, http.MethodDelete, "/tags/"+tg.Id, nil), hs.do(b, http.MethodDelete, "/tags/"+missing, nil))
	notFound("organise foreign site",
		hs.do(b, http.MethodPut, "/sites/secret/organisation", api.SetSiteOrganisationRequest{TagIds: []string{}}),
		hs.do(b, http.MethodPut, "/sites/nothing/organisation", api.SetSiteOrganisationRequest{TagIds: []string{}}))
	notFound("own site, foreign group",
		hs.do(b, http.MethodPut, "/sites/mine/organisation", api.SetSiteOrganisationRequest{GroupId: &g.Id, TagIds: []string{}}),
		hs.do(b, http.MethodPut, "/sites/mine/organisation", api.SetSiteOrganisationRequest{GroupId: &missing, TagIds: []string{}}))
	notFound("own site, foreign tag",
		hs.do(b, http.MethodPut, "/sites/mine/organisation", api.SetSiteOrganisationRequest{TagIds: []string{tg.Id}}),
		hs.do(b, http.MethodPut, "/sites/mine/organisation", api.SetSiteOrganisationRequest{TagIds: []string{missing}}))
	notFound("pin", hs.do(b, http.MethodPut, "/sites/secret/pin", nil), hs.do(b, http.MethodPut, "/sites/nothing/pin", nil))
	notFound("unpin", hs.do(b, http.MethodDelete, "/sites/secret/pin", nil), hs.do(b, http.MethodDelete, "/sites/nothing/pin", nil))
	notFound("bulk foreign site",
		hs.do(b, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{"mine", "secret"}, Pinned: ptr(false)}),
		hs.do(b, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{"mine", "nothing"}, Pinned: ptr(false)}))
	notFound("bulk foreign group",
		hs.do(b, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{"mine"}, GroupId: &g.Id}),
		hs.do(b, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{"mine"}, GroupId: &missing}))
	notFound("bulk foreign tag",
		hs.do(b, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{"mine"}, RemoveTagIds: &[]string{tg.Id}}),
		hs.do(b, http.MethodPost, "/organise", api.OrganiseSitesRequest{Sites: []string{"mine"}, RemoveTagIds: &[]string{missing}}))

	// B's lists hold none of A's, and A's site is as A left it.
	for _, path := range []string{"/groups", "/tags"} {
		res := hs.do(b, http.MethodGet, path, nil)
		res.want(t, http.StatusOK, "")
		if string(res.body) != `{"items":[]}`+"\n" {
			t.Errorf("B's %s = %s, want empty", path, res.body)
		}
	}
	if s := hs.getSite(a, "secret"); s.GroupId == nil || *s.GroupId != g.Id || !slices.Equal(s.TagIds, []string{tg.Id}) || !s.Pinned {
		t.Fatalf("A's site after B's attempts %+v", s)
	}
	if s := hs.getSite(b, "mine"); s.GroupId != nil || len(s.TagIds) != 0 || s.Pinned {
		t.Fatalf("B's site changed %+v", s)
	}
	// A's names stay free for B.
	hs.createGroup(b, "Private")
	hs.createTag(b, "private")
}

// TestOrganisationTokenScopes checks reads take either scope, writes need
// manage, and a deploy token may set one site's group and tags by
// existing IDs but create nothing (V5.15).
func TestOrganisationTokenScopes(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	deploy := createToken(t, h, session, "CI", api.ScopeDeploy).Token
	manage := createToken(t, h, session, "Admin", api.ScopeManage).Token

	wantStatus(t, h.bearer(deploy, req{method: http.MethodPost, path: "/api/v0/sites", body: api.CreateSiteRequest{Name: "docs"}}), http.StatusCreated)
	rec := h.bearer(manage, req{method: http.MethodPost, path: "/api/v0/groups", body: api.CreateGroupRequest{Name: "Clients"}})
	wantStatus(t, rec, http.StatusCreated)
	var g api.Group
	decodeInto(t, rec, &g)
	rec = h.bearer(manage, req{method: http.MethodPost, path: "/api/v0/tags", body: api.CreateTagRequest{Name: "preview"}})
	wantStatus(t, rec, http.StatusCreated)
	var tg api.Tag
	decodeInto(t, rec, &tg)

	for _, tok := range []string{deploy, manage} {
		wantStatus(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/groups"}), http.StatusOK)
		wantStatus(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/tags"}), http.StatusOK)
	}
	// The deploy token files the site under existing ones...
	rec = h.bearer(deploy, req{method: http.MethodPut, path: "/api/v0/sites/docs/organisation", body: api.SetSiteOrganisationRequest{GroupId: &g.Id, TagIds: []string{tg.Id}}})
	wantStatus(t, rec, http.StatusOK)
	var s api.Site
	decodeInto(t, rec, &s)
	if s.GroupId == nil || *s.GroupId != g.Id || !slices.Equal(s.TagIds, []string{tg.Id}) {
		t.Fatalf("filed %+v", s)
	}
	wantError(t, h.bearer(deploy, req{method: http.MethodPut, path: "/api/v0/sites/docs/organisation", body: api.SetSiteOrganisationRequest{TagIds: []string{store.NewID()}}}), http.StatusNotFound, "not_found")
	// ...but creates, renames, deletes, pins and bulk changes need manage.
	for _, r := range []req{
		{method: http.MethodPost, path: "/api/v0/groups", body: api.CreateGroupRequest{Name: "New"}},
		{method: http.MethodPatch, path: "/api/v0/groups/" + g.Id, body: api.UpdateGroupRequest{Name: ptr("Renamed")}},
		{method: http.MethodDelete, path: "/api/v0/groups/" + g.Id},
		{method: http.MethodPost, path: "/api/v0/tags", body: api.CreateTagRequest{Name: "new"}},
		{method: http.MethodPatch, path: "/api/v0/tags/" + tg.Id, body: api.UpdateTagRequest{Colour: ptr[api.TagColour](2)}},
		{method: http.MethodDelete, path: "/api/v0/tags/" + tg.Id},
		{method: http.MethodPut, path: "/api/v0/sites/docs/pin"},
		{method: http.MethodDelete, path: "/api/v0/sites/docs/pin"},
		{method: http.MethodPost, path: "/api/v0/organise", body: api.OrganiseSitesRequest{Sites: []string{"docs"}, Pinned: ptr(true)}},
	} {
		wantError(t, h.bearer(deploy, r), http.StatusForbidden, "forbidden")
		if rec := h.bearer(manage, r); rec.Code == http.StatusForbidden {
			t.Errorf("%s %s with manage: 403", r.method, r.path)
		}
	}
	wantStatus(t, h.bearer(manage, req{method: http.MethodPut, path: "/api/v0/sites/docs/organisation", body: api.SetSiteOrganisationRequest{TagIds: []string{}}}), http.StatusOK)
}
