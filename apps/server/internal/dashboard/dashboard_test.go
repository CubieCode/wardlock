// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"wardlock.nz/server/internal/dashboard"
)

func TestServesHTMLWithSecurityHeaders(t *testing.T) {
	for _, path := range []string{"/", "/sites/some-client-route"} {
		rec := httptest.NewRecorder()
		dashboard.Handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: missing security headers: %v", path, rec.Header())
		}
	}
}

func TestDevProxyKeepsHost(t *testing.T) {
	var gotHost string
	vite := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { gotHost = r.Host }))
	defer vite.Close()
	target, _ := url.Parse(vite.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "wardlock.localhost:1234"
	dashboard.Handler(target).ServeHTTP(httptest.NewRecorder(), req)
	if gotHost != "wardlock.localhost:1234" {
		t.Fatalf("Vite saw Host %q", gotHost)
	}
}
