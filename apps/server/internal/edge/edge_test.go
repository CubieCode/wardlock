// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
)

const base = "wardlock.localhost"

var activated = time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)

// fixtures holds fixtures/sites as manifests and blobs.
type fixtures struct {
	files map[string][]edge.ServedFile // by fixture site name
	blobs *fakeBlobs
}

func loadFixtures(t testing.TB, names ...string) fixtures {
	t.Helper()
	fx := fixtures{files: map[string][]edge.ServedFile{}, blobs: &fakeBlobs{m: map[string][]byte{}}}
	for _, name := range names {
		root := filepath.Join("..", "..", "..", "..", "fixtures", "sites", name)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			fx.files[name] = append(fx.files[name], fx.blobs.add(filepath.ToSlash(rel), body))
			return nil
		})
		if err != nil {
			t.Fatalf("load fixture %s: %v", name, err)
		}
	}
	return fx
}

func (fx fixtures) file(t testing.TB, site, p string) edge.ServedFile {
	t.Helper()
	for _, f := range fx.files[site] {
		if f.Path == p {
			return f
		}
	}
	t.Fatalf("fixture %s has no %s", site, p)
	return edge.ServedFile{}
}

type fakeBlobs struct {
	mu   sync.Mutex
	m    map[string][]byte
	fail bool
}

func (b *fakeBlobs) add(p string, body []byte) edge.ServedFile {
	sum := sha256.Sum256(body)
	h := hex.EncodeToString(sum[:])
	b.mu.Lock()
	b.m[h] = body
	b.mu.Unlock()
	return edge.ServedFile{Path: p, SHA256: h, Size: int64(len(body))}
}

func (b *fakeBlobs) Open(_ context.Context, sha string) (io.ReadSeekCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	body, ok := b.m[sha]
	if !ok || b.fail {
		return nil, errors.New("blob not found")
	}
	return nopCloser{bytes.NewReader(body)}, nil
}

type nopCloser struct{ io.ReadSeeker }

func (nopCloser) Close() error { return nil }

// fakeSource resolves hosts the way the cmd/wardlockd adapter does: a label
// under the base domain by site name, anything else as a custom domain.
type fakeSource struct {
	mu      sync.Mutex
	sites   map[string]edge.ServedSite   // by name
	domains map[string]string            // active custom domain -> site name
	files   map[string][]edge.ServedFile // by deploy ID
	calls   int
	err     error
	during  func() // runs inside SiteForHost, to race an invalidation
}

func (s *fakeSource) SiteForHost(_ context.Context, host string) (edge.ServedSite, error) {
	s.mu.Lock()
	during := s.during
	s.calls++
	name, ok := edge.SiteLabel(host, base)
	if !ok {
		name, ok = s.domains[host]
	}
	site, found := s.sites[name]
	err := s.err
	s.mu.Unlock()
	if during != nil {
		during()
	}
	if err != nil {
		return edge.ServedSite{}, err
	}
	if !ok || !found {
		return edge.ServedSite{}, edge.ErrNoSite
	}
	return site, nil
}

func (s *fakeSource) ActiveFiles(_ context.Context, site edge.ServedSite) ([]edge.ServedFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files[site.ActiveDeployID], nil
}

func (s *fakeSource) set(site edge.ServedSite, files []edge.ServedFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sites[site.Name] = site
	s.files[site.ActiveDeployID] = files
}

func (s *fakeSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type fakeNotifier struct {
	mu   sync.Mutex
	next int
	fns  map[int]func(string)
}

func (n *fakeNotifier) Subscribe(fn func(string)) func() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fns == nil {
		n.fns = map[int]func(string){}
	}
	id := n.next
	n.next++
	n.fns[id] = fn
	return func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		delete(n.fns, id)
	}
}

func (n *fakeNotifier) Notify(siteID string) {
	n.mu.Lock()
	fns := make([]func(string), 0, len(n.fns))
	for _, fn := range n.fns {
		fns = append(fns, fn)
	}
	n.mu.Unlock()
	for _, fn := range fns {
		fn(siteID)
	}
}

// publicAccess lets everything in, as a public default policy does.
type publicAccess struct{}

func (publicAccess) Decide(context.Context, access.Request) (access.Decision, error) {
	return access.Decision{Outcome: access.Allow, Via: access.ViaPublic}, nil
}

func (publicAccess) EndSession(context.Context, string, string) error { return nil }

func (publicAccess) IssueSession(context.Context, access.NewSession) (access.Cookie, error) {
	return access.Cookie{}, errors.New("public sites issue no sessions")
}

func (publicAccess) View(context.Context, string, string) (access.Viewed, error) {
	return access.Viewed{}, access.ErrTicket
}

func (publicAccess) PasswordLogin(context.Context, access.PasswordAttempt) (access.Cookie, error) {
	return access.Cookie{}, access.ErrWrongPassword
}

func (publicAccess) SendEmailCode(context.Context, access.EmailCodeRequest) error { return nil }

func (publicAccess) EmailCodeLogin(context.Context, access.EmailCodeAttempt) (access.Cookie, error) {
	return access.Cookie{}, access.ErrInvalidCode
}

type harness struct {
	edge     *edge.Edge
	fx       fixtures
	src      *fakeSource
	notifier *fakeNotifier
}

// newHarness serves:
//   - basic (fixtures/sites/basic) at basic.<base> and docs.example.test, with
//     smuggled .wardlock/ files and a scheme-like directory in its manifest;
//   - spa (fixtures/sites/spa, SPA deploy) and plain (the same files without
//     SPA, so no 404.html);
//   - empty, a site with nothing published.
func newHarness(t testing.TB, access edge.Access) *harness {
	t.Helper()
	fx := loadFixtures(t, "basic", "spa")
	basic := append(fx.files["basic"],
		fx.blobs.add(".wardlock/health", []byte("deployed health")),
		fx.blobs.add(".wardlock/index.html", []byte("deployed reserved page")),
		fx.blobs.add("docs/javascript:x/index.html", []byte("<p>Odd directory</p>")),
		fx.blobs.add("docs/odd dir/index.html", []byte("<p>Spaced directory</p>")),
		fx.blobs.add(".WARDLOCK/login", []byte("deployed upper-case reserved page")),
		fx.blobs.add(".Wardlock", []byte("deployed reserved file")),
		fx.blobs.add("docs/notes", []byte("plain notes without an extension")),
	)
	src := &fakeSource{
		sites:   map[string]edge.ServedSite{},
		domains: map[string]string{"docs.example.test": "basic"},
		files:   map[string][]edge.ServedFile{},
	}
	src.set(edge.ServedSite{ID: "site-basic", Name: "basic", ActiveDeployID: "dep-basic-1", ActivatedAt: activated}, basic)
	src.set(edge.ServedSite{ID: "site-spa", Name: "spa", ActiveDeployID: "dep-spa-1", SPA: true, ActivatedAt: activated}, fx.files["spa"])
	src.set(edge.ServedSite{ID: "site-plain", Name: "plain", ActiveDeployID: "dep-plain-1", ActivatedAt: activated}, fx.files["spa"])
	src.set(edge.ServedSite{ID: "site-empty", Name: "empty"}, nil)
	n := &fakeNotifier{}
	if access == nil {
		access = publicAccess{}
	}
	e, err := edge.New(edge.Config{
		DashboardHost: base,
		Dashboard:     text("dashboard"),
		Health:        text("healthy"),
		Sites:         src,
		Blobs:         fx.blobs,
		Invalidator:   n,
		Access:        access,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return &harness{edge: e, fx: fx, src: src, notifier: n}
}

func (h *harness) do(method, host, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://placeholder"+target, nil)
	req.Host = host
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.edge.ServeHTTP(rec, req)
	return rec
}

func TestServing(t *testing.T) {
	h := newHarness(t, nil)
	etag := func(site, p string) string { return `"` + h.fx.file(t, site, p).SHA256 + `"` }
	size := func(site, p string) string { return strconv.FormatInt(h.fx.file(t, site, p).Size, 10) }
	const (
		basic = "basic." + base
		spa   = "spa." + base
		plain = "plain." + base
		html  = "text/html; charset=utf-8"
		js    = "text/javascript; charset=utf-8"
		css   = "text/css; charset=utf-8"
		cache = "public, max-age=0, must-revalidate"
	)
	report := "Harbour Lights quarterly report"
	about := "About this report"
	notFound := "Page not found"
	tide := "Tide tables for an imaginary bay"

	tests := []struct {
		name         string
		method       string
		host, target string
		header       http.Header
		status       int
		body         string // substring; "" checks nothing
		exactBody    []byte
		headers      map[string]string // "" means absent
	}{
		// Host routing.
		{name: "dashboard", host: base + ":8080", target: "/", status: 200, body: "dashboard"},
		{name: "dashboard host is case-insensitive", host: "WardLock.Localhost.", target: "/x", status: 200, body: "dashboard"},
		{name: "healthz on dashboard", host: base, target: "/healthz", status: 200, body: "healthy"},
		{name: "healthz on a site", host: basic, target: "/healthz", status: 200, body: "healthy"},
		{name: "site under base domain", host: basic + ":8080", target: "/", status: 200, body: report},
		{name: "site host is case-insensitive", host: "BASIC.Wardlock.Localhost.", target: "/", status: 200, body: report},
		{name: "active custom domain", host: "docs.example.test", target: "/about/", status: 200, body: about},
		{name: "custom domain with port and case", host: "Docs.Example.Test.:443", target: "/", status: 200, body: report},
		{name: "unknown site", host: "nope." + base, target: "/", status: 404, body: "No site"},
		{name: "nested subdomain", host: "a." + basic, target: "/", status: 404, body: "No site"},
		{name: "foreign host", host: "example.org", target: "/", status: 404, body: "No site"},
		{name: "site with nothing published", host: "empty." + base, target: "/", status: 404, body: "No site"},

		// Index files and clean URLs.
		{name: "root serves index.html", host: basic, target: "/", status: 200, body: report, headers: map[string]string{
			"Content-Type": html, "ETag": etag("basic", "index.html"), "Last-Modified": "Wed, 01 Jul 2026 09:30:00 GMT",
			"Cache-Control": cache, "X-Content-Type-Options": "nosniff", "Accept-Ranges": "bytes", "Vary": "",
		}},
		{name: "index.html as is", host: basic, target: "/index.html", status: 200, body: report},
		{name: "directory with slash serves its index.html", host: basic, target: "/about/", status: 200, body: about, headers: map[string]string{"ETag": etag("basic", "about/index.html")}},
		{name: "directory without slash redirects", host: basic, target: "/about", status: 301, headers: map[string]string{"Location": "/about/", "Cache-Control": cache}},
		{name: "redirect keeps the query", host: basic, target: "/about?ref=a%20b", status: 301, headers: map[string]string{"Location": "/about/?ref=a%20b"}},
		{name: "redirect is an absolute path", host: basic, target: "/docs/javascript:x", status: 301, headers: map[string]string{"Location": "/docs/javascript:x/"}},
		{name: "redirect from a dot segment", host: basic, target: "/about/.", status: 301, headers: map[string]string{"Location": "/about/"}},
		{name: "redirect from a doubled slash stays on the host", host: basic, target: "//about", status: 301, headers: map[string]string{"Location": "/about/"}},
		{name: "redirect escapes segments", host: basic, target: "/docs/odd%20dir", status: 301, headers: map[string]string{"Location": "/docs/odd%20dir/"}},
		{name: "clean URL serves page.html", host: basic, target: "/guide", status: 200, body: "Reading guide", headers: map[string]string{"ETag": etag("basic", "guide.html")}},
		{name: "page.html as is", host: basic, target: "/guide.html", status: 200, body: "Reading guide"},
		{name: "nested clean URL", host: basic, target: "/about/index", status: 200, body: about},
		{name: "dot segments are resolved", host: basic, target: "/assets/../guide", status: 200, body: "Reading guide"},
		{name: "unknown type is not sniffed", host: basic, target: "/docs/notes", status: 200, body: "plain notes", headers: map[string]string{"Content-Type": "application/octet-stream"}},
		{name: "asset", host: basic, target: "/assets/site.css", status: 200, body: "font-family", headers: map[string]string{"Content-Type": css, "Cache-Control": cache}},

		// 404.html and the plain 404.
		{name: "missing path serves 404.html", host: basic, target: "/missing", status: 404, body: notFound, headers: map[string]string{"Content-Type": html, "Cache-Control": cache, "ETag": ""}},
		{name: "missing file with extension serves 404.html", host: basic, target: "/missing.png", status: 404, body: notFound},
		{name: "missing directory serves 404.html", host: basic, target: "/nope/", status: 404, body: notFound},
		{name: "HEAD on 404.html has no body", method: http.MethodHead, host: basic, target: "/missing", status: 404, exactBody: []byte{}},
		{name: "plain 404 without 404.html", host: plain, target: "/missing", status: 404, body: "404 page not found"},
		{name: "no SPA fallback unless the deploy asks", host: plain, target: "/settings/profile", status: 404, body: "404 page not found"},

		// SPA fallback.
		{name: "SPA serves index.html for an extensionless path", host: spa, target: "/settings/profile", status: 200, body: tide, headers: map[string]string{"Content-Type": html, "ETag": etag("spa", "index.html")}},
		{name: "SPA serves index.html for a trailing slash", host: spa, target: "/settings/", status: 200, body: tide},
		{name: "SPA does not catch paths with an extension", host: spa, target: "/missing.js", status: 404, body: "404 page not found"},
		{name: "SPA still serves real files", host: spa, target: "/assets/app.js", status: 200, body: "Synthetic single-page app", headers: map[string]string{"Content-Type": js}},

		// Methods.
		{name: "HEAD has headers and no body", method: http.MethodHead, host: basic, target: "/guide", status: 200, exactBody: []byte{}, headers: map[string]string{
			"Content-Length": size("basic", "guide.html"), "ETag": etag("basic", "guide.html"),
		}},
		{name: "POST is not allowed", method: http.MethodPost, host: basic, target: "/", status: 405, headers: map[string]string{"Allow": "GET, HEAD"}},

		// Precompressed siblings.
		{name: "br preferred", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"gzip, deflate, br"}}, status: 200,
			exactBody: h.blob(t, "basic", "assets/app.js.br"), headers: map[string]string{
				"Content-Encoding": "br", "Content-Type": js, "Vary": "Accept-Encoding", "ETag": etag("basic", "assets/app.js.br"), "Content-Length": size("basic", "assets/app.js.br"),
			}},
		{name: "gzip when br is not accepted", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"gzip"}}, status: 200,
			exactBody: h.blob(t, "basic", "assets/app.js.gz"), headers: map[string]string{
				"Content-Encoding": "gzip", "Content-Type": js, "Vary": "Accept-Encoding", "ETag": etag("basic", "assets/app.js.gz"),
			}},
		{name: "br;q=0 is refused", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"br;q=0, gzip;q=0.5"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "gzip"}},
		{name: "wildcard accepts br", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"*"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "br"}},
		{name: "refused wildcard leaves named codings", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"*;q=0, GZIP"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "gzip"}},
		{name: "higher gzip q beats br", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"br;q=0.5, gzip;q=0.8"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "gzip"}},
		{name: "br wins a q tie", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"gzip;q=0.5, br;q=0.5"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "br"}},
		{name: "unparseable q counts as 0", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"br;q=high, gzip;q=0.1"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "gzip"}},
		{name: "out-of-range q counts as 0", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"br;q=2, gzip;q=-1"}}, status: 200,
			headers: map[string]string{"Content-Encoding": ""}},
		{name: "explicit entry beats the wildcard", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"*;q=0.9, br;q=0.1"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "gzip"}},
		{name: "identity without Accept-Encoding", host: basic, target: "/assets/app.js", status: 200,
			exactBody: h.blob(t, "basic", "assets/app.js"), headers: map[string]string{
				"Content-Encoding": "", "Vary": "Accept-Encoding", "ETag": etag("basic", "assets/app.js"),
			}},
		{name: "identity when every coding is refused", host: basic, target: "/assets/app.js", header: http.Header{"Accept-Encoding": {"br;q=0, gzip;q=0"}}, status: 200,
			headers: map[string]string{"Content-Encoding": ""}},
		{name: "no Vary without siblings", host: basic, target: "/assets/site.css", header: http.Header{"Accept-Encoding": {"gzip, br"}}, status: 200,
			headers: map[string]string{"Content-Encoding": "", "Vary": ""}},
		{name: "a sibling requested directly is served as is", host: basic, target: "/assets/app.js.gz", header: http.Header{"Accept-Encoding": {"gzip"}}, status: 200,
			exactBody: h.blob(t, "basic", "assets/app.js.gz"), headers: map[string]string{"Content-Encoding": ""}},

		// Reserved path.
		{name: "reserved health", host: basic, target: "/.wardlock/health", status: 200, body: `{"status":"ok"}`, headers: map[string]string{"Cache-Control": "no-store", "Content-Type": "application/json"}},
		{name: "reserved health on a custom domain", host: "docs.example.test", target: "/.wardlock/health", status: 200, body: `{"status":"ok"}`},
		{name: "no health on a site with nothing published", host: "empty." + base, target: "/.wardlock/health", status: 404, body: "No site", headers: map[string]string{"Cache-Control": ""}},
		{name: "POST on a site with nothing published", method: http.MethodPost, host: "empty." + base, target: "/", status: 404, body: "No site", headers: map[string]string{"Allow": ""}},
		{name: "upper-case reserved path is reserved", host: basic, target: "/.WARDLOCK/health", status: 404, body: "404 page not found", headers: map[string]string{"Cache-Control": "no-store"}},
		{name: "mixed-case deployed reserved file is never served", host: basic, target: "/.WARDLOCK/login", status: 404, body: "404 page not found"},
		{name: "mixed-case reserved path is not SPA content", host: spa, target: "/.Wardlock/login", status: 404, body: "404 page not found", headers: map[string]string{"Cache-Control": "no-store"}},
		{name: "a deployed .Wardlock file is never served", host: basic, target: "/.Wardlock", status: 404, body: "404 page not found"},
		{name: "a lookalike prefix is not reserved", host: spa, target: "/.wardlocked/x", status: 200, body: tide},
		{name: "deployed reserved file is never served", host: basic, target: "/.wardlock/index.html", status: 404, headers: map[string]string{"Cache-Control": "no-store"}},
		{name: "reserved directory", host: basic, target: "/.wardlock/", status: 404},
		{name: "reserved without slash", host: basic, target: "/.wardlock", status: 404},
		{name: "dot segments cannot reach deployed reserved files", host: basic, target: "/assets/../.wardlock/index.html", status: 404},
		{name: "doubled slash still reserved", host: basic, target: "//.wardlock/health", status: 200, body: `{"status":"ok"}`},
		{name: "POST to reserved path is reserved", method: http.MethodPost, host: basic, target: "/.wardlock/health", status: 200, body: `{"status":"ok"}`},
		{name: "reserved files never become 404 pages", host: basic, target: "/.wardlock/nope", status: 404, body: "404 page not found"},

		// Public sites carry no protected-site headers.
		{name: "public site is indexable", host: basic, target: "/", status: 200, headers: map[string]string{"X-Robots-Tag": "", "Referrer-Policy": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			rec := h.do(method, tt.host, tt.target, tt.header)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.status, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.body) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tt.body)
			}
			if strings.Contains(rec.Body.String(), "deployed") {
				t.Errorf("served a deployed /.wardlock/ file: %q", rec.Body.String())
			}
			if tt.exactBody != nil && !bytes.Equal(rec.Body.Bytes(), tt.exactBody) {
				t.Errorf("body = %q, want %q", rec.Body.Bytes(), tt.exactBody)
			}
			for k, want := range tt.headers {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func (h *harness) blob(t testing.TB, site, p string) []byte {
	t.Helper()
	return h.fx.blobs.m[h.fx.file(t, site, p).SHA256]
}

func TestGzipSiblingDecodes(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.do(http.MethodGet, "basic."+base, "/assets/app.js", http.Header{"Accept-Encoding": {"gzip"}})
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if want := h.blob(t, "basic", "assets/app.js"); !bytes.Equal(got, want) {
		t.Fatalf("decoded %q, want %q", got, want)
	}
}

func TestConditionalAndRange(t *testing.T) {
	h := newHarness(t, nil)
	css := h.blob(t, "basic", "assets/site.css")
	etag := `"` + h.fx.file(t, "basic", "assets/site.css").SHA256 + `"`
	gzEtag := `"` + h.fx.file(t, "basic", "assets/app.js.gz").SHA256 + `"`
	lastMod := activated.Format(http.TimeFormat)
	earlier := activated.Add(-time.Hour).Format(http.TimeFormat)

	tests := []struct {
		name    string
		target  string
		header  http.Header
		status  int
		body    string
		headers map[string]string
	}{
		{"If-None-Match matches", "/assets/site.css", http.Header{"If-None-Match": {etag}}, 304, "", map[string]string{"ETag": etag}},
		{"If-None-Match in a list", "/assets/site.css", http.Header{"If-None-Match": {`"other", ` + etag}}, 304, "", nil},
		{"If-None-Match differs", "/assets/site.css", http.Header{"If-None-Match": {`"other"`}}, 200, string(css), nil},
		{"If-None-Match on a sibling uses its ETag", "/assets/app.js", http.Header{"If-None-Match": {gzEtag}, "Accept-Encoding": {"gzip"}}, 304, "", nil},
		{"If-None-Match of identity does not match the sibling", "/assets/app.js", http.Header{"If-None-Match": {etag}, "Accept-Encoding": {"gzip"}}, 200, "", map[string]string{"Content-Encoding": "gzip"}},
		{"If-Modified-Since at activation", "/assets/site.css", http.Header{"If-Modified-Since": {lastMod}}, 304, "", nil},
		{"If-Modified-Since before activation", "/assets/site.css", http.Header{"If-Modified-Since": {earlier}}, 200, string(css), nil},
		{"If-Match mismatch", "/assets/site.css", http.Header{"If-Match": {`"other"`}}, 412, "", nil},
		{"Range", "/assets/site.css", http.Header{"Range": {"bytes=0-3"}}, 206, "body", map[string]string{
			"Content-Range": "bytes 0-3/88", "Content-Length": "4", "ETag": etag,
		}},
		{"suffix Range", "/assets/site.css", http.Header{"Range": {"bytes=-2"}}, 206, "}\n", nil},
		{"unsatisfiable Range", "/assets/site.css", http.Header{"Range": {"bytes=500-"}}, 416, "", nil},
		{"If-Range current serves the range", "/assets/site.css", http.Header{"Range": {"bytes=0-3"}, "If-Range": {etag}}, 206, "body", nil},
		{"If-Range stale serves everything", "/assets/site.css", http.Header{"Range": {"bytes=0-3"}, "If-Range": {`"old"`}}, 200, string(css), nil},
		{"Range over a sibling", "/assets/app.js", http.Header{"Range": {"bytes=0-1"}, "Accept-Encoding": {"gzip"}}, 206, "\x1f\x8b", map[string]string{
			"Content-Encoding": "gzip", "Content-Range": "bytes 0-1/104",
		}},
		{"multiple ranges of identity", "/assets/site.css", http.Header{"Range": {"bytes=0-1,4-5"}}, 206, "Content-Range: bytes 4-5/88", map[string]string{
			"Content-Encoding": "",
		}},
		{"one range of an encoded variant", "/assets/app.js", http.Header{"Range": {"bytes=2-3"}, "Accept-Encoding": {"br"}}, 206, "", map[string]string{
			"Content-Encoding": "br", "Content-Length": "2",
		}},
		{"multiple ranges of an encoded variant serve it whole", "/assets/app.js", http.Header{"Range": {"bytes=0-1,4-5"}, "Accept-Encoding": {"gzip"}}, 200, "\x1f\x8b", map[string]string{
			"Content-Encoding": "gzip", "Content-Range": "", "Content-Length": "104",
		}},
		{"404.html ignores Range", "/missing", http.Header{"Range": {"bytes=0-3"}}, 404, "Page not found", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := h.do(http.MethodGet, "basic."+base, tt.target, tt.header)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if tt.status == 304 && rec.Body.Len() != 0 {
				t.Errorf("304 has a body %q", rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.body) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tt.body)
			}
			for k, want := range tt.headers {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestCacheInvalidation(t *testing.T) {
	h := newHarness(t, nil)
	host := "basic." + base
	get := func(host, target string) string {
		t.Helper()
		return h.do(http.MethodGet, host, target, nil).Body.String()
	}

	if !strings.Contains(get(host, "/"), "Harbour Lights") || !strings.Contains(get("docs.example.test", "/"), "Harbour Lights") {
		t.Fatal("first deploy not served")
	}
	calls := h.src.callCount()
	for range 3 {
		get(host, "/guide")
	}
	if h.src.callCount() != calls {
		t.Fatalf("cached host looked up again: %d calls, want %d", h.src.callCount(), calls)
	}

	// Activate a second deploy: the source changes, the edge keeps the
	// cached deploy until notified.
	v2 := h.fx.blobs.add("index.html", []byte("<p>Second deploy</p>"))
	h.src.set(edge.ServedSite{ID: "site-basic", Name: "basic", ActiveDeployID: "dep-basic-2", ActivatedAt: activated.Add(time.Hour)}, []edge.ServedFile{v2})
	if !strings.Contains(get(host, "/"), "Harbour Lights") {
		t.Fatal("served the new deploy before the notification")
	}

	h.notifier.Notify("site-other")
	if !strings.Contains(get(host, "/"), "Harbour Lights") {
		t.Fatal("another site's notification dropped this site")
	}

	h.notifier.Notify("site-basic")
	for _, hh := range []string{host, "docs.example.test"} {
		rec := h.do(http.MethodGet, hh, "/", nil)
		if !strings.Contains(rec.Body.String(), "Second deploy") {
			t.Fatalf("%s after activation = %q", hh, rec.Body.String())
		}
		if lm := rec.Header().Get("Last-Modified"); lm != activated.Add(time.Hour).Format(http.TimeFormat) {
			t.Errorf("Last-Modified = %q", lm)
		}
	}
	if rec := h.do(http.MethodGet, host, "/guide", nil); rec.Code != 404 {
		t.Errorf("file from the old deploy still served: %d", rec.Code)
	}

	// Teardown: the site disappears.
	h.src.mu.Lock()
	delete(h.src.sites, "basic")
	h.src.mu.Unlock()
	h.notifier.Notify("site-basic")
	if body := get(host, "/"); !strings.Contains(body, "No site") {
		t.Fatalf("torn-down site still served: %q", body)
	}
}

func TestInvalidationDuringLoadIsNotCached(t *testing.T) {
	h := newHarness(t, nil)
	h.src.mu.Lock()
	h.src.during = func() { h.notifier.Notify("site-basic") }
	h.src.mu.Unlock()
	h.do(http.MethodGet, "basic."+base, "/", nil)

	h.src.mu.Lock()
	h.src.during = nil
	h.src.mu.Unlock()
	calls := h.src.callCount()
	h.do(http.MethodGet, "basic."+base, "/", nil)
	if h.src.callCount() != calls+1 {
		t.Fatal("a load raced by an invalidation was cached")
	}
}

func TestConcurrentServingAndInvalidation(t *testing.T) {
	h := newHarness(t, nil)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 200 {
				if rec := h.do(http.MethodGet, "basic."+base, "/guide", nil); rec.Code != 200 {
					t.Errorf("status = %d", rec.Code)
					return
				}
				if i == 0 {
					h.notifier.Notify("site-basic")
				}
			}
		})
	}
	wg.Wait()
}

func TestUnpublishedSiteLooksUnknown(t *testing.T) {
	h := newHarness(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodDelete} {
		for _, target := range []string{"/", "/.wardlock/health", "/index.html"} {
			unknown := h.do(method, "nope."+base, target, nil)
			empty := h.do(method, "empty."+base, target, nil)
			if empty.Code != unknown.Code || empty.Body.String() != unknown.Body.String() || !maps.EqualFunc(empty.Header(), unknown.Header(), slices.Equal) {
				t.Errorf("%s %s: unpublished = %d %v %q, unknown = %d %v %q", method, target,
					empty.Code, empty.Header(), empty.Body, unknown.Code, unknown.Header(), unknown.Body)
			}
		}
	}
}

func TestConcurrentColdLoadsShareOneLoad(t *testing.T) {
	h := newHarness(t, nil)
	release := make(chan struct{})
	h.src.mu.Lock()
	h.src.during = func() { <-release }
	h.src.mu.Unlock()

	const n = 16
	var started, done sync.WaitGroup
	started.Add(n)
	for range n {
		done.Go(func() {
			started.Done()
			if rec := h.do(http.MethodGet, "basic."+base, "/guide", nil); rec.Code != 200 {
				t.Errorf("status = %d", rec.Code)
			}
		})
	}
	started.Wait()
	close(release)
	done.Wait()
	if calls := h.src.callCount(); calls != 1 {
		t.Fatalf("%d concurrent cold requests made %d loads, want 1", n, calls)
	}
}

func TestCloseUnsubscribes(t *testing.T) {
	h := newHarness(t, nil)
	h.edge.Close()
	if len(h.notifier.fns) != 0 {
		t.Fatalf("%d subscriptions left after Close", len(h.notifier.fns))
	}
}

func TestFailures(t *testing.T) {
	t.Run("site source error", func(t *testing.T) {
		h := newHarness(t, nil)
		h.src.err = errors.New("database is locked")
		if rec := h.do(http.MethodGet, "basic."+base, "/", nil); rec.Code != 500 || strings.Contains(rec.Body.String(), "locked") {
			t.Fatalf("= %d %q", rec.Code, rec.Body.String())
		}
	})
	t.Run("missing blob", func(t *testing.T) {
		h := newHarness(t, nil)
		h.fx.blobs.fail = true
		if rec := h.do(http.MethodGet, "basic."+base, "/", nil); rec.Code != 500 {
			t.Fatalf("= %d", rec.Code)
		}
	})
}

func TestSiteLabel(t *testing.T) {
	tests := []struct {
		host, name string
		ok         bool
	}{
		{"docs." + base, "docs", true},
		{base, "", false},
		{"a.docs." + base, "", false},
		{"." + base, "", false},
		{"docs.example.test", "", false},
		{"docs" + base, "", false},
	}
	for _, tt := range tests {
		if name, ok := edge.SiteLabel(tt.host, base); name != tt.name || ok != tt.ok {
			t.Errorf("SiteLabel(%q) = %q, %v", tt.host, name, ok)
		}
	}
}

// BenchmarkCachedSmallFile serves an 88-byte stylesheet from a warm cache.
func BenchmarkCachedSmallFile(b *testing.B) {
	h := newHarness(b, nil)
	req := httptest.NewRequest(http.MethodGet, "/assets/site.css", nil)
	req.Host = "basic." + base
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.edge.ServeHTTP(rec, req)
		if rec.Code != 200 {
			b.Fatal(rec.Code)
		}
	}
}

func text(s string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, s) })
}
