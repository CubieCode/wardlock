// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"errors"
	"testing"

	"wardlock.nz/server/internal/store"
)

func TestMatchIdentity(t *testing.T) {
	list := store.Allowlist{
		Emails:     []string{"visitor@example.test"},
		Domains:    []string{"crew.test", "*.harbour.test", "xn--bcher-kva.test"},
		GitHubOrgs: []string{"harbour-crew"},
	}
	tests := []struct {
		name    string
		id      Identity
		github  bool
		subject string
		err     error
	}{
		{"exact address", Identity{Emails: []string{"Visitor@Example.TEST"}}, false, "visitor@example.test", nil},
		{"domain", Identity{Emails: []string{"anyone@crew.test"}}, false, "anyone@crew.test", nil},
		{"domain covers only itself", Identity{Emails: []string{"anyone@team.crew.test"}}, false, "", ErrNotAllowed},
		{"wildcard subdomain", Identity{Emails: []string{"anyone@a.b.harbour.test"}}, false, "anyone@a.b.harbour.test", nil},
		{"wildcard not the apex", Identity{Emails: []string{"anyone@harbour.test"}}, false, "", ErrNotAllowed},
		{"wildcard not a lookalike", Identity{Emails: []string{"anyone@badharbour.test"}}, false, "", ErrNotAllowed},
		{"internationalised domain", Identity{Emails: []string{"anyone@bücher.test"}}, false, "anyone@xn--bcher-kva.test", nil},
		{"second verified address", Identity{Emails: []string{"me@elsewhere.test", "visitor@example.test"}}, false, "visitor@example.test", nil},
		{"organisation", Identity{Emails: []string{"me@elsewhere.test"}, Orgs: []string{"Harbour-Crew"}}, true, "me@elsewhere.test", nil},
		{"organisation off GitHub", Identity{Emails: []string{"me@elsewhere.test"}, Orgs: []string{"harbour-crew"}}, false, "", ErrNotAllowed},
		{"other organisation", Identity{Emails: []string{"me@elsewhere.test"}, Orgs: []string{"other"}}, true, "", ErrNotAllowed},
		{"no verified address", Identity{Orgs: []string{"harbour-crew"}}, true, "", ErrNoVerifiedEmail},
		{"unreadable address", Identity{Emails: []string{"not-an-address"}}, false, "", ErrNoVerifiedEmail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchIdentity(list, tt.id, tt.github)
			if got != tt.subject || !errors.Is(err, tt.err) {
				t.Fatalf("matchIdentity = %q, %v; want %q, %v", got, err, tt.subject, tt.err)
			}
		})
	}
	if _, err := matchIdentity(store.Allowlist{}, Identity{Emails: []string{"visitor@example.test"}}, true); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("empty allowlist: %v", err)
	}
}

func TestNormaliseGitHubOrg(t *testing.T) {
	for raw, want := range map[string]string{
		"Harbour-Crew": "harbour-crew", " crew7 ": "crew7", "a": "a",
		"-crew": "", "crew-": "", "har--bour": "", "har_bour": "", "har bour": "", "": "",
		"a234567890123456789012345678901234567890": "",
	} {
		got, ok := normaliseGitHubOrg(raw)
		if got != want || ok != (want != "") {
			t.Errorf("normaliseGitHubOrg(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
}
