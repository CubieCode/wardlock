// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/owners"
)

// FuzzDashboardCredentials sends arbitrary Authorization headers, dashboard
// session cookies and CLI device codes to the API through its real
// middleware (request logging included). Whatever arrives, the API never
// fails, authenticates only with the owner's own session or API token,
// never delivers a token to an unapproved CLI login, and never writes a
// credential it holds, or a token-shaped one it was sent, to its logs or
// echoes it back.
//
// Normal test runs execute only the seeds; run it with
// go test -run='^$' -fuzz='^FuzzDashboardCredentials$' ./internal/api/
func FuzzDashboardCredentials(f *testing.F) {
	many := owners.Rate{N: math.MaxInt, Per: time.Minute}
	// Every execution is a fresh guess: tripped limits would only answer
	// 429 from then on.
	h := newTokenHarness(f, owners.TokenLimits{FailuresPerIP: many, StartPerIP: many, PollPerIP: many, DecidePerOwner: many})
	session := h.owner("owner@example.test")
	token := createToken(f, h, session, "fuzz", api.ScopeDeploy).Token
	login := startLogin(f, h, "fuzz CLI")
	h.secret(token)
	// The seeds' real credentials work, so the properties below are not
	// vacuous.
	wantStatus(f, h.bearer(token, req{method: http.MethodGet, path: "/api/v0/me"}), http.StatusOK)
	wantStatus(f, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: session}), http.StatusOK)

	for _, s := range []struct {
		shape        uint8
		cred, cookie string
	}{
		{0, "Bearer " + token, ""},
		{0, "bearer   " + token + "  ", session},
		{0, "Bearer " + strings.Replace(token, token[len(token)-4:], "AAAA", 1), session},
		{0, "Basic " + token, ""},
		{0, "Bearer", ""},
		{1, "", session},
		{1, "", session[:len(session)-1] + "A"},
		{1, "", strings.Repeat("A", 43)},
		{2, login.DeviceCode, ""},
		{2, "dc_" + strings.Repeat("A", 43), ""},
		{2, "dc_", ""},
	} {
		f.Add(s.shape, s.cred, s.cookie)
	}

	f.Fuzz(func(t *testing.T, shape uint8, cred, cookie string) {
		h.logs.reset()
		var r *http.Request
		switch shape % 3 {
		case 0, 1: // an owner-only read, by Authorization or cookie
			r = httptest.NewRequest(http.MethodGet, "/api/v0/me", nil)
			if shape%3 == 0 {
				r.Header.Set("Authorization", cred)
			}
		default: // a CLI login poll
			body, err := json.Marshal(api.PollCliLoginRequest{DeviceCode: cred})
			if err != nil {
				t.Skip("not JSON-encodable")
			}
			r = httptest.NewRequest(http.MethodPost, "/api/v0/cli-login/poll", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		}
		r.Host = dashHost
		if cookie != "" {
			r.Header.Set("Cookie", api.SessionCookie+"="+cookie)
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, r)
		body := rec.Body.String()
		logs := h.logs.String()

		if rec.Code >= 500 {
			t.Fatalf("%d for shape %d: %s", rec.Code, shape%3, body)
		}
		switch shape % 3 {
		case 0, 1:
			if rec.Code == http.StatusOK && !strings.Contains(cred, token) && !strings.Contains(cookie, session) {
				t.Fatalf("authenticated without a credential: %q %q", cred, cookie)
			}
		default:
			if rec.Code == http.StatusOK {
				t.Fatalf("an unapproved login delivered a token for %q", cred)
			}
		}
		for _, secret := range []string{token, session, login.DeviceCode} {
			if strings.Contains(logs, secret) {
				t.Fatalf("a credential reached the logs:\n%s", logs)
			}
		}
		// A token-shaped credential the client sent is never repeated.
		_, sent, _ := strings.Cut(cred, " ")
		for _, c := range []string{strings.TrimSpace(sent), cred} {
			if (strings.HasPrefix(c, "wl_") || strings.HasPrefix(c, "dc_")) && len(c) >= 14 && !strings.ContainsAny(c, " \t\"\\") {
				if strings.Contains(logs, c) || strings.Contains(body, c) {
					t.Fatalf("credential %q reached the logs or body:\n%s\n%s", c, logs, body)
				}
			}
		}
	})
}
