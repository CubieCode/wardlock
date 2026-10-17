// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"

	"wardlock.nz/server/internal/store"
)

func domainsContract(t *testing.T, open Opener) {
	type owned struct {
		ownerID string
		d       store.Domain
	}
	runInsertCases(t, open,
		func(f *fixture) owned {
			o := f.owner()
			return owned{o.ID, f.validDomain(f.site(o.ID).ID)}
		},
		func(f *fixture, v owned) error { return f.r.Domains.Create(f.t.Context(), v.ownerID, v.d) },
		[]insertCase[owned]{
			{name: "valid"},
			{name: "failing with reason", mutate: func(_ *fixture, v *owned) {
				v.d.Status, v.d.LastCheckedAt, v.d.FailingSince, v.d.LastFailureReason = store.DomainActive, now, now, store.DomainCNAMEElsewhere
			}},
			{name: "253 characters", mutate: func(_ *fixture, v *owned) { v.d.Hostname = strings.Repeat("a", 249) + ".com" }},
			{name: "duplicate hostname on another owner's site", mutate: func(f *fixture, v *owned) {
				o := f.owner()
				other := f.validDomain(f.site(o.ID).ID)
				other.Hostname = v.d.Hostname
				f.must(f.r.Domains.Create(f.t.Context(), o.ID, other))
			}, want: store.ErrConflict},
			{name: "duplicate verification token", mutate: func(f *fixture, v *owned) {
				other := f.validDomain(v.d.SiteID)
				other.VerificationToken = v.d.VerificationToken
				f.must(f.r.Domains.Create(f.t.Context(), v.ownerID, other))
			}, want: store.ErrConflict},
			{name: "another owner's site", mutate: func(f *fixture, v *owned) { v.ownerID = f.owner().ID }, want: store.ErrNotFound},
			{name: "unknown site", mutate: func(_ *fixture, v *owned) { v.d.SiteID = store.NewID() }, want: store.ErrNotFound},
			{name: "uppercase", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "Docs.example.test" }, want: store.ErrConstraint},
			{name: "trailing dot", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "docs.example.test." }, want: store.ErrConstraint},
			{name: "port", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "docs.example.test:443" }, want: store.ErrConstraint},
			{name: "254 characters", mutate: func(_ *fixture, v *owned) { v.d.Hostname = strings.Repeat("a", 250) + ".com" }, want: store.ErrConstraint},
			{name: "empty", mutate: func(_ *fixture, v *owned) { v.d.Hostname = "" }, want: store.ErrConstraint},
			{name: "unknown status", mutate: func(_ *fixture, v *owned) { v.d.Status = "verified" }, want: store.ErrConstraint},
			{name: "short token", mutate: func(_ *fixture, v *owned) { v.d.VerificationToken = "short" }, want: store.ErrConstraint},
			{name: "failing without reason", mutate: func(_ *fixture, v *owned) { v.d.FailingSince = now }, want: store.ErrConstraint},
			{name: "unknown reason", mutate: func(_ *fixture, v *owned) { v.d.LastFailureReason = "timeout" }, want: store.ErrConstraint},
		})
}
