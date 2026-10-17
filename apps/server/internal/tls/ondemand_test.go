// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"wardlock.nz/server/internal/store"
)

// fakeDomains answers like store.Domains.GetActiveByHostname: only active
// hostnames are found. It records every lookup.
type fakeDomains struct {
	active  map[string]bool
	err     error
	mu      sync.Mutex
	lookups []string
}

func (f *fakeDomains) GetActiveByHostname(_ context.Context, hostname string) (store.Domain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, hostname)
	if f.err != nil {
		return store.Domain{}, f.err
	}
	if !f.active[hostname] {
		return store.Domain{}, store.ErrNotFound
	}
	return store.Domain{Hostname: hostname, Status: store.DomainActive}, nil
}

func (f *fakeDomains) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.lookups)
}

func TestOnDemandAllow(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		wantAllow  bool
		wantLookup bool
	}{
		{"active custom domain", "docs.customer.example", true, true},
		{"active, any case and trailing dot", "Docs.Customer.Example.", true, true},
		{"active apex", "customer.example", true, true},
		{"pending or lapsed custom domain", "pending.customer.example", false, true},
		{"unknown domain", "evil.example", false, true},
		{"site under the base domain", "docs.sites.example", false, false},
		{"nested name under the base domain", "a.docs.sites.example", false, false},
		{"base domain itself", "sites.example", false, false},
		{"dashboard host", "wardlock.example", false, false},
		{"IPv4 address", "192.0.2.10", false, false},
		{"IPv6 address", "2001:db8::1", false, false},
		{"bracketed IPv6", "[2001:db8::1]", false, false},
		{"wildcard", "*.customer.example", false, false},
		{"empty", "", false, false},
		{"with port", "customer.example:443", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domains := &fakeDomains{active: map[string]bool{"docs.customer.example": true, "customer.example": true}}
			o := OnDemand{BaseDomain: "sites.example", DashboardHost: "wardlock.example", Domains: domains}
			err := o.Allow(t.Context(), tt.host)
			if tt.wantAllow && err != nil {
				t.Fatalf("Allow(%q) = %v, want nil", tt.host, err)
			}
			if !tt.wantAllow && !errors.Is(err, ErrNotAllowed) {
				t.Fatalf("Allow(%q) = %v, want ErrNotAllowed", tt.host, err)
			}
			if got := len(domains.seen()) > 0; got != tt.wantLookup {
				t.Fatalf("Allow(%q) looked up the repository = %v, want %v", tt.host, got, tt.wantLookup)
			}
		})
	}
}

func TestOnDemandLookupFailureDenies(t *testing.T) {
	broken := errors.New("database is locked")
	o := OnDemand{BaseDomain: "sites.example", DashboardHost: "wardlock.example", Domains: &fakeDomains{err: broken}}
	err := o.Allow(t.Context(), "docs.customer.example")
	if !errors.Is(err, broken) || errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Allow = %v, want the lookup error (which denies) and not ErrNotAllowed", err)
	}
}
