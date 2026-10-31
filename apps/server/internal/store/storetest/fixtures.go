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
	// emails and siteOwners remember what valid values should read back as:
	// a repository fills in a site's owner and what made each row.
	emails     map[string]string
	siteOwners map[string]string
}

func newFixture(t testing.TB, open Opener) *fixture {
	return &fixture{t: t, r: open(t), emails: map[string]string{}, siteOwners: map[string]string{}}
}

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
	f.emails[o.ID] = o.Email
	return o
}

func (f *fixture) validSite(ownerID string) store.Site {
	s := store.Site{
		ID: store.NewID(), OwnerID: ownerID, Name: fmt.Sprintf("site-%d", f.seq()), CreatedAt: now,
		OwnerKind: store.AccountOwner, OwnerName: f.emails[ownerID],
	}
	f.siteOwners[s.ID] = ownerID
	return s
}

func (f *fixture) site(ownerID string) store.Site {
	f.t.Helper()
	s := f.validSite(ownerID)
	f.must(f.r.Sites.Create(f.t.Context(), store.ActorFor(s.OwnerID), s, store.Quotas{}))
	return s
}

func (f *fixture) validDeploy(siteID string) store.Deploy {
	return store.Deploy{ID: store.NewID(), SiteID: siteID, FileCount: 1, TotalSize: 5, CreatedAt: now, CreatedBy: f.siteOwners[siteID]}
}

func sha(c byte) string { return strings.Repeat(string(c), 64) }

func (f *fixture) deploy(ownerID, siteID string) store.Deploy {
	f.t.Helper()
	d := f.validDeploy(siteID)
	f.must(f.r.Deploys.Create(f.t.Context(), store.ActorFor(ownerID), d, []store.DeployFile{{Path: "index.html", SHA256: sha('a'), Size: 5}}, store.Quotas{}))
	return d
}

// deployFiles creates a deploy with files on one of ownerID's sites.
func (f *fixture) deployFiles(ownerID, siteID string, files ...store.DeployFile) store.Deploy {
	f.t.Helper()
	d := f.validDeploy(siteID)
	d.FileCount, d.TotalSize = len(files), 0
	for _, file := range files {
		d.TotalSize += file.Size
	}
	f.must(f.r.Deploys.Create(f.t.Context(), store.ActorFor(ownerID), d, files, store.Quotas{}))
	return d
}

// uploadAll records every blob in the deploy's manifest as ownerID's.
func (f *fixture) uploadAll(ownerID, siteID, deployID string) {
	f.t.Helper()
	hashes, err := f.r.Deploys.Hashes(f.t.Context(), store.ActorFor(ownerID), siteID, deployID)
	f.must(err)
	for _, h := range hashes {
		size, err := f.r.Deploys.ManifestSize(f.t.Context(), store.ActorFor(ownerID), siteID, deployID, h)
		f.must(err)
		f.must(f.r.Deploys.RecordUpload(f.t.Context(), store.ActorFor(ownerID), ownerID, store.Blob{SHA256: h, Size: size, CreatedAt: now}))
	}
}

// activated creates a deploy and activates it at.
func (f *fixture) activated(ownerID, siteID string, at time.Time) store.Deploy {
	f.t.Helper()
	d := f.deploy(ownerID, siteID)
	f.uploadAll(ownerID, siteID, d.ID)
	d, err := f.r.Deploys.Activate(f.t.Context(), store.ActorFor(ownerID), siteID, d.ID, at)
	f.must(err)
	return d
}

func (f *fixture) validPolicy(siteID string) store.Policy {
	return store.Policy{
		ID: store.NewID(), SiteID: siteID, PathPrefix: "/", Visibility: store.Protected,
		LinksEnabled: true, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: f.siteOwners[siteID],
	}
}

func (f *fixture) policy(ownerID, siteID string) store.Policy {
	f.t.Helper()
	p := f.validPolicy(siteID)
	f.must(f.r.Policies.Create(f.t.Context(), store.ActorFor(ownerID), p))
	return p
}

func (f *fixture) validLink(siteID string) store.ShareLink {
	return store.ShareLink{
		ID: store.NewID(), SiteID: siteID, PublicID: f.publicID(), TokenHash: f.hash(),
		Label: "Linear ENG-1", PathPrefix: "/", CreatedAt: now, CreatedBy: f.siteOwners[siteID],
	}
}

func (f *fixture) link(ownerID, siteID string) store.ShareLink {
	f.t.Helper()
	l := f.validLink(siteID)
	f.must(f.r.Links.Create(f.t.Context(), store.ActorFor(ownerID), l))
	return l
}

func (f *fixture) validDomain(siteID string) store.Domain {
	n := f.seq()
	return store.Domain{
		ID: store.NewID(), SiteID: siteID, Hostname: fmt.Sprintf("docs%d.example.test", n),
		Status: store.DomainPending, VerificationToken: fmt.Sprintf("verify-token-%06d", n), CreatedAt: now, CreatedBy: f.siteOwners[siteID],
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
