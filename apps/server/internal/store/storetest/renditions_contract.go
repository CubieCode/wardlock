// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func renditionsContract(t *testing.T, open Opener) {
	// pages creates a deploy with HTML and other files.
	pages := func(f *fixture, ownerID, siteID string) store.Deploy {
		f.t.Helper()
		return f.deployFiles(ownerID, siteID,
			store.DeployFile{Path: "index.html", SHA256: sha('a'), Size: 5},
			store.DeployFile{Path: "docs/Guide.HTM", SHA256: sha('b'), Size: 5},
			store.DeployFile{Path: "about.html", SHA256: sha('a'), Size: 5},
			store.DeployFile{Path: "site.css", SHA256: sha('c'), Size: 5},
			store.DeployFile{Path: "index.html.gz", SHA256: sha('d'), Size: 5},
			store.DeployFile{Path: "html", SHA256: sha('e'), Size: 5})
	}
	paths := func(fs []store.DeployFile) []string {
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, f.Path)
		}
		return out
	}

	t.Run("Sources", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		s := f.site(a.ID)
		d := pages(f, a.ID, s.ID)
		got, err := f.r.Renditions.Sources(t.Context(), a.ID, s.ID, d.ID)
		wantErr(t, err, nil)
		if want := []string{"about.html", "docs/Guide.HTM", "index.html"}; !slices.Equal(paths(got), want) {
			t.Fatalf("sources = %v, want %v", paths(got), want)
		}
		if got[0].SHA256 != sha('a') || got[0].Size != 5 {
			t.Fatalf("source = %+v", got[0])
		}
		// Another owner, another site and a missing deploy look the same.
		other := f.site(a.ID)
		for _, tc := range []struct{ owner, site, deploy string }{
			{b.ID, s.ID, d.ID}, {a.ID, other.ID, d.ID}, {a.ID, s.ID, store.NewID()},
		} {
			_, err := f.r.Renditions.Sources(t.Context(), tc.owner, tc.site, tc.deploy)
			wantErr(t, err, store.ErrNotFound)
		}
	})
	t.Run("PutAndList", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		s := f.site(a.ID)
		d := pages(f, a.ID, s.ID)
		empty, err := f.r.Renditions.List(t.Context(), d.ID)
		wantErr(t, err, nil)
		if len(empty) != 0 {
			t.Fatalf("renditions before Put = %v", empty)
		}
		rs := []store.Rendition{
			{Path: "index.html", Format: "txt", SHA256: sha('1'), Size: 3},
			{Path: "index.html", Format: "md", SHA256: sha('2'), Size: 4},
			{Path: "about.html", Format: "md", SHA256: sha('2'), Size: 4},
		}
		wantErr(t, f.r.Renditions.Put(t.Context(), a.ID, s.ID, d.ID, rs, now), nil)
		// Putting again replaces what changed.
		rs[0].SHA256, rs[0].Size = sha('3'), 6
		wantErr(t, f.r.Renditions.Put(t.Context(), a.ID, s.ID, d.ID, rs[:1], now.Add(time.Hour)), nil)
		got, err := f.r.Renditions.List(t.Context(), d.ID)
		wantErr(t, err, nil)
		want := []store.Rendition{rs[2], rs[1], rs[0]}
		if !slices.Equal(got, want) {
			t.Fatalf("renditions = %v, want %v", got, want)
		}
		// Each hash has a blob record, from the first Put that stored it.
		cutoff := now.Add(2 * time.Hour)
		blobs, err := f.r.GC.UnreferencedBlobs(t.Context(), cutoff, "", 10)
		wantErr(t, err, nil)
		if len(blobs) != 1 || blobs[0].SHA256 != sha('1') || blobs[0].Size != 3 || !blobs[0].CreatedAt.Equal(now) {
			t.Fatalf("unreferenced = %+v, want only the replaced %s", blobs, sha('1'))
		}

		// Another owner, or another site, cannot add renditions.
		wantErr(t, f.r.Renditions.Put(t.Context(), b.ID, s.ID, d.ID, rs, now), store.ErrNotFound)
		wantErr(t, f.r.Renditions.Put(t.Context(), a.ID, f.site(a.ID).ID, d.ID, rs, now), store.ErrNotFound)
		// A path outside the manifest, or a bad format, fails, and nothing
		// from that Put is kept.
		for _, bad := range []store.Rendition{
			{Path: "missing.html", Format: "md", SHA256: sha('4'), Size: 1},
			{Path: "about.html", Format: "pdf", SHA256: sha('4'), Size: 1},
			{Path: "about.html", Format: "txt", SHA256: "NOT-A-HASH", Size: 1},
		} {
			good := store.Rendition{Path: "about.html", Format: "txt", SHA256: sha('5'), Size: 1}
			wantErr(t, f.r.Renditions.Put(t.Context(), a.ID, s.ID, d.ID, []store.Rendition{good, bad}, now), store.ErrConstraint)
		}
		got, err = f.r.Renditions.List(t.Context(), d.ID)
		wantErr(t, err, nil)
		if !slices.Equal(got, want) {
			t.Fatalf("renditions after failed Puts = %v, want %v", got, want)
		}

		// Deleting the deploy deletes its renditions.
		wantErr(t, f.r.Deploys.Delete(t.Context(), a.ID, d.ID), nil)
		got, err = f.r.Renditions.List(t.Context(), d.ID)
		wantErr(t, err, nil)
		if len(got) != 0 {
			t.Fatalf("renditions of a deleted deploy = %v", got)
		}
	})
}
