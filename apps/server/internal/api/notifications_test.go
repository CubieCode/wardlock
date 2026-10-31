// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/notify"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store/storetest"
)

const notificationSettingsPath = "/api/v0/notification-settings"

func newNotificationHarness(t *testing.T) *ownerHarness {
	t.Helper()
	db := storetest.New(t)
	return newOwnerHarnessWith(t, db, owners.Limits{}, func(s *api.Server) {
		s.Notifications = notify.New(notify.Config{Store: db.Notifications()})
		tokens, err := owners.NewTokens(owners.TokensConfig{
			Tokens: db.APITokens(), Logger: s.Logger, Secret: bytes.Repeat([]byte("k"), 32),
			DashboardURL: &url.URL{Scheme: "http", Host: dashHost},
		})
		if err != nil {
			t.Fatal(err)
		}
		s.Tokens = tokens
	})
}

func allOn() api.NotificationSettings {
	return api.NotificationSettings{
		DomainVerified: true, DomainFailing: true, LinkUsedUp: true, LinksExpiring: true, TokenExpiring: true, PreviewExpiring: true,
	}
}

func TestNotificationSettings(t *testing.T) {
	h := newNotificationHarness(t)
	cookie := h.owner("one@example.test")
	read := func(cookie string) api.NotificationSettings {
		t.Helper()
		rec := h.do(req{method: http.MethodGet, path: notificationSettingsPath, cookie: cookie})
		wantStatus(t, rec, http.StatusOK)
		return decode[api.NotificationSettings](t, rec)
	}
	if got := read(cookie); got != allOn() {
		t.Fatalf("default settings = %+v", got)
	}

	rec := h.do(req{method: http.MethodPatch, path: notificationSettingsPath, cookie: cookie, body: map[string]any{"linksExpiring": false}})
	wantStatus(t, rec, http.StatusOK)
	want := allOn()
	want.LinksExpiring = false
	if got := decode[api.NotificationSettings](t, rec); got != want {
		t.Fatalf("PATCH answered %+v", got)
	}
	if got := read(cookie); got != want {
		t.Fatalf("stored settings = %+v", got)
	}

	// A body that names nothing changes nothing.
	rec = h.do(req{method: http.MethodPatch, path: notificationSettingsPath, cookie: cookie, body: map[string]any{}})
	wantStatus(t, rec, http.StatusOK)
	if got := decode[api.NotificationSettings](t, rec); got != want {
		t.Fatalf("empty PATCH answered %+v", got)
	}
	rec = h.do(req{method: http.MethodPatch, path: notificationSettingsPath, cookie: cookie, body: map[string]any{"linksExpiring": true, "tokenExpiring": false}})
	wantStatus(t, rec, http.StatusOK)
	want.LinksExpiring, want.TokenExpiring = true, false
	if got := read(cookie); got != want {
		t.Fatalf("stored settings = %+v", got)
	}
}

// TestNotificationSettingsOwnerIsolation checks each owner sees and changes
// only their own settings.
func TestNotificationSettingsOwnerIsolation(t *testing.T) {
	h := newNotificationHarness(t)
	a, b := h.owner("one@example.test"), h.owner("two@example.test")
	wantStatus(t, h.do(req{method: http.MethodPatch, path: notificationSettingsPath, cookie: a, body: map[string]any{"domainVerified": false}}), http.StatusOK)
	rec := h.do(req{method: http.MethodGet, path: notificationSettingsPath, cookie: b})
	wantStatus(t, rec, http.StatusOK)
	if got := decode[api.NotificationSettings](t, rec); got != allOn() {
		t.Fatalf("another owner's settings = %+v", got)
	}
	wantStatus(t, h.do(req{method: http.MethodPatch, path: notificationSettingsPath, cookie: b, body: map[string]any{"linkUsedUp": false}}), http.StatusOK)
	rec = h.do(req{method: http.MethodGet, path: notificationSettingsPath, cookie: a})
	want := allOn()
	want.DomainVerified = false
	if got := decode[api.NotificationSettings](t, rec); got != want {
		t.Fatalf("own settings after another owner's change = %+v", got)
	}
}

func TestNotificationSettingsAccess(t *testing.T) {
	h := newNotificationHarness(t)
	cookie := h.owner("one@example.test")
	token := createToken(t, h, cookie, "ci", api.ScopeDeploy, api.ScopeManage)
	for _, r := range []req{
		{method: http.MethodGet, path: notificationSettingsPath},
		{method: http.MethodPatch, path: notificationSettingsPath, body: map[string]any{"linkUsedUp": false}},
	} {
		rec := h.bearer(token.Token, r)
		wantStatus(t, rec, http.StatusForbidden)
		h.secret(token.Token)
		rec = h.do(r)
		wantStatus(t, rec, http.StatusUnauthorized)
	}
	bad := h.do(req{method: http.MethodPatch, path: notificationSettingsPath, cookie: cookie, body: map[string]any{"linkUsedUp": "no"}})
	wantStatus(t, bad, http.StatusBadRequest)
	h.assertLogsClean()
}
