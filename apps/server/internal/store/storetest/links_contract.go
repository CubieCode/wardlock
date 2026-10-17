// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func linksContract(t *testing.T, open Opener) {
	type owned struct {
		ownerID string
		l       store.ShareLink
	}
	runInsertCases(t, open,
		func(f *fixture) owned {
			o := f.owner()
			return owned{o.ID, f.validLink(f.site(o.ID).ID)}
		},
		func(f *fixture, v owned) error { return f.r.Links.Create(f.t.Context(), v.ownerID, v.l) },
		[]insertCase[owned]{
			{name: "never expires"},
			{name: "one-time, scoped, expiring", mutate: func(_ *fixture, v *owned) {
				v.l.MaxUses, v.l.PathPrefix, v.l.ExpiresAt = 1, "/docs/", now.Add(7*24*time.Hour)
			}},
			{name: "used up", mutate: func(_ *fixture, v *owned) { v.l.MaxUses, v.l.UseCount = 2, 2 }},
			{name: "200-character label", mutate: func(_ *fixture, v *owned) { v.l.Label = strings.Repeat("l", 200) }},
			{name: "duplicate public ID on another owner's site", mutate: func(f *fixture, v *owned) {
				o := f.owner()
				other := f.validLink(f.site(o.ID).ID)
				other.PublicID = v.l.PublicID
				f.must(f.r.Links.Create(f.t.Context(), o.ID, other))
			}, want: store.ErrConflict},
			{name: "another owner's site", mutate: func(f *fixture, v *owned) { v.ownerID = f.owner().ID }, want: store.ErrNotFound},
			{name: "short public ID", mutate: func(_ *fixture, v *owned) { v.l.PublicID = "abc" }, want: store.ErrConstraint},
			{name: "201-character label", mutate: func(_ *fixture, v *owned) { v.l.Label = strings.Repeat("l", 201) }, want: store.ErrConstraint},
			{name: "uses beyond limit", mutate: func(_ *fixture, v *owned) { v.l.MaxUses, v.l.UseCount = 1, 2 }, want: store.ErrConstraint},
			{name: "negative limit", mutate: func(_ *fixture, v *owned) { v.l.MaxUses = -1 }, want: store.ErrConstraint},
			{name: "relative prefix", mutate: func(_ *fixture, v *owned) { v.l.PathPrefix = "docs/" }, want: store.ErrConstraint},
			{name: "expires at creation", mutate: func(_ *fixture, v *owned) { v.l.ExpiresAt = v.l.CreatedAt }, want: store.ErrConstraint},
		})
}
