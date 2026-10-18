// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"testing"

	"wardlock.nz/server/internal/store"
)

func TestAllows(t *testing.T) {
	list := store.Allowlist{
		Emails:  []string{"ana@example.test"},
		Domains: []string{"crew.test", "*.fleet.test"},
		// GitHub organisations are OAuth's (A2.5); an address never
		// matches one.
		GitHubOrgs: []string{"example.test"},
	}
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"ana@example.test", true},
		{"bo@example.test", false},
		{"ana@sub.example.test", false},
		{"cook@crew.test", true},
		{"cook@deck.crew.test", false},
		{"cook@xcrew.test", false},
		{"cook@north.fleet.test", true},
		{"cook@a.b.fleet.test", true},
		{"cook@fleet.test", false},
		{"cook@notfleet.test", false},
		{"cook@fleet.test.evil.test", false},
		{"crew.test", false},
	} {
		if got := allows(list, tc.addr); got != tc.want {
			t.Errorf("allows(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
	if allows(store.Allowlist{}, "ana@example.test") {
		t.Error("an empty allowlist allows")
	}
}

func TestNormalise(t *testing.T) {
	for raw, want := range map[string]string{
		"Ana@Example.TEST":     "ana@example.test",
		"  ana@example.test":   "ana@example.test",
		"ana":                  "",
		"Ana <a@example.test>": "",
		"a@b@example.test":     "",
	} {
		got, ok := NormaliseAddress(raw)
		if ok != (want != "") || got != want {
			t.Errorf("NormaliseAddress(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	for raw, want := range map[string]string{
		"Crew.Test":             "crew.test",
		"crew.test.":            "crew.test",
		"*.Fleet.test":          "*.fleet.test",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
		"a-b.c1.test":           "a-b.c1.test",
		"test":                  "",
		"*.test":                "",
		"*.*.test":              "",
		"a.*.test":              "",
		"bücher.example":        "",
		"-a.test":               "",
		"a-.test":               "",
		"a..test":               "",
		"192.0.2.1":             "",
		"@crew.test":            "",
		"crew.test/x":           "",
		"":                      "",
	} {
		got, ok := normaliseDomain(raw)
		if ok != (want != "") || got != want {
			t.Errorf("normaliseDomain(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
}
