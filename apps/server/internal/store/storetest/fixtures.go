// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// now is the fixed clock for fixtures.
var now = time.UnixMilli(1_800_000_000_000).UTC()

// fixture builds valid rows on one backend. valid* methods return a value
// that would insert cleanly; the other methods insert it and fail t on error.
type fixture struct {
	t testing.TB
	r Repos
	n int
}

func newFixture(t testing.TB, open Opener) *fixture { return &fixture{t: t, r: open(t)} }

// seq returns a value unique within this fixture.
func (f *fixture) seq() int { f.n++; return f.n }

// publicID returns a unique 10-character token public ID.
func (f *fixture) publicID() string { return fmt.Sprintf("p%09d", f.seq()) }

// hash returns a unique stand-in for an HMAC or other stored hash.
func (f *fixture) hash() string { return fmt.Sprintf("hash-%d-%s", f.seq(), store.NewID()) }

func (f *fixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatalf("fixture: %v", err)
	}
}

func (f *fixture) validOwner() store.Owner {
	return store.Owner{ID: store.NewID(), Email: fmt.Sprintf("owner%d@example.test", f.seq()), CreatedAt: now}
}

func (f *fixture) owner() store.Owner {
	f.t.Helper()
	o := f.validOwner()
	f.must(f.r.Owners.Create(f.t.Context(), o))
	return o
}

func (f *fixture) validSite(ownerID string) store.Site {
	return store.Site{ID: store.NewID(), OwnerID: ownerID, Name: fmt.Sprintf("site-%d", f.seq()), CreatedAt: now}
}

func (f *fixture) site(ownerID string) store.Site {
	f.t.Helper()
	s := f.validSite(ownerID)
	f.must(f.r.Sites.Create(f.t.Context(), s))
	return s
}

func (f *fixture) validDeploy(siteID string) store.Deploy {
	return store.Deploy{ID: store.NewID(), SiteID: siteID, FileCount: 1, TotalSize: 5, CreatedAt: now}
}

func sha(c byte) string { return strings.Repeat(string(c), 64) }

func (f *fixture) deploy(ownerID, siteID string) store.Deploy {
	f.t.Helper()
	d := f.validDeploy(siteID)
	f.must(f.r.Deploys.Create(f.t.Context(), ownerID, d, []store.DeployFile{{Path: "index.html", SHA256: sha('a'), Size: 5}}))
	return d
}

func (f *fixture) validPolicy(siteID string) store.Policy {
	return store.Policy{
		ID: store.NewID(), SiteID: siteID, PathPrefix: "/", Visibility: store.Protected,
		LinksEnabled: true, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func (f *fixture) policy(ownerID, siteID string) store.Policy {
	f.t.Helper()
	p := f.validPolicy(siteID)
	f.must(f.r.Policies.Create(f.t.Context(), ownerID, p))
	return p
}

func (f *fixture) validLink(siteID string) store.ShareLink {
	return store.ShareLink{
		ID: store.NewID(), SiteID: siteID, PublicID: f.publicID(), TokenHash: f.hash(),
		Label: "Linear ENG-1", PathPrefix: "/", CreatedAt: now,
	}
}

func (f *fixture) link(ownerID, siteID string) store.ShareLink {
	f.t.Helper()
	l := f.validLink(siteID)
	f.must(f.r.Links.Create(f.t.Context(), ownerID, l))
	return l
}

func (f *fixture) validDomain(siteID string) store.Domain {
	n := f.seq()
	return store.Domain{
		ID: store.NewID(), SiteID: siteID, Hostname: fmt.Sprintf("docs%d.example.test", n),
		Status: store.DomainPending, VerificationToken: fmt.Sprintf("verify-token-%06d", n), CreatedAt: now,
	}
}

// wantErr fails t unless err matches want (nil means success).
func wantErr(t testing.TB, err, want error) {
	t.Helper()
	switch {
	case want == nil && err != nil:
		t.Fatalf("error = %v, want nil", err)
	case want != nil && !errors.Is(err, want):
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// insertCase is one attempt to insert a mutated valid row.
type insertCase[T any] struct {
	name   string
	mutate func(f *fixture, v *T)
	want   error
}

// runInsertCases runs each case on a fresh backend: setup prepares parent
// rows and returns a valid value, the case mutates it, insert writes it.
func runInsertCases[T any](t *testing.T, open Opener, setup func(f *fixture) T, insert func(f *fixture, v T) error, cases []insertCase[T]) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, open)
			v := setup(f)
			if tc.mutate != nil {
				tc.mutate(f, &v)
			}
			wantErr(t, insert(f, v), tc.want)
		})
	}
}
