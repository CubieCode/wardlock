// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Team errors.
var (
	// ErrLastAdmin means a change would leave the team with no admin.
	ErrLastAdmin = errors.New("a team needs an admin")
	// ErrAlreadyMember means an invitation names someone in the team: their
	// role changes with SetRole, and an open invitation of theirs could
	// otherwise bring them back after they were removed.
	ErrAlreadyMember = errors.New("already a member")
	// ErrTeamHasSites means a team cannot be deleted while it has sites.
	ErrTeamHasSites = errors.New("team has sites")
)

// Team is a shared account (T7.1). Its ID is also a row in owners, kind
// 'team', which is what sites, groups, tags, tokens and quotas name.
type Team struct {
	ID        string
	Name      string
	CreatedBy string
	CreatedAt time.Time
	// Role is the actor's role in the team, set by reads that have an actor.
	Role Role
}

// TeamMember is one person's membership of a team.
type TeamMember struct {
	TeamID      string
	OwnerID     string
	Email       string
	Role        Role
	CreatedAt   time.Time
	SuspendedAt time.Time
}

// TeamInvitation invites one address to one team. It is bound to the address:
// only an owner signed in with that address can accept it.
type TeamInvitation struct {
	ID         string
	TeamID     string
	TeamName   string // read only
	Email      string
	Role       Role
	InvitedBy  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	AcceptedAt time.Time
}

// Teams stores teams, their members and invitations. Every method taking an
// Actor checks it against the team: a team it cannot see is ErrNotFound and
// too low a role ErrForbiddenRole.
type Teams interface {
	// Memberships lists the owner's teams and roles. It is what an Actor is
	// built from, on every authenticated request. Instance-wide.
	Memberships(ctx context.Context, ownerID string) ([]Membership, error)
	// Create adds t with the actor as its first admin, a person and never a
	// team token. An owner who already started as many teams as maxTeams
	// (after their override; 0 is unlimited) gets a *QuotaError.
	Create(ctx context.Context, actor Actor, t Team, maxTeams int64) error
	Get(ctx context.Context, actor Actor, id string) (Team, error)
	// List returns every team the actor can see, by name.
	List(ctx context.Context, actor Actor) ([]Team, error)
	Rename(ctx context.Context, actor Actor, id, name string) error
	// Delete removes a team that has no sites, with its members,
	// invitations, tokens, groups and tags; ErrTeamHasSites otherwise.
	Delete(ctx context.Context, actor Actor, id string) error

	// Members returns up to limit members by address, after afterEmail.
	Members(ctx context.Context, actor Actor, id, afterEmail string, limit int) ([]TeamMember, error)
	// SetRole changes a member's role; taking the last working admin's is
	// ErrLastAdmin. An admin who stops being one loses the open invitations
	// they sent.
	SetRole(ctx context.Context, actor Actor, id, ownerID string, role Role) error
	// RemoveMember ends ownerID's membership. An admin may remove anyone and
	// any member themselves, by the actor's own ID; removing the last admin
	// is ErrLastAdmin. Invitations to them and open ones from them go too.
	// What the member made stays with the team.
	RemoveMember(ctx context.Context, actor Actor, id, ownerID string) error

	// Invite creates i, replacing any invitation to its address. A current
	// member is ErrAlreadyMember.
	Invite(ctx context.Context, actor Actor, i TeamInvitation) error
	// Invitations lists the team's open invitations at now, newest first.
	Invitations(ctx context.Context, actor Actor, id string, now time.Time) ([]TeamInvitation, error)
	DeleteInvitation(ctx context.Context, actor Actor, id, invitationID string) error

	// InvitationsFor lists open invitations to the owner's own address. The
	// address is read from the owner's row, never from the request.
	InvitationsFor(ctx context.Context, ownerID string, now time.Time) ([]TeamInvitation, error)
	// Accept makes the owner a member with the invitation's role, when it was
	// sent to the owner's address as stored now and is open at now, and
	// returns the team. Accepting one already accepted, while still a member,
	// changes nothing and succeeds; anyone else, an expired invitation or one
	// accepted by a member since removed is ErrNotFound.
	Accept(ctx context.Context, ownerID, invitationID string, now time.Time) (Team, error)
	// Decline removes an invitation to the owner's own address.
	Decline(ctx context.Context, ownerID, invitationID string) error

	// CreateToken adds t, owned by the team t.OwnerID. Admins only.
	CreateToken(ctx context.Context, actor Actor, t APIToken) error
	// Tokens returns one page of the team's usable tokens. Admins only.
	Tokens(ctx context.Context, actor Actor, q APITokenQuery) ([]APIToken, error)
	// RevokeToken revokes the team's token tokenID. Admins only.
	RevokeToken(ctx context.Context, actor Actor, id, tokenID string, at time.Time) error

	// Operator controls, for `wardlockd admin` only: instance-wide.

	// ListAll returns up to limit teams on the instance, by ID after afterID.
	ListAll(ctx context.Context, afterID string, limit int) ([]Team, error)
	// AllMembers returns every member of the team by address.
	AllMembers(ctx context.Context, id string) ([]TeamMember, error)
	// Find returns the team named id, whoever asks.
	Find(ctx context.Context, id string) (Team, error)
	// Started counts the teams the owner started.
	Started(ctx context.Context, ownerID string) (int64, error)
	// RevokeAllTokens revokes every token of the team and returns how many.
	RevokeAllTokens(ctx context.Context, id string, at time.Time) (int64, error)
	// ForceRole makes ownerID a member with role, for a team with nobody
	// left to do it. Taking the last working admin's is ErrLastAdmin.
	ForceRole(ctx context.Context, id, ownerID string, role Role, at time.Time) error
	// TeamQuota returns the owner's override of how many teams they may
	// start; nil follows the instance.
	TeamQuota(ctx context.Context, ownerID string) (*int64, error)
	// SetTeamQuota replaces that override.
	SetTeamQuota(ctx context.Context, ownerID string, max *int64) error
}

// Teams returns the SQLite Teams repository.
func (db *DB) Teams() Teams { return teamsRepo{db} }

type teamsRepo struct{ db *DB }

// teamAddress is the reserved address of a team's row in owners: RFC 2606
// keeps .invalid out of DNS, so nothing can ever be delivered to it.
func teamAddress(id string) string { return id + teamDomain }

const teamDomain = "@team.wardlock.invalid"

// IsTeamAddress reports whether email is a team's reserved address, which no
// person can have: owners refuses it wherever an address comes in.
func IsTeamAddress(email string) bool { return strings.HasSuffix(email, teamDomain) }

// TeamAddress is the reserved address of a team's account, as GET /me shows
// it to the team's own tokens.
func TeamAddress(id string) string { return teamAddress(id) }

func (r teamsRepo) Memberships(ctx context.Context, ownerID string) ([]Membership, error) {
	rows, err := r.db.q.ListMemberships(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list memberships", err)
	}
	out := make([]Membership, len(rows))
	for i, row := range rows {
		out[i] = Membership{TeamID: row.TeamID, Role: Role(row.Role)}
	}
	return out, nil
}

func (r teamsRepo) Create(ctx context.Context, actor Actor, t Team, maxTeams int64) error {
	if actor.IsTeamToken() {
		return fmt.Errorf("create team: %w", ErrForbiddenRole)
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if _, err := q.LockOwnerQuotas(ctx, actor.OwnerID); err != nil {
			return mapErr("lock owner", err)
		}
		if err := checkTeamQuota(ctx, q, actor.OwnerID, maxTeams); err != nil {
			return err
		}
		at := ms(t.CreatedAt)
		if err := q.CreateTeamAccount(ctx, sqlcgen.CreateTeamAccountParams{ID: t.ID, Email: teamAddress(t.ID), CreatedAt: at}); err != nil {
			return mapErr("create team account", err)
		}
		err := q.CreateTeam(ctx, sqlcgen.CreateTeamParams{ID: t.ID, Name: t.Name, CreatedBy: nullString(actor.OwnerID), CreatedAt: at})
		if err != nil {
			return mapErr("create team", err)
		}
		return mapErr("add team admin", q.AddTeamMember(ctx, sqlcgen.AddTeamMemberParams{
			TeamID: t.ID, OwnerID: actor.OwnerID, Role: string(RoleAdmin), CreatedAt: at,
		}))
	})
}

// checkTeamQuota refuses another team once the owner has started maxTeams,
// or their own override of it. This keeps per-team quotas from multiplying:
// without it, an owner at a quota could start a team for more room.
func checkTeamQuota(ctx context.Context, q *sqlcgen.Queries, ownerID string, maxTeams int64) error {
	override, err := q.GetOwnerMaxTeams(ctx, ownerID)
	if err != nil {
		return mapErr("get team quota", err)
	}
	if override.Valid {
		maxTeams = override.Int64
	}
	if maxTeams == 0 {
		return nil
	}
	n, err := q.CountTeamsCreatedBy(ctx, nullString(ownerID))
	if err != nil {
		return mapErr("count teams", err)
	}
	if n >= maxTeams {
		return &QuotaError{Quota: QuotaTeams, Limit: maxTeams, Used: n, Adding: 1}
	}
	return nil
}

func teamFrom(row sqlcgen.Team) Team {
	return Team{ID: row.ID, Name: row.Name, CreatedBy: row.CreatedBy.String, CreatedAt: fromMs(row.CreatedAt)}
}

func (r teamsRepo) Get(ctx context.Context, actor Actor, id string) (Team, error) {
	if err := actor.Require(id, RoleViewer); err != nil {
		return Team{}, fmt.Errorf("get team: %w", err)
	}
	t, err := r.Find(ctx, id)
	if err != nil {
		return Team{}, err
	}
	t.Role, _ = actor.Role(id)
	return t, nil
}

func (r teamsRepo) Find(ctx context.Context, id string) (Team, error) {
	row, err := r.db.q.GetTeam(ctx, id)
	if err != nil {
		return Team{}, mapErr("get team", err)
	}
	return teamFrom(row), nil
}

func (r teamsRepo) List(ctx context.Context, actor Actor) ([]Team, error) {
	ids := make([]string, 0, len(actor.Memberships)+1)
	for _, m := range actor.Memberships {
		ids = append(ids, m.TeamID)
	}
	if actor.IsTeamToken() {
		ids = append(ids, actor.OwnerID)
	}
	if len(ids) == 0 {
		return []Team{}, nil
	}
	rows, err := r.db.q.ListTeams(ctx, ids)
	if err != nil {
		return nil, mapErr("list teams", err)
	}
	out := make([]Team, len(rows))
	for i, row := range rows {
		out[i] = teamFrom(row)
		out[i].Role, _ = actor.Role(row.ID)
	}
	return out, nil
}

func (r teamsRepo) Rename(ctx context.Context, actor Actor, id, name string) error {
	if err := actor.Require(id, RoleAdmin); err != nil {
		return fmt.Errorf("rename team: %w", err)
	}
	n, err := r.db.q.RenameTeam(ctx, sqlcgen.RenameTeamParams{Name: name, ID: id})
	return rowsAffected("rename team", n, err)
}

func (r teamsRepo) Delete(ctx context.Context, actor Actor, id string) error {
	if err := actor.Require(id, RoleAdmin); err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if _, err := q.LockOwnerQuotas(ctx, id); err != nil {
			return mapErr("lock team", err)
		}
		sites, err := q.CountOwnerSites(ctx, id)
		if err != nil {
			return mapErr("count team sites", err)
		}
		if sites > 0 {
			return fmt.Errorf("delete team: %w", ErrTeamHasSites)
		}
		n, err := q.DeleteTeamAccount(ctx, id)
		return rowsAffected("delete team", n, err)
	})
}

func (r teamsRepo) Members(ctx context.Context, actor Actor, id, afterEmail string, limit int) ([]TeamMember, error) {
	if err := actor.Require(id, RoleViewer); err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	return r.members(ctx, id, afterEmail, limit)
}

func (r teamsRepo) members(ctx context.Context, id, afterEmail string, limit int) ([]TeamMember, error) {
	rows, err := r.db.q.ListTeamMembers(ctx, sqlcgen.ListTeamMembersParams{TeamID: id, AfterEmail: afterEmail, RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list team members", err)
	}
	out := make([]TeamMember, len(rows))
	for i, row := range rows {
		out[i] = TeamMember{
			TeamID: row.TeamID, OwnerID: row.OwnerID, Email: row.Email, Role: Role(row.Role),
			CreatedAt: fromMs(row.CreatedAt), SuspendedAt: fromNullMs(row.SuspendedAt),
		}
	}
	return out, nil
}

func (r teamsRepo) SetRole(ctx context.Context, actor Actor, id, ownerID string, role Role) error {
	if err := actor.Require(id, RoleAdmin); err != nil {
		return fmt.Errorf("set member role: %w", err)
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		return setRole(ctx, q, id, ownerID, role)
	})
}

func setRole(ctx context.Context, q *sqlcgen.Queries, id, ownerID string, role Role) error {
	if err := keepAdmin(ctx, q, id, ownerID, role); err != nil {
		return err
	}
	if role != RoleAdmin {
		err := q.DeleteOpenInvitationsFrom(ctx, sqlcgen.DeleteOpenInvitationsFromParams{TeamID: id, InvitedBy: nullString(ownerID)})
		if err != nil {
			return mapErr("delete invitations", err)
		}
	}
	n, err := q.SetTeamMemberRole(ctx, sqlcgen.SetTeamMemberRoleParams{Role: string(role), TeamID: id, OwnerID: ownerID})
	return rowsAffected("set member role", n, err)
}

func (r teamsRepo) RemoveMember(ctx context.Context, actor Actor, id, ownerID string) error {
	if actor.OwnerID == ownerID && !actor.IsTeamToken() {
		if err := actor.Require(id, RoleViewer); err != nil {
			return fmt.Errorf("leave team: %w", err)
		}
	} else if err := actor.Require(id, RoleAdmin); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := keepAdmin(ctx, q, id, ownerID, ""); err != nil {
			return err
		}
		o, err := q.GetOwner(ctx, ownerID)
		if err != nil {
			return mapErr("get member", err)
		}
		if err := q.DeleteInvitationsTo(ctx, sqlcgen.DeleteInvitationsToParams{TeamID: id, Email: o.Email}); err != nil {
			return mapErr("delete invitations", err)
		}
		err = q.DeleteOpenInvitationsFrom(ctx, sqlcgen.DeleteOpenInvitationsFromParams{TeamID: id, InvitedBy: nullString(ownerID)})
		if err != nil {
			return mapErr("delete invitations", err)
		}
		n, err := q.RemoveTeamMember(ctx, sqlcgen.RemoveTeamMemberParams{TeamID: id, OwnerID: ownerID})
		return rowsAffected("remove member", n, err)
	})
}

// keepAdmin refuses taking ownerID's admin role when no other working admin
// would remain; role is what they would become ("" for nothing). It locks the team's
// row, so two admins cannot each demote the other at once.
func keepAdmin(ctx context.Context, q *sqlcgen.Queries, teamID, ownerID string, role Role) error {
	if _, err := q.LockOwnerQuotas(ctx, teamID); err != nil {
		return mapErr("lock team", err)
	}
	cur, err := q.GetTeamMemberRole(ctx, sqlcgen.GetTeamMemberRoleParams{TeamID: teamID, OwnerID: ownerID})
	if err != nil {
		return mapErr("get member role", err)
	}
	if Role(cur) != RoleAdmin || role == RoleAdmin {
		return nil
	}
	n, err := q.CountOtherWorkingAdmins(ctx, sqlcgen.CountOtherWorkingAdminsParams{TeamID: teamID, OwnerID: ownerID})
	if err != nil {
		return mapErr("count team admins", err)
	}
	if n == 0 {
		return fmt.Errorf("change member: %w", ErrLastAdmin)
	}
	return nil
}

func (r teamsRepo) Invite(ctx context.Context, actor Actor, i TeamInvitation) error {
	if err := actor.Require(i.TeamID, RoleAdmin); err != nil {
		return fmt.Errorf("invite member: %w", err)
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		n, err := q.CountMembersWithEmail(ctx, sqlcgen.CountMembersWithEmailParams{TeamID: i.TeamID, Email: i.Email})
		if err != nil {
			return mapErr("find member", err)
		}
		if n > 0 {
			return fmt.Errorf("invite member: %w", ErrAlreadyMember)
		}
		return mapErr("invite member", q.UpsertTeamInvitation(ctx, sqlcgen.UpsertTeamInvitationParams{
			ID: i.ID, TeamID: i.TeamID, Email: i.Email, Role: string(i.Role), InvitedBy: nullString(actor.OwnerID),
			CreatedAt: ms(i.CreatedAt), ExpiresAt: ms(i.ExpiresAt),
		}))
	})
}

func (r teamsRepo) Invitations(ctx context.Context, actor Actor, id string, now time.Time) ([]TeamInvitation, error) {
	if err := actor.Require(id, RoleAdmin); err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	rows, err := r.db.q.ListTeamInvitations(ctx, sqlcgen.ListTeamInvitationsParams{TeamID: id, Now: ms(now)})
	if err != nil {
		return nil, mapErr("list invitations", err)
	}
	out := make([]TeamInvitation, len(rows))
	for i, row := range rows {
		out[i] = invitationFrom(sqlcgen.GetTeamInvitationRow(row))
	}
	return out, nil
}

func invitationFrom(row sqlcgen.GetTeamInvitationRow) TeamInvitation {
	return TeamInvitation{
		ID: row.ID, TeamID: row.TeamID, TeamName: row.TeamName, Email: row.Email, Role: Role(row.Role),
		InvitedBy: row.InvitedBy.String, CreatedAt: fromMs(row.CreatedAt), ExpiresAt: fromMs(row.ExpiresAt),
		AcceptedAt: fromNullMs(row.AcceptedAt),
	}
}

func (r teamsRepo) DeleteInvitation(ctx context.Context, actor Actor, id, invitationID string) error {
	if err := actor.Require(id, RoleAdmin); err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	n, err := r.db.q.DeleteTeamInvitation(ctx, sqlcgen.DeleteTeamInvitationParams{ID: invitationID, TeamID: id})
	return rowsAffected("delete invitation", n, err)
}

func (r teamsRepo) InvitationsFor(ctx context.Context, ownerID string, now time.Time) ([]TeamInvitation, error) {
	owner, err := r.db.q.GetOwner(ctx, ownerID)
	if err != nil {
		return nil, mapErr("get owner", err)
	}
	rows, err := r.db.q.ListInvitationsForEmail(ctx, sqlcgen.ListInvitationsForEmailParams{Email: owner.Email, Now: ms(now)})
	if err != nil {
		return nil, mapErr("list invitations", err)
	}
	out := make([]TeamInvitation, len(rows))
	for i, row := range rows {
		out[i] = invitationFrom(sqlcgen.GetTeamInvitationRow(row))
	}
	return out, nil
}

// invitationFor reads an invitation addressed to ownerID's current address,
// so anyone else's is ErrNotFound, exactly like a missing one.
func invitationFor(ctx context.Context, q *sqlcgen.Queries, ownerID, id string) (sqlcgen.GetTeamInvitationRow, error) {
	owner, err := q.GetOwner(ctx, ownerID)
	if err != nil {
		return sqlcgen.GetTeamInvitationRow{}, mapErr("get owner", err)
	}
	row, err := q.GetTeamInvitation(ctx, id)
	if err != nil {
		return sqlcgen.GetTeamInvitationRow{}, mapErr("get invitation", err)
	}
	if row.Email != owner.Email {
		return sqlcgen.GetTeamInvitationRow{}, fmt.Errorf("get invitation: %w", ErrNotFound)
	}
	return row, nil
}

func (r teamsRepo) Accept(ctx context.Context, ownerID, invitationID string, now time.Time) (Team, error) {
	var team Team
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		row, err := invitationFor(ctx, q, ownerID, invitationID)
		if err != nil {
			return err
		}
		role, err := q.GetTeamMemberRole(ctx, sqlcgen.GetTeamMemberRoleParams{TeamID: row.TeamID, OwnerID: ownerID})
		member := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return mapErr("get member role", err)
		}
		team = Team{ID: row.TeamID, Name: row.TeamName, Role: Role(role)}
		if row.AcceptedAt.Valid {
			if !member {
				return fmt.Errorf("accept invitation: %w", ErrNotFound)
			}
			return nil
		}
		if row.ExpiresAt <= ms(now) {
			return fmt.Errorf("accept invitation: %w", ErrNotFound)
		}
		if !member {
			team.Role = Role(row.Role)
			err := q.AddTeamMember(ctx, sqlcgen.AddTeamMemberParams{TeamID: row.TeamID, OwnerID: ownerID, Role: row.Role, CreatedAt: ms(now)})
			if err != nil {
				return mapErr("add team member", err)
			}
		}
		return mapErr("mark invitation accepted", q.MarkTeamInvitationAccepted(ctx, sqlcgen.MarkTeamInvitationAcceptedParams{
			AcceptedAt: nullMs(now), ID: row.ID,
		}))
	})
	if err != nil {
		return Team{}, err
	}
	return team, nil
}

func (r teamsRepo) Decline(ctx context.Context, ownerID, invitationID string) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		row, err := invitationFor(ctx, q, ownerID, invitationID)
		if err != nil {
			return err
		}
		n, err := q.DeleteTeamInvitation(ctx, sqlcgen.DeleteTeamInvitationParams{ID: row.ID, TeamID: row.TeamID})
		return rowsAffected("decline invitation", n, err)
	})
}

func (r teamsRepo) CreateToken(ctx context.Context, actor Actor, t APIToken) error {
	if err := actor.Require(t.OwnerID, RoleAdmin); err != nil {
		return fmt.Errorf("create team token: %w", err)
	}
	t.CreatedBy = actor.OwnerID
	return insertAPIToken(ctx, r.db.q, t, AccountTeam)
}

func (r teamsRepo) Tokens(ctx context.Context, actor Actor, q APITokenQuery) ([]APIToken, error) {
	if err := actor.Require(q.OwnerID, RoleAdmin); err != nil {
		return nil, fmt.Errorf("list team tokens: %w", err)
	}
	return r.db.APITokens().List(ctx, q)
}

func (r teamsRepo) RevokeToken(ctx context.Context, actor Actor, id, tokenID string, at time.Time) error {
	if err := actor.Require(id, RoleAdmin); err != nil {
		return fmt.Errorf("revoke team token: %w", err)
	}
	return r.db.APITokens().Revoke(ctx, id, tokenID, at)
}

func (r teamsRepo) ListAll(ctx context.Context, afterID string, limit int) ([]Team, error) {
	rows, err := r.db.q.ListAllTeams(ctx, sqlcgen.ListAllTeamsParams{AfterID: afterID, RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list all teams", err)
	}
	out := make([]Team, len(rows))
	for i, row := range rows {
		out[i] = teamFrom(row)
	}
	return out, nil
}

// allMembersPage bounds one read of AllMembers.
const allMembersPage = 200

func (r teamsRepo) AllMembers(ctx context.Context, id string) ([]TeamMember, error) {
	var out []TeamMember
	after := ""
	for {
		page, err := r.members(ctx, id, after, allMembersPage)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < allMembersPage {
			return out, nil
		}
		after = page[len(page)-1].Email
	}
}

func (r teamsRepo) RevokeAllTokens(ctx context.Context, id string, at time.Time) (int64, error) {
	if _, err := r.Find(ctx, id); err != nil {
		return 0, err
	}
	n, err := r.db.q.RevokeAllAccountTokens(ctx, sqlcgen.RevokeAllAccountTokensParams{RevokedAt: nullMs(at), OwnerID: id})
	return n, mapErr("revoke team tokens", err)
}

func (r teamsRepo) ForceRole(ctx context.Context, id, ownerID string, role Role, at time.Time) error {
	if _, err := r.Find(ctx, id); err != nil {
		return err
	}
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		_, err := q.GetTeamMemberRole(ctx, sqlcgen.GetTeamMemberRoleParams{TeamID: id, OwnerID: ownerID})
		if errors.Is(err, sql.ErrNoRows) {
			return mapErr("add team member", q.AddTeamMember(ctx, sqlcgen.AddTeamMemberParams{
				TeamID: id, OwnerID: ownerID, Role: string(role), CreatedAt: ms(at),
			}))
		}
		if err != nil {
			return mapErr("get member role", err)
		}
		return setRole(ctx, q, id, ownerID, role)
	})
}

func (r teamsRepo) Started(ctx context.Context, ownerID string) (int64, error) {
	n, err := r.db.q.CountTeamsCreatedBy(ctx, nullString(ownerID))
	return n, mapErr("count teams", err)
}

func (r teamsRepo) TeamQuota(ctx context.Context, ownerID string) (*int64, error) {
	v, err := r.db.q.GetOwnerMaxTeams(ctx, ownerID)
	if err != nil {
		return nil, mapErr("get team quota", err)
	}
	if !v.Valid {
		return nil, nil
	}
	return &v.Int64, nil
}

func (r teamsRepo) SetTeamQuota(ctx context.Context, ownerID string, max *int64) error {
	n, err := r.db.q.SetOwnerMaxTeams(ctx, sqlcgen.SetOwnerMaxTeamsParams{MaxTeams: nullOverride(max), ID: ownerID})
	return rowsAffected("set team quota", n, err)
}
