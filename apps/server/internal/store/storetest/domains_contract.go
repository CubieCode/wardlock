// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"

	"wardlock.nz/server/internal/store"
)

func domainsContract(t *testing.T, open Opener) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		domainsCreateContract(t, open)
	})
	t.Run("GetActiveByHostname", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		active := f.validDomain(s.ID)
		active.Status, active.LastCheckedAt, active.VerifiedAt = store.DomainActive, now, now
		f.must(f.r.Domains.Create(t.Context(), o.ID, active))
		got, err := f.r.Domains.GetActiveByHostname(t.Context(), active.Hostname)
		if err != nil {
			t.Fatal(err)
		}
		if got != active {
			t.Fatalf("GetActiveByHostname() = %+v, want %+v", got, active)
		}
		for _, status := range []store.DomainStatus{store.DomainPending, store.DomainLapsed} {
			d := f.validDomain(s.ID)
			d.Status = status
			if status == store.DomainLapsed {
				d.LastCheckedAt, d.FailingSince, d.LastFailureReason, d.FailureDetail = now, now, store.DomainNoRecord, "No CNAME or TXT record found."
			}
			f.must(f.r.Domains.Create(t.Context(), o.ID, d))
			_, err := f.r.Domains.GetActiveByHostname(t.Context(), d.Hostname)
			wantErr(t, err, store.ErrNotFound)
		}
		_, err = f.r.Domains.GetActiveByHostname(t.Context(), "unknown.example.test")
		wantErr(t, err, store.ErrNotFound)
	})
}

func domainsCreateContract(t *testing.T, open Opener) {
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
			{name: "verified with failure detail", mutate: func(_ *fixture, v *owned) {
				v.d.Status, v.d.LastCheckedAt, v.d.VerifiedAt = store.DomainActive, now, now
				v.d.FailingSince, v.d.LastFailureReason, v.d.FailureDetail = now, store.DomainDNSError, "SERVFAIL from 192.0.2.53"
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
