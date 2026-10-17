// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"testing"

	"wardlock.nz/server/internal/store"
)

func accessLogContract(t *testing.T, open Opener) {
	valid := func() store.AccessEntry {
		return store.AccessEntry{
			ID: store.NewID(), OccurredAt: now, SiteID: store.NewID(), Host: "docs.wardlock.localhost", Path: "/",
			Status: 200, Via: store.ViaLink, LinkPublicID: "p000000001", IP: "192.0.2.0", UserAgent: "curl/8",
		}
	}
	t.Run("Append", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(*fixture) store.AccessEntry { return valid() },
			func(f *fixture, e store.AccessEntry) error {
				return f.r.AccessLog.Append(f.t.Context(), []store.AccessEntry{e})
			},
			[]insertCase[store.AccessEntry]{
				{name: "link for a site that no longer exists"},
				{name: "denied", mutate: func(_ *fixture, e *store.AccessEntry) { e.Via, e.LinkPublicID, e.Status = store.ViaNone, "", 401 }},
				{name: "link without public ID", mutate: func(_ *fixture, e *store.AccessEntry) { e.LinkPublicID = "" }, want: store.ErrConstraint},
				{name: "public ID without link", mutate: func(_ *fixture, e *store.AccessEntry) { e.Via = store.ViaPublic }, want: store.ErrConstraint},
				{name: "unknown via", mutate: func(_ *fixture, e *store.AccessEntry) { e.Via = "cookie" }, want: store.ErrConstraint},
				{name: "bad status", mutate: func(_ *fixture, e *store.AccessEntry) { e.Status = 0 }, want: store.ErrConstraint},
			})
	})
	t.Run("AppendIsAtomic", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		good, bad := valid(), valid()
		bad.Via = "cookie"
		wantErr(t, f.r.AccessLog.Append(t.Context(), []store.AccessEntry{good, bad}), store.ErrConstraint)
		// The good entry was rolled back with the batch, so its ID is free.
		wantErr(t, f.r.AccessLog.Append(t.Context(), []store.AccessEntry{good, valid()}), nil)
	})
}
