// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// ErrForbiddenRole means the actor can see the account but their role does
// not allow the operation. Only an actor who can see it ever gets this:
// anyone else gets ErrNotFound (T7.1).
var ErrForbiddenRole = errors.New("role does not allow this")

// Role is a member's role in a team, each including the one after it.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// Valid reports whether r is one of the roles.
func (r Role) Valid() bool { return r.rank() > 0 }

func (r Role) rank() int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// Membership is an owner's role in one team.
type Membership struct {
	TeamID string
	Role   Role
}

// Actor is who an owner-scoped repository call acts for (T7.1). The API
// builds one from the verified session or token, never from the request.
//
// OwnerID is the verified owner, or for a team token the team. An actor may
// use its own account as an owner uses their sites, and each team in
// Memberships as far as its role allows. A team token is held to an editor
// on its team: tokens never administer.
type Actor struct {
	OwnerID     string
	Memberships []Membership
	// TeamTokenID is the team token acting, when one is.
	TeamTokenID string
}

// ActorFor is the account's own actor, with no memberships: an owner in no
// team, or the server acting for the account a site already names.
func ActorFor(accountID string) Actor { return Actor{OwnerID: accountID} }

// TeamTokenActor is a team token acting for its team.
func TeamTokenActor(teamID, tokenID string) Actor {
	return Actor{OwnerID: teamID, TeamTokenID: tokenID}
}

// Account returns id, or the actor's own account when id is empty: what an
// operation reads or writes when the request names no team.
func (a Actor) Account(id string) string {
	if id == "" {
		return a.OwnerID
	}
	return id
}

// IsTeamToken reports whether a team token is acting.
func (a Actor) IsTeamToken() bool { return a.TeamTokenID != "" }

// Role returns the actor's role in teamID: its membership, or editor for a
// team token on its own team. ok is false for anything else.
func (a Actor) Role(teamID string) (Role, bool) {
	if a.IsTeamToken() {
		return RoleEditor, a.OwnerID == teamID
	}
	i := slices.IndexFunc(a.Memberships, func(m Membership) bool { return m.TeamID == teamID })
	if i < 0 {
		return "", false
	}
	return a.Memberships[i].Role, true
}

// Require is nil when the actor may use accountID at role need. An account it
// cannot see is ErrNotFound, exactly like a missing one; one it can see with
// too low a role is ErrForbiddenRole. An owner's own account allows anything.
func (a Actor) Require(accountID string, need Role) error {
	if accountID == "" {
		return fmt.Errorf("account: %w", ErrNotFound)
	}
	if accountID == a.OwnerID && !a.IsTeamToken() {
		return nil
	}
	role, ok := a.Role(accountID)
	if !ok {
		return fmt.Errorf("account: %w", ErrNotFound)
	}
	if role.rank() < need.rank() {
		return fmt.Errorf("account: %w", ErrForbiddenRole)
	}
	return nil
}

// MadeBy returns what a created row records of who made it.
func (a Actor) MadeBy() (ownerID, tokenID string) { return a.OwnerID, a.TeamTokenID }

// authorizeSite returns the account siteID belongs to, when the actor may use
// it at role need. Repository methods then match that account in their own
// SQL, so a site moved meanwhile is ErrNotFound rather than a stale write.
func (db *DB) authorizeSite(ctx context.Context, actor Actor, siteID string, need Role) (string, error) {
	return authorizeSite(ctx, db.q, actor, siteID, need)
}

func authorizeSite(ctx context.Context, q *sqlcgen.Queries, actor Actor, siteID string, need Role) (string, error) {
	account, err := q.SiteAccount(ctx, siteID)
	if err != nil {
		return "", mapErr("find site", err)
	}
	if err := actor.Require(account, need); err != nil {
		return "", fmt.Errorf("site: %w", err)
	}
	return account, nil
}
