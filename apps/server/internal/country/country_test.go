// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package country_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/country"
	"wardlock.nz/server/internal/country/countrytest"
)

// networks is synthetic: documentation prefixes given made-up countries.
var networks = countrytest.Networks{
	"203.0.113.0/24":  "NZ",
	"198.51.100.0/24": "AU",
	"192.0.2.0/25":    "", // a record with no country
	"2001:db8:1::/48": "NZ",
	"2001:db8:2::/48": "DE",
	"2001:db8:3::/48": "nz", // not a code
}

func write(t *testing.T, path string, n countrytest.Networks) {
	t.Helper()
	if err := countrytest.Write(path, n); err != nil {
		t.Fatal(err)
	}
}

func TestLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	write(t, path, networks)
	db := country.Open(path, nil)
	if !db.Loaded() {
		t.Fatal("Loaded() = false after a good file")
	}
	for _, tc := range []struct{ ip, want string }{
		// IPs as the access log stores them: /24 and /48 by default.
		{"203.0.113.0", "NZ"},
		{"198.51.100.0", "AU"},
		{"2001:db8:1::", "NZ"},
		{"2001:db8:2::", "DE"},
		// Other truncations, and full addresses, look up the same way.
		{"203.0.113.77", "NZ"},
		{"2001:db8:2:ab::1", "DE"},
		{"::ffff:203.0.113.9", "NZ"},
		// No match, no country in the record, or not a code: unknown.
		{"192.0.2.200", country.Unknown},
		{"192.0.2.0", country.Unknown},
		{"2001:db8:3::", country.Unknown},
		{"2001:db8:4::", country.Unknown},
		{"0.0.0.0", country.Unknown},
		// Not an address: nothing.
		{"", ""},
		{"not an ip", ""},
	} {
		if got := db.Lookup(tc.ip); got != tc.want {
			t.Errorf("Lookup(%q) = %q, want %q", tc.ip, got, tc.want)
		}
	}
}

func TestMissingOrBadFile(t *testing.T) {
	dir := t.TempDir()
	for name, setup := range map[string]func(path string){
		"missing":   func(string) {},
		"empty":     func(p string) { _ = os.WriteFile(p, nil, 0o600) },
		"not mmdb":  func(p string) { _ = os.WriteFile(p, []byte("country,code\nNZ,NZ\n"), 0o600) },
		"truncated": func(p string) { _ = os.WriteFile(p, countrytest.Build(networks)[:200], 0o600) },
		"directory": func(p string) { _ = os.Mkdir(p, 0o700) },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".mmdb")
			setup(path)
			var logs bytes.Buffer
			db := country.Open(path, slog.New(slog.NewTextHandler(&logs, nil)))
			if db.Loaded() {
				t.Fatal("Loaded() = true for a bad file")
			}
			if got := db.Lookup("203.0.113.0"); got != "" {
				t.Errorf("Lookup() = %q, want nothing recorded", got)
			}
			if !strings.Contains(logs.String(), "level=WARN") {
				t.Errorf("logs = %q, want a warning", logs.String())
			}
			// An unchanged bad file warns only once.
			before := logs.Len()
			db.Check()
			db.Check()
			if logs.Len() != before {
				t.Errorf("Check() on an unchanged file logged again: %q", logs.String()[before:])
			}
		})
	}
}

// TestReload proves the file is reread when it changes, without a
// restart: replaced by rename, rewritten in place, removed and restored,
// and replaced by a bad file, which stops lookups until a good one.
func TestReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	var logs bytes.Buffer
	write(t, path, countrytest.Networks{"203.0.113.0/24": "NZ"})
	db := country.Open(path, slog.New(slog.NewTextHandler(&logs, nil)))
	want := func(step, ip, code string) {
		t.Helper()
		if got := db.Lookup(ip); got != code {
			t.Fatalf("%s: Lookup(%q) = %q, want %q", step, ip, got, code)
		}
	}
	want("first file", "203.0.113.0", "NZ")

	// Unchanged: Check keeps the database it has.
	db.Check()
	want("unchanged", "203.0.113.0", "NZ")

	// The usual update: a new file renamed over the old one.
	next := filepath.Join(dir, "country.mmdb.new")
	write(t, next, countrytest.Networks{"203.0.113.0/24": "AU", "198.51.100.0/24": "DE"})
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
	want("before Check", "203.0.113.0", "NZ")
	db.Check()
	want("renamed into place", "203.0.113.0", "AU")
	want("renamed into place", "198.51.100.0", "DE")

	// Rewritten in place (same inode).
	write(t, path, countrytest.Networks{"203.0.113.0/24": "FR"})
	bump(t, path)
	db.Check()
	want("rewritten in place", "203.0.113.0", "FR")

	// Removed: nothing is recorded until it is back.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	db.Check()
	want("removed", "203.0.113.0", "")
	write(t, path, countrytest.Networks{"203.0.113.0/24": "NZ"})
	db.Check()
	want("restored", "203.0.113.0", "NZ")

	// A bad file stops lookups rather than keeping stale answers.
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	bump(t, path)
	db.Check()
	want("bad file", "203.0.113.0", "")
	write(t, path, countrytest.Networks{"203.0.113.0/24": "NZ"})
	bump(t, path)
	db.Check()
	want("good again", "203.0.113.0", "NZ")
	if !strings.Contains(logs.String(), "country database loaded") || !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("logs = %q, want loads and warnings", logs.String())
	}
}

// TestWatch proves Watch rereads a changed file on its own and stops when
// its context ends.
func TestWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	write(t, path, countrytest.Networks{"203.0.113.0/24": "NZ"})
	db := country.Open(path, nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { db.Watch(ctx, time.Millisecond); close(done) }()
	write(t, path, countrytest.Networks{"203.0.113.0/24": "AU"})
	bump(t, path)
	deadline := time.Now().Add(5 * time.Second)
	for db.Lookup("203.0.113.0") != "AU" {
		if time.Now().After(deadline) {
			t.Fatal("Watch did not reload the changed file")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return after its context ended")
	}
}

// bump moves the file's modification time on, as a later write would on a
// file system with coarse timestamps.
func bump(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	at := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestIsCode(t *testing.T) {
	for s, want := range map[string]bool{"NZ": true, "AU": true, "nz": false, "N": false, "NZL": false, "N1": false, "": false, "ÑZ": false} {
		if got := country.IsCode(s); got != want {
			t.Errorf("IsCode(%q) = %v, want %v", s, got, want)
		}
	}
}
