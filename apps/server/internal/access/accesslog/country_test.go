// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package accesslog_test

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/country"
	"wardlock.nz/server/internal/country/countrytest"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// The split /25s tell a lookup of the stored /24 (NZ) from one of the full
// address (AU), and the /48s likewise for IPv6.
var testNetworks = countrytest.Networks{
	"203.0.113.0/25":   "NZ",
	"203.0.113.128/25": "AU",
	"2001:db8:1::/48":  "DE",
	"2001:db8::/48":    "FR",
}

func openCountries(t *testing.T, log *slog.Logger) *country.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := countrytest.Write(path, testNetworks); err != nil {
		t.Fatal(err)
	}
	db := country.Open(path, false, log)
	if !db.Loaded() {
		t.Fatal("synthetic country database did not load")
	}
	return db
}

// TestCountryFromStoredIP writes entries through the writer into a real
// store: the country comes from the IP as stored, after truncation, for
// IPv4 and IPv6; no match is unknown; and nothing is recorded when the
// lookup is off or no IP is stored. The rollup counts the same countries,
// and neither the logs nor the rollup hold an untruncated IP.
func TestCountryFromStoredIP(t *testing.T) {
	ctx := t.Context()
	db := storetest.New(t)
	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(ctx, store.ActorFor(site.OwnerID), site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	if err := accesslog.Backfill(ctx, db.Traffic(), time.Now, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	countries := openCountries(t, log)
	now := time.Now().UTC()

	for _, tc := range []struct {
		name       string
		ip         string
		truncation accesslog.Truncation
		off        bool
		wantIP     string
		want       string
	}{
		{"IPv4 /24", "203.0.113.200", accesslog.DefaultTruncation, false, "203.0.113.0", "NZ"},
		{"IPv4 kept whole", "203.0.113.200", accesslog.Truncation{IPv4Prefix: 32, IPv6Prefix: 128}, false, "203.0.113.200", "AU"},
		{"IPv6 /48", "2001:db8:1:ffff::1", accesslog.DefaultTruncation, false, "2001:db8:1::", "DE"},
		{"IPv6 /32 falls in another network", "2001:db8:1:ffff::1", accesslog.Truncation{IPv4Prefix: 24, IPv6Prefix: 32}, false, "2001:db8::", "FR"},
		{"IPv4-mapped IPv6", "::ffff:203.0.113.9", accesslog.DefaultTruncation, false, "203.0.113.0", "NZ"},
		{"no match", "198.51.100.7", accesslog.DefaultTruncation, false, "198.51.100.0", store.CountryUnknown},
		{"no IP stored", "203.0.113.200", accesslog.Truncation{}, false, "", ""},
		{"no IP sent", "", accesslog.DefaultTruncation, false, "", ""},
		{"lookup off", "203.0.113.200", accesslog.DefaultTruncation, true, "203.0.113.0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := accesslog.Config{Repo: db.AccessLog(), Log: log, Truncation: tc.truncation, FlushInterval: time.Hour}
			if !tc.off {
				cfg.Countries = countries
			}
			w := accesslog.Start(cfg)
			e := entry("/" + strings.ReplaceAll(tc.name, " ", "-"))
			e.SiteID, e.Time, e.IP = site.ID, now, tc.ip
			w.Record(e)
			closeWriter(t, w)
			got, err := db.AccessLog().ListBySite(ctx, store.ActorFor(owner.ID), site.ID, store.AccessCursor{}, 1)
			if err != nil || len(got) != 1 {
				t.Fatalf("ListBySite() = %v, %v", got, err)
			}
			if got[0].IP != tc.wantIP || got[0].Country != tc.want {
				t.Fatalf("stored IP %q country %q, want %q %q", got[0].IP, got[0].Country, tc.wantIP, tc.want)
			}
		})
	}

	r, err := db.Traffic().Report(ctx, store.ActorFor(owner.ID), owner.ID, store.TrafficScope{}, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Busiest first, then by code; entries without a country are left out.
	want := []store.TrafficCountry{
		{Country: "NZ", TrafficCount: store.TrafficCount{Allowed: 2}},
		{Country: "AU", TrafficCount: store.TrafficCount{Allowed: 1}},
		{Country: "DE", TrafficCount: store.TrafficCount{Allowed: 1}},
		{Country: "FR", TrafficCount: store.TrafficCount{Allowed: 1}},
		{Country: store.CountryUnknown, TrafficCount: store.TrafficCount{Allowed: 1}},
	}
	if fmt.Sprint(r.Countries) != fmt.Sprint(want) {
		t.Fatalf("Countries = %+v, want %+v", r.Countries, want)
	}
	if dump := fmt.Sprintf("%+v", r); strings.Contains(dump, "203.0.113") || strings.Contains(dump, "2001:db8") {
		t.Fatalf("rollup holds an IP: %s", dump)
	}
	for _, ip := range []string{"203.0.113.200", "2001:db8:1:ffff::1", "198.51.100.7", "203.0.113.0"} {
		if strings.Contains(logs.String(), ip) {
			t.Fatalf("logs hold IP %s: %s", ip, logs.String())
		}
	}
}

// recordingLookup answers fixed values and records the IPs it was asked
// for. When gate is set, each lookup waits for it.
type recordingLookup struct {
	mu     sync.Mutex
	asked  []string
	answer string
	gate   chan struct{}
}

func (l *recordingLookup) Lookup(ip string) string {
	if l.gate != nil {
		<-l.gate
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, ip)
	return l.answer
}

func (l *recordingLookup) ips() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.asked)
}

// TestLookupSeesOnlyStoredIPs proves the lookup is handed the truncated
// IP, never the address the client came from, and that an answer the
// table cannot hold records nothing rather than failing the batch.
func TestLookupSeesOnlyStoredIPs(t *testing.T) {
	for _, tc := range []struct{ answer, want string }{
		{"NZ", "NZ"},
		{store.CountryUnknown, store.CountryUnknown},
		{"nz", ""},
		{"Wellington", ""},
		{"", ""},
	} {
		lookup, repo := &recordingLookup{answer: tc.answer}, &fakeRepo{}
		w := accesslog.Start(accesslog.Config{Repo: repo, Truncation: accesslog.DefaultTruncation, Countries: lookup, FlushInterval: time.Hour})
		w.Record(entry("/"))
		closeWriter(t, w)
		if got := lookup.ips(); !slices.Equal(got, []string{"192.0.2.0"}) {
			t.Fatalf("lookup asked for %v, want only the stored 192.0.2.0", got)
		}
		if got := repo.entries(); len(got) != 1 || got[0].Country != tc.want {
			t.Fatalf("answer %q: stored %+v, want country %q", tc.answer, got, tc.want)
		}
	}
}

// TestRequestPathNeverWaitsOnLookup holds every lookup until the end:
// Record keeps returning at once while the writer is stuck in one, since
// the lookup runs in the writer goroutine as batches are written, and the
// entries are written with their countries once it is released.
func TestRequestPathNeverWaitsOnLookup(t *testing.T) {
	lookup := &recordingLookup{answer: "NZ", gate: make(chan struct{})}
	repo := &fakeRepo{}
	w := accesslog.Start(accesslog.Config{
		Repo: repo, Truncation: accesslog.DefaultTruncation, Countries: lookup,
		BatchSize: 1, FlushInterval: time.Millisecond,
	})
	const n = 500
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range n {
			w.Record(entry("/"))
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(lookup.gate)
		t.Fatal("Record waited on a blocked country lookup")
	}
	if got := len(repo.entries()); got != 0 {
		t.Fatalf("%d entries written while the lookup was blocked", got)
	}
	close(lookup.gate)
	closeWriter(t, w)
	got := repo.entries()
	if len(got) != n || w.Dropped() != 0 {
		t.Fatalf("wrote %d entries, dropped %d; want %d and none", len(got), w.Dropped(), n)
	}
	for _, e := range got {
		if e.Country != "NZ" {
			t.Fatalf("entry country %q, want NZ", e.Country)
		}
	}
}
