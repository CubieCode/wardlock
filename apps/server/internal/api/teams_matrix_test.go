// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
)

// The T7.1 isolation matrix: every owner-scoped operation against every kind
// of caller, through the real routing, sessions and tokens, each answering
// exactly as SPEC "Teams" says. A caller who cannot see the resource gets the
// same 404 as for a missing one; 403 forbidden_role only where they can.

type discardMail struct{}

func (discardMail) Send(context.Context, mail.Message) error { return nil }

// credential is how a matrix caller authenticates: a session cookie or a
// bearer token.
type credential struct{ cookie, bearer string }

func (e *teamEnv) send(c credential, method, path string, body any) response {
	e.t.Helper()
	var r io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		r, contentType = bytes.NewReader(b), "application/octet-stream"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v0"+path, r)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: c.cookie})
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	rec := httptest.NewRecorder()
	e.hs.h.ServeHTTP(rec, req)
	return response{rec.Code, rec.Body.Bytes(), rec.Result().Header}
}

// person is a signed-in owner.
type person struct {
	id, email string
	cred      credential
}

// fixtures are the things one site's operations act on.
type fixtures struct {
	site, team                string
	v1, active, pending, next string // deploys: an old one, the live one, one waiting for a blob, one ready
	policy, link, host        string
	group, tag                string
}

// teamEnv is one instance with a team, its members, a stranger's team and a
// personal site, each with something on it.
type teamEnv struct {
	t       *testing.T
	hs      *harness
	svc     *owners.Service
	tokens  *owners.Tokens
	people  map[string]*person
	creds   map[string]credential
	teamFx  fixtures
	ownerFx fixtures
	other   string // the stranger's team
}

var (
	liveFile    = file{"index.html", "<p>live</p>"}
	oldFile     = file{"index.html", "<p>old</p>"}
	waitingFile = file{"new.html", "<p>waiting</p>"}
	readyFile   = file{"ready.html", "<p>ready</p>"}
)

func newTeamEnv(t *testing.T) *teamEnv {
	t.Helper()
	hs := newHarness(t, deploys.DefaultLimits())
	log := slog.New(slog.DiscardHandler)
	secret := bytes.Repeat([]byte("k"), 32)
	dash := &url.URL{Scheme: "http", Host: "console." + baseDomain}
	svc, err := owners.New(owners.Config{
		Owners: hs.db.Owners(), Sessions: hs.db.Sessions(), Mailer: discardMail{}, Logger: log, Secret: secret, DashboardURL: dash,
		Teams: owners.TeamsConfig{Teams: hs.db.Teams()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	// Suspended owners' tokens are refused all through the matrix.
	limits := owners.DefaultTokenLimits()
	limits.FailuresPerIP.N = 1 << 20
	tokens, err := owners.NewTokens(owners.TokensConfig{
		Tokens: hs.db.APITokens(), Teams: hs.db.Teams(), Logger: log, Secret: secret, DashboardURL: dash, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	hs.srv.Owners, hs.srv.Tokens = svc, tokens
	e := &teamEnv{t: t, hs: hs, svc: svc, tokens: tokens, people: map[string]*person{}, creds: map[string]credential{}}
	for _, name := range []string{"admin", "editor", "viewer", "removed", "suspended", "stranger", "owner"} {
		e.person(name)
	}

	admin := e.people["admin"]
	res := e.send(admin.cred, http.MethodPost, "/teams", api.CreateTeamRequest{Name: "Web team"})
	res.want(t, http.StatusCreated, "")
	var team api.Team
	res.decode(t, &team)
	for name, role := range map[string]api.TeamRole{"editor": api.TeamRoleEditor, "viewer": api.TeamRoleViewer, "removed": api.TeamRoleEditor, "suspended": api.TeamRoleEditor} {
		e.join(admin, team.Id, e.people[name], role)
	}
	res = e.send(e.people["stranger"].cred, http.MethodPost, "/teams", api.CreateTeamRequest{Name: "Other team"})
	res.want(t, http.StatusCreated, "")
	var other api.Team
	res.decode(t, &other)
	e.other = other.Id

	e.teamFx = e.seed(admin.cred, "team-site", team.Id)
	e.ownerFx = e.seed(e.people["owner"].cred, "own-site", "")

	// Tokens, made before anyone goes, so a token's fate after is tested too.
	e.creds["editor-deploy"] = e.personalToken("editor", owners.Scopes{Deploy: true})
	e.creds["editor-manage"] = e.personalToken("editor", owners.Scopes{Manage: true})
	e.creds["viewer-manage"] = e.personalToken("viewer", owners.Scopes{Manage: true})
	e.creds["viewer-both"] = e.personalToken("viewer", owners.Scopes{Deploy: true, Manage: true})
	e.creds["admin-manage"] = e.personalToken("admin", owners.Scopes{Manage: true})
	e.creds["removed-both"] = e.personalToken("removed", owners.Scopes{Deploy: true, Manage: true})
	e.creds["suspended-both"] = e.personalToken("suspended", owners.Scopes{Deploy: true, Manage: true})
	e.creds["owner-deploy"] = e.personalToken("owner", owners.Scopes{Deploy: true})
	e.creds["owner-manage"] = e.personalToken("owner", owners.Scopes{Manage: true})
	e.creds["team-deploy"] = e.teamToken(team.Id, api.Scopes{api.ScopeDeploy})
	e.creds["team-manage"] = e.teamToken(team.Id, api.Scopes{api.ScopeManage})
	e.creds["team-both"] = e.teamToken(team.Id, api.Scopes{api.ScopeDeploy, api.ScopeManage})
	res = e.send(e.people["stranger"].cred, http.MethodPost, "/teams/"+e.other+"/tokens",
		api.CreateApiTokenRequest{Name: "theirs", Scopes: api.Scopes{api.ScopeDeploy, api.ScopeManage}})
	res.want(t, http.StatusCreated, "")
	var theirs api.CreatedApiToken
	res.decode(t, &theirs)
	e.creds["other-team-both"] = credential{bearer: theirs.Token}

	res = e.send(admin.cred, http.MethodDelete, "/teams/"+team.Id+"/members/"+e.people["removed"].id, nil)
	res.want(t, http.StatusNoContent, "")
	if _, err := e.svc.Suspend(t.Context(), e.people["suspended"].email); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *teamEnv) person(name string) *person {
	e.t.Helper()
	email := name + "@example.test"
	_, link, err := e.svc.CreateOwner(e.t.Context(), email)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		e.t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" {
		code = u.Fragment
		code = strings.TrimPrefix(code, "code=")
	}
	o, sess, err := e.svc.RedeemSignInLink(e.t.Context(), owners.Client{IP: "192.0.2.1"}, code)
	if err != nil {
		e.t.Fatal(err)
	}
	p := &person{id: o.ID, email: email, cred: credential{cookie: sess.Token}}
	e.people[name] = p
	e.creds[name] = p.cred
	return p
}

// join invites someone and accepts, through the API.
func (e *teamEnv) join(admin *person, teamID string, p *person, role api.TeamRole) {
	e.t.Helper()
	res := e.send(admin.cred, http.MethodPost, "/teams/"+teamID+"/invitations",
		api.CreateTeamInvitationRequest{Email: api.Email(p.email), Role: role})
	res.want(e.t, http.StatusCreated, "")
	var inv api.TeamInvitation
	res.decode(e.t, &inv)
	e.send(p.cred, http.MethodPost, "/team-invitations/"+inv.Id+"/accept", nil).want(e.t, http.StatusOK, "")
}

func (e *teamEnv) personalToken(name string, scopes owners.Scopes) credential {
	e.t.Helper()
	_, raw, err := e.tokens.Create(e.t.Context(), e.people[name].id, name+" token", scopes, time.Time{})
	if err != nil {
		e.t.Fatal(err)
	}
	return credential{bearer: raw}
}

func (e *teamEnv) teamToken(teamID string, scopes api.Scopes) credential {
	e.t.Helper()
	res := e.send(e.people["admin"].cred, http.MethodPost, "/teams/"+teamID+"/tokens", api.CreateApiTokenRequest{Name: "ci", Scopes: scopes})
	res.want(e.t, http.StatusCreated, "")
	var c api.CreatedApiToken
	res.decode(e.t, &c)
	return credential{bearer: c.Token}
}

// seed gives a site, as c and under team (or personally), one of everything.
func (e *teamEnv) seed(c credential, name, team string) fixtures {
	e.t.Helper()
	fx := fixtures{site: name, team: team}
	body := map[string]any{"name": name}
	if team != "" {
		body["team"] = team
	}
	e.send(c, http.MethodPost, "/sites", body).want(e.t, http.StatusCreated, "")
	base := "/sites/" + name
	deploy := func(f file, activate bool) string {
		e.t.Helper()
		res := e.send(c, http.MethodPost, base+"/deploys", manifest(f))
		res.want(e.t, http.StatusCreated, "")
		var d api.CreatedDeploy
		res.decode(e.t, &d)
		if slices.Contains(d.Missing, f.hash()) {
			e.send(c, http.MethodPut, base+"/deploys/"+d.Deploy.Id+"/blobs/"+f.hash(), []byte(f.content)).want(e.t, http.StatusNoContent, "")
		}
		if activate {
			e.send(c, http.MethodPost, base+"/deploys/"+d.Deploy.Id+"/activate", nil).want(e.t, http.StatusOK, "")
		}
		return d.Deploy.Id
	}
	fx.v1 = deploy(oldFile, true)
	fx.active = deploy(liveFile, true)
	fx.next = deploy(readyFile, false)
	res := e.send(c, http.MethodPost, base+"/deploys", manifest(waitingFile))
	res.want(e.t, http.StatusCreated, "")
	var waiting api.CreatedDeploy
	res.decode(e.t, &waiting)
	fx.pending = waiting.Deploy.Id

	preview := manifest(liveFile)
	pr := "pr-1"
	preview.Preview = &pr
	res = e.send(c, http.MethodPost, base+"/deploys", preview)
	res.want(e.t, http.StatusCreated, "")
	var p api.CreatedDeploy
	res.decode(e.t, &p)
	e.send(c, http.MethodPost, base+"/deploys/"+p.Deploy.Id+"/activate", nil).want(e.t, http.StatusOK, "")

	res = e.send(c, http.MethodPut, base+"/policies", map[string]any{"path": "/docs/", "visibility": "protected", "methods": []string{"links"}})
	res.want(e.t, http.StatusOK, "")
	var pol api.Policy
	res.decode(e.t, &pol)
	fx.policy = pol.Id

	res = e.send(c, http.MethodPost, base+"/links", map[string]any{"path": "/"})
	res.want(e.t, http.StatusCreated, "")
	var l api.CreatedShareLink
	res.decode(e.t, &l)
	fx.link = l.ShareLink.PublicId

	fx.host = name + ".example.test"
	e.send(c, http.MethodPost, base+"/domains", map[string]any{"hostname": fx.host}).want(e.t, http.StatusCreated, "")

	q := ""
	if team != "" {
		q = "?team=" + team
	}
	res = e.send(c, http.MethodPost, "/groups"+q, api.CreateGroupRequest{Name: "Clients"})
	res.want(e.t, http.StatusCreated, "")
	var g api.Group
	res.decode(e.t, &g)
	fx.group = g.Id
	res = e.send(c, http.MethodPost, "/tags"+q, map[string]any{"name": "live"})
	res.want(e.t, http.StatusCreated, "")
	var tag api.Tag
	res.decode(e.t, &tag)
	fx.tag = tag.Id
	return fx
}

// matrixOp is one owner-scoped operation.
type matrixOp struct {
	id     string
	method string
	path   func(fixtures) string
	body   func(fixtures) any
	need   store.Role
	// scope is the token scope it takes: "any", "deploy", "manage", or
	// "session" when tokens are refused.
	scope string
	// teamOnly operations name a team, so only the team row applies.
	teamOnly bool
	// before readies the instance for a call that succeeds.
	before func(*teamEnv)
}

func sitePath(suffix string) func(fixtures) string {
	return func(fx fixtures) string { return "/sites/" + fx.site + suffix }
}

func fxPath(f func(fixtures) string) func(fixtures) string { return f }

func noTeamBody(fixtures) any { return nil }

func constBody(v any) func(fixtures) any { return func(fixtures) any { return v } }

func matrixOps() []matrixOp {
	deploy := func(f func(fixtures) string, suffix string) func(fixtures) string {
		return func(fx fixtures) string { return "/sites/" + fx.site + "/deploys/" + f(fx) + suffix }
	}
	link := func(suffix string) func(fixtures) string {
		return func(fx fixtures) string { return "/sites/" + fx.site + "/links/" + fx.link + suffix }
	}
	domain := func(suffix string) func(fixtures) string {
		return func(fx fixtures) string { return "/sites/" + fx.site + "/domains/" + fx.host + suffix }
	}
	active := func(fx fixtures) string { return fx.active }
	team := func(path string) func(fixtures) string {
		return func(fx fixtures) string { return path + "?team=" + fx.team }
	}
	members := func(suffix string) func(fixtures) string {
		return func(fx fixtures) string { return "/teams/" + fx.team + suffix }
	}
	ops := []matrixOp{
		{id: "GetSite", method: http.MethodGet, path: sitePath(""), need: store.RoleViewer, scope: "any"},
		{id: "ListDeploys", method: http.MethodGet, path: sitePath("/deploys"), need: store.RoleViewer, scope: "any"},
		{id: "ListDeployFiles", method: http.MethodGet, path: deploy(active, "/files"), need: store.RoleViewer, scope: "any"},
		{id: "DownloadDeployBlob", method: http.MethodGet, path: deploy(active, "/blobs/"+liveFile.hash()), need: store.RoleViewer, scope: "any"},
		{id: "ListPreviews", method: http.MethodGet, path: sitePath("/previews"), need: store.RoleViewer, scope: "any"},
		{id: "ListDomains", method: http.MethodGet, path: sitePath("/domains"), need: store.RoleViewer, scope: "any"},
		{id: "GetDomain", method: http.MethodGet, path: domain(""), need: store.RoleViewer, scope: "any"},
		{id: "ListPolicies", method: http.MethodGet, path: sitePath("/policies"), need: store.RoleViewer, scope: "any"},
		{id: "ListShareLinks", method: http.MethodGet, path: sitePath("/links"), need: store.RoleViewer, scope: "any"},
		{id: "ListShareLinkAccessLog", method: http.MethodGet, path: link("/access-log"), need: store.RoleViewer, scope: "any"},
		{id: "ListSiteAccessLog", method: http.MethodGet, path: sitePath("/access-log"), need: store.RoleViewer, scope: "any"},
		{id: "GetAnalytics", method: http.MethodGet, path: func(fx fixtures) string { return "/analytics?days=7&site=" + fx.site }, need: store.RoleViewer, scope: "any"},

		{id: "CreateDeploy", method: http.MethodPost, path: sitePath("/deploys"), body: constBody(manifest(file{"more.html", "more"})), need: store.RoleEditor, scope: "deploy"},
		{id: "UploadDeployBlob", method: http.MethodPut, path: func(fx fixtures) string {
			return "/sites/" + fx.site + "/deploys/" + fx.pending + "/blobs/" + waitingFile.hash()
		}, body: constBody([]byte(waitingFile.content)), need: store.RoleEditor, scope: "deploy"},
		{id: "ActivateDeploy", method: http.MethodPost, path: deploy(func(fx fixtures) string { return fx.next }, "/activate"), need: store.RoleEditor, scope: "deploy"},
		{id: "RollbackSite", method: http.MethodPost, path: sitePath("/rollback"), body: constBody(map[string]any{}), need: store.RoleEditor, scope: "deploy"},
		{id: "DeletePreview", method: http.MethodDelete, path: sitePath("/previews/pr-1"), need: store.RoleEditor, scope: "deploy"},
		{id: "PromotePreview", method: http.MethodPost, path: sitePath("/previews/pr-1/promote"), need: store.RoleEditor, scope: "deploy"},
		{id: "SetSiteOrganisation", method: http.MethodPut, path: sitePath("/organisation"), body: func(fx fixtures) any {
			return map[string]any{"groupId": fx.group, "tagIds": []string{fx.tag}}
		}, need: store.RoleEditor, scope: "any"},

		{id: "DeleteSite", method: http.MethodDelete, path: sitePath(""), need: store.RoleEditor, scope: "manage"},
		{id: "AddDomain", method: http.MethodPost, path: sitePath("/domains"), body: constBody(map[string]any{"hostname": "added.example.test"}), need: store.RoleEditor, scope: "manage"},
		{id: "RemoveDomain", method: http.MethodDelete, path: domain(""), need: store.RoleEditor, scope: "manage"},
		{id: "VerifyDomain", method: http.MethodPost, path: domain("/verify"), need: store.RoleEditor, scope: "manage", before: func(e *teamEnv) { e.hs.advance(time.Hour) }},
		{id: "SetPrimaryHost", method: http.MethodPut, path: sitePath("/primary-host"), body: func(fx fixtures) any {
			return map[string]any{"host": fx.site + "." + baseDomain}
		}, need: store.RoleEditor, scope: "manage"},
		{id: "ClearPrimaryHost", method: http.MethodDelete, path: sitePath("/primary-host"), need: store.RoleEditor, scope: "manage"},
		{id: "SetPolicy", method: http.MethodPut, path: sitePath("/policies"), body: constBody(map[string]any{"path": "/more/", "visibility": "protected", "methods": []string{"links"}}), need: store.RoleEditor, scope: "manage"},
		{id: "DeletePolicy", method: http.MethodDelete, path: func(fx fixtures) string { return "/sites/" + fx.site + "/policies/" + fx.policy }, need: store.RoleEditor, scope: "manage"},
		{id: "CreateShareLink", method: http.MethodPost, path: sitePath("/links"), body: constBody(map[string]any{"path": "/"}), need: store.RoleEditor, scope: "manage"},
		{id: "RevokeShareLink", method: http.MethodDelete, path: link(""), need: store.RoleEditor, scope: "manage"},
		{id: "RotateShareLink", method: http.MethodPost, path: link("/rotate"), need: store.RoleEditor, scope: "manage"},
		// A pin is the caller's own, so reading the site is enough.
		{id: "PinSite", method: http.MethodPut, path: sitePath("/pin"), need: store.RoleViewer, scope: "manage"},
		{id: "UnpinSite", method: http.MethodDelete, path: sitePath("/pin"), need: store.RoleViewer, scope: "manage"},
		// Admin of the source, moving to the caller's own account.
		{id: "TransferSite", method: http.MethodPost, path: sitePath("/transfer"), body: constBody(map[string]any{"to": map[string]any{"kind": "owner"}}), need: store.RoleAdmin, scope: "manage"},

		// Groups and tags have IDs of their own, and an account behind them.
		{id: "UpdateGroup", method: http.MethodPatch, path: func(fx fixtures) string { return "/groups/" + fx.group }, body: constBody(map[string]any{"name": "Renamed"}), need: store.RoleEditor, scope: "manage"},
		{id: "DeleteGroup", method: http.MethodDelete, path: func(fx fixtures) string { return "/groups/" + fx.group }, need: store.RoleEditor, scope: "manage"},
		{id: "UpdateTag", method: http.MethodPatch, path: func(fx fixtures) string { return "/tags/" + fx.tag }, body: constBody(map[string]any{"name": "renamed"}), need: store.RoleEditor, scope: "manage"},
		{id: "DeleteTag", method: http.MethodDelete, path: func(fx fixtures) string { return "/tags/" + fx.tag }, need: store.RoleEditor, scope: "manage"},

		// Everything that names a team.
		{id: "ListSites", method: http.MethodGet, path: team("/sites"), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "ListGroups", method: http.MethodGet, path: team("/groups"), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "ListTags", method: http.MethodGet, path: team("/tags"), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "ListSiteSummaries", method: http.MethodGet, path: team("/site-summaries"), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "GetSiteCounts", method: http.MethodGet, path: team("/site-counts"), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "ListExpiringLinks", method: http.MethodGet, path: team("/expiring-links"), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "CreateSite", method: http.MethodPost, path: fxPath(func(fixtures) string { return "/sites" }), body: func(fx fixtures) any {
			return map[string]any{"name": "new-site", "team": fx.team}
		}, need: store.RoleEditor, scope: "deploy", teamOnly: true},
		{id: "CreateGroup", method: http.MethodPost, path: team("/groups"), body: constBody(map[string]any{"name": "More"}), need: store.RoleEditor, scope: "manage", teamOnly: true},
		{id: "CreateTag", method: http.MethodPost, path: team("/tags"), body: constBody(map[string]any{"name": "more"}), need: store.RoleEditor, scope: "manage", teamOnly: true},
		{id: "OrganiseSites", method: http.MethodPost, path: team("/organise"), body: func(fx fixtures) any {
			return map[string]any{"sites": []string{fx.site}, "addTagIds": []string{fx.tag}}
		}, need: store.RoleEditor, scope: "manage", teamOnly: true},

		{id: "GetTeam", method: http.MethodGet, path: members(""), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "ListTeamMembers", method: http.MethodGet, path: members("/members"), need: store.RoleViewer, scope: "any", teamOnly: true},
		{id: "UpdateTeam", method: http.MethodPatch, path: members(""), body: constBody(api.UpdateTeamRequest{Name: "Renamed"}), need: store.RoleAdmin, scope: "session", teamOnly: true},
		{id: "ListTeamInvitations", method: http.MethodGet, path: members("/invitations"), need: store.RoleAdmin, scope: "session", teamOnly: true},
		{id: "InviteTeamMember", method: http.MethodPost, path: members("/invitations"), body: constBody(map[string]any{"email": "new@example.test", "role": "viewer"}), need: store.RoleAdmin, scope: "session", teamOnly: true},
		{id: "ListTeamTokens", method: http.MethodGet, path: members("/tokens"), need: store.RoleAdmin, scope: "session", teamOnly: true},
		{id: "CreateTeamToken", method: http.MethodPost, path: members("/tokens"), body: constBody(map[string]any{"name": "new", "scopes": []string{"deploy"}}), need: store.RoleAdmin, scope: "session", teamOnly: true},
	}
	return ops
}

// matrixCaller is one row: who is calling, and what they should get.
type matrixCaller struct {
	cred string
	// scopes is nil for a session.
	scopes []string
	// role is the caller's role where they can see the target; empty where
	// they cannot.
	role store.Role
	// gone is a credential that no longer authenticates anyone.
	gone bool
}

func tokenAccepts(scopes []string, need string) bool {
	switch need {
	case "session":
		return false
	case "any":
		return true
	}
	return slices.Contains(scopes, need)
}

type verdict struct {
	status int
	code   string
}

func (c matrixCaller) expect(op matrixOp) verdict {
	switch {
	case c.gone:
		return verdict{http.StatusUnauthorized, "unauthenticated"}
	case c.scopes != nil && !tokenAccepts(c.scopes, op.scope):
		return verdict{http.StatusForbidden, "forbidden"}
	case c.role == "":
		return verdict{http.StatusNotFound, "not_found"}
	case !roleAtLeast(c.role, op.need):
		return verdict{http.StatusForbidden, "forbidden_role"}
	}
	return verdict{}
}

func roleAtLeast(have, need store.Role) bool {
	rank := map[store.Role]int{store.RoleViewer: 1, store.RoleEditor: 2, store.RoleAdmin: 3}
	return rank[have] >= rank[need]
}

var both = []string{"deploy", "manage"}

// teamCallers is the rows against a team's site. An owner's own site has a
// role of admin for its owner, which is what "everything" means there.
var teamCallers = []matrixCaller{
	{cred: "admin", role: store.RoleAdmin},
	{cred: "editor", role: store.RoleEditor},
	{cred: "viewer", role: store.RoleViewer},
	{cred: "stranger"},
	{cred: "owner"},
	{cred: "removed"},
	{cred: "suspended", gone: true},
	{cred: "admin-manage", scopes: []string{"manage"}, role: store.RoleAdmin},
	{cred: "editor-deploy", scopes: []string{"deploy"}, role: store.RoleEditor},
	{cred: "editor-manage", scopes: []string{"manage"}, role: store.RoleEditor},
	{cred: "viewer-manage", scopes: []string{"manage"}, role: store.RoleViewer},
	{cred: "viewer-both", scopes: both, role: store.RoleViewer},
	{cred: "removed-both", scopes: both},
	{cred: "suspended-both", gone: true},
	{cred: "team-deploy", scopes: []string{"deploy"}, role: store.RoleEditor},
	{cred: "team-manage", scopes: []string{"manage"}, role: store.RoleEditor},
	{cred: "team-both", scopes: both, role: store.RoleEditor},
	{cred: "other-team-both", scopes: both},
}

var ownerCallers = []matrixCaller{
	{cred: "owner", role: store.RoleAdmin},
	{cred: "owner-deploy", scopes: []string{"deploy"}, role: store.RoleAdmin},
	{cred: "owner-manage", scopes: []string{"manage"}, role: store.RoleAdmin},
	{cred: "admin"},
	{cred: "stranger"},
	{cred: "editor-manage", scopes: []string{"manage"}},
	{cred: "team-both", scopes: both},
}

// missing is what the matrix calls a thing that does not exist.
func missing(fx fixtures) fixtures {
	id := "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9eff"
	fx.site, fx.team, fx.group, fx.tag, fx.policy = "no-such-site", id, id, id, id
	return fx
}

func TestIsolationMatrix(t *testing.T) {
	shared := newTeamEnv(t)
	run := func(t *testing.T, op matrixOp, c matrixCaller, target func(*teamEnv) fixtures) {
		want := c.expect(op)
		if want.status != 0 {
			// A refusal changes nothing, so it shares the instance.
			res := shared.send(shared.creds[c.cred], op.method, op.path(target(shared)), bodyOf(op, target(shared)))
			if res.status != want.status || res.err(t).Code != want.code {
				t.Fatalf("%s %s: %d %s; want %d %s", op.method, op.path(target(shared)), res.status, res.body, want.status, want.code)
			}
			if want.status == http.StatusNotFound {
				absent := shared.send(shared.people["admin"].cred, op.method, op.path(missing(target(shared))), bodyOf(op, missing(target(shared))))
				if absent.status != http.StatusNotFound {
					t.Fatalf("missing target: %d %s", absent.status, absent.body)
				}
				if !bytes.Equal(res.body, absent.body) {
					t.Fatalf("foreign %s differs from missing %s", res.body, absent.body)
				}
			}
			return
		}
		e := shared
		if op.method != http.MethodGet {
			t.Parallel()
			e = newTeamEnv(t)
		}
		if op.before != nil {
			op.before(e)
		}
		fx := target(e)
		res := e.send(e.creds[c.cred], op.method, op.path(fx), bodyOf(op, fx))
		if res.status >= 300 {
			t.Fatalf("%s %s: %d %s; want success", op.method, op.path(fx), res.status, res.body)
		}
	}
	for _, op := range matrixOps() {
		t.Run(op.id, func(t *testing.T) {
			for _, c := range teamCallers {
				t.Run("team/"+c.cred, func(t *testing.T) { run(t, op, c, func(e *teamEnv) fixtures { return e.teamFx }) })
			}
			if op.teamOnly {
				return
			}
			for _, c := range ownerCallers {
				t.Run("own/"+c.cred, func(t *testing.T) { run(t, op, c, func(e *teamEnv) fixtures { return e.ownerFx }) })
			}
		})
	}
}

func bodyOf(op matrixOp, fx fixtures) any {
	if op.body == nil {
		return noTeamBody(fx)
	}
	return op.body(fx)
}

// Operations that act on one member, invitation or token take IDs of their
// own, so they change the instance on every call and get their own rows here.
func TestTeamMemberOperationsMatrix(t *testing.T) {
	for _, c := range teamCallers {
		t.Run(c.cred, func(t *testing.T) {
			t.Parallel()
			e := newTeamEnv(t)
			team := e.teamFx.team
			setRole := matrixOp{need: store.RoleAdmin, scope: "session"}
			want := c.expect(setRole)
			check := func(res response) {
				t.Helper()
				if want.status == 0 {
					if res.status >= 300 {
						t.Fatalf("%d %s; want success", res.status, res.body)
					}
					return
				}
				if res.status != want.status || res.err(t).Code != want.code {
					t.Fatalf("%d %s; want %d %s", res.status, res.body, want.status, want.code)
				}
			}
			cred := e.creds[c.cred]
			viewer := e.people["viewer"].id
			check(e.send(cred, http.MethodPatch, "/teams/"+team+"/members/"+viewer, map[string]any{"role": "editor"}))

			inv := e.send(e.people["admin"].cred, http.MethodPost, "/teams/"+team+"/invitations", map[string]any{"email": "extra@example.test", "role": "viewer"})
			inv.want(t, http.StatusCreated, "")
			var invitation api.TeamInvitation
			inv.decode(t, &invitation)
			check(e.send(cred, http.MethodDelete, "/teams/"+team+"/invitations/"+invitation.Id, nil))

			tok := e.send(e.people["admin"].cred, http.MethodPost, "/teams/"+team+"/tokens", map[string]any{"name": "x", "scopes": []string{"deploy"}})
			tok.want(t, http.StatusCreated, "")
			var created api.CreatedApiToken
			tok.decode(t, &created)
			check(e.send(cred, http.MethodDelete, "/teams/"+team+"/tokens/"+created.ApiToken.Id, nil))

			// Someone else: anyone may remove themselves.
			target := "editor"
			if strings.HasPrefix(c.cred, "editor") {
				target = "viewer"
			}
			check(e.send(cred, http.MethodDelete, "/teams/"+team+"/members/"+e.people[target].id, nil))
		})
	}
}

// A role change and a removal take effect on the very next request.
func TestAccessEndsAtOnce(t *testing.T) {
	e := newTeamEnv(t)
	site, team := "/sites/"+e.teamFx.site, e.teamFx.team
	editor := e.people["editor"]
	admin := e.people["admin"].cred
	e.send(editor.cred, http.MethodDelete, site+"/pin", nil).want(t, http.StatusOK, "")
	e.send(editor.cred, http.MethodPut, site+"/primary-host", map[string]any{"host": e.teamFx.site + "." + baseDomain}).want(t, http.StatusOK, "")

	e.send(admin, http.MethodPatch, "/teams/"+team+"/members/"+editor.id, map[string]any{"role": "viewer"}).want(t, http.StatusOK, "")
	e.send(editor.cred, http.MethodDelete, site+"/primary-host", nil).want(t, http.StatusForbidden, "forbidden_role")
	e.send(editor.cred, http.MethodGet, site, nil).want(t, http.StatusOK, "")

	e.send(admin, http.MethodDelete, "/teams/"+team+"/members/"+editor.id, nil).want(t, http.StatusNoContent, "")
	e.send(editor.cred, http.MethodGet, site, nil).want(t, http.StatusNotFound, "not_found")
	e.send(e.creds["editor-manage"], http.MethodGet, site, nil).want(t, http.StatusNotFound, "not_found")
	// What they made stays with the team.
	e.send(admin, http.MethodGet, site, nil).want(t, http.StatusOK, "")
}

func TestSuspensionEndsTeamAccessNotTheSite(t *testing.T) {
	e := newTeamEnv(t)
	res := e.send(e.people["admin"].cred, http.MethodGet, "/sites/"+e.teamFx.site, nil)
	res.want(t, http.StatusOK, "")
	var s api.Site
	res.decode(t, &s)
	if s.Unavailable {
		t.Fatal("a suspended member took the team's site down")
	}
	// A team token outlives both a removal and a suspension of its maker.
	if _, err := e.svc.Suspend(t.Context(), e.people["admin"].email); err != nil {
		t.Fatal(err)
	}
	e.send(e.people["admin"].cred, http.MethodGet, "/sites/"+e.teamFx.site, nil).want(t, http.StatusUnauthorized, "unauthenticated")
	e.send(e.creds["team-both"], http.MethodGet, "/sites/"+e.teamFx.site, nil).want(t, http.StatusOK, "")
}

func TestSiteOwnerAndCreatedByInTheAPI(t *testing.T) {
	e := newTeamEnv(t)
	res := e.send(e.people["editor"].cred, http.MethodGet, "/sites/"+e.teamFx.site, nil)
	res.want(t, http.StatusOK, "")
	var s api.Site
	res.decode(t, &s)
	if s.Owner.Kind != api.SiteOwnerKindTeam || s.Owner.Id != e.teamFx.team || s.Owner.Name != "Web team" {
		t.Fatalf("team site owner = %+v", s.Owner)
	}
	res = e.send(e.people["owner"].cred, http.MethodGet, "/sites/"+e.ownerFx.site, nil)
	res.want(t, http.StatusOK, "")
	res.decode(t, &s)
	if s.Owner.Kind != api.SiteOwnerKindOwner || s.Owner.Id != e.people["owner"].id || s.Owner.Name != e.people["owner"].email {
		t.Fatalf("own site owner = %+v", s.Owner)
	}

	res = e.send(e.people["editor"].cred, http.MethodPost, "/sites/"+e.teamFx.site+"/deploys", manifest(file{"x", "x"}))
	res.want(t, http.StatusCreated, "")
	var d api.CreatedDeploy
	res.decode(t, &d)
	if by := d.Deploy.CreatedBy; by == nil || by.Kind != api.CreatedByKindOwner || by.Id != e.people["editor"].id || by.Name != e.people["editor"].email {
		t.Fatalf("createdBy = %+v", by)
	}
	res = e.send(e.creds["team-both"], http.MethodPost, "/sites/"+e.teamFx.site+"/links", map[string]any{"path": "/"})
	res.want(t, http.StatusCreated, "")
	var l api.CreatedShareLink
	res.decode(t, &l)
	if by := l.ShareLink.CreatedBy; by == nil || by.Kind != api.CreatedByKindToken || by.Name != "ci" {
		t.Fatalf("createdBy = %+v", by)
	}
	// A member who leaves stays named, to those who can still see it.
	e.send(e.people["admin"].cred, http.MethodDelete, "/teams/"+e.teamFx.team+"/members/"+e.people["editor"].id, nil).want(t, http.StatusNoContent, "")
	res = e.send(e.people["admin"].cred, http.MethodGet, "/sites/"+e.teamFx.site+"/deploys", nil)
	res.want(t, http.StatusOK, "")
	var page api.DeployPage
	res.decode(t, &page)
	if !slices.ContainsFunc(page.Items, func(d api.Deploy) bool { return d.CreatedBy != nil && d.CreatedBy.Name == e.people["editor"].email }) {
		t.Fatalf("no deploy names the departed editor: %s", res.body)
	}
}

func TestListSitesShowsEverythingVisible(t *testing.T) {
	e := newTeamEnv(t)
	res := e.send(e.people["owner"].cred, http.MethodPost, "/teams/"+e.other+"/invitations", map[string]any{"email": "x@example.test", "role": "viewer"})
	res.want(t, http.StatusNotFound, "not_found")
	names := func(c credential, q string) []string {
		t.Helper()
		res := e.send(c, http.MethodGet, "/sites"+q, nil)
		res.want(t, http.StatusOK, "")
		var page api.SitePage
		res.decode(t, &page)
		out := []string{}
		for _, s := range page.Items {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names(e.people["viewer"].cred, ""); !slices.Equal(got, []string{e.teamFx.site}) {
		t.Fatalf("viewer lists %v", got)
	}
	if got := names(e.people["owner"].cred, ""); !slices.Equal(got, []string{e.ownerFx.site}) {
		t.Fatalf("an owner in no team lists %v", got)
	}
	if got := names(e.creds["team-deploy"], ""); !slices.Equal(got, []string{e.teamFx.site}) {
		t.Fatalf("team token lists %v", got)
	}
	// A hostname outside the named team is simply not in the list.
	if got := names(e.people["viewer"].cred, "?hostname="+e.teamFx.site+"."+baseDomain+"&team="+e.other); len(got) != 0 {
		t.Fatalf("hostname outside the team found %v", got)
	}
}

func TestTeamTokenIdentityAndWebhooks(t *testing.T) {
	e := newTeamEnv(t)
	res := e.send(e.creds["team-manage"], http.MethodGet, "/me", nil)
	res.want(t, http.StatusOK, "")
	var me api.Me
	res.decode(t, &me)
	if me.Team == nil || me.Team.Name != "Web team" || me.Credential != api.MeCredentialApiToken {
		t.Fatalf("me = %+v", me)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/webhooks", nil},
		{http.MethodPost, "/webhooks", map[string]any{"url": "https://hooks.example.test/x", "events": []string{"deploy.activated"}}},
	} {
		e.send(e.creds["team-manage"], r.method, r.path, r.body).want(t, http.StatusForbidden, "forbidden")
	}
	// An owner's webhook cannot list a team's site.
	e.send(e.people["admin"].cred, http.MethodPost, "/webhooks", map[string]any{
		"url": "https://hooks.example.test/x", "events": []string{"deploy.activated"}, "sites": []string{e.teamFx.site},
	}).want(t, http.StatusNotFound, "not_found")
}

func TestInvitationAcceptance(t *testing.T) {
	e := newTeamEnv(t)
	admin, stranger, owner := e.people["admin"], e.people["stranger"], e.people["owner"]
	team := e.teamFx.team
	res := e.send(admin.cred, http.MethodPost, "/teams/"+team+"/invitations", map[string]any{"email": owner.email, "role": "viewer"})
	res.want(t, http.StatusCreated, "")
	var inv api.TeamInvitation
	res.decode(t, &inv)

	// The ID alone is nothing: the signed-in address has to match.
	e.send(stranger.cred, http.MethodPost, "/team-invitations/"+inv.Id+"/accept", nil).want(t, http.StatusNotFound, "not_found")
	e.send(stranger.cred, http.MethodPost, "/team-invitations/"+inv.Id+"/decline", nil).want(t, http.StatusNotFound, "not_found")
	e.send(e.creds["owner-manage"], http.MethodPost, "/team-invitations/"+inv.Id+"/accept", nil).want(t, http.StatusForbidden, "forbidden")
	res = e.send(owner.cred, http.MethodGet, "/team-invitations", nil)
	res.want(t, http.StatusOK, "")
	var mine api.TeamInvitationList
	res.decode(t, &mine)
	if len(mine.Items) != 1 || mine.Items[0].TeamName != "Web team" {
		t.Fatalf("invitations = %+v", mine)
	}
	for range 2 {
		res = e.send(owner.cred, http.MethodPost, "/team-invitations/"+inv.Id+"/accept", nil)
		res.want(t, http.StatusOK, "")
		var joined api.Team
		res.decode(t, &joined)
		if joined.Id != team || joined.Role != api.TeamRoleViewer {
			t.Fatalf("accepted = %+v", joined)
		}
	}
}

func TestInviteRefusedWithoutAnAccountWhenRestricted(t *testing.T) {
	e := newTeamEnv(t)
	log := slog.New(slog.DiscardHandler)
	svc, err := owners.New(owners.Config{
		Owners: e.hs.db.Owners(), Sessions: e.hs.db.Sessions(), Mailer: discardMail{}, Logger: log,
		Secret: bytes.Repeat([]byte("k"), 32), DashboardURL: &url.URL{Scheme: "http", Host: "console." + baseDomain},
		Invites: owners.InvitesOperator, Teams: owners.TeamsConfig{Teams: e.hs.db.Teams()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	e.hs.srv.Owners = svc
	team := e.teamFx.team
	admin := e.people["admin"].cred
	e.send(admin, http.MethodPost, "/teams/"+team+"/invitations", map[string]any{"email": "nobody@example.test", "role": "viewer"}).
		want(t, http.StatusForbidden, "invites_restricted")
	e.send(admin, http.MethodPost, "/teams/"+team+"/invitations", map[string]any{"email": e.people["owner"].email, "role": "viewer"}).
		want(t, http.StatusCreated, "")
}

func TestTeamAddressIsRefusedWhereverAnAddressComesIn(t *testing.T) {
	e := newTeamEnv(t)
	address := store.TeamAddress(e.teamFx.team)
	e.send(e.people["admin"].cred, http.MethodPost, "/teams/"+e.teamFx.team+"/invitations", map[string]any{"email": address, "role": "viewer"}).
		want(t, http.StatusBadRequest, "invalid_request")
	if err := e.svc.Invite(t.Context(), e.people["admin"].id, address); err == nil {
		t.Fatal("owner invitation to a team's address accepted")
	}
	if err := e.svc.SendEmailCode(t.Context(), owners.Client{IP: "192.0.2.9"}, address); err == nil {
		t.Fatal("email code to a team's address accepted")
	}
	if _, _, err := e.svc.CreateOwner(t.Context(), address); err == nil {
		t.Fatal("owner created with a team's address")
	}
	if _, err := e.svc.Suspend(t.Context(), address); err == nil {
		t.Fatal("a team was suspended by its address")
	}
}

func TestTransferThroughTheAPI(t *testing.T) {
	e := newTeamEnv(t)
	admin := e.people["admin"]
	team := e.teamFx.team
	owner := e.people["owner"]
	e.send(owner.cred, http.MethodPost, "/sites/"+e.ownerFx.site+"/transfer",
		map[string]any{"to": map[string]any{"kind": "team", "id": team}}).want(t, http.StatusNotFound, "not_found")
	e.send(owner.cred, http.MethodPost, "/sites/"+e.ownerFx.site+"/transfer",
		map[string]any{"to": map[string]any{"kind": "owner", "id": team}}).want(t, http.StatusBadRequest, "invalid_request")
	e.send(owner.cred, http.MethodPost, "/sites/"+e.ownerFx.site+"/transfer",
		map[string]any{"to": map[string]any{"kind": "team"}}).want(t, http.StatusBadRequest, "invalid_request")

	e.join(admin, team, owner, api.TeamRoleAdmin)
	res := e.send(owner.cred, http.MethodPost, "/sites/"+e.ownerFx.site+"/transfer", map[string]any{"to": map[string]any{"kind": "team", "id": team}})
	res.want(t, http.StatusOK, "")
	var s api.Site
	res.decode(t, &s)
	if s.Owner.Kind != api.SiteOwnerKindTeam || s.GroupId != nil || len(s.TagIds) != 0 {
		t.Fatalf("after moving: %+v", s)
	}
	// Reading it back through the team needs no further upload.
	e.send(e.people["viewer"].cred, http.MethodGet, "/sites/"+e.ownerFx.site+"/deploys/"+e.ownerFx.active+"/blobs/"+liveFile.hash(), nil).
		want(t, http.StatusOK, "")

	if _, err := e.hs.db.Sites().GetByName(t.Context(), e.ownerFx.site); err != nil {
		t.Fatal(err)
	}
	site, err := e.hs.db.Sites().GetByName(t.Context(), e.ownerFx.site)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.hs.db.Sites().TakeDown(t.Context(), site.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.send(owner.cred, http.MethodPost, "/sites/"+e.ownerFx.site+"/transfer", map[string]any{"to": map[string]any{"kind": "owner"}}).
		want(t, http.StatusConflict, "site_unavailable")
}

func TestInviteRefusesCurrentMembers(t *testing.T) {
	e := newTeamEnv(t)
	e.send(e.people["admin"].cred, http.MethodPost, "/teams/"+e.teamFx.team+"/invitations", map[string]any{"email": e.people["editor"].email, "role": "admin"}).
		want(t, http.StatusConflict, "already_member")
}

func TestLastAdminAndTeamWithSites(t *testing.T) {
	e := newTeamEnv(t)
	admin := e.people["admin"]
	team := e.teamFx.team
	e.send(admin.cred, http.MethodDelete, "/teams/"+team+"/members/"+admin.id, nil).want(t, http.StatusConflict, "team_needs_admin")
	e.send(admin.cred, http.MethodPatch, "/teams/"+team+"/members/"+admin.id, map[string]any{"role": "viewer"}).want(t, http.StatusConflict, "team_needs_admin")
	e.send(admin.cred, http.MethodDelete, "/teams/"+team, nil).want(t, http.StatusConflict, "team_has_sites")
	e.send(e.people["viewer"].cred, http.MethodDelete, "/teams/"+team, nil).want(t, http.StatusForbidden, "forbidden_role")
	e.send(e.people["owner"].cred, http.MethodDelete, "/teams/"+team, nil).want(t, http.StatusNotFound, "not_found")
	e.send(admin.cred, http.MethodDelete, "/sites/"+e.teamFx.site, nil).want(t, http.StatusNoContent, "")
	e.send(admin.cred, http.MethodDelete, "/teams/"+team, nil).want(t, http.StatusNoContent, "")
	// Its tokens went with it.
	e.send(e.creds["team-both"], http.MethodGet, "/me", nil).want(t, http.StatusUnauthorized, "")
}

// TestMatrixCoversEveryOperation holds the matrix to the contract: an
// operation added to openapi.yaml must land in a row here, or be named as not
// owner-scoped.
func TestMatrixCoversEveryOperation(t *testing.T) {
	spec, err := os.ReadFile("../../../../packages/api-spec/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{
		// Acted on above: one row each for a member, invitation and token.
		"SetTeamMemberRole": true, "RemoveTeamMember": true, "DeleteTeamInvitation": true, "RevokeTeamToken": true,
		"CreateTeam": true, "DeleteTeam": true, "ListTeams": true, "ListMyTeamInvitations": true, "AcceptTeamInvitation": true, "DeclineTeamInvitation": true,
	}
	for _, op := range matrixOps() {
		covered[op.id] = true
	}
	// The caller's own, or no one's: nothing here names a site or a team.
	notOwnerScoped := []string{
		"GetHealth", "CompleteSetup", "RedeemSignInLink", "SendEmailCode", "VerifyEmailCode", "StartPasskeySignIn", "VerifyPasskeySignIn",
		"SignOut", "GetMe", "InviteOwner", "ListSessions", "RevokeSession", "GetNotificationSettings", "SetNotificationSettings",
		"ListPasskeys", "StartPasskeyRegistration", "CreatePasskey", "DeletePasskey",
		"StartCliLogin", "PollCliLogin", "ApproveCliLogin", "DenyCliLogin", "ListApiTokens", "CreateApiToken", "RevokeApiToken",
		"ListWebhooks", "CreateWebhook", "GetWebhook", "UpdateWebhook", "DeleteWebhook", "TestWebhook", "ListWebhookDeliveries", "RedeliverWebhookDelivery",
		"ListSignInProviders",
	}
	for _, m := range regexp.MustCompile(`operationId: (\w+)`).FindAllSubmatch(spec, -1) {
		id := string(m[1])
		id = strings.ToUpper(id[:1]) + id[1:]
		if !covered[id] && !slices.Contains(notOwnerScoped, id) {
			t.Errorf("operation %s is in no matrix row", id)
		}
	}
	if len(covered)+len(notOwnerScoped) < 90 {
		t.Fatalf("only %d operations accounted for", len(covered)+len(notOwnerScoped))
	}
}

// A team's quotas are the team's, with their own overrides, through the API.
func TestTeamQuotasThroughTheAPI(t *testing.T) {
	e := newTeamEnv(t)
	ctx := t.Context()
	team, admin := e.teamFx.team, e.people["admin"]
	one := int64(1)
	if err := e.hs.db.Owners().SetQuotas(ctx, team, store.QuotaOverrides{Sites: &one}); err != nil {
		t.Fatal(err)
	}
	res := e.send(e.people["editor"].cred, http.MethodPost, "/sites", map[string]any{"name": "second", "team": team})
	res.want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
	// The member's own allowance is untouched.
	e.send(e.people["editor"].cred, http.MethodPost, "/sites", map[string]any{"name": "mine"}).want(t, http.StatusCreated, "")
	// A move is checked against the target too.
	res = e.send(admin.cred, http.MethodPost, "/sites/mine/transfer", map[string]any{"to": map[string]any{"kind": "team", "id": team}})
	res.want(t, http.StatusNotFound, "not_found")
	e.send(admin.cred, http.MethodPost, "/sites", map[string]any{"name": "admins"}).want(t, http.StatusCreated, "")
	res = e.send(admin.cred, http.MethodPost, "/sites/admins/transfer", map[string]any{"to": map[string]any{"kind": "team", "id": team}})
	res.want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
}

func TestTeamsPerOwnerThroughTheAPI(t *testing.T) {
	e := newTeamEnv(t)
	one := int64(1)
	if err := e.hs.db.Teams().SetTeamQuota(t.Context(), e.people["stranger"].id, &one); err != nil {
		t.Fatal(err)
	}
	e.send(e.people["stranger"].cred, http.MethodPost, "/teams", api.CreateTeamRequest{Name: "Another"}).
		want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
	e.send(e.people["viewer"].cred, http.MethodPost, "/teams", api.CreateTeamRequest{Name: "Fine"}).want(t, http.StatusCreated, "")
	e.send(e.creds["team-manage"], http.MethodPost, "/teams", api.CreateTeamRequest{Name: "Refused"}).want(t, http.StatusForbidden, "forbidden")
}
