// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"net/http"
	"testing"
	"time"
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

// TestPreviewHosts covers a preview host (C1.19) with the edge's own fakes:
// it is never redirected to the site's primary, every answer is noindex,
// and once it expires it is an unknown host, though the cache holds it.
func TestPreviewHosts(t *testing.T) {
	h := newHarness(t, nil)
	site := h.src.sites["basic"]
	site.PrimaryHost = "docs.example.test"
	preview := site
	preview.Preview, preview.ExpiresAt = "pr-1", time.Now().Add(time.Hour)
	expired := site
	expired.Preview, expired.ExpiresAt = "old", time.Now().Add(-time.Millisecond)
	h.src.mu.Lock()
	h.src.sites["basic"] = site
	h.src.sites["basic--pr-1"] = preview
	h.src.sites["basic--old"] = expired
	h.src.mu.Unlock()
	h.notifier.Notify(site.ID)

	for _, target := range []string{"/", "/docs/?a=1", "/missing", "/.wardlock/health"} {
		rec := h.do(http.MethodGet, "basic--pr-1."+base, target, nil)
		if rec.Code == http.StatusPermanentRedirect || rec.Header().Values("X-Robots-Tag")[0] != "noindex" {
			t.Fatalf("preview %s = %d %v", target, rec.Code, rec.Header())
		}
	}
	if rec := h.do(http.MethodGet, "basic."+base, "/", nil); rec.Code != http.StatusPermanentRedirect || rec.Header().Get("X-Robots-Tag") != "" {
		t.Fatalf("live base host = %d %v, want the 308 without noindex", rec.Code, rec.Header())
	}
	unknown := h.do(http.MethodGet, "nothing."+base, "/", nil)
	if rec := h.do(http.MethodGet, "basic--old."+base, "/", nil); rec.Code != unknown.Code || rec.Body.String() != unknown.Body.String() {
		t.Fatalf("expired preview = %d %q, want the unknown-host answer", rec.Code, rec.Body)
	}
}
