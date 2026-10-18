// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
)

const (
	pageMD  = "# Field notes\n\nSee [setup](https://notes.wardlock.localhost/setup).\n"
	pageTxt = "Field notes\n\nSee setup (https://notes.wardlock.localhost/setup).\n"
	lostMD  = "# Not here\n"
	notes   = "notes." + base
	token   = "wl_abcdefghij_fakesecretfakesecretfakesecretfakesecret0"
)

// withRenditions publishes "notes": an index page and a 404.html with
// renditions, a page without them (as deploys activated before A2.6 have)
// and a stylesheet.
func withRenditions(t *testing.T, a edge.Access) *harness {
	t.Helper()
	h := newHarness(t, a)
	b := h.fx.blobs
	rend := func(body string) edge.Rendition {
		f := b.add("", []byte(body))
		return edge.Rendition{SHA256: f.SHA256, Size: f.Size}
	}
	index := b.add("index.html", []byte("<h1>Field notes</h1><p>See <a href=\"/setup\">setup</a>.</p>"))
	index.Renditions = map[string]edge.Rendition{"md": rend(pageMD), "txt": rend(pageTxt)}
	lost := b.add("404.html", []byte("<h1>Not here</h1>"))
	lost.Renditions = map[string]edge.Rendition{"md": rend(lostMD), "txt": rend("Not here\n")}
	h.src.set(edge.ServedSite{ID: "site-notes", Name: "notes", ActiveDeployID: "dep-notes-1", ActivatedAt: activated}, []edge.ServedFile{
		index, lost,
		b.add("old.html", []byte("<p>Old page</p>")),
		b.add("site.css", []byte("body { color: teal }")),
	})
	return h
}

func TestFormatSelection(t *testing.T) {
	h := withRenditions(t, nil)
	mdTag := `"` + h.fx.blobs.add("", []byte(pageMD)).SHA256 + `"`
	const (
		public = "public, max-age=0, must-revalidate"
		links  = `</?format=md>; rel="alternate"; type="text/markdown", </?format=txt>; rel="alternate"; type="text/plain"`
	)
	for _, tc := range []struct {
		name, method, target string
		header               http.Header
		status               int
		body                 string
		headers              map[string]string
	}{
		{name: "markdown", target: "/?format=md", status: 200, body: pageMD, headers: map[string]string{
			"Content-Type": "text/markdown; charset=utf-8", "ETag": mdTag, "Cache-Control": public, "Link": "", "X-Content-Type-Options": "nosniff",
		}},
		{name: "plain text", target: "/?format=txt", status: 200, body: pageTxt, headers: map[string]string{"Content-Type": "text/plain; charset=utf-8", "Link": ""}},
		{name: "index by name", target: "/index.html?format=md", status: 200, body: pageMD},
		{name: "with a token and other parameters", target: "/?t=" + token + "&format=md&x=1", status: 200, body: pageMD},
		{name: "HTML links its renditions", target: "/", status: 200, body: "<h1>Field notes</h1>", headers: map[string]string{
			"Content-Type": "text/html; charset=utf-8", "Link": links,
		}},
		{name: "the links never carry the token", target: "/?t=" + token + "&x=1", status: 200, body: "<h1>Field notes</h1>", headers: map[string]string{"Link": links}},
		{name: "unknown format serves HTML", target: "/?format=pdf", status: 200, body: "<h1>Field notes</h1>", headers: map[string]string{"Content-Type": "text/html; charset=utf-8"}},
		{name: "only the first format counts", target: "/?format=pdf&format=md", status: 200, body: "<h1>Field notes</h1>"},
		{name: "never precompressed", target: "/?format=md", header: http.Header{"Accept-Encoding": {"br, gzip"}}, status: 200, body: pageMD, headers: map[string]string{"Content-Encoding": "", "Vary": ""}},
		{name: "missing path serves 404.html's rendition", target: "/missing?format=md", status: 404, body: lostMD, headers: map[string]string{"Content-Type": "text/markdown; charset=utf-8", "ETag": ""}},
		{name: "404.html links nothing", target: "/missing", status: 404, body: "Not here", headers: map[string]string{"Link": ""}},
		{name: "page without renditions ignores format", target: "/old?format=md", status: 200, body: "<p>Old page</p>", headers: map[string]string{"Content-Type": "text/html; charset=utf-8", "Link": ""}},
		{name: "non-HTML ignores format", target: "/site.css?format=txt", status: 200, body: "teal", headers: map[string]string{"Content-Type": "text/css; charset=utf-8"}},
		{name: "HEAD", method: http.MethodHead, target: "/?format=md", status: 200, body: "", headers: map[string]string{"Content-Length": strconv.Itoa(len(pageMD))}},
		{name: "conditional", target: "/?format=md", header: http.Header{"If-None-Match": {mdTag}}, status: 304, body: ""},
		{name: "range", target: "/?format=md", header: http.Header{"Range": {"bytes=0-6"}}, status: 206, body: "# Field"},
		{name: "rendition of a clean URL", target: "/index?format=md", status: 200, body: pageMD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			rec := h.do(method, notes, tc.target, tc.header)
			if rec.Code != tc.status || (tc.body == "" && rec.Body.Len() > 0) || !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("%d %q, want %d with %q", rec.Code, rec.Body.String(), tc.status, tc.body)
			}
			if strings.Contains(rec.Body.String(), token) || strings.Contains(strings.Join(rec.Header().Values("Link"), ","), token) {
				t.Fatalf("the token leaked: %v %q", rec.Header(), rec.Body.String())
			}
			for k, want := range tc.headers {
				if got := strings.Join(rec.Header().Values(k), ", "); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// TestProtectedRenditions: a rendition is decided exactly like its page.
// Without access there is nothing of it, only the 401; with access it has
// the protected-site headers.
func TestProtectedRenditions(t *testing.T) {
	a := &fakeAccess{fn: func(r access.Request) (access.Decision, error) {
		if r.Cookie == "good" {
			return allow(true), nil
		}
		return deny(r), nil
	}}
	h := withRenditions(t, a)
	for _, target := range []string{"/?format=md", "/?format=txt", "/missing?format=md", "/?t=" + token + "&format=md"} {
		for _, header := range []http.Header{nil, browser} {
			rec := h.do(http.MethodGet, notes, target, header)
			body := rec.Body.String()
			if rec.Code != 401 || strings.Contains(body, "Field notes") || strings.Contains(body, "Not here") || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s without access = %d %v %q", target, rec.Code, rec.Header(), body)
			}
		}
	}
	for _, r := range a.requests() {
		if r.Path != "/" && r.Path != "/missing" {
			t.Fatalf("decided %q: the format must not change the path decided", r.Path)
		}
	}
	rec := h.do(http.MethodGet, notes, "/?format=md", withCookie)
	if rec.Code != 200 || rec.Body.String() != pageMD {
		t.Fatalf("with access = %d %q", rec.Code, rec.Body.String())
	}
	for k, want := range map[string]string{
		"Cache-Control": "private, max-age=0, must-revalidate", "X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer", "Vary": "Cookie",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
