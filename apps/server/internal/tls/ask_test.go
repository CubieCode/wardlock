// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
)

func TestAsk(t *testing.T) {
	proxies := TrustedProxies{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	domains := &fakeDomains{active: map[string]bool{"docs.customer.test": true, "xn--bcher-kva.example": true}}
	ask := proxies.Handler(Ask(OnDemand{BaseDomain: "sites.test", DashboardHost: "wardlock.example.test", Domains: domains}, slog.New(slog.DiscardHandler)))
	tests := []struct {
		name   string
		peer   string
		method string
		domain string
		want   int
	}{
		{"active domain from the proxy", "127.0.0.1:5000", http.MethodGet, "docs.customer.test", http.StatusOK},
		{"over IPv6", "[::1]:5000", http.MethodGet, "docs.customer.test", http.StatusOK},
		{"HEAD", "127.0.0.1:5000", http.MethodHead, "docs.customer.test", http.StatusOK},
		{"in another case with a trailing dot", "127.0.0.1:5000", http.MethodGet, "Docs.Customer.Test.", http.StatusOK},
		{"an A-label", "127.0.0.1:5000", http.MethodGet, "xn--bcher-kva.example", http.StatusOK},
		{"unknown, pending or lapsed", "127.0.0.1:5000", http.MethodGet, "other.customer.test", http.StatusNotFound},
		{"a site under the base domain", "127.0.0.1:5000", http.MethodGet, "docs.sites.test", http.StatusNotFound},
		{"the dashboard host", "127.0.0.1:5000", http.MethodGet, "wardlock.example.test", http.StatusNotFound},
		{"an IP address", "127.0.0.1:5000", http.MethodGet, "192.0.2.1", http.StatusNotFound},
		{"no domain", "127.0.0.1:5000", http.MethodGet, "", http.StatusNotFound},
		{"POST", "127.0.0.1:5000", http.MethodPost, "docs.customer.test", http.StatusNotFound},
		{"an untrusted peer", "192.0.2.10:5000", http.MethodGet, "docs.customer.test", http.StatusNotFound},
		{"an untrusted peer claiming to be forwarded by the proxy", "192.0.2.10:5000", http.MethodGet, "docs.customer.test", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://anything.example"+AskPath+"?domain="+url.QueryEscape(tt.domain), nil)
			req.RemoteAddr = tt.peer
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			rec := httptest.NewRecorder()
			ask.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("got %d, want %d", rec.Code, tt.want)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control %q", rec.Header().Get("Cache-Control"))
			}
		})
	}

	t.Run("without the trusted-proxy middleware nobody is trusted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, AskPath+"?domain=docs.customer.test", nil)
		req.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		Ask(OnDemand{BaseDomain: "sites.test", Domains: domains}, slog.New(slog.DiscardHandler)).ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
	})
	t.Run("a failed lookup is an error, not a yes", func(t *testing.T) {
		failing := proxies.Handler(Ask(OnDemand{BaseDomain: "sites.test", Domains: &fakeDomains{err: errors.New("database is locked")}}, slog.New(slog.DiscardHandler)))
		req := httptest.NewRequest(http.MethodGet, AskPath+"?domain=docs.customer.test", nil)
		req.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		failing.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("got %d, want 500", rec.Code)
		}
	})
}
