// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"testing"

	"wardlock.nz/server/internal/store"
)

func policiesContract(t *testing.T, open Opener) {
	type owned struct {
		ownerID string
		p       store.Policy
	}
	runInsertCases(t, open,
		func(f *fixture) owned {
			o := f.owner()
			return owned{o.ID, f.validPolicy(f.site(o.ID).ID)}
		},
		func(f *fixture, v owned) error { return f.r.Policies.Create(f.t.Context(), v.ownerID, v.p) },
		[]insertCase[owned]{
			{name: "valid"},
			{name: "public", mutate: func(_ *fixture, v *owned) { v.p.Visibility, v.p.LinksEnabled = store.Public, false }},
			{name: "password", mutate: func(_ *fixture, v *owned) { v.p.PasswordEnabled, v.p.PasswordHash = true, "$argon2id$stand-in" }},
			{name: "same prefix on another site", mutate: func(f *fixture, v *owned) { f.policy(v.ownerID, f.site(v.ownerID).ID) }},
			{name: "path policy beside default", mutate: func(f *fixture, v *owned) {
				f.policy(v.ownerID, v.p.SiteID)
				v.p.PathPrefix = "/docs/"
			}},
			{name: "duplicate prefix", mutate: func(f *fixture, v *owned) { f.policy(v.ownerID, v.p.SiteID) }, want: store.ErrConflict},
			{name: "another owner's site", mutate: func(f *fixture, v *owned) { v.ownerID = f.owner().ID }, want: store.ErrNotFound},
			{name: "public with methods", mutate: func(_ *fixture, v *owned) { v.p.Visibility = store.Public }, want: store.ErrConstraint},
			{name: "password without hash", mutate: func(_ *fixture, v *owned) { v.p.PasswordEnabled = true }, want: store.ErrConstraint},
			{name: "relative prefix", mutate: func(_ *fixture, v *owned) { v.p.PathPrefix = "docs/" }, want: store.ErrConstraint},
			{name: "unknown visibility", mutate: func(_ *fixture, v *owned) { v.p.Visibility = "private" }, want: store.ErrConstraint},
			{name: "version zero", mutate: func(_ *fixture, v *owned) { v.p.Version = 0 }, want: store.ErrConstraint},
		})
}
