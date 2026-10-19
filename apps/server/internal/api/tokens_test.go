// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store/storetest"
)

// newTokenHarness is the owner harness with the token service, sites and
// deploys wired in. The token service shares the harness clock and logger
// output, so the cleanup log assertion covers tokens and device codes too.
func newTokenHarness(t testing.TB, limits owners.TokenLimits) *ownerHarness {
	t.Helper()
	db := storetest.New(t)
	var h *ownerHarness
	h = newOwnerHarnessWith(t, db, owners.Limits{}, func(s *api.Server) {
		notifier := deploys.NewLocalNotifier()
		s.Sites = sites.NewService(sites.Config{
			Sites: db.Sites(), Notifier: notifier, BaseDomain: "sites.example.test", DashboardHost: dashHost, Now: time.Now,
		})
		s.Deploys = deploys.NewService(deploys.Config{
			Sites: db.Sites(), Deploys: db.Deploys(), Blobs: deploys.NewFSBlobStore(t.TempDir()), Notifier: notifier,
			Limits: deploys.DefaultLimits(), Now: time.Now,
		})
		tokens, err := owners.NewTokens(owners.TokensConfig{
			Tokens: db.APITokens(), Logger: s.Logger, Secret: bytes.Repeat([]byte("k"), 32),
			DashboardURL: &url.URL{Scheme: "http", Host: dashHost},
			Now:          func() time.Time { return h.clock.Now() },
			Limits:       limits,
		})
		if err != nil {
			t.Fatal(err)
		}
		s.Tokens = tokens
	})
	return h
}

// bearer sends r with "Authorization: Bearer token" and no cookie.
func (h *ownerHarness) bearer(token string, r req) *httptest.ResponseRecorder {
	h.t.Helper()
	r.headers = map[string]string{"Authorization": "Bearer " + token}
	return h.do(r)
}

func decodeInto(t testing.TB, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
}

// createToken creates an API token with a dashboard session and returns it.
func createToken(t testing.TB, h *ownerHarness, session, name string, scopes ...api.Scope) api.CreatedApiToken {
	t.Helper()
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/tokens", cookie: session,
		body: api.CreateApiTokenRequest{Name: name, Scopes: scopes}})
	wantStatus(t, rec, http.StatusCreated)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("create Cache-Control %q", cc)
	}
	var created api.CreatedApiToken
	decodeInto(t, rec, &created)
	h.secret(created.Token)
	return created
}

func TestAPITokensEndToEnd(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	created := createToken(t, h, session, "CI deploys", api.ScopeDeploy)
	if created.ApiToken.Name != "CI deploys" || created.ApiToken.PublicId == "" ||
		!strings.HasPrefix(created.Token, "wl_"+created.ApiToken.PublicId+"_") {
		t.Fatalf("created %+v", created.ApiToken)
	}
	tok := created.Token

	// The token authenticates as its owner, with its scopes.
	rec := h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/me"})
	wantStatus(t, rec, http.StatusOK)
	var m api.Me
	decodeInto(t, rec, &m)
	if m.Credential != api.MeCredentialApiToken || len(m.Scopes) != 1 || m.Scopes[0] != api.ScopeDeploy || m.Owner.Email != "owner@example.test" {
		t.Fatalf("me = %+v", m)
	}
	// Deploy scope: create and read sites, not delete them.
	wantStatus(t, h.bearer(tok, req{method: http.MethodPost, path: "/api/v0/sites", body: api.CreateSiteRequest{Name: "docs"}}), http.StatusCreated)
	wantStatus(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/sites/docs"}), http.StatusOK)
	wantError(t, h.bearer(tok, req{method: http.MethodDelete, path: "/api/v0/sites/docs"}), http.StatusForbidden, "forbidden")
	// Tokens never manage tokens, sessions or owners.
	wantError(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/tokens"}), http.StatusForbidden, "forbidden")
	wantError(t, h.bearer(tok, req{method: http.MethodPost, path: "/api/v0/tokens", body: api.CreateApiTokenRequest{Name: "x", Scopes: api.Scopes{api.ScopeDeploy}}}), http.StatusForbidden, "forbidden")
	wantError(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/sessions"}), http.StatusForbidden, "forbidden")
	// Nor passkeys: a leaked token must not register a dashboard passkey.
	wantError(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/passkeys"}), http.StatusForbidden, "forbidden")
	wantError(t, h.bearer(tok, req{method: http.MethodPost, path: "/api/v0/passkeys/registration"}), http.StatusForbidden, "forbidden")
	// Public operations do not mind a token.
	wantStatus(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/health"}), http.StatusOK)

	// A manage token deletes the site.
	manage := createToken(t, h, session, "Admin", api.ScopeManage)
	wantStatus(t, h.bearer(manage.Token, req{method: http.MethodDelete, path: "/api/v0/sites/docs"}), http.StatusNoContent)

	// Listing shows both without secrets, newest first, with last use.
	rec = h.do(req{method: http.MethodGet, path: "/api/v0/tokens", cookie: session})
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), tok) || strings.Contains(rec.Body.String(), manage.Token) {
		t.Fatal("list returns a token secret")
	}
	var page api.ApiTokenPage
	decodeInto(t, rec, &page)
	if len(page.Items) != 2 || page.Items[0].Id != manage.ApiToken.Id || page.Items[1].LastUsedAt == nil {
		t.Fatalf("list = %+v", page.Items)
	}

	// Revocation takes effect on the next request.
	wantStatus(t, h.do(req{method: http.MethodDelete, path: "/api/v0/tokens/" + created.ApiToken.Id, cookie: session}), http.StatusNoContent)
	wantError(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusUnauthorized, "unauthenticated")
	wantError(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/sites"}), http.StatusUnauthorized, "unauthenticated")
}

func TestCreateAPITokenValidation(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	past := h.clock.Now().Add(-time.Hour).UnixMilli()
	for name, body := range map[string]any{
		"no scopes":      api.CreateApiTokenRequest{Name: "x", Scopes: api.Scopes{}},
		"unknown scope":  map[string]any{"name": "x", "scopes": []string{"admin"}},
		"repeated scope": api.CreateApiTokenRequest{Name: "x", Scopes: api.Scopes{api.ScopeDeploy, api.ScopeDeploy}},
		"empty name":     api.CreateApiTokenRequest{Name: "", Scopes: api.Scopes{api.ScopeDeploy}},
		"past expiry":    api.CreateApiTokenRequest{Name: "x", Scopes: api.Scopes{api.ScopeDeploy}, ExpiresAt: &past},
	} {
		t.Run(name, func(t *testing.T) {
			wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/tokens", cookie: session, body: body}), http.StatusBadRequest, "invalid_request")
		})
	}
}

func TestExpiredAPIToken(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	exp := h.clock.Now().Add(time.Hour).UnixMilli()
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/tokens", cookie: session,
		body: api.CreateApiTokenRequest{Name: "short", Scopes: api.Scopes{api.ScopeDeploy}, ExpiresAt: &exp}})
	wantStatus(t, rec, http.StatusCreated)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("create Cache-Control %q", cc)
	}
	var created api.CreatedApiToken
	decodeInto(t, rec, &created)
	h.secret(created.Token)
	wantStatus(t, h.bearer(created.Token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusOK)
	h.clock.advance(time.Hour)
	wantError(t, h.bearer(created.Token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusUnauthorized, "unauthenticated")
}

// Owner B cannot see or revoke owner A's tokens: a foreign token ID gets
// the same 404 as a missing one.
func TestAPITokenOwnerIsolation(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	a := h.owner("a@example.test")
	b := h.owner("b@example.test")
	aTok := createToken(t, h, a, "A's token", api.ScopeDeploy)

	rec := h.do(req{method: http.MethodGet, path: "/api/v0/tokens", cookie: b})
	wantStatus(t, rec, http.StatusOK)
	var page api.ApiTokenPage
	decodeInto(t, rec, &page)
	if len(page.Items) != 0 {
		t.Fatalf("owner B sees %+v", page.Items)
	}
	foreign := h.do(req{method: http.MethodDelete, path: "/api/v0/tokens/" + aTok.ApiToken.Id, cookie: b})
	missing := h.do(req{method: http.MethodDelete, path: "/api/v0/tokens/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e99", cookie: b})
	wantError(t, foreign, http.StatusNotFound, "not_found")
	if foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign %s, missing %s", foreign.Body, missing.Body)
	}
	wantStatus(t, h.bearer(aTok.Token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusOK)
	// A's token only reaches A's sites.
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/sites", cookie: b, body: api.CreateSiteRequest{Name: "bsite"}}), http.StatusCreated)
	wantError(t, h.bearer(aTok.Token, req{method: http.MethodGet, path: "/api/v0/sites/bsite"}), http.StatusNotFound, "not_found")
}

func TestBearerGuessesAreRateLimited(t *testing.T) {
	limits := owners.DefaultTokenLimits()
	limits.FailuresPerIP = owners.Rate{N: 2, Per: time.Minute}
	h := newTokenHarness(t, limits)
	session := h.owner("owner@example.test")
	tok := createToken(t, h, session, "CI", api.ScopeDeploy).Token
	guess := tok[:len(tok)-43] + strings.Repeat("A", 43)
	for range 2 {
		wantError(t, h.bearer(guess, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusUnauthorized, "unauthenticated")
	}
	rec := h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/me"})
	wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	h.clock.advance(time.Minute)
	wantStatus(t, h.bearer(tok, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusOK)
}

// A bearer request is authenticated by the token alone: a session cookie
// beside a bad token does not rescue it.
func TestBearerIgnoresCookie(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	rec := h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: session,
		headers: map[string]string{"Authorization": "Bearer wl_aaaaaaaaaa_" + strings.Repeat("A", 43)}})
	wantError(t, rec, http.StatusUnauthorized, "unauthenticated")
}

func startLogin(t testing.TB, h *ownerHarness, clientName string) api.CliLogin {
	t.Helper()
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/cli-login", body: api.StartCliLoginRequest{ClientName: &clientName}})
	wantStatus(t, rec, http.StatusCreated)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("start Cache-Control %q", cc)
	}
	var l api.CliLogin
	decodeInto(t, rec, &l)
	h.secret(l.DeviceCode)
	return l
}

func poll(h *ownerHarness, deviceCode string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/poll", body: api.PollCliLoginRequest{DeviceCode: deviceCode}})
}

func TestCLILoginEndToEnd(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	l := startLogin(t, h, "wardlock dev on build-host")
	if l.VerificationUrl != "http://"+dashHost+"/cli-login" || l.VerificationUrlComplete != l.VerificationUrl+"?code="+l.UserCode || l.Interval != 5 {
		t.Fatalf("login %+v", l)
	}

	rec := poll(h, l.DeviceCode)
	wantStatus(t, rec, http.StatusAccepted)
	if !strings.Contains(rec.Body.String(), `"pending"`) {
		t.Fatalf("pending body %s", rec.Body)
	}
	rec = poll(h, l.DeviceCode)
	wantError(t, rec, http.StatusTooManyRequests, "slow_down")
	if rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("Retry-After %q", rec.Header().Get("Retry-After"))
	}

	// Approval needs a session from the dashboard origin.
	approve := api.ApproveCliLoginRequest{UserCode: l.UserCode, Scopes: api.Scopes{api.ScopeDeploy, api.ScopeManage}}
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/approve", body: approve}), http.StatusUnauthorized, "unauthenticated")
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/approve", cookie: session, body: approve,
		headers: map[string]string{"Origin": "https://evil.example"}}), http.StatusForbidden, "forbidden")
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/approve", cookie: session, body: approve}), http.StatusNoContent)

	h.clock.advance(5 * time.Second)
	rec = poll(h, l.DeviceCode)
	wantStatus(t, rec, http.StatusOK)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("delivery Cache-Control %q", cc)
	}
	var created api.CreatedApiToken
	decodeInto(t, rec, &created)
	h.secret(created.Token)
	if created.ApiToken.Name != "wardlock dev on build-host" || len(created.ApiToken.Scopes) != 2 {
		t.Fatalf("token %+v", created.ApiToken)
	}
	// Delivered once.
	h.clock.advance(5 * time.Second)
	wantError(t, poll(h, l.DeviceCode), http.StatusGone, "login_expired")

	// The CLI's token works, and `wardlock logout` revokes it.
	wantStatus(t, h.bearer(created.Token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusOK)
	// An approval needs a session: the new token cannot approve logins.
	other := startLogin(t, h, "")
	wantError(t, h.bearer(created.Token, req{method: http.MethodPost, path: "/api/v0/cli-login/approve",
		body: api.ApproveCliLoginRequest{UserCode: other.UserCode, Scopes: api.Scopes{api.ScopeDeploy}}}), http.StatusForbidden, "forbidden")
	rec = h.bearer(created.Token, req{method: http.MethodPost, path: "/api/v0/auth/sign-out"})
	wantStatus(t, rec, http.StatusNoContent)
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("token sign-out set a cookie")
	}
	wantError(t, h.bearer(created.Token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusUnauthorized, "unauthenticated")
	// The dashboard session is untouched.
	me(t, h, session)
}

func TestCLILoginDeniedAndExpired(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	session := h.owner("owner@example.test")
	denied := startLogin(t, h, "")
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/deny", cookie: session,
		body: api.DenyCliLoginRequest{UserCode: denied.UserCode}}), http.StatusNoContent)
	wantError(t, poll(h, denied.DeviceCode), http.StatusGone, "login_expired")

	expired := startLogin(t, h, "")
	h.clock.advance(owners.CLILoginValidity)
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/approve", cookie: session,
		body: api.ApproveCliLoginRequest{UserCode: expired.UserCode, Scopes: api.Scopes{api.ScopeDeploy}}}), http.StatusBadRequest, "invalid_code")
	wantError(t, poll(h, expired.DeviceCode), http.StatusGone, "login_expired")
	wantError(t, poll(h, "dc_unknown"), http.StatusGone, "login_expired")
	wantError(t, poll(h, ""), http.StatusBadRequest, "invalid_request")
}

// Another owner's login (already decided by them) answers exactly as a
// missing code.
func TestCLILoginOwnerIsolation(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	a := h.owner("a@example.test")
	b := h.owner("b@example.test")
	l := startLogin(t, h, "")
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/approve", cookie: a,
		body: api.ApproveCliLoginRequest{UserCode: l.UserCode, Scopes: api.Scopes{api.ScopeDeploy}}}), http.StatusNoContent)
	for _, path := range []string{"/api/v0/cli-login/approve", "/api/v0/cli-login/deny"} {
		foreign := h.do(req{method: http.MethodPost, path: path, cookie: b,
			body: api.ApproveCliLoginRequest{UserCode: l.UserCode, Scopes: api.Scopes{api.ScopeManage}}})
		missing := h.do(req{method: http.MethodPost, path: path, cookie: b,
			body: api.ApproveCliLoginRequest{UserCode: "ZZZZ-ZZZZ", Scopes: api.Scopes{api.ScopeManage}}})
		wantError(t, foreign, http.StatusBadRequest, "invalid_code")
		if foreign.Body.String() != missing.Body.String() {
			t.Fatalf("%s: foreign %s, missing %s", path, foreign.Body, missing.Body)
		}
	}
	h.clock.advance(5 * time.Second)
	rec := poll(h, l.DeviceCode)
	wantStatus(t, rec, http.StatusOK)
	var created api.CreatedApiToken
	decodeInto(t, rec, &created)
	h.secret(created.Token)
	rec = h.bearer(created.Token, req{method: http.MethodGet, path: "/api/v0/me"})
	var m api.Me
	decodeInto(t, rec, &m)
	if m.Owner.Email != "a@example.test" || len(m.Scopes) != 1 || m.Scopes[0] != api.ScopeDeploy {
		t.Fatalf("CLI token = %+v, want owner A with deploy only", m)
	}
}

func TestCLILoginRateLimits(t *testing.T) {
	limits := owners.DefaultTokenLimits()
	limits.StartPerIP = owners.Rate{N: 1, Per: time.Hour}
	limits.PollPerIP = owners.Rate{N: 2, Per: time.Minute}
	limits.DecidePerOwner = owners.Rate{N: 1, Per: 15 * time.Minute}
	h := newTokenHarness(t, limits)
	session := h.owner("owner@example.test")
	l := startLogin(t, h, "")
	limited := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
		if rec.Header().Get("Retry-After") == "" {
			t.Fatal("no Retry-After")
		}
	}
	start := func() *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/cli-login", body: api.StartCliLoginRequest{}})
	}
	deny := func(code string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/deny", cookie: session, body: api.DenyCliLoginRequest{UserCode: code}})
	}
	limited(start())

	// Polls per IP, unknown device codes included.
	wantStatus(t, poll(h, "dc_"+strings.Repeat("A", 43)), http.StatusGone)
	wantStatus(t, poll(h, l.DeviceCode), http.StatusAccepted)
	limited(poll(h, l.DeviceCode))
	h.clock.advance(time.Minute)
	wantStatus(t, poll(h, l.DeviceCode), http.StatusAccepted)

	// Decisions per owner.
	wantError(t, deny("ZZZZ-ZZZZ"), http.StatusBadRequest, "invalid_code")
	limited(deny(l.UserCode))
	h.clock.advance(15 * time.Minute)
	// The login has expired meanwhile: refused as a bad code, not limited.
	wantError(t, deny(l.UserCode), http.StatusBadRequest, "invalid_code")

	// Starts per IP, after the hour.
	limited(start())
	h.clock.advance(44 * time.Minute)
	wantStatus(t, start(), http.StatusCreated)
}
