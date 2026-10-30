// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/moderation"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// policyEdge is the edge wired to the access service and the store as serve
// wires them, over a throwaway SQLite database, with a clock the test moves.
type policyEdge struct {
	t   testing.TB
	db  *store.DB
	acc *access.Service
	e   *edge.Edge
	// h is e behind the request logger, as serve wires it, so log
	// assertions cover request lines too.
	h             http.Handler
	deploys       *deploys.Service
	owner         store.Owner
	logs          *bytes.Buffer
	log           *slog.Logger
	notifier      *deploys.LocalNotifier
	blobs         *deploys.FSBlobStore
	dashboardHits int
	// quarantine is what content screening withholds (A2.15).
	quarantine *moderation.Quarantine

	mu      sync.Mutex
	clock   time.Time
	links   int
	records []edge.AccessRecord
	// tokens are every share-link token minted, for log assertions.
	tokens []string
	// mailDir is where the dev:// mail sink writes visitor email codes;
	// mailLogs is the sink's own log, kept apart from logs.
	mailDir  string
	mailLogs *bytes.Buffer
	// siteHost is the host visit uses when none is given: host unless a
	// matrix runs on a preview (C1.19).
	siteHost string
}

// Record keeps the edge's access records for assertions.
func (p *policyEdge) Record(r edge.AccessRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = append(p.records, r)
}

// accessRecords returns and forgets the records so far.
func (p *policyEdge) accessRecords() []edge.AccessRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.records
	p.records = nil
	return out
}

func (p *policyEdge) now() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clock
}

func (p *policyEdge) advance(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock = p.clock.Add(d)
}

func newPolicyEdge(t testing.TB, opts ...func(*access.Config)) *policyEdge {
	t.Helper()
	db := storetest.New(t)
	p := &policyEdge{t: t, db: db, logs: &bytes.Buffer{}, clock: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), mailDir: t.TempDir(), mailLogs: &bytes.Buffer{}}
	blobs := deploys.NewFSBlobStore(t.TempDir())
	notifier := deploys.NewLocalNotifier()
	log := slog.New(slog.NewTextHandler(p.logs, nil))
	p.notifier, p.log, p.blobs = notifier, log, blobs
	p.quarantine = moderation.NewQuarantine()
	cfg := access.Config{
		Sites: db.Sites(), Policies: db.Policies(), Sessions: db.Sessions(), Links: db.Links(), PasswordSessions: db.PasswordSessions(), EmailCodes: db.EmailCodes(), Notifier: notifier,
		Mailer: mail.NewDevSink(mail.Config{DevDir: p.mailDir, Logger: slog.New(slog.NewTextHandler(p.mailLogs, nil)), Now: p.now}),
		Secret: []byte(testSecret), Now: p.now, Log: log,
	}
	for _, o := range opts {
		o(&cfg)
	}
	acc, err := access.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(acc.Close)
	p.acc = acc
	p.deploys = deploys.NewService(deploys.Config{
		Sites: db.Sites(), Deploys: db.Deploys(), Previews: db.Previews(), Blobs: blobs, Notifier: notifier,
		Limits: deploys.DefaultLimits(), Now: p.now, Renditions: db.Renditions(),
	})
	p.e = p.newEdge(siteSource{baseDomain: "wardlock.localhost", owners: db.Owners(), sites: db.Sites(), domains: db.Domains(), deploys: db.Deploys(), previews: db.Previews(), renditions: db.Renditions(), now: p.now}, notifier)
	t.Cleanup(p.e.Close)
	p.h = platform.LogRequests(log, p.e)
	p.owner = store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: p.now()}
	if err := db.Owners().Create(t.Context(), p.owner); err != nil {
		t.Fatal(err)
	}
	return p
}

// newEdge builds the edge over src, invalidated by inv.
func (p *policyEdge) newEdge(src siteSource, inv edge.Invalidator) *edge.Edge {
	p.t.Helper()
	e, err := edge.New(edge.Config{
		DashboardHost: "wardlock.localhost",
		AuthHost:      "auth.wardlock.localhost",
		Dashboard:     http.HandlerFunc(func(http.ResponseWriter, *http.Request) { p.dashboardHits++ }),
		// The report form (A2.13) lives on the dashboard host.
		DashboardOrigin: "http://wardlock.localhost",
		SiteOrigin:      func(host string) string { return "http://" + host },
		Health:          http.NotFoundHandler(),
		Sites:           src,
		Blobs:           p.blobs,
		Invalidator:     inv,
		Access:          p.acc,
		Recorder:        p,
		Withheld:        p.quarantine,
		Log:             p.log,
		Now:             p.now,
	})
	if err != nil {
		p.t.Fatal(err)
	}
	return e
}

// screening switches the edge to one with content screening on (A2.15)
// and returns the notifier the screening worker announces verdicts on.
func (p *policyEdge) screening() *deploys.LocalNotifier {
	p.t.Helper()
	p.e.Close()
	screened := deploys.NewLocalNotifier()
	db := p.db
	p.e = p.newEdge(siteSource{
		baseDomain: "wardlock.localhost", owners: db.Owners(), sites: db.Sites(), domains: db.Domains(), deploys: db.Deploys(),
		previews: db.Previews(), renditions: db.Renditions(), moderation: db.Moderation(), now: p.now,
	}, invalidators{p.notifier, screened})
	p.t.Cleanup(p.e.Close)
	p.h = platform.LogRequests(p.log, p.e)
	return screened
}

var policyFiles = map[string]string{
	"index.html":          "<p>Home page</p>",
	"404.html":            "<p>Custom not found</p>",
	"assets/site.css":     "body { color: teal }",
	"internal/index.html": "<p>Internal plans</p>",
}

// publish creates a site and activates a deploy of policyFiles on it. No
// policy row is written.
func (p *policyEdge) publish(name string) store.Site {
	t, ctx := p.t, p.t.Context()
	t.Helper()
	site := store.Site{ID: store.NewID(), OwnerID: p.owner.ID, Name: name, CreatedAt: p.now()}
	if err := p.db.Sites().Create(ctx, site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	p.deployFiles(name, deploys.Manifest{}, policyFiles)
	return site
}

// deployFiles creates, uploads and activates a deploy of files (path to
// body) on the site called name, with m's settings.
func (p *policyEdge) deployFiles(name string, m deploys.Manifest, files map[string]string) store.Deploy {
	t, ctx := p.t, p.t.Context()
	t.Helper()
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
	if _, d, err = p.deploys.Activate(ctx, p.owner.ID, name, d.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

func (p *policyEdge) set(site, prefix string, vis store.Visibility, links bool) store.Policy {
	p.t.Helper()
	pol, err := p.acc.Set(p.t.Context(), p.owner.ID, site, access.Rule{PathPrefix: prefix, Visibility: vis, Links: links})
	if err != nil {
		p.t.Fatal(err)
	}
	return pol
}

// issue creates a share-link session (as A2.2 will) bound to pol.
func (p *policyEdge) issue(site store.Site, pol store.Policy) string {
	t, ctx := p.t, p.t.Context()
	t.Helper()
	p.links++
	l := store.ShareLink{
		ID: store.NewID(), SiteID: site.ID, PublicID: "link" + strings.Repeat("0", 5) + string(rune('a'+p.links)),
		TokenHash: store.NewID(), PathPrefix: "/", CreatedAt: p.now(),
	}
	if err := p.db.Links().Create(ctx, p.owner.ID, l); err != nil {
		t.Fatal(err)
	}
	c, err := p.acc.IssueSession(ctx, access.NewSession{SiteID: site.ID, Policy: pol, Method: store.SessionLink, ShareLinkID: l.ID, LinkTokenHash: l.TokenHash})
	if err != nil {
		t.Fatal(err)
	}
	return c.Value
}

type result struct {
	code int
	body string
	h    http.Header
}

func (p *policyEdge) do(method, host, target, cookie string, header http.Header) result {
	p.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	for k, v := range header {
		req.Header[k] = v
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: access.SessionCookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return result{rec.Code, rec.Body.String(), rec.Result().Header}
}

func (r result) expect(t *testing.T, code int, body, hidden string) {
	t.Helper()
	if r.code != code || !strings.Contains(r.body, body) || (hidden != "" && strings.Contains(r.body, hidden)) {
		t.Fatalf("%d %q, want %d with %q and never %q", r.code, r.body, code, body, hidden)
	}
}

func (r result) cleared() bool {
	c := r.h.Get("Set-Cookie")
	return strings.HasPrefix(c, access.SessionCookieName+"=;") && strings.Contains(c, "Max-Age=0")
}

func (r result) protected(t *testing.T, cache string) {
	t.Helper()
	for k, want := range map[string]string{
		"X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer", "Cache-Control": cache,
	} {
		if got := r.h.Get(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(strings.Join(r.h.Values("Vary"), ","), "Cookie") {
		t.Fatalf("Vary = %q, want Cookie", r.h.Values("Vary"))
	}
}

var (
	html       = http.Header{"Accept": {"text/html"}}
	sameOrigin = http.Header{"Origin": {"http://docs.wardlock.localhost"}}
)

const host, otherHost = "docs.wardlock.localhost", "other.wardlock.localhost"

// previewHost is docs's preview pr-1 (C1.19).
const previewHost = "docs--pr-1.wardlock.localhost"

// TestPoliciesThroughTheEdge checks policies end to end: the public default
// for a site with no policy row, opting in, precedence, sessions, cookie
// isolation, fallbacks, logout, the reserved path and the protected-site
// headers.
func TestPoliciesThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	docs := p.publish("docs")
	p.publish("other")

	// A site with no policy row is public: deploys keep working.
	r := p.do(http.MethodGet, host, "/", "", html)
	r.expect(t, 200, "Home page", "")
	if r.h.Get("X-Robots-Tag") != "" || r.h.Get("Cross-Origin-Resource-Policy") != "" || r.h.Get("Cache-Control") != "public, max-age=0, must-revalidate" || r.h.Get("Vary") != "" {
		t.Fatalf("public page headers %v", r.h)
	}

	// Opting in protects the whole site on the next request.
	p.set("docs", "/", store.Protected, true)
	r = p.do(http.MethodGet, host, "/", "", html)
	r.expect(t, 401, "This page is private", "Home page")
	r.protected(t, "no-store")
	p.do(http.MethodGet, host, "/", "", nil).expect(t, 401, "protected", "Home page")
	p.do(http.MethodGet, host, "/no/such/page", "", nil).expect(t, 401, "protected", "Custom not found")
	p.do(http.MethodGet, host, "/.wardlock/health", "", nil).expect(t, 200, `"ok"`, "")
	p.do(http.MethodGet, host, "/.wardlock/login", "", html).expect(t, 200, "Share link", "")

	// Precedence: public default, protected /internal/.
	p.set("docs", "/", store.Public, false)
	internal := p.set("docs", "/internal/", store.Protected, true)
	p.do(http.MethodGet, host, "/", "", html).expect(t, 200, "Home page", "")
	p.do(http.MethodGet, host, "/internal/", "", html).expect(t, 401, "This page is private", "Internal plans")
	p.do(http.MethodGet, host, "/internal", "", nil).expect(t, 401, "protected", "")
	p.do(http.MethodGet, host, "/.wardlock/health", "", nil).expect(t, 200, `"ok"`, "")

	// A session for /internal/ lets the visitor in, with the protected
	// headers.
	cookie := p.issue(docs, internal)
	r = p.do(http.MethodGet, host, "/internal/", cookie, html)
	r.expect(t, 200, "Internal plans", "")
	r.protected(t, "private, max-age=0, must-revalidate")
	p.do(http.MethodHead, host, "/internal/", cookie, nil).expect(t, 200, "", "")

	// Cookie isolation: the same cookie grants nothing on another site and
	// is cleared there; the dashboard host never reads it.
	p.set("other", "/", store.Protected, true)
	r = p.do(http.MethodGet, otherHost, "/", cookie, nil)
	r.expect(t, 401, "protected", "Home page")
	if !r.cleared() {
		t.Fatalf("foreign cookie not cleared: %v", r.h)
	}
	p.do(http.MethodGet, "wardlock.localhost", "/api/v0/me", cookie, nil)
	if p.dashboardHits != 1 {
		t.Fatalf("dashboard host not routed to the dashboard")
	}

	// A fallback file behind a stricter policy is not served on a public
	// path: protect the default, open /assets/, and ask for a missing asset.
	p.set("docs", "/", store.Protected, true)
	p.set("docs", "/assets/", store.Public, false)
	p.do(http.MethodGet, host, "/assets/site.css", "", nil).expect(t, 200, "teal", "")
	p.do(http.MethodGet, host, "/assets/missing.css", "", nil).expect(t, 404, "404 page not found", "Custom not found")

	// Logout from another site is refused and ends nothing; from the site
	// itself it ends the session.
	r = p.do(http.MethodPost, host, "/.wardlock/logout", cookie, http.Header{"Origin": {"http://" + otherHost}})
	if r.code != 403 || r.cleared() {
		t.Fatalf("cross-site logout = %d %v", r.code, r.h)
	}
	p.do(http.MethodGet, host, "/internal/", cookie, nil).expect(t, 200, "Internal plans", "")
	r = p.do(http.MethodPost, host, "/.wardlock/logout", cookie, sameOrigin)
	if r.code != 303 || !r.cleared() {
		t.Fatalf("logout = %d %v", r.code, r.h)
	}
	p.do(http.MethodGet, host, "/internal/", cookie, nil).expect(t, 401, "protected", "Internal plans")

	// Logs name policies and sites, never cookie values.
	enc, sig, _ := strings.Cut(cookie, ".")
	if out := p.logs.String(); strings.Contains(out, enc) || strings.Contains(out, sig) {
		t.Fatalf("a cookie reached the logs:\n%s", out)
	}
}

// TestSessionMatrixThroughTheEdge runs the wardlock-access cookie cases
// through Edge.ServeHTTP with the real access service and SQLite: each bad
// cookie is refused with 401 and cleared, and a good one keeps working.
func TestSessionMatrixThroughTheEdge(t *testing.T) {
	for _, tc := range []struct {
		name string
		// spoil turns a working cookie for target into the case's cookie,
		// changing server state as needed.
		spoil func(p *policyEdge, site store.Site, cookie string) string
	}{
		{"valid", func(_ *policyEdge, _ store.Site, c string) string { return c }},
		{"tampered payload", func(_ *policyEdge, _ store.Site, c string) string {
			enc, sig, _ := strings.Cut(c, ".")
			b := []byte(enc)
			if b[12] == 'A' {
				b[12] = 'B'
			} else {
				b[12] = 'A'
			}
			return string(b) + "." + sig
		}},
		{"forged signature", func(_ *policyEdge, _ store.Site, c string) string {
			enc, _, _ := strings.Cut(c, ".")
			return enc + "." + strings.Repeat("A", 43)
		}},
		{"expired", func(p *policyEdge, _ store.Site, c string) string {
			p.advance(access.SessionLifetime)
			return c
		}},
		{"revoked", func(p *policyEdge, site store.Site, c string) string {
			if err := p.acc.EndSession(p.t.Context(), site.ID, c); err != nil {
				p.t.Fatal(err)
			}
			return c
		}},
		{"version bump", func(p *policyEdge, _ store.Site, c string) string {
			p.set("docs", "/internal/", store.Public, false)
			p.set("docs", "/internal/", store.Protected, true)
			return c
		}},
		{"policy deleted", func(p *policyEdge, _ store.Site, c string) string {
			ps, err := p.acc.List(p.t.Context(), p.owner.ID, "docs", "", 10)
			if err != nil {
				p.t.Fatal(err)
			}
			for _, pol := range ps {
				if pol.PathPrefix == "/internal/" {
					if err := p.acc.Delete(p.t.Context(), p.owner.ID, "docs", pol.ID); err != nil {
						p.t.Fatal(err)
					}
				}
			}
			return c
		}},
		{"another site's cookie", func(p *policyEdge, _ store.Site, _ string) string {
			other := p.publish("other")
			return p.issue(other, p.set("other", "/", store.Protected, true))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPolicyEdge(t)
			site := p.publish("docs")
			// Both protected, so the "policy deleted" case falls back to a
			// protected default the cookie does not satisfy.
			p.set("docs", "/", store.Protected, true)
			cookie := p.issue(site, p.set("docs", "/internal/", store.Protected, true))
			p.do(http.MethodGet, host, "/internal/", cookie, nil).expect(t, 200, "Internal plans", "")

			r := p.do(http.MethodGet, host, "/internal/", tc.spoil(p, site, cookie), nil)
			if tc.name == "valid" {
				r.expect(t, 200, "Internal plans", "")
				if r.cleared() {
					t.Fatal("valid cookie cleared")
				}
				return
			}
			r.expect(t, 401, "protected", "Internal plans")
			r.protected(t, "no-store")
			if !r.cleared() {
				t.Fatalf("bad cookie not cleared: %v", r.h)
			}
		})
	}
}

// A policy on "<dir>index.html/" protects that one file; asking for its
// directory must not serve it past that policy.
func TestDirectoryIndexWithItsOwnPolicy(t *testing.T) {
	p := newPolicyEdge(t)
	docs := p.publish("docs")
	pol := p.set("docs", "/internal/index.html/", store.Protected, true)
	// The directory answers a plain 404, not a challenge: its own policy
	// (the default) allowed the request, the index's refused it.
	r := p.visit(visit{target: "/internal/", header: html})
	if r.code != http.StatusNotFound || strings.Contains(r.body, "Internal plans") || strings.Contains(r.body, "This page is private") {
		t.Fatalf("/internal/ = %d %q, want a plain 404", r.code, r.body)
	}
	r.protected(t, "private, max-age=0, must-revalidate")
	// The file's own URL gets the sign-in prompt, and the clean URL its
	// redirect to the directory.
	r = p.visit(visit{target: "/internal/index.html", header: html})
	r.expect(t, http.StatusUnauthorized, "This page is private", "Internal plans")
	r.protected(t, "no-store")
	r = p.visit(visit{target: "/internal", header: html})
	if r.code != http.StatusMovedPermanently || strings.Contains(r.body, "Internal plans") {
		t.Fatalf("/internal = %d %q", r.code, r.body)
	}
	// With a session for that policy, the directory serves its index.
	cookie := p.issue(docs, pol)
	p.visit(visit{target: "/internal/", cookie: cookie, header: html}).expect(t, 200, "Internal plans", "")
	// Other directories keep one decision and their own policy.
	p.visit(visit{target: "/", header: html}).expect(t, 200, "Home page", "")
}
