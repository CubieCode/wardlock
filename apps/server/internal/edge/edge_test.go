// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wardlock.nz/server/internal/edge"
)

func TestRouting(t *testing.T) {
	h := edge.New(edge.Config{
		BaseDomain:    "wardlock.localhost",
		DashboardHost: "wardlock.localhost",
		Dashboard:     text("dashboard"),
		Health:        text("healthy"),
	})
	tests := []struct {
		name, host, path string
		status           int
		body             string
	}{
		{"dashboard", "wardlock.localhost:8080", "/", 200, "dashboard"},
		{"dashboard host is case-insensitive", "WardLock.Localhost.", "/x", 200, "dashboard"},
		{"healthz on dashboard", "wardlock.localhost", "/healthz", 200, "healthy"},
		{"healthz on a site", "hello.wardlock.localhost", "/healthz", 200, "healthy"},
		{"hello site", "hello.wardlock.localhost:8080", "/", 200, "Hello from Wardlock"},
		{"site health", "hello.wardlock.localhost", "/.wardlock/health", 200, `"ok"`},
		{"unknown reserved path", "hello.wardlock.localhost", "/.wardlock/index.html", 404, ""},
		{"missing file", "hello.wardlock.localhost", "/missing.html", 404, ""},
		{"unknown site", "nope.wardlock.localhost", "/", 404, "No site"},
		{"nested subdomain", "a.hello.wardlock.localhost", "/", 404, "No site"},
		{"foreign host", "example.org", "/", 404, "No site"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			body, _ := io.ReadAll(rec.Body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if !strings.Contains(string(body), tt.body) {
				t.Fatalf("body %q does not contain %q", body, tt.body)
			}
		})
	}
}

func text(s string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, s) })
}
