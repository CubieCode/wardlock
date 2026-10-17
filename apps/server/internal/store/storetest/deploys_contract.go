// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"

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
			func(f *fixture, v input) error { return f.r.Deploys.Create(f.t.Context(), v.ownerID, v.d, v.files) },
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
		wantErr(t, f.r.Deploys.Create(t.Context(), o.ID, d, bad), store.ErrConstraint)
		// Nothing was kept, so the same ID inserts cleanly.
		wantErr(t, f.r.Deploys.Create(t.Context(), o.ID, d, bad[:1]), nil)
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
}
