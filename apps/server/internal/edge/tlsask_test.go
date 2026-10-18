// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wardlock.nz/server/internal/edge"
)

// TestTLSAsk: /.wardlock/tls-ask goes to Config.TLSAsk on every host,
// before routing, and is a plain 404 everywhere without one; a deploy can
// never answer it.
func TestTLSAsk(t *testing.T) {
	h := newHarness(t, nil)
	hosts := []string{base, "basic." + base, "docs.example.test", "unknown.example.test", "empty." + base}
	targets := []string{"/.wardlock/tls-ask?domain=docs.example.test", "/x/../.wardlock/tls-ask?domain=a.example"}

	for _, host := range hosts {
		for _, target := range targets {
			rec := h.do(http.MethodGet, host, target, nil)
			if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "no-store" || rec.Body.String() == "dashboard" {
				t.Errorf("without TLSAsk %s%s = %d %q %q, want a no-store 404", host, target, rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
			}
		}
	}

	var asked []string
	e, err := edge.New(edge.Config{
		DashboardHost: base,
		Dashboard:     text("dashboard"),
		Health:        text("healthy"),
		Sites:         h.src,
		Blobs:         h.fx.blobs,
		Invalidator:   h.notifier,
		Access:        publicAccess{},
		TLSAsk: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked = append(asked, r.Host+" "+r.URL.Query().Get("domain"))
			w.WriteHeader(http.StatusTeapot)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	for _, host := range hosts {
		for _, target := range targets {
			req := httptest.NewRequest(http.MethodGet, "http://placeholder"+target, nil)
			req.Host = host
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusTeapot {
				t.Errorf("with TLSAsk %s%s = %d, want the ask handler", host, target, rec.Code)
			}
		}
	}
	if len(asked) != len(hosts)*len(targets) {
		t.Fatalf("asked %v", asked)
	}
	// Other reserved paths are not the ask handler's.
	req := httptest.NewRequest(http.MethodGet, "http://placeholder/.wardlock/health", nil)
	req.Host = "basic." + base
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health = %d", rec.Code)
	}
}
