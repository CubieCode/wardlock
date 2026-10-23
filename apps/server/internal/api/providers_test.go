// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/oauth"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// Provider credentials for the tests: none may ever reach a response.
const (
	providerClientID = "client-id-must-not-leak"
	providerSecret   = "client-secret-must-not-leak"
	providerIssuer   = "https://sso.issuer-must-not-leak.example"
)

// providersServer serves the API with the given providers configured.
func providersServer(t *testing.T, providers ...access.Provider) http.Handler {
	t.Helper()
	db := storetest.New(t)
	acc, err := access.New(access.Config{
		Sites: db.Sites(), Policies: db.Policies(), Sessions: db.Sessions(), Links: db.Links(), PasswordSessions: db.PasswordSessions(),
		AccessLog: db.AccessLog(), Traffic: db.Traffic(), EmailCodes: db.EmailCodes(), Notifier: deploys.NewLocalNotifier(),
		Secret: bytes.Repeat([]byte("p"), 32), Now: time.Now,
		OAuth: access.OAuthConfig{Providers: providers, AuthOrigin: "https://auth.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(acc.Close)
	inner := (&api.Server{Access: acc}).Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(ownerHeader); id != "" {
			r = r.WithContext(api.WithOwner(r.Context(), id))
		}
		inner.ServeHTTP(w, r)
	})
}

func TestListSignInProviders(t *testing.T) {
	github := func() access.Provider {
		return oauth.NewGitHub(oauth.GitHubConfig{ClientID: providerClientID, ClientSecret: providerSecret})
	}
	google := func() access.Provider { return oauth.NewGoogle(providerClientID, providerSecret, nil) }
	oidc := func(name string) access.Provider {
		return oauth.NewOIDC(oauth.OIDCConfig{ID: "oidc", Name: name, Issuer: providerIssuer, ClientID: providerClientID, ClientSecret: providerSecret})
	}
	for _, tc := range []struct {
		name      string
		providers []access.Provider
		want      []api.SignInProvider
	}{
		{"none", nil, []api.SignInProvider{}},
		{"GitHub only", []access.Provider{github()}, []api.SignInProvider{{Id: api.SignInProviderIdGithub, Name: "GitHub"}}},
		{"Google and OIDC with the default name", []access.Provider{google(), oidc("single sign-on")},
			[]api.SignInProvider{{Id: api.SignInProviderIdGoogle, Name: "Google"}, {Id: api.SignInProviderIdOidc, Name: "single sign-on"}}},
		{"all, configured in another order", []access.Provider{oidc("Harbour SSO"), google(), github()},
			[]api.SignInProvider{{Id: api.SignInProviderIdGithub, Name: "GitHub"}, {Id: api.SignInProviderIdGoogle, Name: "Google"}, {Id: api.SignInProviderIdOidc, Name: "Harbour SSO"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := providersServer(t, tc.providers...)
			r := httptest.NewRequest(http.MethodGet, "/api/v0/sign-in-providers", nil)
			r.Header.Set(ownerHeader, store.NewID())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			for _, secret := range []string{providerClientID, providerSecret, providerIssuer, "issuer-must-not-leak"} {
				if strings.Contains(body, secret) {
					t.Fatalf("response %s contains %q", body, secret)
				}
			}
			var got api.SignInProviderList
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Items == nil || !slices.Equal(got.Items, tc.want) {
				t.Fatalf("items = %+v, want %+v", got.Items, tc.want)
			}
			var raw map[string][]map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			for _, item := range raw["items"] {
				if len(item) != 2 {
					t.Fatalf("item %v has fields beyond id and name", item)
				}
			}

			// Without an owner it answers 401 and names nothing.
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v0/sign-in-providers", nil))
			if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "GitHub") {
				t.Fatalf("unauthenticated: %d %s", rec.Code, rec.Body)
			}
		})
	}
}
