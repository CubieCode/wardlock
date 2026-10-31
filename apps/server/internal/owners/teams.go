// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store"
)

// Team limits.
const (
	// MaxTeamName is the longest team name, in characters.
	MaxTeamName = 80
	// TeamInviteValidity is how long a team invitation can be accepted.
	TeamInviteValidity = 7 * 24 * time.Hour
	// TeamInvitationsPath is the dashboard page for an owner's invitations.
	TeamInvitationsPath = "/team-invitations"
)

// Team errors.
var (
	// ErrInvalidTeamName means a team name is empty or too long.
	ErrInvalidTeamName = errors.New("invalid team name")
	// ErrInvalidRole means a role is not admin, editor or viewer.
	ErrInvalidRole = errors.New("invalid role")
	// ErrTeamLimit means the owner has started as many teams as they may.
	ErrTeamLimit = errors.New("team limit reached")
)

// TeamsConfig is what the Service needs for teams; zero MaxTeams is
// unlimited.
type TeamsConfig struct {
	Teams store.Teams
	// MaxTeams is how many teams one owner may start
	// (WARDLOCK_QUOTA_TEAMS), before their own override.
	MaxTeams int64
}

func teamName(name string) (string, error) {
	name = strings.TrimSpace(strings.ToValidUTF8(name, ""))
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxTeamName || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidTeamName
	}
	return name, nil
}

// Memberships returns the owner's teams and roles, for their Actor.
func (s *Service) Memberships(ctx context.Context, ownerID string) ([]store.Membership, error) {
	if s.teams == nil {
		return nil, nil
	}
	return s.teams.Memberships(ctx, ownerID)
}

// CreateTeam starts a team with the actor as its first admin.
func (s *Service) CreateTeam(ctx context.Context, actor store.Actor, name string) (store.Team, error) {
	name, err := teamName(name)
	if err != nil {
		return store.Team{}, err
	}
	if err := s.allow("team:"+actor.OwnerID, s.limits.InvitePerOwner); err != nil {
		return store.Team{}, err
	}
	t := store.Team{ID: store.NewID(), Name: name, CreatedBy: actor.OwnerID, CreatedAt: s.now(), Role: store.RoleAdmin}
	err = s.teams.Create(ctx, actor, t, s.maxTeams)
	if qe := (*store.QuotaError)(nil); errors.As(err, &qe) {
		return store.Team{}, fmt.Errorf("%w: %d", ErrTeamLimit, qe.Limit)
	}
	if err != nil {
		return store.Team{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "team created", slog.String("team_id", t.ID), slog.String("owner_id", actor.OwnerID))
	return t, nil
}

// Team returns a team the actor can see.
func (s *Service) Team(ctx context.Context, actor store.Actor, id string) (store.Team, error) {
	return s.teams.Get(ctx, actor, id)
}

// Teams lists the teams the actor can see.
func (s *Service) Teams(ctx context.Context, actor store.Actor) ([]store.Team, error) {
	return s.teams.List(ctx, actor)
}

// RenameTeam renames a team the actor administers.
func (s *Service) RenameTeam(ctx context.Context, actor store.Actor, id, name string) (store.Team, error) {
	name, err := teamName(name)
	if err != nil {
		return store.Team{}, err
	}
	if err := s.teams.Rename(ctx, actor, id, name); err != nil {
		return store.Team{}, err
	}
	return s.teams.Get(ctx, actor, id)
}

// DeleteTeam deletes a team with no sites that the actor administers.
func (s *Service) DeleteTeam(ctx context.Context, actor store.Actor, id string) error {
	if err := s.teams.Delete(ctx, actor, id); err != nil {
		return err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "team deleted", slog.String("team_id", id), slog.String("owner_id", actor.OwnerID))
	return nil
}

// TeamMembers returns up to limit members by address, after afterEmail.
func (s *Service) TeamMembers(ctx context.Context, actor store.Actor, id, afterEmail string, limit int) ([]store.TeamMember, error) {
	return s.teams.Members(ctx, actor, id, afterEmail, limit)
}

// SetMemberRole changes a member's role and returns the member.
func (s *Service) SetMemberRole(ctx context.Context, actor store.Actor, id, ownerID string, role store.Role) (store.TeamMember, error) {
	if !role.Valid() {
		return store.TeamMember{}, ErrInvalidRole
	}
	if err := s.teams.SetRole(ctx, actor, id, ownerID, role); err != nil {
		return store.TeamMember{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "team role changed", slog.String("team_id", id), slog.String("owner_id", ownerID), slog.String("role", string(role)))
	o, err := s.owners.Get(ctx, ownerID)
	if err != nil {
		return store.TeamMember{}, err
	}
	return store.TeamMember{TeamID: id, OwnerID: ownerID, Email: o.Email, Role: role, CreatedAt: o.CreatedAt, SuspendedAt: o.SuspendedAt}, nil
}

// RemoveMember ends a membership: an admin's removal or the member leaving.
func (s *Service) RemoveMember(ctx context.Context, actor store.Actor, id, ownerID string) error {
	if err := s.teams.RemoveMember(ctx, actor, id, ownerID); err != nil {
		return err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "team member removed", slog.String("team_id", id), slog.String("owner_id", ownerID))
	return nil
}

// InviteToTeam invites email to the team as role. An owner who has signed in
// is emailed a link to their invitations; an address with no account, or one
// that never signed in, gets the normal owner invitation instead when
// invitations are open, and the invitation is refused otherwise. Sending runs
// in the background.
func (s *Service) InviteToTeam(ctx context.Context, actor store.Actor, teamID, email string, role store.Role) (store.TeamInvitation, error) {
	email, err := NormaliseEmail(email)
	if err != nil {
		return store.TeamInvitation{}, err
	}
	if !role.Valid() {
		return store.TeamInvitation{}, ErrInvalidRole
	}
	team, err := s.teams.Get(ctx, actor, teamID)
	if err != nil {
		return store.TeamInvitation{}, err
	}
	if err := actor.Require(teamID, store.RoleAdmin); err != nil {
		return store.TeamInvitation{}, err
	}
	if err := s.allow("invite:"+actor.OwnerID, s.limits.InvitePerOwner); err != nil {
		return store.TeamInvitation{}, err
	}
	o, err := s.owners.GetByEmail(ctx, email)
	known := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.TeamInvitation{}, err
	}
	if !known && s.invites == InvitesOperator {
		return store.TeamInvitation{}, ErrInvitesRestricted
	}
	now := s.now()
	inv := store.TeamInvitation{
		ID: store.NewID(), TeamID: teamID, TeamName: team.Name, Email: email, Role: role,
		InvitedBy: actor.OwnerID, CreatedAt: now, ExpiresAt: now.Add(TeamInviteValidity),
	}
	if err := s.teams.Invite(ctx, actor, inv); err != nil {
		return store.TeamInvitation{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "team invitation created", slog.String("team_id", teamID), slog.String("invitation_id", inv.ID))
	s.background(ctx, "team invitation", func(ctx context.Context) error {
		switch {
		case !known, o.AcceptedAt.IsZero() && s.invites != InvitesOperator:
			return s.invite(ctx, actor.OwnerID, email)
		case !o.SuspendedAt.IsZero():
			return nil
		}
		return s.sendTeamInvite(ctx, actor.OwnerID, inv)
	})
	return inv, nil
}

func (s *Service) sendTeamInvite(ctx context.Context, inviterID string, inv store.TeamInvitation) error {
	inviter, err := s.owners.Get(ctx, inviterID)
	if err != nil {
		return fmt.Errorf("inviter %s: %w", inviterID, err)
	}
	u := *s.dash
	u.Path = TeamInvitationsPath
	msg, err := mail.TeamInviteMessage(netmail.Address{Address: inv.Email}, mail.TeamInvite{
		URL: u.String(), Host: s.dash.Hostname(), Team: inv.TeamName, InvitedBy: inviter.Email, ValidFor: TeamInviteValidity,
	})
	if err != nil {
		return fmt.Errorf("team invitation %s: %w", inv.ID, err)
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("team invitation %s: %w", inv.ID, err)
	}
	return nil
}

// TeamInvitations lists a team's open invitations.
func (s *Service) TeamInvitations(ctx context.Context, actor store.Actor, id string) ([]store.TeamInvitation, error) {
	return s.teams.Invitations(ctx, actor, id, s.now())
}

// CancelTeamInvitation deletes an open invitation.
func (s *Service) CancelTeamInvitation(ctx context.Context, actor store.Actor, id, invitationID string) error {
	return s.teams.DeleteInvitation(ctx, actor, id, invitationID)
}

// MyTeamInvitations lists invitations to the owner's own address.
func (s *Service) MyTeamInvitations(ctx context.Context, ownerID string) ([]store.TeamInvitation, error) {
	return s.teams.InvitationsFor(ctx, ownerID, s.now())
}

// AcceptTeamInvitation joins the team. The address is checked against the
// owner's account inside the repository, never taken from the request.
func (s *Service) AcceptTeamInvitation(ctx context.Context, ownerID, invitationID string) (store.Team, error) {
	t, err := s.teams.Accept(ctx, ownerID, invitationID, s.now())
	if err != nil {
		return store.Team{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "team invitation accepted", slog.String("team_id", t.ID), slog.String("owner_id", ownerID))
	return t, nil
}

// DeclineTeamInvitation removes an invitation to the owner's own address.
func (s *Service) DeclineTeamInvitation(ctx context.Context, ownerID, invitationID string) error {
	return s.teams.Decline(ctx, ownerID, invitationID)
}

// Creator returns the address a createdBy shows for an owner; "" if none.
func (s *Service) Creator(ctx context.Context, ownerID string) (string, error) {
	o, err := s.owners.Get(ctx, ownerID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return o.Email, nil
}

// ListAllTeams calls fn for every team on the instance, for
// `wardlockd admin list-teams`.
func (s *Service) ListAllTeams(ctx context.Context, fn func(store.Team, []store.TeamMember) error) error {
	const page = 200
	after := ""
	for {
		teams, err := s.teams.ListAll(ctx, after, page)
		if err != nil {
			return err
		}
		for _, t := range teams {
			members, err := s.teams.AllMembers(ctx, t.ID)
			if err != nil {
				return err
			}
			if err := fn(t, members); err != nil {
				return err
			}
		}
		if len(teams) < page {
			return nil
		}
		after = teams[len(teams)-1].ID
	}
}

// FindTeam returns the team with this ID, for the operator.
func (s *Service) FindTeam(ctx context.Context, id string) (store.Team, error) {
	return s.teams.Find(ctx, id)
}

// TeamQuotas returns the team's overrides and usage, for the operator: a
// team's quotas are its own (T7.1).
func (s *Service) TeamQuotas(ctx context.Context, id string) (store.Team, store.QuotaOverrides, store.OwnerUsage, error) {
	t, err := s.teams.Find(ctx, id)
	if err != nil {
		return store.Team{}, store.QuotaOverrides{}, store.OwnerUsage{}, err
	}
	over, usage, err := s.owners.Quotas(ctx, id, s.now())
	return t, over, usage, err
}

// SetTeamQuotas replaces the team's overrides, for the operator.
func (s *Service) SetTeamQuotas(ctx context.Context, id string, over store.QuotaOverrides) (store.Team, error) {
	t, err := s.teams.Find(ctx, id)
	if err != nil {
		return store.Team{}, err
	}
	return t, s.owners.SetQuotas(ctx, id, over)
}

// OwnerTeamQuota returns the owner's override of how many teams they may
// start, and SetOwnerTeamQuota replaces it: nil follows the instance.
func (s *Service) OwnerTeamQuota(ctx context.Context, ownerID string) (*int64, error) {
	return s.teams.TeamQuota(ctx, ownerID)
}

// RevokeTeamTokens revokes every token of the team, for the operator: a
// team has no suspension of its own.
func (s *Service) RevokeTeamTokens(ctx context.Context, id string) (int64, error) {
	return s.teams.RevokeAllTokens(ctx, id, s.now())
}

// ForceTeamRole gives the owner with this address a role in the team, for the
// operator repairing a team with no working admin.
func (s *Service) ForceTeamRole(ctx context.Context, id, email string, role store.Role) (store.Owner, error) {
	if !role.Valid() {
		return store.Owner{}, ErrInvalidRole
	}
	o, err := s.operatorOwner(ctx, email)
	if err != nil {
		return store.Owner{}, err
	}
	return o, s.teams.ForceRole(ctx, id, o.ID, role, s.now())
}

// TeamsStarted counts the teams the owner has started.
func (s *Service) TeamsStarted(ctx context.Context, ownerID string) (int64, error) {
	return s.teams.Started(ctx, ownerID)
}

// SetOwnerTeamQuota replaces the owner's team override.
func (s *Service) SetOwnerTeamQuota(ctx context.Context, ownerID string, max *int64) error {
	return s.teams.SetTeamQuota(ctx, ownerID, max)
}

// CreateTeamToken mints a token owned by the team. The actor must be its
// admin; the token outlives them.
func (s *Tokens) CreateTeamToken(ctx context.Context, actor store.Actor, teamID, name string, scopes Scopes, expiresAt time.Time) (APITokenInfo, string, error) {
	if err := actor.Require(teamID, store.RoleAdmin); err != nil {
		return APITokenInfo{}, "", err
	}
	name, err := tokenName(name)
	if err != nil {
		return APITokenInfo{}, "", err
	}
	if !scopes.Any() {
		return APITokenInfo{}, "", ErrInvalidScopes
	}
	now := s.now()
	if !expiresAt.IsZero() && !expiresAt.After(now) {
		return APITokenInfo{}, "", ErrInvalidExpiry
	}
	save := func(ctx context.Context, t store.APIToken) error { return s.teams.CreateToken(ctx, actor, t) }
	row, token, err := s.mint(ctx, teamID, name, scopes, expiresAt, now, save)
	if err != nil {
		return APITokenInfo{}, "", err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "team token created",
		slog.String("team_id", teamID), slog.String("token_id", row.ID), slog.String("public_id", row.PublicID), slog.String("created_by", actor.OwnerID))
	return tokenInfo(row), token, nil
}

// ListTeamTokens returns one page of the team's usable tokens.
func (s *Tokens) ListTeamTokens(ctx context.Context, actor store.Actor, teamID, cursor string, limit int) ([]APITokenInfo, string, error) {
	return s.page(ctx, teamID, cursor, limit, func(ctx context.Context, q store.APITokenQuery) ([]store.APIToken, error) {
		return s.teams.Tokens(ctx, actor, q)
	})
}

// RevokeTeamToken revokes the team's token id.
func (s *Tokens) RevokeTeamToken(ctx context.Context, actor store.Actor, teamID, id string) error {
	return s.teams.RevokeToken(ctx, actor, teamID, id, s.now())
}

// TokenName returns a token's name for createdBy, revoked or not; "" if gone.
func (s *Tokens) TokenName(ctx context.Context, id string) (string, error) {
	name, err := s.tokens.Name(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return name, err
}
