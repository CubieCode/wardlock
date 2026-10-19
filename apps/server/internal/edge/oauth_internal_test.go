// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
)

func TestFormAction(t *testing.T) {
	for _, tt := range []struct {
		name      string
		providers []access.ProviderInfo
		want      string
	}{
		{"no providers", nil, "'self'"},
		{"providers and the auth origin, once each", []access.ProviderInfo{
			{ID: "github", Origins: []string{"https://github.com", "https://auth.example-sites.net"}},
			{ID: "google", Origins: []string{"https://accounts.google.com", "https://auth.example-sites.net"}},
		}, "'self' https://github.com https://auth.example-sites.net https://accounts.google.com"},
		{"discovered endpoint on another host", []access.ProviderInfo{
			{ID: "oidc", Origins: []string{"https://sso.example.test", "https://login.example.test:8443"}},
		}, "'self' https://sso.example.test https://login.example.test:8443"},
		{"anything but a plain origin is dropped", []access.ProviderInfo{
			{ID: "oidc", Origins: []string{"https:", "https://", "javascript:x", "https://a.test; script-src *", "https://a.test/path", "*", "", "http://127.0.0.1:9999"}},
		}, "'self' http://127.0.0.1:9999"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formAction(tt.providers); got != tt.want {
				t.Fatalf("formAction = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCookieMaxAge(t *testing.T) {
	binding := oauthBindingCookie(access.Cookie{Name: access.OAuthBindingCookieName, Value: "v", Expires: time.Now().Add(access.OAuthFlowLifetime)})
	if binding.MaxAge != int(access.OAuthFlowLifetime/time.Second) {
		t.Errorf("binding cookie Max-Age = %d, want %d", binding.MaxAge, int(access.OAuthFlowLifetime/time.Second))
	}
	if c := SessionCookie(access.Cookie{Name: access.SessionCookieName, Value: "v", Expires: time.Now().Add(-time.Second)}); c.MaxAge != 1 {
		t.Errorf("expired session cookie Max-Age = %d, want 1", c.MaxAge)
	}
}
