// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// fakeToken has the share-link format and grants nothing.
const fakeToken = "wl_abcdefghij_fakesecretfakesecretfakesecretfakesecret0fa"

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

// TestRenditionsWithShareLinks: a share-link token opens a protected
// page's renditions for clients that keep no cookies, with ?t= or
// Authorization: Bearer, sets no session for them, never injects the
// strip script and never echoes the token. Rotated and revoked tokens get
// nothing.
func TestRenditionsWithShareLinks(t *testing.T) {
	p := newPolicyEdge(t)
	p.publishFiles("docs", renditionFiles)
	p.set("docs", "/", store.Protected, true)
	l, token := p.link("docs", access.NewLink{Label: "AI fetch"})
	want := map[string]struct{ body, ctype string }{
		"md":  {"# Field notes\n\nRead the [setup](/setup.html?v=2).\n", "text/markdown; charset=utf-8"},
		"txt": {"Field notes\n\nRead the setup (/setup.html?v=2).\n", "text/plain; charset=utf-8"},
	}
	secret := token[len("wl_0123456789_"):]
	inURL := func(format, tok string) string { return "/?format=" + format + "&t=" + url.QueryEscape(tok) }
	clean := func(t *testing.T, name string, r result) {
		t.Helper()
		for _, s := range append(r.h.Values("Link"), r.body, r.h.Get("Location")) {
			if strings.Contains(s, secret) {
				t.Fatalf("%s: the token leaked: %q", name, s)
			}
		}
		for _, l := range r.h.Values("Link") {
			if strings.Contains(l, "?t=") || strings.Contains(l, "&t=") {
				t.Fatalf("%s: Link carries t: %q", name, l)
			}
		}
	}
	for format, w := range want {
		for name, v := range map[string]visit{
			"?t= without Accept (curl)":   {target: inURL(format, token)},
			"?t= asking for its type":     {target: inURL(format, token), header: http.Header{"Accept": {w.ctype}}},
			"Bearer without Accept":       {target: "/?format=" + format, header: bearer(token)},
			"Bearer from fetch()":         {target: "/?format=" + format, header: http.Header{"Authorization": {"Bearer " + token}, "Sec-Fetch-Mode": {"cors"}}},
			"?t= on a clean URL fallback": {target: "/index?format=" + format + "&t=" + url.QueryEscape(token)},
		} {
			r := p.visit(v)
			if r.code != 200 || r.body != w.body || r.h.Get("Content-Type") != w.ctype {
				t.Fatalf("%s %s = %d %q %v", format, name, r.code, r.body, r.h)
			}
			if c := r.h.Values("Set-Cookie"); len(c) != 0 {
				t.Fatalf("%s %s set a cookie: %v", format, name, c)
			}
			if strings.Contains(r.body, stripMarker) || r.h.Get("Link") != "" {
				t.Fatalf("%s %s: rendition altered or linked: %q %v", format, name, r.body, r.h)
			}
			r.protected(t, "private, max-age=0, must-revalidate")
			clean(t, format+" "+name, r)
		}
		// A browser navigation earns its session as for the page, but the
		// rendition still gets no strip script.
		r := p.visit(visit{target: inURL(format, token), header: http.Header{"Accept": {"text/html"}, "Sec-Fetch-Mode": {"navigate"}}})
		if r.code != 200 || r.body != w.body || strings.Contains(r.body, stripMarker) {
			t.Fatalf("%s navigation = %d %q", format, r.code, r.body)
		}
		clean(t, format+" navigation", r)
	}

	// The HTML a token opens links its renditions without the token.
	r := p.visit(visit{target: "/?t=" + url.QueryEscape(token)})
	r.expect(t, 200, "Field notes", "")
	if links := strings.Join(r.h.Values("Link"), ", "); links != `</?format=md>; rel="alternate"; type="text/markdown", </?format=txt>; rel="alternate"; type="text/plain"` {
		t.Fatalf("Link = %q", links)
	}
	clean(t, "HTML", r)

	denied := func(t *testing.T, tok string) {
		t.Helper()
		for _, format := range []string{"md", "txt"} {
			for _, v := range []visit{{target: inURL(format, tok)}, {target: "/?format=" + format, header: bearer(tok)}} {
				r := p.visit(v)
				if r.code != 401 || strings.Contains(r.body, "Field notes") || len(r.h.Values("Set-Cookie")) != 0 {
					t.Fatalf("%s with a dead token = %d %q %v", v.target, r.code, r.body, r.h)
				}
				clean(t, "denied", r)
			}
		}
	}
	// Rotation: the old token dies at once, the new one works.
	_, rotated, err := p.acc.RotateLink(t.Context(), p.owner.ID, "docs", l.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	p.tokens = append(p.tokens, rotated)
	denied(t, token)
	if r := p.visit(visit{target: inURL("md", rotated)}); r.code != 200 || r.body != want["md"].body {
		t.Fatalf("rotated token = %d %q", r.code, r.body)
	}
	// Revocation: nothing works any more.
	if err := p.acc.RevokeLink(t.Context(), p.owner.ID, "docs", l.PublicID); err != nil {
		t.Fatal(err)
	}
	denied(t, rotated)
	p.noRawTokens(t, p.accessRecords())
}

// TestTokenPatternMatchesShareLinks pins the renditions' token pattern to
// the tokens A2.2's generator actually issues (deploys does not import
// access): every freshly minted or rotated token is redacted wherever a
// page carries it, keeping its public ID.
func TestTokenPatternMatchesShareLinks(t *testing.T) {
	p := newPolicyEdge(t)
	p.publishFiles("docs", renditionFiles)
	u, err := deploys.PageURL("https://docs.wardlock.localhost", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 64 {
		l, token := p.link("docs", access.NewLink{Label: "pin"})
		if i%2 == 1 {
			if _, token, err = p.acc.RotateLink(t.Context(), p.owner.ID, "docs", l.PublicID); err != nil {
				t.Fatal(err)
			}
		}
		page := `<title>` + token + `</title><p>Paste ` + token + ` or open <a href="https://elsewhere.test/?next=` + url.QueryEscape("/?t="+token) + `">this</a>.</p><pre>` + token + `</pre>`
		md, txt := deploys.Render([]byte(page), u)
		secret := token[len("wl_0123456789_"):]
		for _, out := range []string{string(md), string(txt)} {
			if strings.Contains(out, secret) || !strings.Contains(strings.ReplaceAll(out, `\_`, "_"), "wl_"+l.PublicID+"_redacted") {
				t.Fatalf("token %s not redacted: %q", l.PublicID, out)
			}
		}
	}
}
