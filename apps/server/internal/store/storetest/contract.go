// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"testing"

	"wardlock.nz/server/internal/store"
)

// Repos is one backend's set of repositories, as domain packages see them.
type Repos struct {
	Owners    store.Owners
	Sessions  store.Sessions
	APITokens store.APITokens
	Sites     store.Sites
	Domains   store.Domains
	Deploys   store.Deploys
	Policies  store.Policies
	Links     store.Links
	AccessLog store.AccessLog
	GC        store.GC
}

// Opener returns repositories over a fresh, empty, migrated database that
// lives until t ends. Every contract case opens its own.
type Opener func(t testing.TB) Repos

// SQLite opens Repos over a throwaway SQLite database (see New).
func SQLite(t testing.TB) Repos { return ReposOf(New(t)) }

// ReposOf returns db's repositories.
func ReposOf(db *store.DB) Repos {
	return Repos{
		Owners:    db.Owners(),
		Sessions:  db.Sessions(),
		APITokens: db.APITokens(),
		Sites:     db.Sites(),
		Domains:   db.Domains(),
		Deploys:   db.Deploys(),
		Policies:  db.Policies(),
		Links:     db.Links(),
		AccessLog: db.AccessLog(),
		GC:        db.GC(),
	}
}

// suite is one repository interface's contract. Feature items add cases to
// their interface's suite for every repository method they add.
type suite struct {
	name string
	run  func(t *testing.T, open Opener)
}

func suites() []suite {
	return []suite{
		{"Owners", ownersContract},
		{"Sessions", sessionsContract},
		{"APITokens", apiTokensContract},
		{"Sites", sitesContract},
		{"Domains", domainsContract},
		{"Deploys", deploysContract},
		{"Policies", policiesContract},
		{"Links", linksContract},
		{"AccessLog", accessLogContract},
		{"GC", gcContract},
	}
}

// RunContract runs every repository contract suite against open. A backend
// is correct when this passes under go test -race.
func RunContract(t *testing.T, open Opener) {
	for _, s := range suites() {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			s.run(t, open)
		})
	}
}
