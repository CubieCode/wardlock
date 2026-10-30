// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites_test

import (
	"errors"
	"slices"
	"testing"

	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

// activeDomain adds host to the env's docs site with a CNAME, so it is
// active straight away.
func (e *env) activeDomain(t *testing.T, host string) store.Domain {
	t.Helper()
	e.dns.CNAME(host, "docs."+base)
	d, err := e.svc.AddDomain(t.Context(), e.owner.ID, "docs", host)
	if err != nil || d.Status != store.DomainActive {
		t.Fatalf("AddDomain(%s) = %+v, %v; want active", host, d, err)
	}
	return d
}

func TestSetPrimaryHost(t *testing.T) {
	e := newEnv(t)
	active := e.activeDomain(t, "www.example.com")
	if _, err := e.svc.AddDomain(t.Context(), e.owner.ID, "docs", "pending.example.com"); err != nil {
		t.Fatal(err)
	}
	other := e.newOwner(t)
	theirs, err := e.svc.Create(t.Context(), other.ID, "theirs")
	if err != nil {
		t.Fatal(err)
	}
	e.dns.CNAME("theirs.example.org", "theirs."+base)
	if _, err := e.svc.AddDomain(t.Context(), other.ID, "theirs", "theirs.example.org"); err != nil {
		t.Fatal(err)
	}
	sibling, err := e.svc.Create(t.Context(), e.owner.ID, "sibling")
	if err != nil {
		t.Fatal(err)
	}
	e.note.take()

	tests := []struct {
		name, host string
		want       string // the primary in effect afterwards
		err        error
	}{
		{"base-domain hostname", "docs." + base, "docs." + base, nil},
		{"base-domain hostname, any spelling", "Docs." + base + ".", "docs." + base, nil},
		{"active custom domain", "www.example.com", active.Hostname, nil},
		{"active custom domain, any spelling", "WWW.Example.com.", active.Hostname, nil},
		{"pending custom domain", "pending.example.com", "", sites.ErrDomainNotActive},
		{"another owner's custom domain", "theirs.example.org", "", sites.ErrNotFound},
		{"another owner's base-domain hostname", theirs.Name + "." + base, "", sites.ErrNotFound},
		{"the owner's other site", sibling.Name + "." + base, "", sites.ErrNotFound},
		{"unknown hostname", "nowhere.example.net", "", sites.ErrNotFound},
		{"not a hostname", "http://www.example.com/", "", sites.ErrNotFound},
		{"the dashboard host", "wardlock.example.com", "", sites.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.ClearPrimaryHost(t.Context(), e.owner.ID, "docs"); err != nil {
				t.Fatal(err)
			}
			e.note.take()
			got, err := e.svc.SetPrimaryHost(t.Context(), e.owner.ID, "docs", tt.host)
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("SetPrimaryHost(%q) error = %v, want %v", tt.host, err, tt.err)
			}
			notified, _ := e.note.take()
			if tt.err != nil {
				if len(notified) != 0 {
					t.Errorf("a refused change notified %v", notified)
				}
				got, err = e.svc.Get(t.Context(), e.owner.ID, "docs")
				if err != nil {
					t.Fatal(err)
				}
			} else if !slices.Equal(notified, []string{e.site.ID}) {
				t.Errorf("notified %v, want the site", notified)
			}
			if p := e.svc.PrimaryHost(got); p != tt.want {
				t.Errorf("PrimaryHost() = %q, want %q", p, tt.want)
			}
		})
	}
}

func TestPrimaryHostOwnerIsolation(t *testing.T) {
	e := newEnv(t)
	e.activeDomain(t, "www.example.com")
	other := e.newOwner(t)
	for _, host := range []string{"docs." + base, "www.example.com"} {
		if _, err := e.svc.SetPrimaryHost(t.Context(), other.ID, "docs", host); !errors.Is(err, sites.ErrNotFound) {
			t.Errorf("another owner's SetPrimaryHost(%s) = %v, want ErrNotFound", host, err)
		}
	}
	if _, err := e.svc.SetPrimaryHost(t.Context(), e.owner.ID, "docs", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ClearPrimaryHost(t.Context(), other.ID, "docs"); !errors.Is(err, sites.ErrNotFound) {
		t.Errorf("another owner's ClearPrimaryHost = %v, want ErrNotFound", err)
	}
	got, err := e.svc.Get(t.Context(), e.owner.ID, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if p := e.svc.PrimaryHost(got); p != "www.example.com" {
		t.Errorf("PrimaryHost() = %q after another owner's attempts, want www.example.com", p)
	}
}

func TestPrimaryHostTakenDown(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.SetPrimaryHost(t.Context(), e.owner.ID, "docs", "docs."+base); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Sites().TakeDown(t.Context(), e.site.ID, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetPrimaryHost(t.Context(), e.owner.ID, "docs", "docs."+base); !errors.Is(err, sites.ErrUnavailable) {
		t.Errorf("SetPrimaryHost on a taken-down site = %v, want ErrUnavailable", err)
	}
	if _, err := e.svc.ClearPrimaryHost(t.Context(), e.owner.ID, "docs"); !errors.Is(err, sites.ErrUnavailable) {
		t.Errorf("ClearPrimaryHost on a taken-down site = %v, want ErrUnavailable", err)
	}
}

// TestPrimaryHostLapseAndRemoval: a lapse suspends a custom primary and
// recovery resumes it; removing the domain clears it for good.
func TestPrimaryHostLapseAndRemoval(t *testing.T) {
	e := newEnv(t)
	d := e.activeDomain(t, "www.example.com")
	if _, err := e.svc.SetPrimaryHost(t.Context(), e.owner.ID, "docs", d.Hostname); err != nil {
		t.Fatal(err)
	}
	primary := func() string {
		t.Helper()
		got, err := e.svc.Get(t.Context(), e.owner.ID, "docs")
		if err != nil {
			t.Fatal(err)
		}
		return e.svc.PrimaryHost(got)
	}

	// Lapse it directly: the 72-hour run of failures is verify's to test.
	lapsed := d
	lapsed.Status, lapsed.LastCheckedAt, lapsed.FailingSince = store.DomainLapsed, t0, t0
	lapsed.LastFailureReason = store.DomainNoRecord
	if err := e.db.Domains().RecordCheck(t.Context(), d, lapsed); err != nil {
		t.Fatal(err)
	}
	if p := primary(); p != "" {
		t.Fatalf("PrimaryHost() = %q while lapsed, want none", p)
	}
	if _, err := e.svc.SetPrimaryHost(t.Context(), e.owner.ID, "docs", d.Hostname); !errors.Is(err, sites.ErrDomainNotActive) {
		t.Fatalf("SetPrimaryHost(lapsed) = %v, want ErrDomainNotActive", err)
	}
	got, err := e.svc.Get(t.Context(), e.owner.ID, "docs")
	if err != nil || got.PrimaryDomain != d.Hostname {
		t.Fatalf("the lapsed choice was not kept: %+v, %v", got, err)
	}

	// The next check passes: the primary resumes.
	e.clock.advance(sites.VerifyCooldown + 1)
	if _, err := e.svc.VerifyDomain(t.Context(), e.owner.ID, "docs", d.Hostname); err != nil {
		t.Fatal(err)
	}
	if p := primary(); p != d.Hostname {
		t.Fatalf("PrimaryHost() = %q after recovery, want %s", p, d.Hostname)
	}

	if err := e.svc.RemoveDomain(t.Context(), e.owner.ID, "docs", d.Hostname); err != nil {
		t.Fatal(err)
	}
	if p := primary(); p != "" {
		t.Fatalf("PrimaryHost() = %q after removal, want none", p)
	}
	// Claimed again, it is a new domain and not the primary.
	e.activeDomain(t, d.Hostname)
	if p := primary(); p != "" {
		t.Fatalf("PrimaryHost() = %q after a new claim, want none", p)
	}
}
