// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"testing"

	"wardlock.nz/server/internal/store"
)

// Repos is one backend's set of repositories, as domain packages see them.
type Repos struct {
	Owners     store.Owners
	Sessions   store.Sessions
	Passkeys   store.Passkeys
	APITokens  store.APITokens
	Sites      store.Sites
	Domains    store.Domains
	Deploys    store.Deploys
	Policies   store.Policies
	Links      store.Links
	AccessLog  store.AccessLog
	GC         store.GC
	Renditions store.Renditions
	// PasswordSessions creates and ends password sessions (A2.3).
	PasswordSessions store.PasswordSessions
	// EmailCodes stores visitor email codes and their sessions (A2.4).
	EmailCodes store.EmailCodes
}

// Opener returns repositories over a fresh, empty, migrated database that
// lives until t ends. Every contract case opens its own.
type Opener func(t testing.TB) Repos

// SQLite opens Repos over a throwaway SQLite database (see New).
func SQLite(t testing.TB) Repos { return ReposOf(New(t)) }

// ReposOf returns db's repositories.
func ReposOf(db *store.DB) Repos {
	return Repos{
		Owners:     db.Owners(),
		Sessions:   db.Sessions(),
		Passkeys:   db.Passkeys(),
		APITokens:  db.APITokens(),
		Sites:      db.Sites(),
		Domains:    db.Domains(),
		Deploys:    db.Deploys(),
		Policies:   db.Policies(),
		Links:      db.Links(),
		AccessLog:  db.AccessLog(),
		GC:         db.GC(),
		Renditions: db.Renditions(),

		PasswordSessions: db.PasswordSessions(),
		EmailCodes:       db.EmailCodes(),
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
		{"Passkeys", passkeysContract},
		{"APITokens", apiTokensContract},
		{"Sites", sitesContract},
		{"Domains", domainsContract},
		{"Deploys", deploysContract},
		{"Policies", policiesContract},
		{"Links", linksContract},
		{"AccessLog", accessLogContract},
		{"GC", gcContract},
		{"Renditions", renditionsContract},
		{"PasswordSessions", passwordSessionsContract},
		{"EmailCodes", emailCodesContract},
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
