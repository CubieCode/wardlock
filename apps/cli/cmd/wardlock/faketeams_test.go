// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"slices"

	"wardlock.nz/apiclient"
)

// fakeTeams is the fake API's teams (T7.1), as the real server answers them:
// a team the caller is not in is the same 404 as a missing one, and 403
// forbidden_role comes only where they can see it.
type fakeTeams struct {
	// teams are the caller's, each with their role in it.
	teams   []apiclient.Team
	members map[string][]apiclient.TeamMember
	// tokenTeam, when set, makes the token a team token of that team: it
	// sees only that team, as an editor, and no personal account.
	tokenTeam      string
	suspendedTeams map[string]bool
	// account is the team of each team's group and tag, by ID; anything not
	// listed is the owner's.
	account map[string]string
	// transfers records transferSite bodies; quotaFull refuses the next.
	transfers []apiclient.TransferSiteRequest
	quotaFull bool
}

var (
	personalOwner = apiclient.SiteOwner{Kind: apiclient.SiteOwnerKindOwner, Id: "01928c5e-7a1b-7c3d-9e4f-000000000001", Name: "owner@example.test"}
	forbiddenRole = apiErr("forbidden_role", "Your role in this team does not allow this.")
)

func roleRank(r apiclient.TeamRole) int {
	return slices.Index([]apiclient.TeamRole{apiclient.TeamRoleViewer, apiclient.TeamRoleEditor, apiclient.TeamRoleAdmin}, r)
}

// role is the caller's role in a team, false for a team they cannot see.
func (f *fakeAPI) role(team string) (apiclient.TeamRole, bool) {
	if f.tokenTeam != "" {
		return apiclient.TeamRoleEditor, team == f.tokenTeam
	}
	i := slices.IndexFunc(f.teams, func(t apiclient.Team) bool { return t.Id == team })
	if i < 0 {
		return "", false
	}
	return f.teams[i].Role, true
}

// require answers the refusal and returns false unless the caller has need in
// team. "" is the caller's own account, which allows anything.
func (f *fakeAPI) require(w http.ResponseWriter, team string, need apiclient.TeamRole) bool {
	if team == "" {
		return true
	}
	role, ok := f.role(team)
	switch {
	case !ok:
		reply(w, http.StatusNotFound, notFound)
		return false
	case roleRank(role) < roleRank(need):
		reply(w, http.StatusForbidden, forbiddenRole)
		return false
	}
	return true
}

// accountOf is the account a request works in: ?team=, else the team of a
// team token, else "" for the owner's own.
func (f *fakeAPI) accountOf(r *http.Request) string {
	if team := r.URL.Query().Get("team"); team != "" {
		return team
	}
	return f.tokenTeam
}

// siteAccount is the team holding a site, "" for the owner's own.
func siteAccount(s *fakeSite) string {
	if s.site.Owner.Kind == apiclient.SiteOwnerKindTeam {
		return s.site.Owner.Id
	}
	return ""
}

func (f *fakeAPI) visible(s *fakeSite) bool {
	team := siteAccount(s)
	if team == "" {
		return f.tokenTeam == ""
	}
	_, ok := f.role(team)
	return ok
}

func (f *fakeAPI) teamOwner(id string) apiclient.SiteOwner {
	i := slices.IndexFunc(f.teams, func(t apiclient.Team) bool { return t.Id == id })
	return apiclient.SiteOwner{Kind: apiclient.SiteOwnerKindTeam, Id: id, Name: f.teams[i].Name}
}

func (f *fakeAPI) handleTeams(handle func(string, string, func(http.ResponseWriter, *http.Request))) {
	handle("GET /api/v0/teams", "", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		items := []apiclient.Team{}
		for _, t := range f.teams {
			if role, ok := f.role(t.Id); ok {
				t.Role = role
				items = append(items, t)
			}
		}
		reply(w, http.StatusOK, apiclient.TeamList{Items: items})
	})
	handle("GET /api/v0/teams/{teamId}/members", "", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		team := r.PathValue("teamId")
		if !f.require(w, team, apiclient.TeamRoleViewer) {
			return
		}
		items, next := paginate(r, f.members[team])
		reply(w, http.StatusOK, apiclient.TeamMemberPage{Items: items, NextCursor: next})
	})
	handle("POST /api/v0/sites/{site}/transfer", "manage", f.withSite(f.transfer))
}

func (f *fakeAPI) transfer(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	var body apiclient.TransferSiteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusBadRequest, apiErr("invalid_request", "The request is malformed."))
		return
	}
	team := deref(body.To.Id)
	if (body.To.Kind == apiclient.TransferSiteRequestToKindTeam) != (team != "") {
		reply(w, http.StatusBadRequest, apiclient.Error{Code: "invalid_request", Message: "to.id names the team, and only with kind team.", Target: ptr("to")})
		return
	}
	// A team token acts as an editor, so it is refused here for the site it
	// can see, whatever the target.
	if !f.require(w, siteAccount(s), apiclient.TeamRoleAdmin) || !f.require(w, team, apiclient.TeamRoleAdmin) {
		return
	}
	switch {
	case f.takenDown[s.site.Name]:
		reply(w, http.StatusConflict, apiErr("site_unavailable", "The operator of this server has taken this site down."))
		return
	case f.suspendedTeams[team]:
		reply(w, http.StatusConflict, apiErr("team_suspended", "The operator of this server has suspended this team."))
		return
	case f.quotaFull:
		reply(w, http.StatusRequestEntityTooLarge, apiErr("limit_exceeded", "The target has as many sites as this server allows it."))
		return
	}
	f.transfers = append(f.transfers, body)
	s.site.Owner = personalOwner
	if team != "" {
		s.site.Owner = f.teamOwner(team)
	}
	s.site.GroupId, s.site.TagIds = nil, []string{}
	reply(w, http.StatusOK, s.site)
}

// addTeam makes the caller a member of a new team with this role.
func (f *fakeAPI) addTeam(name string, role apiclient.TeamRole) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := apiclient.Team{Id: f.id(), Name: name, Role: role, CreatedAt: 1_800_000_000_000}
	f.teams = append(f.teams, t)
	return t.Id
}

// addTeamSite creates a site in a team.
func (f *fakeAPI) addTeamSite(name, team string) *fakeSite {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.newSite(name, "")
	s.site.Owner = f.teamOwner(team)
	return s
}

// addTeamGroup and addTeamTag create a team's group or tag and return its ID.
func (f *fakeAPI) addTeamGroup(team, name string) string {
	id := f.addGroup(name)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.account[id] = team
	return id
}

func (f *fakeAPI) addTeamTag(team, name string) string {
	id := f.addTag(name, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.account[id] = team
	return id
}

// ownerOf is who holds a site now.
func (f *fakeAPI) ownerOf(name string) apiclient.SiteOwner {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sites[name].site.Owner
}
