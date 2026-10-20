// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func deploysContract(t *testing.T, open Opener) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		type input struct {
			ownerID string
			d       store.Deploy
			files   []store.DeployFile
		}
		runInsertCases(t, open,
			func(f *fixture) input {
				o := f.owner()
				return input{o.ID, f.validDeploy(f.site(o.ID).ID), []store.DeployFile{
					{Path: "index.html", SHA256: sha('a'), Size: 5},
					{Path: "assets/app.js", SHA256: sha('b'), Size: 0},
				}}
			},
			func(f *fixture, v input) error {
				return f.r.Deploys.Create(f.t.Context(), v.ownerID, v.d, v.files, store.Quotas{})
			},
			[]insertCase[input]{
				{name: "valid"},
				{name: "empty manifest with message", mutate: func(_ *fixture, v *input) {
					v.files, v.d.FileCount, v.d.TotalSize, v.d.Message = nil, 0, 0, "first deploy"
				}},
				{name: "another owner's site", mutate: func(f *fixture, v *input) { v.ownerID = f.owner().ID }, want: store.ErrNotFound},
				{name: "unknown site", mutate: func(_ *fixture, v *input) { v.d.SiteID = store.NewID() }, want: store.ErrNotFound},
				{name: "duplicate path", mutate: func(_ *fixture, v *input) { v.files[1].Path = v.files[0].Path }, want: store.ErrConflict},
				{name: "leading slash", mutate: func(_ *fixture, v *input) { v.files[0].Path = "/index.html" }, want: store.ErrConstraint},
				{name: "empty path", mutate: func(_ *fixture, v *input) { v.files[0].Path = "" }, want: store.ErrConstraint},
				{name: "short hash", mutate: func(_ *fixture, v *input) { v.files[0].SHA256 = "abc" }, want: store.ErrConstraint},
				{name: "uppercase hash", mutate: func(_ *fixture, v *input) { v.files[0].SHA256 = sha('A') }, want: store.ErrConstraint},
				{name: "negative size", mutate: func(_ *fixture, v *input) { v.files[0].Size = -1 }, want: store.ErrConstraint},
				{name: "negative file count", mutate: func(_ *fixture, v *input) { v.d.FileCount = -1 }, want: store.ErrConstraint},
				{name: "long message", mutate: func(_ *fixture, v *input) { v.d.Message = strings.Repeat("m", 501) }, want: store.ErrConstraint},
			})
	})
	t.Run("CreateIsAtomic", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		d := f.validDeploy(f.site(o.ID).ID)
		bad := []store.DeployFile{{Path: "index.html", SHA256: sha('a')}, {Path: "/bad", SHA256: sha('a')}}
		wantErr(t, f.r.Deploys.Create(t.Context(), o.ID, d, bad, store.Quotas{}), store.ErrConstraint)
		// Nothing was kept, so the same ID inserts cleanly.
		wantErr(t, f.r.Deploys.Create(t.Context(), o.ID, d, bad[:1], store.Quotas{}), nil)
	})
	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.deploy(o.ID, s.ID)
		old := f.deploy(o.ID, s.ID)
		f.must(f.r.Sites.SetActiveDeploy(t.Context(), o.ID, s.ID, active.ID))

		wantErr(t, f.r.Deploys.Delete(t.Context(), o.ID, active.ID), store.ErrConstraint)
		wantErr(t, f.r.Deploys.Delete(t.Context(), f.owner().ID, old.ID), store.ErrNotFound)
		wantErr(t, f.r.Deploys.Delete(t.Context(), o.ID, old.ID), nil)
		wantErr(t, f.r.Deploys.Delete(t.Context(), o.ID, old.ID), store.ErrNotFound)
	})
	t.Run("PutBlob", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(*fixture) store.Blob { return store.Blob{SHA256: sha('c'), Size: 10, CreatedAt: now} },
			func(f *fixture, b store.Blob) error { return f.r.Deploys.PutBlob(f.t.Context(), b) },
			[]insertCase[store.Blob]{
				{name: "valid"},
				{name: "already recorded", mutate: func(f *fixture, b *store.Blob) { f.must(f.r.Deploys.PutBlob(f.t.Context(), *b)) }},
				{name: "short hash", mutate: func(_ *fixture, b *store.Blob) { b.SHA256 = "c" }, want: store.ErrConstraint},
				{name: "uppercase hash", mutate: func(_ *fixture, b *store.Blob) { b.SHA256 = sha('C') }, want: store.ErrConstraint},
				{name: "negative size", mutate: func(_ *fixture, b *store.Blob) { b.Size = -1 }, want: store.ErrConstraint},
			})
	})
	t.Run("Get", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		want := f.validDeploy(s.ID)
		want.Message, want.SPA, want.ImmutableAssets = "first", true, true
		f.must(f.r.Deploys.Create(t.Context(), o.ID, want, []store.DeployFile{{Path: "index.html", SHA256: sha('a'), Size: 5}}, store.Quotas{}))
		got, err := f.r.Deploys.Get(t.Context(), o.ID, s.ID, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("Get() = %+v, want %+v", got, want)
		}
		_, err = f.r.Deploys.Get(t.Context(), f.owner().ID, s.ID, want.ID)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Deploys.Get(t.Context(), o.ID, f.site(o.ID).ID, want.ID)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Deploys.Get(t.Context(), o.ID, s.ID, store.NewID())
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("List", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		flags := map[string]bool{} // "<id>/spa" and "<id>/immutable" to the flag created
		for i := range 4 {
			d := f.validDeploy(s.ID)
			d.CreatedAt = now.Add(time.Duration(i%2) * time.Minute) // ties broken by ID
			d.SPA, d.ImmutableAssets = i%2 == 0, i < 2
			flags[d.ID+"/spa"], flags[d.ID+"/immutable"] = d.SPA, d.ImmutableAssets
			f.must(f.r.Deploys.Create(t.Context(), o.ID, d, nil, store.Quotas{}))
		}
		all, err := f.r.Deploys.List(t.Context(), o.ID, s.ID, store.DeployCursor{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 4 {
			t.Fatalf("List() returned %d deploys, want 4", len(all))
		}
		for _, d := range all {
			if d.SPA != flags[d.ID+"/spa"] || d.ImmutableAssets != flags[d.ID+"/immutable"] {
				t.Errorf("List() deploy %s SPA %v ImmutableAssets %v; want %v %v",
					d.ID, d.SPA, d.ImmutableAssets, flags[d.ID+"/spa"], flags[d.ID+"/immutable"])
			}
		}
		for i := 1; i < len(all); i++ {
			a, b := all[i-1], all[i]
			if a.CreatedAt.Before(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID < b.ID) {
				t.Fatalf("List() not newest first at %d: %+v before %+v", i, a, b)
			}
		}
		var paged []store.Deploy
		cursor := store.DeployCursor{}
		for {
			page, err := f.r.Deploys.List(t.Context(), o.ID, s.ID, cursor, 3)
			if err != nil {
				t.Fatal(err)
			}
			paged = append(paged, page...)
			if len(page) < 3 {
				break
			}
			last := page[len(page)-1]
			cursor = store.DeployCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		if !slices.Equal(paged, all) {
			t.Fatalf("paged List() = %+v, want %+v", paged, all)
		}
		other, err := f.r.Deploys.List(t.Context(), f.owner().ID, s.ID, store.DeployCursor{}, 10)
		if err != nil || len(other) != 0 {
			t.Fatalf("List(other owner) = %v, %v; want none", other, err)
		}
	})
	t.Run("Files", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		want := []store.DeployFile{
			{Path: "assets/app.js", SHA256: sha('b'), Size: 3},
			{Path: "index.html", SHA256: sha('a'), Size: 5},
			{Path: "copy.html", SHA256: sha('a'), Size: 5},
		}
		d := f.deployFiles(o.ID, s.ID, want...)
		got, err := f.r.Deploys.Files(t.Context(), d.ID)
		if err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(want, func(a, b store.DeployFile) int { return strings.Compare(a.Path, b.Path) })
		if !slices.Equal(got, want) {
			t.Fatalf("Files() = %+v, want %+v", got, want)
		}
		got, err = f.r.Deploys.Files(t.Context(), store.NewID())
		if err != nil || len(got) != 0 {
			t.Fatalf("Files(unknown) = %v, %v; want none", got, err)
		}
	})
	t.Run("HashesAndManifestSize", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.deployFiles(o.ID, s.ID,
			store.DeployFile{Path: "a.html", SHA256: sha('a'), Size: 5},
			store.DeployFile{Path: "b.html", SHA256: sha('a'), Size: 5},
			store.DeployFile{Path: "c.js", SHA256: sha('c'), Size: 7},
		)
		got, err := f.r.Deploys.Hashes(t.Context(), o.ID, s.ID, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, []string{sha('a'), sha('c')}) {
			t.Fatalf("Hashes() = %v", got)
		}
		_, err = f.r.Deploys.Hashes(t.Context(), f.owner().ID, s.ID, d.ID)
		wantErr(t, err, store.ErrNotFound)

		size, err := f.r.Deploys.ManifestSize(t.Context(), o.ID, s.ID, d.ID, sha('c'))
		if err != nil || size != 7 {
			t.Fatalf("ManifestSize() = %d, %v; want 7", size, err)
		}
		_, err = f.r.Deploys.ManifestSize(t.Context(), o.ID, s.ID, d.ID, sha('d'))
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Deploys.ManifestSize(t.Context(), f.owner().ID, s.ID, d.ID, sha('c'))
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Deploys.ManifestSize(t.Context(), o.ID, f.site(o.ID).ID, d.ID, sha('c'))
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("RecordUpload", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) ownedValue[store.Blob] {
				return ownedValue[store.Blob]{f.owner().ID, store.Blob{SHA256: sha('c'), Size: 10, CreatedAt: now}}
			},
			func(f *fixture, v ownedValue[store.Blob]) error {
				return f.r.Deploys.RecordUpload(f.t.Context(), v.ownerID, v.v)
			},
			[]insertCase[ownedValue[store.Blob]]{
				{name: "valid"},
				{name: "already recorded by this owner", mutate: func(f *fixture, v *ownedValue[store.Blob]) {
					f.must(f.r.Deploys.RecordUpload(f.t.Context(), v.ownerID, v.v))
				}},
				{name: "already recorded by another owner", mutate: func(f *fixture, v *ownedValue[store.Blob]) {
					f.must(f.r.Deploys.RecordUpload(f.t.Context(), f.owner().ID, v.v))
				}},
				{name: "unknown owner", mutate: func(_ *fixture, v *ownedValue[store.Blob]) { v.ownerID = store.NewID() }, want: store.ErrConstraint},
				{name: "short hash", mutate: func(_ *fixture, v *ownedValue[store.Blob]) { v.v.SHA256 = "c" }, want: store.ErrConstraint},
				{name: "uppercase hash", mutate: func(_ *fixture, v *ownedValue[store.Blob]) { v.v.SHA256 = sha('C') }, want: store.ErrConstraint},
				{name: "non-hex hash", mutate: func(_ *fixture, v *ownedValue[store.Blob]) { v.v.SHA256 = sha('z') }, want: store.ErrConstraint},
			})
	})
	t.Run("MissingBlobsIsPerOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		sa, sb := f.site(a.ID), f.site(b.ID)
		files := []store.DeployFile{
			{Path: "index.html", SHA256: sha('a'), Size: 5},
			{Path: "copy.html", SHA256: sha('a'), Size: 5},
			{Path: "app.js", SHA256: sha('b'), Size: 5},
		}
		da := f.deployFiles(a.ID, sa.ID, files...)
		db := f.deployFiles(b.ID, sb.ID, files...)
		missing := func(ownerID, siteID, deployID string) []string {
			t.Helper()
			m, err := f.r.Deploys.MissingBlobs(t.Context(), ownerID, siteID, deployID)
			if err != nil {
				t.Fatal(err)
			}
			return m
		}
		if got := missing(a.ID, sa.ID, da.ID); !slices.Equal(got, []string{sha('a'), sha('b')}) {
			t.Fatalf("MissingBlobs(A) = %v", got)
		}
		f.must(f.r.Deploys.RecordUpload(t.Context(), a.ID, store.Blob{SHA256: sha('a'), Size: 5, CreatedAt: now}))
		if got := missing(a.ID, sa.ID, da.ID); !slices.Equal(got, []string{sha('b')}) {
			t.Fatalf("MissingBlobs(A) after upload = %v", got)
		}
		// A's upload proves nothing about B.
		if got := missing(b.ID, sb.ID, db.ID); !slices.Equal(got, []string{sha('a'), sha('b')}) {
			t.Fatalf("MissingBlobs(B) = %v", got)
		}
		_, err := f.r.Deploys.MissingBlobs(t.Context(), b.ID, sa.ID, da.ID)
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("Activate", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		d := f.deploy(o.ID, s.ID)
		at := now.Add(time.Minute)

		_, err := f.r.Deploys.Activate(t.Context(), o.ID, s.ID, d.ID, at)
		wantErr(t, err, store.ErrBlobsMissing)
		f.uploadAll(o.ID, s.ID, d.ID)
		_, err = f.r.Deploys.Activate(t.Context(), f.owner().ID, s.ID, d.ID, at)
		wantErr(t, err, store.ErrNotFound)
		_, err = f.r.Deploys.Activate(t.Context(), o.ID, f.site(o.ID).ID, d.ID, at)
		wantErr(t, err, store.ErrNotFound)

		got, err := f.r.Deploys.Activate(t.Context(), o.ID, s.ID, d.ID, at)
		if err != nil {
			t.Fatal(err)
		}
		if !got.ActivatedAt.Equal(at) {
			t.Fatalf("ActivatedAt = %v, want %v", got.ActivatedAt, at)
		}
		site, err := f.r.Sites.Get(t.Context(), o.ID, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if site.ActiveDeployID != d.ID {
			t.Fatalf("ActiveDeployID = %q, want %q", site.ActiveDeployID, d.ID)
		}
		_, err = f.r.Deploys.Activate(t.Context(), o.ID, s.ID, d.ID, at)
		wantErr(t, err, store.ErrDeployNotPending)
	})
	t.Run("ActivateAnotherOwnersBlob", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		f.must(f.r.Deploys.RecordUpload(t.Context(), a.ID, store.Blob{SHA256: sha('a'), Size: 5, CreatedAt: now}))
		sb := f.site(b.ID)
		d := f.deploy(b.ID, sb.ID) // lists sha('a'), which only A uploaded
		_, err := f.r.Deploys.Activate(t.Context(), b.ID, sb.ID, d.ID, now)
		wantErr(t, err, store.ErrBlobsMissing)
	})
	t.Run("ConcurrentActivations", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		const n = 8
		ids := make([]string, n)
		for i := range ids {
			d := f.deploy(o.ID, s.ID)
			f.uploadAll(o.ID, s.ID, d.ID)
			ids[i] = d.ID
		}
		// Each deploy activated twice at once: exactly one of each pair wins.
		errs := make(chan error, 2*n)
		var wg sync.WaitGroup
		for i := range 2 * n {
			wg.Go(func() {
				_, err := f.r.Deploys.Activate(t.Context(), o.ID, s.ID, ids[i%n], now.Add(time.Duration(i)*time.Millisecond))
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		var ok, notPending int
		for err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, store.ErrDeployNotPending):
				notPending++
			default:
				t.Fatalf("Activate() error = %v", err)
			}
		}
		if ok != n || notPending != n {
			t.Fatalf("%d activations succeeded and %d were not pending, want %d each", ok, notPending, n)
		}
		site, err := f.r.Sites.Get(t.Context(), o.ID, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(ids, site.ActiveDeployID) {
			t.Fatalf("ActiveDeployID = %q, not one of the activated deploys", site.ActiveDeployID)
		}
		active, err := f.r.Deploys.Get(t.Context(), o.ID, s.ID, site.ActiveDeployID)
		if err != nil {
			t.Fatal(err)
		}
		if !active.ActivatedAt.Equal(site.ActivatedAt) {
			t.Fatalf("site ActivatedAt %v does not match its deploy's %v", site.ActivatedAt, active.ActivatedAt)
		}
	})
	t.Run("Rollback", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := func() string {
			t.Helper()
			site, err := f.r.Sites.Get(t.Context(), o.ID, s.ID)
			if err != nil {
				t.Fatal(err)
			}
			return site.ActiveDeployID
		}
		_, _, err := f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, "", now)
		wantErr(t, err, store.ErrNoPreviousDeploy)
		d1 := f.activated(o.ID, s.ID, now.Add(1*time.Minute))
		_, _, err = f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, "", now)
		wantErr(t, err, store.ErrNoPreviousDeploy)
		d2 := f.activated(o.ID, s.ID, now.Add(2*time.Minute))
		d3 := f.activated(o.ID, s.ID, now.Add(3*time.Minute))
		pending := f.deploy(o.ID, s.ID)

		// Without an ID: the deploy active before the current one.
		got, changed, err := f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, "", now.Add(4*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if !changed || got.ID != d2.ID || active() != d2.ID || !got.ActivatedAt.Equal(now.Add(4*time.Minute)) {
			t.Fatalf("Rollback() = %+v, active %s; want %s", got, active(), d2.ID)
		}
		// Again: back to d3, which was active before d2.
		got, changed, err = f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, "", now.Add(5*time.Minute))
		if err != nil || !changed || got.ID != d3.ID {
			t.Fatalf("second Rollback() = %+v, %v; want %s", got, err, d3.ID)
		}
		// Any earlier deploy by ID.
		got, changed, err = f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, d1.ID, now.Add(6*time.Minute))
		if err != nil || !changed || got.ID != d1.ID || active() != d1.ID {
			t.Fatalf("Rollback(d1) = %+v, %v; active %s", got, err, active())
		}
		// The active deploy: no change.
		got, changed, err = f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, d1.ID, now.Add(7*time.Minute))
		if err != nil || changed || got.ID != d1.ID || !got.ActivatedAt.Equal(now.Add(6*time.Minute)) {
			t.Fatalf("Rollback(active) = %+v, %v; want unchanged", got, err)
		}
		_, _, err = f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, pending.ID, now)
		wantErr(t, err, store.ErrDeployNotActivated)
		_, _, err = f.r.Deploys.Rollback(t.Context(), o.ID, s.ID, store.NewID(), now)
		wantErr(t, err, store.ErrNotFound)
		_, _, err = f.r.Deploys.Rollback(t.Context(), f.owner().ID, s.ID, d2.ID, now)
		wantErr(t, err, store.ErrNotFound)
		_, _, err = f.r.Deploys.Rollback(t.Context(), f.owner().ID, s.ID, "", now)
		wantErr(t, err, store.ErrNotFound)
		if active() != d1.ID {
			t.Fatalf("failed rollbacks changed the active deploy to %s", active())
		}
	})
}

// ownedValue pairs a value with the owner it is written for.
type ownedValue[T any] struct {
	ownerID string
	v       T
}
