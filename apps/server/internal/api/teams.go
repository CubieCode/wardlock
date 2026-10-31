// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
)

// Teams (T7.1). Every handler takes the actor from the verified request
// context. The repositories decide who may do what: a team the actor cannot
// see answers 404, and 403 forbidden_role only where they can see it.

// teamFailed maps a team operation's error to its response; anything else is
// returned to be answered 500.
func teamFailed(err error) (*failure, error) {
	if e, ok := tokenError(err); ok {
		return &failure{Body: e, StatusCode: http.StatusBadRequest}, nil
	}
	for _, m := range []struct {
		err     error
		status  int
		code    string
		message string
		target  string
	}{
		{store.ErrNotFound, http.StatusNotFound, "not_found", "Not found.", ""},
		{owners.ErrNotFound, http.StatusNotFound, "not_found", "Not found.", ""},
		{store.ErrForbiddenRole, http.StatusForbidden, "forbidden_role", "Your role in this team does not allow this.", ""},
		{store.ErrAlreadyMember, http.StatusConflict, "already_member", "This address is already a member. Change their role instead.", "email"},
		{store.ErrLastAdmin, http.StatusConflict, "team_needs_admin", "A team must keep at least one admin.", ""},
		{store.ErrTeamHasSites, http.StatusConflict, "team_has_sites", "This team still has sites. Move or delete them first.", ""},
		{owners.ErrInvalidTeamName, http.StatusBadRequest, "invalid_request", "name must be 1 to 80 characters.", "name"},
		{owners.ErrInvalidRole, http.StatusBadRequest, "invalid_request", "role must be admin, editor or viewer.", "role"},
		{owners.ErrInvalidEmail, http.StatusBadRequest, "invalid_request", "email must be a plain email address.", "email"},
		{owners.ErrInvitesRestricted, http.StatusForbidden, "invites_restricted", "This address has no account here, and only the operator can add owners.", "email"},
		{owners.ErrTeamLimit, http.StatusRequestEntityTooLarge, "limit_exceeded", "You have started as many teams as this server allows you.", "name"},
		{owners.ErrInvalidCursor, http.StatusBadRequest, "invalid_request", "cursor is not a value this server issued.", "cursor"},
	} {
		if errors.Is(err, m.err) {
			return fail(m.status, m.code, m.message, m.target), nil
		}
	}
	if rl := (*owners.RateLimitError)(nil); errors.As(err, &rl) {
		return fail(http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.", ""), nil
	}
	return nil, err
}

func teamBody(t store.Team) Team {
	return Team{Id: t.ID, Name: t.Name, Role: TeamRole(t.Role), CreatedAt: t.CreatedAt.UnixMilli()}
}

func teamMemberBody(m store.TeamMember) TeamMember {
	return TeamMember{
		OwnerId: m.OwnerID, Email: Email(m.Email), Role: TeamRole(m.Role),
		CreatedAt: m.CreatedAt.UnixMilli(), Suspended: !m.SuspendedAt.IsZero(),
	}
}

func teamInvitationBody(i store.TeamInvitation) TeamInvitation {
	return TeamInvitation{
		Id: i.ID, TeamId: i.TeamID, TeamName: i.TeamName, Email: Email(i.Email), Role: TeamRole(i.Role),
		CreatedAt: i.CreatedAt.UnixMilli(), ExpiresAt: i.ExpiresAt.UnixMilli(),
	}
}

// teamTokenMe describes a team token: it has a team, not a person.
func (s *Server) teamTokenMe(ctx context.Context, actor store.Actor) (GetMeResponseObject, error) {
	t, err := s.Owners.Team(ctx, actor, actor.OwnerID)
	if err != nil {
		return nil, err
	}
	me := Me{
		Owner:      Owner{Id: t.ID, Email: Email(store.TeamAddress(t.ID)), CreatedAt: t.CreatedAt.UnixMilli()},
		Credential: MeCredentialApiToken,
		Scopes:     scopesBody(info(ctx).scopes),
	}
	me.Team = &struct {
		Id   Id       `json:"id"`
		Name TeamName `json:"name"`
	}{Id: t.ID, Name: t.Name}
	return GetMe200JSONResponse(me), nil
}

// ListTeams lists the teams the caller can see.
func (s *Server) ListTeams(ctx context.Context, _ ListTeamsRequestObject) (ListTeamsResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return ListTeamsdefaultJSONResponse(*unauthenticated), nil
	}
	teams, err := s.Owners.Teams(ctx, actor)
	if err != nil {
		return nil, err
	}
	out := ListTeams200JSONResponse{Items: make([]Team, len(teams))}
	for i, t := range teams {
		out.Items[i] = teamBody(t)
	}
	return out, nil
}

// CreateTeam starts a team with the caller as its first admin.
func (s *Server) CreateTeam(ctx context.Context, req CreateTeamRequestObject) (CreateTeamResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return CreateTeamdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return CreateTeamdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "name is required.", "name")), nil
	}
	t, err := s.Owners.CreateTeam(ctx, actor, req.Body.Name)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return CreateTeamdefaultJSONResponse(*f), nil
	}
	return CreateTeam201JSONResponse(teamBody(t)), nil
}

// GetTeam returns a team the caller can see.
func (s *Server) GetTeam(ctx context.Context, req GetTeamRequestObject) (GetTeamResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return GetTeamdefaultJSONResponse(*unauthenticated), nil
	}
	t, err := s.Owners.Team(ctx, actor, req.TeamId)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return GetTeamdefaultJSONResponse(*f), nil
	}
	return GetTeam200JSONResponse(teamBody(t)), nil
}

// UpdateTeam renames a team.
func (s *Server) UpdateTeam(ctx context.Context, req UpdateTeamRequestObject) (UpdateTeamResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return UpdateTeamdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return UpdateTeamdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "name is required.", "name")), nil
	}
	t, err := s.Owners.RenameTeam(ctx, actor, req.TeamId, req.Body.Name)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return UpdateTeamdefaultJSONResponse(*f), nil
	}
	return UpdateTeam200JSONResponse(teamBody(t)), nil
}

// DeleteTeam deletes a team with no sites.
func (s *Server) DeleteTeam(ctx context.Context, req DeleteTeamRequestObject) (DeleteTeamResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return DeleteTeamdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Owners.DeleteTeam(ctx, actor, req.TeamId); err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return DeleteTeamdefaultJSONResponse(*f), nil
	}
	return DeleteTeam204Response{}, nil
}

// ListTeamMembers lists a team's members by address.
func (s *Server) ListTeamMembers(ctx context.Context, req ListTeamMembersRequestObject) (ListTeamMembersResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return ListTeamMembersdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListTeamMembersdefaultJSONResponse(*f), nil
	}
	after, ok := decodeCursor(req.Params.Cursor)
	if !ok {
		return ListTeamMembersdefaultJSONResponse(*badCursor), nil
	}
	members, err := s.Owners.TeamMembers(ctx, actor, req.TeamId, after, limit+1)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return ListTeamMembersdefaultJSONResponse(*f), nil
	}
	out := ListTeamMembers200JSONResponse{Items: []TeamMember{}}
	if len(members) > limit {
		members = members[:limit]
		next := base64.RawURLEncoding.EncodeToString([]byte(members[limit-1].Email))
		out.NextCursor = &next
	}
	for _, m := range members {
		out.Items = append(out.Items, teamMemberBody(m))
	}
	return out, nil
}

// SetTeamMemberRole changes a member's role.
func (s *Server) SetTeamMemberRole(ctx context.Context, req SetTeamMemberRoleRequestObject) (SetTeamMemberRoleResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return SetTeamMemberRoledefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return SetTeamMemberRoledefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "role is required.", "role")), nil
	}
	m, err := s.Owners.SetMemberRole(ctx, actor, req.TeamId, req.OwnerId, store.Role(req.Body.Role))
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return SetTeamMemberRoledefaultJSONResponse(*f), nil
	}
	return SetTeamMemberRole200JSONResponse(teamMemberBody(m)), nil
}

// RemoveTeamMember removes a member, or lets one leave.
func (s *Server) RemoveTeamMember(ctx context.Context, req RemoveTeamMemberRequestObject) (RemoveTeamMemberResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return RemoveTeamMemberdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Owners.RemoveMember(ctx, actor, req.TeamId, req.OwnerId); err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return RemoveTeamMemberdefaultJSONResponse(*f), nil
	}
	return RemoveTeamMember204Response{}, nil
}

// ListTeamInvitations lists a team's open invitations.
func (s *Server) ListTeamInvitations(ctx context.Context, req ListTeamInvitationsRequestObject) (ListTeamInvitationsResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return ListTeamInvitationsdefaultJSONResponse(*unauthenticated), nil
	}
	list, err := s.Owners.TeamInvitations(ctx, actor, req.TeamId)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return ListTeamInvitationsdefaultJSONResponse(*f), nil
	}
	out := ListTeamInvitations200JSONResponse{Items: make([]TeamInvitation, len(list))}
	for i, inv := range list {
		out.Items[i] = teamInvitationBody(inv)
	}
	return out, nil
}

// InviteTeamMember invites an address to a team.
func (s *Server) InviteTeamMember(ctx context.Context, req InviteTeamMemberRequestObject) (InviteTeamMemberResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return InviteTeamMemberdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return InviteTeamMemberdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "email is required.", "email")), nil
	}
	inv, err := s.Owners.InviteToTeam(ctx, actor, req.TeamId, string(req.Body.Email), store.Role(req.Body.Role))
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return InviteTeamMemberdefaultJSONResponse(*f), nil
	}
	return InviteTeamMember201JSONResponse(teamInvitationBody(inv)), nil
}

// DeleteTeamInvitation cancels an open invitation.
func (s *Server) DeleteTeamInvitation(ctx context.Context, req DeleteTeamInvitationRequestObject) (DeleteTeamInvitationResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return DeleteTeamInvitationdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Owners.CancelTeamInvitation(ctx, actor, req.TeamId, req.InvitationId); err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return DeleteTeamInvitationdefaultJSONResponse(*f), nil
	}
	return DeleteTeamInvitation204Response{}, nil
}

// ListMyTeamInvitations lists invitations to the signed-in owner's address.
func (s *Server) ListMyTeamInvitations(ctx context.Context, _ ListMyTeamInvitationsRequestObject) (ListMyTeamInvitationsResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return ListMyTeamInvitationsdefaultJSONResponse(*unauthenticated), nil
	}
	list, err := s.Owners.MyTeamInvitations(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := ListMyTeamInvitations200JSONResponse{Items: make([]TeamInvitation, len(list))}
	for i, inv := range list {
		out.Items[i] = teamInvitationBody(inv)
	}
	return out, nil
}

// AcceptTeamInvitation joins a team the signed-in owner was invited to.
func (s *Server) AcceptTeamInvitation(ctx context.Context, req AcceptTeamInvitationRequestObject) (AcceptTeamInvitationResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return AcceptTeamInvitationdefaultJSONResponse(*unauthenticated), nil
	}
	t, err := s.Owners.AcceptTeamInvitation(ctx, ownerID, req.InvitationId)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return AcceptTeamInvitationdefaultJSONResponse(*f), nil
	}
	return AcceptTeamInvitation200JSONResponse(Team{Id: t.ID, Name: t.Name, Role: TeamRole(t.Role), CreatedAt: t.CreatedAt.UnixMilli()}), nil
}

// DeclineTeamInvitation refuses an invitation to the signed-in owner.
func (s *Server) DeclineTeamInvitation(ctx context.Context, req DeclineTeamInvitationRequestObject) (DeclineTeamInvitationResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return DeclineTeamInvitationdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Owners.DeclineTeamInvitation(ctx, ownerID, req.InvitationId); err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return DeclineTeamInvitationdefaultJSONResponse(*f), nil
	}
	return DeclineTeamInvitation204Response{}, nil
}

// ListTeamTokens lists a team's usable tokens.
func (s *Server) ListTeamTokens(ctx context.Context, req ListTeamTokensRequestObject) (ListTeamTokensResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return ListTeamTokensdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListTeamTokensdefaultJSONResponse(*f), nil
	}
	items, next, err := s.Tokens.ListTeamTokens(ctx, actor, req.TeamId, deref(req.Params.Cursor), limit)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return ListTeamTokensdefaultJSONResponse(*f), nil
	}
	out := ListTeamTokens200JSONResponse{Items: make([]ApiToken, len(items))}
	for i, t := range items {
		out.Items[i] = apiTokenBody(t)
	}
	if next != "" {
		out.NextCursor = &next
	}
	return out, nil
}

// CreateTeamToken mints a token owned by the team.
func (s *Server) CreateTeamToken(ctx context.Context, req CreateTeamTokenRequestObject) (CreateTeamTokenResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return CreateTeamTokendefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return CreateTeamTokendefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "name is required.", "name")), nil
	}
	scopes, valid := domainScopes(req.Body.Scopes)
	if !valid {
		return CreateTeamTokendefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "scopes must list deploy, manage or both.", "scopes")), nil
	}
	var expires time.Time
	if req.Body.ExpiresAt != nil {
		expires = time.UnixMilli(*req.Body.ExpiresAt).UTC()
	}
	t, raw, err := s.Tokens.CreateTeamToken(ctx, actor, req.TeamId, req.Body.Name, scopes, expires)
	if err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return CreateTeamTokendefaultJSONResponse(*f), nil
	}
	return CreateTeamToken201JSONResponse(CreatedApiToken{ApiToken: apiTokenBody(t), Token: raw}), nil
}

// RevokeTeamToken revokes a team's token.
func (s *Server) RevokeTeamToken(ctx context.Context, req RevokeTeamTokenRequestObject) (RevokeTeamTokenResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return RevokeTeamTokendefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Tokens.RevokeTeamToken(ctx, actor, req.TeamId, req.TokenId); err != nil {
		f, err := teamFailed(err)
		if f == nil {
			return nil, err
		}
		return RevokeTeamTokendefaultJSONResponse(*f), nil
	}
	return RevokeTeamToken204Response{}, nil
}

// TransferSite moves a site to or from a team.
func (s *Server) TransferSite(ctx context.Context, req TransferSiteRequestObject) (TransferSiteResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return TransferSitedefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return TransferSitedefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "to is required.", "to")), nil
	}
	to := deref(req.Body.To.Id)
	if (req.Body.To.Kind == TransferSiteRequestToKindTeam) != (to != "") {
		return TransferSitedefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "to.id names the team, and only with kind team.", "to")), nil
	}
	site, err := s.Sites.Transfer(ctx, actor, req.Site, to)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return TransferSitedefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, actor, site)
	if err != nil {
		return nil, err
	}
	return TransferSite200JSONResponse(body), nil
}

// creator names who made something; nil when it was never recorded or cannot
// be read, which never fails the response it decorates. Each name is read
// once per request, so a page of rows by one person costs one lookup.
func (s *Server) creator(ctx context.Context, ownerID, tokenID string) *CreatedBy {
	names := info(ctx).creators
	k := [2]string{ownerID, tokenID}
	if by, ok := names[k]; ok {
		return by
	}
	by := s.readCreator(ctx, ownerID, tokenID)
	if names != nil {
		names[k] = by
	}
	return by
}

func (s *Server) readCreator(ctx context.Context, ownerID, tokenID string) *CreatedBy {
	if tokenID != "" {
		if s.Tokens == nil {
			return nil
		}
		name, err := s.Tokens.TokenName(ctx, tokenID)
		if err != nil || name == "" {
			return nil
		}
		return &CreatedBy{Kind: CreatedByKindToken, Id: tokenID, Name: name}
	}
	if ownerID == "" || s.Owners == nil {
		return nil
	}
	name, err := s.Owners.Creator(ctx, ownerID)
	if err != nil || name == "" {
		return nil
	}
	return &CreatedBy{Kind: CreatedByKindOwner, Id: ownerID, Name: name}
}
