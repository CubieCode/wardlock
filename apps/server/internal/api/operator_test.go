// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// TestSuspensionEndsEveryCredential suspends an owner as `wardlockd admin
// suspend-owner` does and checks every kind of owner credential stops at
// once, sign-in is refused like a wrong code, another owner is untouched,
// and reinstating brings back API tokens but not sessions.
func TestSuspensionEndsEveryCredential(t *testing.T) {
	h := newTokenHarness(t, owners.TokenLimits{})
	ctx := t.Context()
	link := h.adminCreate("owner@example.test")
	session := h.signIn("owner@example.test")
	other := h.owner("other@example.test")
	token := createToken(t, h, session, "CI", api.ScopeDeploy)
	approved := startLogin(t, h, "laptop")
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/approve", cookie: session,
		body: api.ApproveCliLoginRequest{UserCode: approved.UserCode, Scopes: api.Scopes{api.ScopeDeploy}}}), http.StatusNoContent)
	pending := startLogin(t, h, "desktop")

	if _, err := h.svc.Suspend(ctx, "Owner@Example.test"); err != nil {
		t.Fatal(err)
	}
	// Every credential is refused, with the usual neutral answers.
	wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: session}), http.StatusUnauthorized, "unauthenticated")
	wantError(t, h.bearer(token.Token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusUnauthorized, "unauthenticated")
	h.clock.advance(5 * time.Second)
	wantError(t, poll(h, approved.DeviceCode), http.StatusGone, "login_expired")
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/cli-login/approve", cookie: session,
		body: api.ApproveCliLoginRequest{UserCode: pending.UserCode, Scopes: api.Scopes{api.ScopeDeploy}}}), http.StatusUnauthorized, "unauthenticated")
	// Sign-in is refused as for an unknown address or a used link.
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": link}}),
		http.StatusBadRequest, "invalid_code")
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code", body: map[string]string{"email": "owner@example.test"}}),
		http.StatusAccepted)
	wantError(t, h.verify("owner@example.test", "123456", ""), http.StatusBadRequest, "invalid_code")
	h.noMail()
	// Another owner is untouched.
	me(t, h, other)

	if _, err := h.svc.Reinstate(ctx, "owner@example.test"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, h.bearer(token.Token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusOK)
	wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: session}), http.StatusUnauthorized, "unauthenticated")
	h.clock.advance(5 * time.Second)
	wantError(t, poll(h, approved.DeviceCode), http.StatusGone, "login_expired")
	me(t, h, h.signIn("owner@example.test"))
}

func TestSuspendUnknownOwner(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	h.owner("owner@example.test")
	if _, err := h.svc.Suspend(t.Context(), "nobody@example.test"); !errors.Is(err, owners.ErrNotFound) {
		t.Errorf("Suspend(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := h.svc.Reinstate(t.Context(), "nobody@example.test"); !errors.Is(err, owners.ErrNotFound) {
		t.Errorf("Reinstate(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := h.svc.Suspend(t.Context(), "not an address"); !errors.Is(err, owners.ErrInvalidEmail) {
		t.Errorf("Suspend(invalid) = %v, want ErrInvalidEmail", err)
	}
}

func TestPasskeySignInRefusedWhileSuspended(t *testing.T) {
	h := newPasskeyHarness(t, owners.Limits{})
	a := newSoftAuthenticator(t)
	h.register(h.owner("owner@example.test"), a, "Laptop")
	if _, err := h.svc.Suspend(t.Context(), "owner@example.test"); err != nil {
		t.Fatal(err)
	}
	c := h.startSignIn()
	rec := h.finishSignIn(c.CeremonyId, a.get(c.Options, dashOrig, nil))
	wantError(t, rec, http.StatusBadRequest, "invalid_passkey")
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("refused sign-in set cookies %q", got)
	}

	if _, err := h.svc.Reinstate(t.Context(), "owner@example.test"); err != nil {
		t.Fatal(err)
	}
	c = h.startSignIn()
	wantStatus(t, h.finishSignIn(c.CeremonyId, a.get(c.Options, dashOrig, nil)), http.StatusOK)
}

// TestInvitesRestricted runs WARDLOCK_INVITES=operator: owners are refused
// with invites_restricted whatever the address, and nothing is sent.
func TestInvitesRestricted(t *testing.T) {
	db := storetest.New(t)
	h := newOwnerHarnessWith(t, db, owners.Limits{}, nil)
	restricted, err := owners.New(owners.Config{
		Owners: db.Owners(), Sessions: db.Sessions(), Mailer: captureMailer(h.mail), Logger: slog.New(slog.DiscardHandler),
		Secret: bytes.Repeat([]byte("k"), 32), DashboardURL: &url.URL{Scheme: "http", Host: dashHost}, Now: h.clock.Now, Invites: owners.InvitesOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &api.Server{Version: "test", Owners: restricted, Logger: slog.New(slog.DiscardHandler)}
	session := h.owner("owner@example.test")
	h.h = r.Handler()
	for _, email := range []string{"new@example.test", "owner@example.test", ""} {
		rec := h.do(req{method: http.MethodPost, path: "/api/v0/owners", cookie: session, body: map[string]string{"email": email}})
		if email == "" {
			wantError(t, rec, http.StatusBadRequest, "invalid_request")
			continue
		}
		wantError(t, rec, http.StatusForbidden, "invites_restricted")
	}
	restricted.Close()
	h.noMail()
	if _, err := owners.New(owners.Config{
		Owners: db.Owners(), Sessions: db.Sessions(), Mailer: captureMailer(h.mail), Logger: slog.New(slog.DiscardHandler),
		Secret: bytes.Repeat([]byte("k"), 32), DashboardURL: &url.URL{Scheme: "http", Host: dashHost}, Invites: "everyone",
	}); err == nil {
		t.Fatal("an unknown invitation mode was accepted")
	}
}

// TestTakenDownSiteRefusesChanges takes a site down as `wardlockd admin
// takedown-site` does: its owner sees it as unavailable and can read it,
// but cannot deploy, activate, roll back or delete it; another owner gets
// the usual 404; restoring lifts every refusal.
func TestTakenDownSiteRefusesChanges(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	first := hs.deploy(hs.ownerA, "docs", file{"index.html", "<p>one</p>"})
	hs.deploy(hs.ownerA, "docs", file{"index.html", "<p>two</p>"})
	pending := hs.createDeploy(hs.ownerA, "docs", manifest(file{"index.html", "<p>three</p>"}))
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]string{"hostname": "docs.example.com"}).want(t, http.StatusCreated, "")
	if err := hs.db.Sites().TakeDown(t.Context(), first.Site.Id, time.Now()); err != nil {
		t.Fatal(err)
	}

	var site api.Site
	res := hs.do(hs.ownerA, http.MethodGet, "/sites/docs", nil)
	res.want(t, http.StatusOK, "")
	res.decode(t, &site)
	if !site.Unavailable || site.ActiveDeployId == nil {
		t.Fatalf("taken-down site = %+v", site)
	}
	var page api.SitePage
	res = hs.do(hs.ownerA, http.MethodGet, "/sites", nil)
	res.decode(t, &page)
	if len(page.Items) != 1 || !page.Items[0].Unavailable {
		t.Fatalf("site list = %+v", page)
	}
	hs.do(hs.ownerA, http.MethodGet, "/sites/docs/deploys", nil).want(t, http.StatusOK, "")

	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/deploys", manifest(file{"index.html", "<p>four</p>"})).want(t, http.StatusConflict, "site_unavailable")
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/deploys/"+pending.Deploy.Id+"/activate", nil).want(t, http.StatusConflict, "site_unavailable")
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/rollback", nil).want(t, http.StatusConflict, "site_unavailable")
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs", nil).want(t, http.StatusConflict, "site_unavailable")
	hs.upload(hs.ownerA, "docs", pending.Deploy.Id, file{"index.html", "<p>three</p>"}).want(t, http.StatusConflict, "site_unavailable")
	// Custom domains are frozen; checking one still works.
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/domains", map[string]string{"hostname": "www.example.com"}).want(t, http.StatusConflict, "site_unavailable")
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/domains/docs.example.com", nil).want(t, http.StatusConflict, "site_unavailable")
	hs.advance(time.Hour)
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/domains/docs.example.com/verify", nil).want(t, http.StatusOK, "")
	// Access can still change, but no record of who had it may go.
	hs.do(hs.ownerA, http.MethodPut, "/sites/docs/policies", map[string]any{"path": "/drafts/", "visibility": "protected", "methods": []string{"links"}}).want(t, http.StatusOK, "")
	var pols api.PolicyPage
	res = hs.do(hs.ownerA, http.MethodGet, "/sites/docs/policies", nil)
	res.decode(t, &pols)
	for _, p := range pols.Items {
		if p.Path == "/drafts/" {
			hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/policies/"+p.Id, nil).want(t, http.StatusConflict, "site_unavailable")
		}
	}
	var link api.CreatedShareLink
	res = hs.do(hs.ownerA, http.MethodPost, "/sites/docs/links", map[string]any{"label": "review"})
	res.want(t, http.StatusCreated, "")
	res.decode(t, &link)
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/links/"+link.ShareLink.PublicId+"/rotate", nil).want(t, http.StatusOK, "")
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs/links/"+link.ShareLink.PublicId, nil).want(t, http.StatusNoContent, "")
	// Another owner learns nothing.
	hs.do(hs.ownerB, http.MethodGet, "/sites/docs", nil).want(t, http.StatusNotFound, "not_found")
	hs.do(hs.ownerB, http.MethodDelete, "/sites/docs", nil).want(t, http.StatusNotFound, "not_found")

	if err := hs.db.Sites().Restore(t.Context(), first.Site.Id); err != nil {
		t.Fatal(err)
	}
	res = hs.do(hs.ownerA, http.MethodGet, "/sites/docs", nil)
	res.decode(t, &site)
	if site.Unavailable {
		t.Fatalf("restored site = %+v", site)
	}
	hs.do(hs.ownerA, http.MethodPost, "/sites/docs/rollback", nil).want(t, http.StatusOK, "")
	hs.do(hs.ownerA, http.MethodDelete, "/sites/docs", nil).want(t, http.StatusNoContent, "")
}

// suspendingSessions suspends the owner just before their session is
// stored, as an operator's `suspend-owner` racing a sign-in would.
type suspendingSessions struct {
	store.Sessions
	owners store.Owners
}

func (s suspendingSessions) CreateOwner(ctx context.Context, row store.OwnerSession) error {
	if err := s.owners.Suspend(ctx, row.OwnerID, row.CreatedAt); err != nil {
		return err
	}
	return s.Sessions.CreateOwner(ctx, row)
}

// TestSignInRacingSuspension checks a sign-in whose session is stored just
// after a suspension gets no session, and reinstating brings none back.
func TestSignInRacingSuspension(t *testing.T) {
	db := storetest.New(t)
	h := newOwnerHarnessWith(t, db, owners.Limits{}, nil)
	link := h.adminCreate("owner@example.test")
	racy, err := owners.New(owners.Config{
		Owners: db.Owners(), Sessions: suspendingSessions{db.Sessions(), db.Owners()}, Mailer: captureMailer(h.mail),
		Logger: slog.New(slog.DiscardHandler), Secret: bytes.Repeat([]byte("k"), 32),
		DashboardURL: &url.URL{Scheme: "http", Host: dashHost}, Now: h.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(racy.Close)
	h.h = (&api.Server{Version: "test", Owners: racy, Logger: slog.New(slog.DiscardHandler)}).Handler()
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": link}})
	wantError(t, rec, http.StatusBadRequest, "invalid_code")
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("refused sign-in set cookies %q", got)
	}
	if _, err := h.svc.Reinstate(t.Context(), "owner@example.test"); err != nil {
		t.Fatal(err)
	}
	o, err := db.Owners().GetByEmail(t.Context(), "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := db.Sessions().ListOwner(t.Context(), store.OwnerSessionQuery{
		OwnerID: o.ID, Now: h.clock.Now(), IdleSince: h.clock.Now().Add(-time.Hour), AfterSeen: h.clock.Now().Add(time.Hour), Limit: 10,
	}); err != nil || len(rows) != 0 {
		t.Fatalf("sessions after reinstating = %v, %v", rows, err)
	}
}
