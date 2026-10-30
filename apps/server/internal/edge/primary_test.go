// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"net/http"
	"testing"
)

// TestPrimaryRedirect covers the 308 with the edge's own fakes: without a
// SiteOrigin the primary is reached over https, the site's own files never
// beat the redirect, the reserved path is never redirected and a change of
// primary applies once the site is invalidated.
func TestPrimaryRedirect(t *testing.T) {
	h := newHarness(t, nil)
	site := h.src.sites["basic"]
	site.PrimaryHost = "docs.example.test"
	h.src.set(site, h.src.files[site.ActiveDeployID])
	h.notifier.Notify(site.ID)

	rec := h.do(http.MethodGet, "basic."+base, "/docs/?a=1", nil)
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://docs.example.test/docs/?a=1" || rec.Body.Len() != 0 {
		t.Fatalf("base host = %d to %q, %q", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	// The deploy's own .wardlock/ files are never served, here as anywhere.
	if rec := h.do(http.MethodGet, "basic."+base, "/.wardlock/health", nil); rec.Code != http.StatusOK || rec.Body.String() == "deployed health" {
		t.Fatalf("reserved path = %d %q", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodGet, "docs.example.test", "/", nil); rec.Code != http.StatusOK {
		t.Fatalf("primary = %d", rec.Code)
	}

	site.PrimaryHost = ""
	h.src.set(site, h.src.files[site.ActiveDeployID])
	h.notifier.Notify(site.ID)
	if rec := h.do(http.MethodGet, "basic."+base, "/", nil); rec.Code != http.StatusOK {
		t.Fatalf("after clearing = %d", rec.Code)
	}
}
