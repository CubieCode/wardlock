// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"net/http"
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
)

const (
	app        = "app." + base
	revalidate = "public, max-age=0, must-revalidate"
	immutable  = "public, max-age=31536000, immutable"
)

// withAssets publishes "app" (an SPA build with hashed and unhashed assets,
// a 404.html and a hashed HTML page with renditions) with immutableAssets
// set to on, and returns the deploy's files so a test can publish them again.
func withAssets(t *testing.T, a edge.Access, on bool) (*harness, []edge.ServedFile) {
	t.Helper()
	h := newHarness(t, a)
	b := h.fx.blobs
	page := b.add("assets/page-AbCdEf12.html", []byte("<h1>Hashed page</h1>"))
	md := b.add("", []byte("# Hashed page\n"))
	page.Renditions = map[string]edge.Rendition{"md": {SHA256: md.SHA256, Size: md.Size}}
	files := []edge.ServedFile{
		b.add("index.html", []byte("<h1>App shell</h1>")),
		b.add("404.html", []byte("<h1>Lost</h1>")),
		b.add("assets/index-BdK3j2Lp.js", []byte("console.log('vite')")),
		b.add("assets/index-BdK3j2Lp.js.br", []byte("brotli bytes")),
		b.add("assets/main.3f2a9c1b.css", []byte("body { color: teal }")),
		b.add("assets/app.js", []byte("console.log('unhashed')")),
		b.add("static/vendor-BdK3j2Lp.js", []byte("console.log('outside')")),
		page,
	}
	h.src.set(edge.ServedSite{ID: "site-app", Name: "app", ActiveDeployID: "dep-app-1", SPA: true, ImmutableAssets: on, ActivatedAt: activated}, files)
	return h, files
}

func TestImmutableAssets(t *testing.T) {
	h, _ := withAssets(t, nil, true)
	for _, tc := range []struct {
		name, method, target string
		header               http.Header
		status               int
		body                 string
		headers              map[string]string
	}{
		{name: "vite name", target: "/assets/index-BdK3j2Lp.js", status: 200, body: "vite", headers: map[string]string{"Cache-Control": immutable, "Vary": "Accept-Encoding"}},
		{name: "precompressed sibling", target: "/assets/index-BdK3j2Lp.js", header: http.Header{"Accept-Encoding": {"br"}}, status: 200, body: "brotli", headers: map[string]string{"Cache-Control": immutable, "Content-Encoding": "br"}},
		{name: "webpack name", target: "/assets/main.3f2a9c1b.css", status: 200, body: "teal", headers: map[string]string{"Cache-Control": immutable, "Vary": ""}},
		{name: "HEAD", method: http.MethodHead, target: "/assets/main.3f2a9c1b.css", status: 200, headers: map[string]string{"Cache-Control": immutable}},
		{name: "range", target: "/assets/main.3f2a9c1b.css", header: http.Header{"Range": {"bytes=0-3"}}, status: 206, body: "body", headers: map[string]string{"Cache-Control": immutable}},
		{name: "hashed HTML page", target: "/assets/page-AbCdEf12.html", status: 200, body: "Hashed page", headers: map[string]string{"Cache-Control": immutable}},
		{name: "unhashed asset", target: "/assets/app.js", status: 200, body: "unhashed", headers: map[string]string{"Cache-Control": revalidate}},
		{name: "outside assets", target: "/static/vendor-BdK3j2Lp.js", status: 200, body: "outside", headers: map[string]string{"Cache-Control": revalidate}},
		{name: "page", target: "/", status: 200, body: "App shell", headers: map[string]string{"Cache-Control": revalidate}},
		{name: "rendition", target: "/assets/page-AbCdEf12.html?format=md", status: 200, body: "# Hashed page", headers: map[string]string{"Cache-Control": revalidate, "Content-Type": "text/markdown; charset=utf-8"}},
		{name: "clean URL", target: "/assets/page-AbCdEf12", status: 200, body: "Hashed page", headers: map[string]string{"Cache-Control": revalidate}},
		{name: "SPA fallback", target: "/assets/chunk-ZyXwVu98", status: 200, body: "App shell", headers: map[string]string{"Cache-Control": revalidate}},
		{name: "missing hashed file gets 404.html", target: "/assets/gone-ZyXwVu98.js", status: 404, body: "Lost", headers: map[string]string{"Cache-Control": revalidate}},
		{name: "reserved path", target: "/.wardlock/health", status: 200, headers: map[string]string{"Cache-Control": "no-store"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			rec := h.do(method, app, tc.target, tc.header)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("%s %s = %d %q; want %d with %q", method, tc.target, rec.Code, rec.Body.String(), tc.status, tc.body)
			}
			for k, v := range tc.headers {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
		})
	}

	// A revalidation the browser should never need still answers 304 with
	// the same headers.
	etag := h.do(http.MethodGet, app, "/assets/main.3f2a9c1b.css", nil).Header().Get("ETag")
	rec := h.do(http.MethodGet, app, "/assets/main.3f2a9c1b.css", http.Header{"If-None-Match": {etag}})
	if rec.Code != http.StatusNotModified || rec.Header().Get("Cache-Control") != immutable {
		t.Errorf("conditional = %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

// TestImmutableAssetsOff: a deploy without the flag, and a rollback to one,
// keeps today's headers for every file.
func TestImmutableAssetsOff(t *testing.T) {
	h, files := withAssets(t, nil, false)
	get := func() string {
		t.Helper()
		return h.do(http.MethodGet, app, "/assets/index-BdK3j2Lp.js", nil).Header().Get("Cache-Control")
	}
	if got := get(); got != revalidate {
		t.Fatalf("flag off: Cache-Control = %q, want %q", got, revalidate)
	}

	// Activate a deploy with the flag, then roll back to one without it.
	h.src.set(edge.ServedSite{ID: "site-app", Name: "app", ActiveDeployID: "dep-app-2", ImmutableAssets: true, ActivatedAt: activated}, files)
	h.notifier.Notify("site-app")
	if got := get(); got != immutable {
		t.Fatalf("after activating a flagged deploy: Cache-Control = %q, want %q", got, immutable)
	}
	h.src.set(edge.ServedSite{ID: "site-app", Name: "app", ActiveDeployID: "dep-app-1", ActivatedAt: activated}, files)
	h.notifier.Notify("site-app")
	if got := get(); got != revalidate {
		t.Fatalf("after rollback: Cache-Control = %q, want %q", got, revalidate)
	}
}

// TestImmutableAssetsProtected: a protected hashed asset is cached
// privately, keeps the protected-site headers, and any response that sets
// or clears a cookie, or is a challenge, is still never stored.
func TestImmutableAssetsProtected(t *testing.T) {
	a := &fakeAccess{fn: func(r access.Request) (access.Decision, error) {
		switch {
		case r.Cookie == "good":
			return allow(true), nil
		case r.Cookie == "stale":
			d := allow(true)
			d.ClearCookie = true
			return d, nil
		case r.Token == token:
			d := allow(true)
			d.LinkPublicID, d.TokenAccepted = "abcdefghij", true
			d.Issue = &access.NewSession{SiteID: r.SiteID, Method: "link", ShareLinkID: "link-1", LinkTokenHash: "hash"}
			return d, nil
		}
		return deny(r), nil
	}}
	h, _ := withAssets(t, a, true)
	const hashed = "/assets/main.3f2a9c1b.css"
	protected := map[string]string{"X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer", "Cross-Origin-Resource-Policy": "same-origin", "Vary": "Cookie"}
	for _, tc := range []struct {
		name, target string
		header       http.Header
		status       int
		cache        string
		cookie       bool
	}{
		{name: "session", target: hashed, header: withCookie, status: 200, cache: "private, max-age=31536000, immutable"},
		{name: "bearer token without a cookie", target: hashed, header: http.Header{"Authorization": {"Bearer " + token}}, status: 200, cache: "private, max-age=31536000, immutable"},
		{name: "token in the URL without a cookie", target: hashed + "?t=" + token, status: 200, cache: "private, max-age=31536000, immutable"},
		{name: "token in a navigation sets a cookie", target: hashed + "?t=" + token, header: browser, status: 200, cache: "no-store", cookie: true},
		{name: "cleared cookie", target: hashed, header: http.Header{"Cookie": {access.SessionCookieName + "=stale"}}, status: 200, cache: "no-store", cookie: true},
		{name: "unhashed", target: "/assets/app.js", header: withCookie, status: 200, cache: "private, max-age=0, must-revalidate"},
		{name: "refused browser gets the login page", target: hashed, header: browser, status: 401, cache: "no-store"},
		{name: "refused client", target: hashed, status: 401, cache: "no-store"},
		{name: "login page", target: "/.wardlock/login?next=" + hashed, header: browser, cache: "no-store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(http.MethodGet, app, tc.target, tc.header)
			if tc.status != 0 && rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tc.status, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.cache {
				t.Errorf("Cache-Control = %q, want %q", got, tc.cache)
			}
			if got := len(rec.Header().Values("Set-Cookie")) > 0; got != tc.cookie {
				t.Errorf("Set-Cookie present = %v, want %v", got, tc.cookie)
			}
			for k, v := range protected {
				if got := rec.Header().Get(k); !strings.Contains(got, v) {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
		})
	}
}
