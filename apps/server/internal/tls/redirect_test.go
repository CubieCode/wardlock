// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirect(t *testing.T) {
	tests := []struct {
		name       string
		port       int
		method     string
		host       string
		target     string
		wantStatus int
		wantLoc    string
	}{
		{"GET keeps path and query", 443, http.MethodGet, "docs.sites.example", "/a/b?x=1&t=tok", http.StatusMovedPermanently, "https://docs.sites.example/a/b?x=1&t=tok"},
		{"HEAD", 443, http.MethodHead, "docs.sites.example", "/", http.StatusMovedPermanently, "https://docs.sites.example/"},
		{"POST keeps the method", 443, http.MethodPost, "wardlock.example", "/api/v0/sites", http.StatusPermanentRedirect, "https://wardlock.example/api/v0/sites"},
		{"port in Host is replaced", 443, http.MethodGet, "docs.sites.example:80", "/", http.StatusMovedPermanently, "https://docs.sites.example/"},
		{"Host is lowercased", 443, http.MethodGet, "Docs.Sites.Example", "/", http.StatusMovedPermanently, "https://docs.sites.example/"},
		{"non-default HTTPS port", 8443, http.MethodGet, "docs.sites.example:8080", "/x", http.StatusMovedPermanently, "https://docs.sites.example:8443/x"},
		{"IPv6 host", 443, http.MethodGet, "[2001:db8::1]:80", "/", http.StatusMovedPermanently, "https://[2001:db8::1]/"},
		{"IPv6 host, other port", 8443, http.MethodGet, "[2001:db8::1]", "/", http.StatusMovedPermanently, "https://[2001:db8::1]:8443/"},
		{"Host with a path is refused", 443, http.MethodGet, "evil.example/x", "/", http.StatusBadRequest, ""},
		{"Host with userinfo is refused", 443, http.MethodGet, "a@evil.example", "/", http.StatusBadRequest, ""},
		{"empty Host is refused", 443, http.MethodGet, "", "/", http.StatusBadRequest, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://placeholder"+tt.target, nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			Redirect(tt.port).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if loc := rec.Header().Get("Location"); loc != tt.wantLoc {
				t.Fatalf("Location = %q, want %q", loc, tt.wantLoc)
			}
		})
	}
}
