// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func gcContract(t *testing.T, open Opener) {
	// deployAt creates a deploy of one file with hash h at now+at.
	deployAt := func(f *fixture, ownerID, siteID string, at time.Duration, h string) store.Deploy {
		f.t.Helper()
		d := f.validDeploy(siteID)
		d.CreatedAt = now.Add(at)
		f.must(f.r.Deploys.Create(f.t.Context(), store.ActorFor(ownerID), d, []store.DeployFile{{Path: "index.html", SHA256: h, Size: 5}}, store.Quotas{}))
		return d
	}
	activate := func(f *fixture, ownerID, siteID string, d store.Deploy) {
		f.t.Helper()
		f.uploadAll(ownerID, siteID, d.ID)
		_, err := f.r.Deploys.Activate(f.t.Context(), store.ActorFor(ownerID), siteID, d.ID, d.CreatedAt)
		f.must(err)
	}
	ids := func(ds []store.PrunableDeploy) []string {
		out := make([]string, 0, len(ds))
		for _, d := range ds {
			out = append(out, d.ID)
		}
		return out
	}
	hashes := func(bs []store.Blob) []string {
		out := make([]string, 0, len(bs))
		for _, b := range bs {
			out = append(out, b.SHA256)
		}
		return out
	}

	t.Run("PrunableDeploys", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := make([]store.Deploy, 6)
		for i := range d {
			d[i] = deployAt(f, o.ID, s.ID, time.Duration(i)*time.Second, sha('a'))
		}
		// d0 is activated last, so although it is the oldest it is active:
		// beyond the limit yet kept. d1 was replaced; d2 is pending and old;
		// d3 is pending and young (created at the cutoff).
		activate(f, o.ID, s.ID, d[1])
		activate(f, o.ID, s.ID, d[0])
		pendingBefore := d[3].CreatedAt
		// Another site with few deploys has nothing to prune.
		small := f.site(o.ID)
		deployAt(f, o.ID, small.ID, 0, sha('a'))

		got, err := f.r.GC.PrunableDeploys(t.Context(), 2, pendingBefore, store.DeployCursor{}, 10)
		wantErr(t, err, nil)
		if want := []string{d[1].ID, d[2].ID}; !slices.Equal(ids(got), want) {
			t.Fatalf("prunable = %v, want %v", ids(got), want)
		}
		if got[0].SiteID != s.ID || !got[0].CreatedAt.Equal(d[1].CreatedAt) {
			t.Fatalf("prunable[0] = %+v", got[0])
		}
		// Paging resumes after the cursor.
		page, err := f.r.GC.PrunableDeploys(t.Context(), 2, pendingBefore, store.DeployCursor{}, 1)
		wantErr(t, err, nil)
		next, err := f.r.GC.PrunableDeploys(t.Context(), 2, pendingBefore, store.DeployCursor{CreatedAt: page[0].CreatedAt, ID: page[0].ID}, 1)
		wantErr(t, err, nil)
		if !slices.Equal(ids(page), []string{d[1].ID}) || !slices.Equal(ids(next), []string{d[2].ID}) {
			t.Fatalf("pages = %v, %v", ids(page), ids(next))
		}
		// A limit covering every deploy prunes nothing.
		got, err = f.r.GC.PrunableDeploys(t.Context(), 6, pendingBefore, store.DeployCursor{}, 10)
		wantErr(t, err, nil)
		if len(got) != 0 {
			t.Fatalf("prunable with keep 6 = %v", ids(got))
		}
	})
	t.Run("DeletePrunableDeploy", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := deployAt(f, o.ID, s.ID, 0, sha('a'))
		replaced := deployAt(f, o.ID, s.ID, time.Second, sha('b'))
		young := deployAt(f, o.ID, s.ID, 2*time.Second, sha('c'))
		activate(f, o.ID, s.ID, replaced)
		activate(f, o.ID, s.ID, active)

		for _, tc := range []struct {
			name string
			id   string
			want bool
		}{
			{"active", active.ID, false},
			{"young pending", young.ID, false},
			{"unknown", store.NewID(), false},
			{"replaced", replaced.ID, true},
			{"already deleted", replaced.ID, false},
		} {
			deleted, err := f.r.GC.DeletePrunableDeploy(t.Context(), tc.id, young.CreatedAt)
			wantErr(t, err, nil)
			if deleted != tc.want {
				t.Fatalf("%s: deleted = %v, want %v", tc.name, deleted, tc.want)
			}
		}
		_, err := f.r.Deploys.Get(t.Context(), store.ActorFor(o.ID), s.ID, replaced.ID)
		wantErr(t, err, store.ErrNotFound)
		files, err := f.r.Deploys.Files(t.Context(), replaced.ID)
		wantErr(t, err, nil)
		if len(files) != 0 {
			t.Fatalf("manifest outlived its deploy: %v", files)
		}
		// Once older than the cutoff, a pending deploy goes too.
		deleted, err := f.r.GC.DeletePrunableDeploy(t.Context(), young.ID, young.CreatedAt.Add(time.Millisecond))
		wantErr(t, err, nil)
		if !deleted {
			t.Fatal("old pending deploy was kept")
		}
	})
	// previewAt creates a deploy for preview name at now+at and activates
	// it as that preview, with a lifetime of days.
	previewAt := func(f *fixture, ownerID, siteID, name string, at time.Duration, days int) store.Deploy {
		f.t.Helper()
		d := f.validDeploy(siteID)
		d.CreatedAt, d.Preview, d.PreviewDays = now.Add(at), name, &days
		f.must(f.r.Deploys.Create(f.t.Context(), store.ActorFor(ownerID), d, []store.DeployFile{{Path: "index.html", SHA256: sha('d'), Size: 5}}, store.Quotas{}))
		f.uploadAll(ownerID, siteID, d.ID)
		d, err := f.r.Deploys.Activate(f.t.Context(), store.ActorFor(ownerID), siteID, d.ID, d.CreatedAt)
		f.must(err)
		return d
	}
	t.Run("PrunableDeploysWithPreviews", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		live := deployAt(f, o.ID, s.ID, 0, sha('a'))
		activate(f, o.ID, s.ID, live)
		// Two previews replaced in turn, then a third serving: the replaced
		// ones are not history and go at once; the serving one stays.
		first := previewAt(f, o.ID, s.ID, "pr-1", time.Second, 14)
		second := previewAt(f, o.ID, s.ID, "pr-1", 2*time.Second, 14)
		serving := previewAt(f, o.ID, s.ID, "pr-1", 3*time.Second, 14)
		// A pending preview deploy inside the grace period is kept too.
		young := f.validDeploy(s.ID)
		young.CreatedAt, young.Preview = now.Add(4*time.Second), "pr-2"
		f.must(f.r.Deploys.Create(t.Context(), store.ActorFor(o.ID), young, []store.DeployFile{{Path: "index.html", SHA256: sha('e'), Size: 5}}, store.Quotas{}))

		got, err := f.r.GC.PrunableDeploys(t.Context(), 1, young.CreatedAt, store.DeployCursor{}, 10)
		wantErr(t, err, nil)
		if want := []string{first.ID, second.ID}; !slices.Equal(ids(got), want) {
			t.Fatalf("prunable = %v, want the replaced previews %v", ids(got), want)
		}
		// Preview deploys never count towards the history limit: with keep
		// 1 the live deploy has no newer history and stays.
		for _, d := range []store.Deploy{serving, young, live} {
			if deleted, err := f.r.GC.DeletePrunableDeploy(t.Context(), d.ID, young.CreatedAt); err != nil || deleted {
				t.Fatalf("DeletePrunableDeploy(%s) = %t, %v; want kept", d.ID, deleted, err)
			}
		}
		// An older live deploy beyond the limit goes, though newer preview
		// deploys exist.
		older := deployAt(f, o.ID, s.ID, -time.Second, sha('f'))
		activate(f, o.ID, s.ID, older)
		_, _, err = f.r.Deploys.Rollback(t.Context(), store.ActorFor(o.ID), s.ID, live.ID, now.Add(5*time.Second))
		wantErr(t, err, nil)
		got, err = f.r.GC.PrunableDeploys(t.Context(), 1, young.CreatedAt, store.DeployCursor{}, 10)
		wantErr(t, err, nil)
		if want := []string{older.ID, first.ID, second.ID}; !slices.Equal(ids(got), want) {
			t.Fatalf("prunable = %v, want %v", ids(got), want)
		}
		// Promoted, a preview deploy is history like any other, and the
		// preview still serves it.
		_, _, err = f.r.Previews.Promote(t.Context(), store.ActorFor(o.ID), s.ID, "pr-1", now.Add(5*time.Second))
		wantErr(t, err, nil)
		if deleted, err := f.r.GC.DeletePrunableDeploy(t.Context(), serving.ID, young.CreatedAt); err != nil || deleted {
			t.Fatalf("DeletePrunableDeploy(promoted) = %t, %v; want kept", deleted, err)
		}
		wantErr(t, f.r.Previews.Delete(t.Context(), store.ActorFor(o.ID), s.ID, "pr-1", now), nil)
		activate(f, o.ID, s.ID, deployAt(f, o.ID, s.ID, 6*time.Second, sha('1')))
		got, err = f.r.GC.PrunableDeploys(t.Context(), 1, young.CreatedAt, store.DeployCursor{}, 10)
		wantErr(t, err, nil)
		if !slices.Contains(ids(got), serving.ID) || !slices.Contains(ids(got), live.ID) {
			t.Fatalf("prunable = %v, want the promoted deploy and the live one beyond the limit", ids(got))
		}
	})
	t.Run("DeleteExpiredPreviews", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		short := previewAt(f, o.ID, s.ID, "short", 0, 1)
		previewAt(f, o.ID, s.ID, "long", 0, 30)
		previewAt(f, o.ID, s.ID, "never", 0, 0)
		// A taken-down site's and a suspended owner's expired previews are
		// evidence and kept.
		down := f.site(o.ID)
		previewAt(f, o.ID, down.ID, "short", 0, 1)
		f.must(f.r.Sites.TakeDown(t.Context(), down.ID, now))
		suspended := f.owner()
		theirs := f.site(suspended.ID)
		previewAt(f, suspended.ID, theirs.ID, "short", 0, 1)
		f.must(f.r.Owners.Suspend(t.Context(), suspended.ID, now))

		n, err := f.r.GC.DeleteExpiredPreviews(t.Context(), now.Add(day-time.Millisecond))
		if err != nil || n != 0 {
			t.Fatalf("DeleteExpiredPreviews() before expiry = %d, %v", n, err)
		}
		n, err = f.r.GC.DeleteExpiredPreviews(t.Context(), now.Add(day))
		if err != nil || n != 1 {
			t.Fatalf("DeleteExpiredPreviews() = %d, %v; want 1", n, err)
		}
		left, err := f.r.Previews.List(t.Context(), store.ActorFor(o.ID), s.ID, "", 10, now)
		wantErr(t, err, nil)
		if want := []string{"long", "never"}; !slices.Equal(previewNames(left), want) {
			t.Fatalf("previews left = %v, want %v", previewNames(left), want)
		}
		for _, k := range []struct{ owner, site string }{{o.ID, down.ID}, {suspended.ID, theirs.ID}} {
			if _, err := f.r.Previews.Get(t.Context(), store.ActorFor(k.owner), k.site, "short", now); err != nil {
				t.Fatalf("kept preview of %s: %v", k.site, err)
			}
		}
		// Its deploy is then prunable.
		got, err := f.r.GC.PrunableDeploys(t.Context(), 50, now, store.DeployCursor{}, 10)
		wantErr(t, err, nil)
		if !slices.Equal(ids(got), []string{short.ID}) {
			t.Fatalf("prunable = %v, want the expired preview's deploy", ids(got))
		}
	})
	t.Run("UnreferencedBlobs", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		s := f.site(a.ID)
		cutoff := now.Add(time.Hour)
		record := func(ownerID, h string, at time.Time) {
			f.must(f.r.Deploys.RecordUpload(t.Context(), store.ActorFor(ownerID), ownerID, store.Blob{SHA256: h, Size: 7, CreatedAt: at}))
		}
		deployAt(f, a.ID, s.ID, 0, sha('a'))
		record(a.ID, sha('a'), now)    // referenced
		record(a.ID, sha('b'), now)    // garbage
		record(a.ID, sha('c'), cutoff) // too young
		record(a.ID, sha('d'), now)    // old, but owner B claimed it since
		record(b.ID, sha('d'), cutoff)
		record(b.ID, sha('e'), now) // garbage
		f.must(f.r.Deploys.PutBlob(t.Context(), store.Blob{SHA256: sha('f'), Size: 3, CreatedAt: now}))

		got, err := f.r.GC.UnreferencedBlobs(t.Context(), cutoff, "", 10)
		wantErr(t, err, nil)
		if want := []string{sha('b'), sha('e'), sha('f')}; !slices.Equal(hashes(got), want) {
			t.Fatalf("unreferenced = %v, want %v", hashes(got), want)
		}
		if got[0].Size != 7 || !got[0].CreatedAt.Equal(now) {
			t.Fatalf("blob = %+v", got[0])
		}
		got, err = f.r.GC.UnreferencedBlobs(t.Context(), cutoff, sha('b'), 1)
		wantErr(t, err, nil)
		if !slices.Equal(hashes(got), []string{sha('e')}) {
			t.Fatalf("page after b = %v", hashes(got))
		}
	})
	t.Run("RenditionsKeepBlobs", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		cutoff := now.Add(time.Hour)
		d := deployAt(f, o.ID, s.ID, 0, sha('a'))
		rs := []store.Rendition{{Path: "index.html", Format: "md", SHA256: sha('b'), Size: 4}}
		f.must(f.r.Renditions.Put(t.Context(), store.ActorFor(o.ID), s.ID, d.ID, rs, now))
		got, err := f.r.GC.UnreferencedBlobs(t.Context(), cutoff, "", 10)
		wantErr(t, err, nil)
		if len(got) != 0 {
			t.Fatalf("unreferenced = %v, want none while a rendition lists b", hashes(got))
		}
		_, deleted, err := f.r.GC.DeleteBlob(t.Context(), sha('b'), cutoff, func(context.Context) error { return nil })
		wantErr(t, err, nil)
		if deleted {
			t.Fatal("a rendition's blob was deleted")
		}
		// Once the deploy goes, so does the reference.
		wantErr(t, f.r.Deploys.Delete(t.Context(), store.ActorFor(o.ID), d.ID), nil)
		got, err = f.r.GC.UnreferencedBlobs(t.Context(), cutoff, "", 10)
		wantErr(t, err, nil)
		if !slices.Equal(hashes(got), []string{sha('b')}) {
			t.Fatalf("unreferenced after deleting the deploy = %v", hashes(got))
		}
	})
	t.Run("DeleteBlob", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		s := f.site(a.ID)
		cutoff := now.Add(time.Hour)
		for _, o := range []string{a.ID, b.ID} {
			for _, h := range []string{sha('a'), sha('b'), sha('c')} {
				f.must(f.r.Deploys.RecordUpload(t.Context(), store.ActorFor(o), o, store.Blob{SHA256: h, Size: 9, CreatedAt: now}))
			}
		}
		deployAt(f, a.ID, s.ID, 0, sha('a'))

		for _, tc := range []struct {
			name       string
			h          string
			removeErr  error
			wantCalled bool
			want       bool
		}{
			{"referenced", sha('a'), nil, false, false},
			{"unknown", sha('9'), nil, false, false},
			{"remove fails", sha('c'), errBoom, true, false},
			{"garbage", sha('b'), nil, true, true},
			{"already deleted", sha('b'), nil, false, false},
		} {
			called := false
			size, deleted, err := f.r.GC.DeleteBlob(t.Context(), tc.h, cutoff, func(context.Context) error {
				called = true
				return tc.removeErr
			})
			wantErr(t, err, tc.removeErr)
			if called != tc.wantCalled || deleted != tc.want || (tc.want && size != 9) {
				t.Fatalf("%s: called %v, deleted %v, size %d", tc.name, called, deleted, size)
			}
		}
		// The failed removal rolled back: c is still garbage, and both
		// owners still hold it.
		got, err := f.r.GC.UnreferencedBlobs(t.Context(), cutoff, "", 10)
		wantErr(t, err, nil)
		if !slices.Equal(hashes(got), []string{sha('c')}) {
			t.Fatalf("unreferenced after deletes = %v", hashes(got))
		}
		// Deleting b removed every owner's claim with it, so a new deploy
		// listing b needs it uploaded by either owner; a and c are still
		// held by both.
		for _, o := range []string{a.ID, b.ID} {
			site := f.site(o)
			d := f.deployFiles(o, site.ID,
				store.DeployFile{Path: "a", SHA256: sha('a'), Size: 9},
				store.DeployFile{Path: "b", SHA256: sha('b'), Size: 9},
				store.DeployFile{Path: "c", SHA256: sha('c'), Size: 9})
			missing, err := f.r.Deploys.MissingBlobs(t.Context(), store.ActorFor(o), site.ID, d.ID)
			wantErr(t, err, nil)
			if !slices.Equal(missing, []string{sha('b')}) {
				t.Fatalf("missing for owner = %v, want only b", missing)
			}
		}
	})
	t.Run("DeleteUnrecorded", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		f.must(f.r.Deploys.RecordUpload(t.Context(), store.ActorFor(o.ID), o.ID, store.Blob{SHA256: sha('a'), Size: 1, CreatedAt: now}))
		for _, tc := range []struct {
			name      string
			h         string
			removeErr error
			want      bool
		}{
			{"recorded", sha('a'), nil, false},
			{"remove fails", sha('b'), errBoom, false},
			{"unrecorded", sha('b'), nil, true},
		} {
			called := false
			deleted, err := f.r.GC.DeleteUnrecorded(t.Context(), tc.h, func(context.Context) error {
				called = true
				return tc.removeErr
			})
			wantErr(t, err, tc.removeErr)
			if deleted != tc.want || called == (tc.h == sha('a')) {
				t.Fatalf("%s: called %v, deleted %v", tc.name, called, deleted)
			}
		}
	})
	gcOperatorContract(t, open)
}

var errBoom = errors.New("boom")
