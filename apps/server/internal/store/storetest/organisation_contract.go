// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// newGroup returns a valid group; its key folds ASCII case, as the
// domain's key does for these names.
func newGroup(ownerID, name string) store.Group {
	return store.Group{ID: store.NewID(), OwnerID: ownerID, Name: name, NameKey: strings.ToLower(name), CreatedAt: now}
}

func (f *fixture) group(ownerID, name string) store.Group {
	f.t.Helper()
	g := newGroup(ownerID, name)
	f.must(f.r.Organisation.CreateGroup(f.t.Context(), store.ActorFor(g.OwnerID), g, 0))
	return g
}

func (f *fixture) tag(ownerID, name string) store.Tag {
	f.t.Helper()
	t, err := f.r.Organisation.CreateTag(f.t.Context(), store.ActorFor(store.Tag{ID: store.NewID(), OwnerID: ownerID, Name: name, Colour: store.AutoColour, CreatedAt: now}.OwnerID), store.Tag{ID: store.NewID(), OwnerID: ownerID, Name: name, Colour: store.AutoColour, CreatedAt: now}, 0)
	f.must(err)
	return t
}

// organisationOf returns the site's group, tags and pin through Sites.
func (f *fixture) organisationOf(ownerID string, s store.Site) (string, []string, time.Time) {
	f.t.Helper()
	got, err := f.r.Sites.Get(f.t.Context(), store.ActorFor(ownerID), s.ID)
	f.must(err)
	tags, err := f.r.Sites.TagIDs(f.t.Context(), store.ActorFor(ownerID), s.ID)
	f.must(err)
	return got.GroupID, tags[s.ID], got.PinnedAt
}

func organisationContract(t *testing.T, open Opener) {
	t.Run("Groups", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		b := f.group(o.ID, "Board")
		a := newGroup(o.ID, "archive")
		a.Note = "Old work"
		f.must(f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(a.OwnerID), a, 0))
		f.group(other.ID, "Archive") // keys are unique per owner only

		// Listed by key, so case does not decide the order.
		got, err := f.r.Organisation.ListGroups(t.Context(), store.ActorFor(o.ID), o.ID)
		f.must(err)
		if !slices.Equal(got, []store.Group{a, b}) {
			t.Fatalf("ListGroups() = %+v, want archive then Board", got)
		}
		// The key decides uniqueness, not the name as typed.
		wantErr(t, f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(o.ID), newGroup(o.ID, "Board"), 0), store.ErrConflict)
		wantErr(t, f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(o.ID), newGroup(o.ID, "BOARD"), 0), store.ErrConflict)

		// Renaming to another case of its own name keeps the key.
		a.Name, a.Note = "ARCHIVE", ""
		f.must(f.r.Organisation.UpdateGroup(t.Context(), store.ActorFor(a.OwnerID), a))
		if g, err := f.r.Organisation.GetGroup(t.Context(), store.ActorFor(o.ID), a.ID); err != nil || g != a {
			t.Fatalf("GetGroup() = %+v, %v; want %+v", g, err, a)
		}
		a.Name, a.NameKey = "Archived", "archived"
		f.must(f.r.Organisation.UpdateGroup(t.Context(), store.ActorFor(a.OwnerID), a))
		a.Name, a.NameKey = "board", "board"
		wantErr(t, f.r.Organisation.UpdateGroup(t.Context(), store.ActorFor(a.OwnerID), a), store.ErrConflict)
		a.Name, a.NameKey = "Archived", "archived"

		// Another owner's group is missing for every method.
		_, err = f.r.Organisation.GetGroup(t.Context(), store.ActorFor(other.ID), b.ID)
		wantErr(t, err, store.ErrNotFound)
		foreign := b
		foreign.OwnerID = other.ID
		wantErr(t, f.r.Organisation.UpdateGroup(t.Context(), store.ActorFor(foreign.OwnerID), foreign), store.ErrNotFound)
		wantErr(t, f.r.Organisation.DeleteGroup(t.Context(), store.ActorFor(other.ID), b.ID), store.ErrNotFound)
		wantErr(t, f.r.Organisation.DeleteGroup(t.Context(), store.ActorFor(o.ID), store.NewID()), store.ErrNotFound)
		f.must(f.r.Organisation.DeleteGroup(t.Context(), store.ActorFor(o.ID), b.ID))
		if got, _ := f.r.Organisation.ListGroups(t.Context(), store.ActorFor(o.ID), o.ID); len(got) != 1 {
			t.Fatalf("groups after delete = %+v, want one", got)
		}
	})
	t.Run("GroupLimit", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		for i := range 2 {
			f.must(f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(o.ID), newGroup(o.ID, fmt.Sprint(i)), 2))
		}
		wantErr(t, f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(o.ID), newGroup(o.ID, "x"), 2), store.ErrLimitReached)
		// Another owner's groups do not count.
		other := f.owner().ID
		f.must(f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(other), newGroup(other, "x"), 2))
		stranger := store.NewID()
		wantErr(t, f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(stranger), newGroup(stranger, "x"), 2), store.ErrConstraint)
		noKey := newGroup(o.ID, "y")
		noKey.NameKey = ""
		wantErr(t, f.r.Organisation.CreateGroup(t.Context(), store.ActorFor(noKey.OwnerID), noKey, 0), store.ErrConstraint)
	})
	t.Run("Tags", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		z := f.tag(o.ID, "zeta")
		a := f.tag(o.ID, "alpha")
		f.tag(other.ID, "alpha")
		got, err := f.r.Organisation.ListTags(t.Context(), store.ActorFor(o.ID), o.ID)
		f.must(err)
		if !slices.Equal(got, []store.Tag{a, z}) {
			t.Fatalf("ListTags() = %+v, want alpha then zeta", got)
		}
		_, err = f.r.Organisation.CreateTag(t.Context(), store.ActorFor(store.Tag{ID: store.NewID(), OwnerID: o.ID, Name: "zeta", Colour: 3, CreatedAt: now}.OwnerID), store.Tag{ID: store.NewID(), OwnerID: o.ID, Name: "zeta", Colour: 3, CreatedAt: now}, 0)
		wantErr(t, err, store.ErrConflict)
		a.Name, a.Colour = "beta", 0
		f.must(f.r.Organisation.UpdateTag(t.Context(), store.ActorFor(a.OwnerID), a))
		if tg, err := f.r.Organisation.GetTag(t.Context(), store.ActorFor(o.ID), a.ID); err != nil || tg != a {
			t.Fatalf("GetTag() = %+v, %v; want %+v", tg, err, a)
		}
		a.Name = "zeta"
		wantErr(t, f.r.Organisation.UpdateTag(t.Context(), store.ActorFor(a.OwnerID), a), store.ErrConflict)
		_, err = f.r.Organisation.GetTag(t.Context(), store.ActorFor(other.ID), z.ID)
		wantErr(t, err, store.ErrNotFound)
		foreign := z
		foreign.OwnerID = other.ID
		wantErr(t, f.r.Organisation.UpdateTag(t.Context(), store.ActorFor(foreign.OwnerID), foreign), store.ErrNotFound)
		wantErr(t, f.r.Organisation.DeleteTag(t.Context(), store.ActorFor(other.ID), z.ID), store.ErrNotFound)
		f.must(f.r.Organisation.DeleteTag(t.Context(), store.ActorFor(o.ID), z.ID))
		wantErr(t, f.r.Organisation.DeleteTag(t.Context(), store.ActorFor(o.ID), z.ID), store.ErrNotFound)
		for _, bad := range []store.Tag{
			{Name: "nine", Colour: 9},
			{Name: "minus", Colour: -2},
			{Name: "Upper", Colour: 1},
			{Name: strings.Repeat("a", 31), Colour: 1},
		} {
			bad.ID, bad.OwnerID, bad.CreatedAt = store.NewID(), o.ID, now
			_, err := f.r.Organisation.CreateTag(t.Context(), store.ActorFor(bad.OwnerID), bad, 0)
			wantErr(t, err, store.ErrConstraint)
		}
	})
	t.Run("TagColours", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		var tags []store.Tag
		for i := range 10 {
			tags = append(tags, f.tag(o.ID, fmt.Sprintf("t%d", i)))
		}
		var colours []int
		for _, tg := range tags {
			colours = append(colours, tg.Colour)
		}
		if want := []int{1, 2, 3, 4, 5, 6, 7, 8, 0, 0}; !slices.Equal(colours, want) {
			t.Fatalf("colours = %v, want %v", colours, want)
		}
		// A freed slot is taken again, lowest first.
		f.must(f.r.Organisation.DeleteTag(t.Context(), store.ActorFor(o.ID), tags[5].ID))
		f.must(f.r.Organisation.DeleteTag(t.Context(), store.ActorFor(o.ID), tags[2].ID))
		if got := f.tag(o.ID, "next").Colour; got != 3 {
			t.Fatalf("colour after freeing 3 and 6 = %d, want 3", got)
		}
		// Recolouring frees a slot too, and a chosen colour is kept.
		tags[0].Colour = 0
		f.must(f.r.Organisation.UpdateTag(t.Context(), store.ActorFor(tags[0].OwnerID), tags[0]))
		if got := f.tag(o.ID, "after-recolour").Colour; got != 1 {
			t.Fatalf("colour after recolouring slot 1 = %d, want 1", got)
		}
		chosen, err := f.r.Organisation.CreateTag(t.Context(), store.ActorFor(store.Tag{ID: store.NewID(), OwnerID: o.ID, Name: "chosen", Colour: 2, CreatedAt: now}.OwnerID), store.Tag{ID: store.NewID(), OwnerID: o.ID, Name: "chosen", Colour: 2, CreatedAt: now}, 0)
		if err != nil || chosen.Colour != 2 {
			t.Fatalf("chosen colour = %d, %v; want 2", chosen.Colour, err)
		}
		// Another owner starts at 1.
		if got := f.tag(f.owner().ID, "first").Colour; got != 1 {
			t.Fatalf("another owner's first colour = %d, want 1", got)
		}
	})
	t.Run("TagLimit", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		newTag := func(name string) error {
			_, err := f.r.Organisation.CreateTag(t.Context(), store.ActorFor(store.Tag{ID: store.NewID(), OwnerID: o.ID, Name: name, Colour: store.AutoColour, CreatedAt: now}.OwnerID), store.Tag{ID: store.NewID(), OwnerID: o.ID, Name: name, Colour: store.AutoColour, CreatedAt: now}, 2)
			return err
		}
		f.must(newTag("a"))
		f.must(newTag("b"))
		wantErr(t, newTag("c"), store.ErrLimitReached)
	})
	t.Run("OrganiseSetsAndReturnsPrior", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s1, s2 := f.site(o.ID), f.site(o.ID)
		g := f.group(o.ID, "Clients")
		red, blue := f.tag(o.ID, "red"), f.tag(o.ID, "blue")

		prior, err := f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s2.Name, s1.Name}, store.OrganiseChange{
			SetGroup: true, GroupID: g.ID, AddTagIDs: []string{red.ID, blue.ID}, SetPinned: true, PinnedAt: now,
		})
		f.must(err)
		want := []store.SiteOrganisation{
			{SiteID: s2.ID, SiteName: s2.Name, TagIDs: []string{}},
			{SiteID: s1.ID, SiteName: s1.Name, TagIDs: []string{}},
		}
		if !organisationsEqual(prior, want) {
			t.Fatalf("prior = %+v, want %+v", prior, want)
		}
		for _, s := range []store.Site{s1, s2} {
			group, tags, pinned := f.organisationOf(o.ID, s)
			if group != g.ID || !slices.Equal(tags, []string{blue.ID, red.ID}) || !pinned.Equal(now) {
				t.Fatalf("%s = %q %v %v; want group, blue then red, pinned", s.Name, group, tags, pinned)
			}
		}

		// Pinning again keeps the first time; removing and ungrouping.
		prior, err = f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s1.Name}, store.OrganiseChange{
			SetGroup: true, RemoveTagIDs: []string{red.ID}, SetPinned: true, PinnedAt: now.Add(time.Hour),
		})
		f.must(err)
		want = []store.SiteOrganisation{{SiteID: s1.ID, SiteName: s1.Name, GroupID: g.ID, TagIDs: []string{blue.ID, red.ID}, PinnedAt: now}}
		if !organisationsEqual(prior, want) {
			t.Fatalf("prior = %+v, want %+v", prior, want)
		}
		group, tags, pinned := f.organisationOf(o.ID, s1)
		if group != "" || !slices.Equal(tags, []string{blue.ID}) || !pinned.Equal(now) {
			t.Fatalf("after = %q %v %v; want ungrouped, blue, pinned at first time", group, tags, pinned)
		}

		// SetTags replaces; unpinning clears.
		_, err = f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s1.Name}, store.OrganiseChange{
			SetTags: true, TagIDs: []string{red.ID}, SetPinned: true,
		})
		f.must(err)
		if _, tags, pinned := f.organisationOf(o.ID, s1); !slices.Equal(tags, []string{red.ID}) || !pinned.IsZero() {
			t.Fatalf("after replace = %v %v; want red, unpinned", tags, pinned)
		}
		_, err = f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s1.Name}, store.OrganiseChange{SetTags: true})
		f.must(err)
		if _, tags, _ := f.organisationOf(o.ID, s1); len(tags) != 0 {
			t.Fatalf("after clearing = %v; want none", tags)
		}
	})
	t.Run("OrganiseIsAtomic", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		s := f.site(o.ID)
		g := f.group(o.ID, "Mine")
		tg := f.tag(o.ID, "mine")
		foreignSite := f.site(other.ID)
		foreignGroup := f.group(other.ID, "Theirs")
		foreignTag := f.tag(other.ID, "theirs")
		for name, tc := range map[string]struct {
			sites []string
			c     store.OrganiseChange
			want  error
		}{
			"missing site":        {[]string{s.Name, "missing"}, store.OrganiseChange{SetGroup: true, GroupID: g.ID}, store.ErrNotFound},
			"foreign site":        {[]string{s.Name, foreignSite.Name}, store.OrganiseChange{AddTagIDs: []string{tg.ID}}, store.ErrNotFound},
			"foreign group":       {[]string{s.Name}, store.OrganiseChange{SetGroup: true, GroupID: foreignGroup.ID, AddTagIDs: []string{tg.ID}}, store.ErrNotFound},
			"foreign tag":         {[]string{s.Name}, store.OrganiseChange{AddTagIDs: []string{tg.ID, foreignTag.ID}}, store.ErrNotFound},
			"foreign removed tag": {[]string{s.Name}, store.OrganiseChange{SetGroup: true, GroupID: g.ID, RemoveTagIDs: []string{foreignTag.ID}}, store.ErrNotFound},
			"foreign set tag":     {[]string{s.Name}, store.OrganiseChange{SetTags: true, TagIDs: []string{foreignTag.ID}}, store.ErrNotFound},
			"missing tag":         {[]string{s.Name}, store.OrganiseChange{AddTagIDs: []string{store.NewID()}}, store.ErrNotFound},
		} {
			_, err := f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, tc.sites, tc.c)
			if err == nil {
				t.Fatalf("%s: no error, want %v", name, tc.want)
			}
			wantErr(t, err, tc.want)
		}
		if group, tags, pinned := f.organisationOf(o.ID, s); group != "" || len(tags) != 0 || !pinned.IsZero() {
			t.Fatalf("site after refused changes = %q %v %v; want unchanged", group, tags, pinned)
		}
		if group, tags, _ := f.organisationOf(other.ID, foreignSite); group != "" || len(tags) != 0 {
			t.Fatalf("other owner's site changed: %q %v", group, tags)
		}

		// Going past the per-site limit on the second site rolls back the
		// first.
		second := f.site(o.ID)
		more := f.tag(o.ID, "more")
		_, err := f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{second.Name}, store.OrganiseChange{AddTagIDs: []string{more.ID}})
		f.must(err)
		_, err = f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s.Name, second.Name}, store.OrganiseChange{
			SetGroup: true, GroupID: g.ID, AddTagIDs: []string{tg.ID}, MaxTagsPerSite: 1,
		})
		wantErr(t, err, store.ErrLimitReached)
		if group, tags, _ := f.organisationOf(o.ID, s); group != "" || len(tags) != 0 {
			t.Fatalf("first site after the limit = %q %v; want unchanged", group, tags)
		}
	})
	t.Run("SiteTagIDs", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		s1, s2, bare := f.site(o.ID), f.site(o.ID), f.site(o.ID)
		b, a := f.tag(o.ID, "b"), f.tag(o.ID, "a")
		_, err := f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s1.Name, s2.Name}, store.OrganiseChange{AddTagIDs: []string{b.ID, a.ID}})
		f.must(err)
		got, err := f.r.Sites.TagIDs(t.Context(), store.ActorFor(o.ID), s1.ID, s2.ID, bare.ID, store.NewID())
		f.must(err)
		if len(got) != 2 || !slices.Equal(got[s1.ID], []string{a.ID, b.ID}) || !slices.Equal(got[s2.ID], []string{a.ID, b.ID}) {
			t.Fatalf("TagIDs() = %v; want a then b on the two tagged sites only", got)
		}
		if got, err := f.r.Sites.TagIDs(t.Context(), store.ActorFor(other.ID), s1.ID); err != nil || len(got) != 0 {
			t.Fatalf("another owner's TagIDs() = %v, %v; want none", got, err)
		}
		// Deleting a tag removes it from its sites; deleting a site, its rows.
		f.must(f.r.Organisation.DeleteTag(t.Context(), store.ActorFor(o.ID), a.ID))
		f.must(f.r.Sites.Delete(t.Context(), store.ActorFor(o.ID), s1.ID))
		got, err = f.r.Sites.TagIDs(t.Context(), store.ActorFor(o.ID), s1.ID, s2.ID)
		f.must(err)
		if len(got) != 1 || !slices.Equal(got[s2.ID], []string{b.ID}) {
			t.Fatalf("TagIDs() after deletes = %v; want b on s2 only", got)
		}
	})
	t.Run("DeleteGroupUngroups", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		g := f.group(o.ID, "Short-lived")
		_, err := f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s.Name}, store.OrganiseChange{SetGroup: true, GroupID: g.ID})
		f.must(err)
		f.must(f.r.Organisation.DeleteGroup(t.Context(), store.ActorFor(o.ID), g.ID))
		if group, _, _ := f.organisationOf(o.ID, s); group != "" {
			t.Fatalf("group after delete = %q, want none", group)
		}
	})
	t.Run("TearDownRemovesTagRows", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		tg := f.tag(o.ID, "evidence")
		_, err := f.r.Organisation.Organise(t.Context(), store.ActorFor(o.ID), o.ID, []string{s.Name}, store.OrganiseChange{AddTagIDs: []string{tg.ID}})
		f.must(err)
		f.must(f.r.Sites.TakeDown(t.Context(), s.ID, now))
		f.must(f.r.Sites.TearDown(t.Context(), s.ID))
		got, err := f.r.Sites.TagIDs(t.Context(), store.ActorFor(o.ID), s.ID)
		if err != nil || len(got) != 0 {
			t.Fatalf("TagIDs() after teardown = %v, %v; want none", got, err)
		}
		if _, err := f.r.Organisation.GetTag(t.Context(), store.ActorFor(o.ID), tg.ID); err != nil {
			t.Fatalf("tag after teardown: %v; want it kept", err)
		}
	})
}

func organisationsEqual(a, b []store.SiteOrganisation) bool {
	return slices.EqualFunc(a, b, func(x, y store.SiteOrganisation) bool {
		return x.SiteID == y.SiteID && x.SiteName == y.SiteName && x.GroupID == y.GroupID &&
			slices.Equal(x.TagIDs, y.TagIDs) && x.PinnedAt.Equal(y.PinnedAt)
	})
}
