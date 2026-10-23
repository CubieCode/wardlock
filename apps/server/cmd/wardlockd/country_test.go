// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/country"
	"wardlock.nz/server/internal/country/countrytest"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

// slowCountries takes delay over every lookup.
type slowCountries struct{ delay time.Duration }

func (s slowCountries) Lookup(string) string {
	time.Sleep(s.delay)
	return "NZ"
}

// TestSlowCountryLookupDoesNotSlowServing serves through the real edge
// and writer with a country lookup that takes a second per entry:
// requests stay fast, and shutdown writes every entry with its country.
func TestSlowCountryLookupDoesNotSlowServing(t *testing.T) {
	db := &slowStore{}
	w := accesslog.Start(accesslog.Config{
		Repo: db, Truncation: accesslog.DefaultTruncation, Countries: slowCountries{time.Second},
		BatchSize: 1, FlushInterval: time.Millisecond,
	})
	handler := newTestEdge(t, w)
	const n = 3
	began := time.Now()
	for range n {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://docs.wardlock.localhost/", nil))
		if res.Code != http.StatusOK {
			t.Fatalf("GET / = %d", res.Code)
		}
	}
	// Waiting on even one lookup would take a second.
	if took := time.Since(began); took > 500*time.Millisecond {
		t.Fatalf("%d requests took %v with a slow country lookup", n, took)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(db.entries) != n {
		t.Fatalf("stored %d entries, want %d", len(db.entries), n)
	}
	for _, e := range db.entries {
		if e.IP != "192.0.2.0" || e.Country != "NZ" {
			t.Fatalf("stored IP %q country %q, want 192.0.2.0 NZ", e.IP, e.Country)
		}
	}
}

// TestUnknownAgrees checks the lookup's "no country" is the value the
// store and the API contract name unknown.
func TestUnknownAgrees(t *testing.T) {
	if country.Unknown != store.CountryUnknown {
		t.Fatalf("country.Unknown = %q, store.CountryUnknown = %q", country.Unknown, store.CountryUnknown)
	}
}

// TestCountryWiring checks serve's wiring: off without WARDLOCK_COUNTRY_DB;
// on, the database is loaded and the API is given the credit and the
// server country; a missing file still turns the lookup on and records
// nothing until a good file appears.
func TestCountryWiring(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	if db := startCountries(t.Context(), platform.Config{}, log); db != nil {
		t.Fatal("lookup on without WARDLOCK_COUNTRY_DB")
	}
	if c := countryLookup(platform.Config{}); c != nil {
		t.Fatalf("API told %+v with the lookup off", c)
	}

	path := filepath.Join(t.TempDir(), "country.mmdb")
	cfg := platform.Config{Country: platform.CountryConfig{DB: path, Attribution: platform.DefaultCountryAttribution, ServerCountry: "NZ"}}
	db := startCountries(t.Context(), cfg, log)
	if db == nil || db.Loaded() || db.Lookup("192.0.2.0") != "" {
		t.Fatal("a missing file should turn the lookup on and record nothing")
	}
	if err := countrytest.Write(path, countrytest.Networks{"192.0.2.0/24": "NZ"}); err != nil {
		t.Fatal(err)
	}
	db.Check()
	if got := db.Lookup("192.0.2.0"); got != "NZ" {
		t.Fatalf("Lookup() after the file appeared = %q, want NZ", got)
	}
	if c := countryLookup(cfg); c == nil || c.Attribution != platform.DefaultCountryAttribution || c.ServerCountry != "NZ" {
		t.Fatalf("countryLookup() = %+v", c)
	}
}
