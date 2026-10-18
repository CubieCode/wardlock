// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// fakeToken has the share-link format and grants nothing.
const fakeToken = "wl_abcdefghij_fakesecretfakesecretfakesecretfakesecret0"

var renditionFiles = map[string]string{
	"index.html": `<!doctype html><title>Notes</title><h1>Field notes</h1>` +
		`<p>Read the <a href="setup.html?t=` + fakeToken + `&amp;v=2">setup</a>.</p><script>secret()</script>`,
	"setup.html": `<h1>Setup</h1><pre><code class="language-sh">./install</code></pre>`,
	"site.css":   "body { color: teal }",
	"share.html": `<p>Open <a href="/?t=` + fakeToken + `">/?t=` + fakeToken + `</a> or ` +
		`<a href="/login?next=%2F%3Ft%3D` + fakeToken + `">sign in</a>; the token is <code>` + fakeToken + `</code>.</p>`,
}

// publishFiles creates a site and activates a deploy of files on it, as the
// CLI does.
func (p *policyEdge) publishFiles(name string, files map[string]string) {
	t, ctx := p.t, p.t.Context()
	t.Helper()
	if err := p.db.Sites().Create(ctx, store.Site{ID: store.NewID(), OwnerID: p.owner.ID, Name: name, CreatedAt: p.now()}); err != nil {
		t.Fatal(err)
	}
	var m deploys.Manifest
	for path, body := range files {
		sum := sha256.Sum256([]byte(body))
		m.Files = append(m.Files, store.DeployFile{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))})
	}
	d, _, err := p.deploys.Create(ctx, p.owner.ID, name, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if err := p.deploys.Upload(ctx, p.owner.ID, name, d.ID, f.SHA256, strings.NewReader(files[f.Path])); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := p.deploys.Activate(ctx, p.owner.ID, name, d.ID); err != nil {
		t.Fatal(err)
	}
}

// TestRenditionsThroughTheEdge checks renditions end to end: generated at
// activation, served by ?format= with their content types, linked from
// HTML, free of tokens and scripts, and decided by the same policy as the
// page.
func TestRenditionsThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	p.publishFiles("docs", renditionFiles)
	const md = "# Field notes\n\nRead the [setup](/setup.html?v=2).\n"

	r := p.do(http.MethodGet, host, "/?format=md", "", nil)
	r.expect(t, 200, md, "secret")
	if r.body != md || r.h.Get("Content-Type") != "text/markdown; charset=utf-8" || r.h.Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatalf("markdown = %q %v", r.body, r.h)
	}
	r = p.do(http.MethodGet, host, "/?format=txt", "", nil)
	if r.code != 200 || r.body != "Field notes\n\nRead the setup (/setup.html?v=2).\n" || r.h.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("text = %d %q %v", r.code, r.body, r.h)
	}
	p.do(http.MethodGet, host, "/setup?format=md", "", nil).expect(t, 200, "```sh\n./install\n```", "<pre>")
	p.do(http.MethodGet, host, "/site.css?format=md", "", nil).expect(t, 200, "teal", "")

	// A request carrying a token gets a rendition without it, and the
	// HTML's alternate links never carry it either.
	p.do(http.MethodGet, host, "/?t="+fakeToken+"&format=md", "", nil).expect(t, 200, md, fakeToken)
	r = p.do(http.MethodGet, host, "/?t="+fakeToken, "", html)
	r.expect(t, 200, "Field notes", "")
	if links := strings.Join(r.h.Values("Link"), ", "); links != `</?format=md>; rel="alternate"; type="text/markdown", </?format=txt>; rel="alternate"; type="text/plain"` {
		t.Fatalf("Link = %q", links)
	}

	// Tokens a page itself contains, in link text, nested parameters or
	// code, never reach its renditions.
	r = p.do(http.MethodGet, host, "/share?format=md", "", nil)
	r.expect(t, 200, "Open [/](/)", "fakesecret")
	if want := "Open [/](/) or [sign in](/login); the token is `wl_abcdefghij_redacted`.\n"; r.body != want {
		t.Fatalf("markdown = %q, want %q", r.body, want)
	}
	r = p.do(http.MethodGet, host, "/share?format=txt", "", nil)
	if want := "Open / or sign in (/login); the token is wl_abcdefghij_redacted.\n"; r.code != 200 || r.body != want {
		t.Fatalf("text = %d %q, want %q", r.code, r.body, want)
	}

	// Protected: no rendition without access, for any client.
	pol := p.set("docs", "/", store.Protected, true)
	for _, target := range []string{"/?format=md", "/?format=txt", "/setup?format=md", "/missing?format=md", "/?t=" + fakeToken + "&format=md"} {
		for _, header := range []http.Header{nil, html} {
			r := p.do(http.MethodGet, host, target, "", header)
			if r.code != 401 || strings.Contains(r.body, "Field notes") || strings.Contains(r.body, "Setup") {
				t.Fatalf("%s without access = %d %q", target, r.code, r.body)
			}
			r.protected(t, "no-store")
		}
	}
	// With a session the rendition comes with the protected headers.
	cookie := p.issue(store.Site{ID: siteID(t, p, "docs")}, pol)
	r = p.do(http.MethodGet, host, "/?format=md", cookie, nil)
	r.expect(t, 200, md, "")
	r.protected(t, "private, max-age=0, must-revalidate")
}

func siteID(t *testing.T, p *policyEdge, name string) string {
	t.Helper()
	s, err := p.db.Sites().GetByName(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}
