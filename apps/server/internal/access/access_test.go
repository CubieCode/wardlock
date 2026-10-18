// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

var start = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

var secret = []byte("test-server-key-0123456789abcdef-0123456789")

// notifier delivers synchronously, like deploys.LocalNotifier.
type notifier struct {
	mu   sync.Mutex
	subs []func(string)
}

func (n *notifier) Notify(_ context.Context, siteID string) {
	n.mu.Lock()
	subs := append([]func(string){}, n.subs...)
	n.mu.Unlock()
	for _, fn := range subs {
		fn(siteID)
	}
}

func (n *notifier) Subscribe(fn func(string)) func() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.subs = append(n.subs, fn)
	return func() {}
}

// countingPolicies counts reads that reach the database.
type countingPolicies struct {
	store.Policies
	mu    sync.Mutex
	reads int
}

func (c *countingPolicies) ForSite(ctx context.Context, siteID string) ([]store.Policy, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Policies.ForSite(ctx, siteID)
}

type harness struct {
	t        *testing.T
	db       *store.DB
	svc      *access.Service
	policies *countingPolicies
	clock    *clock
	log      *bytes.Buffer
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessKey(t, storetest.New(t), secret)
}

func newHarnessKey(t *testing.T, db *store.DB, key []byte) *harness {
	t.Helper()
	h := &harness{t: t, db: db, clock: &clock{now: start}, log: &bytes.Buffer{}}
	h.policies = &countingPolicies{Policies: db.Policies()}
	svc, err := access.New(access.Config{
		Sites:    db.Sites(),
		Policies: h.policies,
		Sessions: db.Sessions(),
		Links:    db.Links(),
		Notifier: &notifier{},
		Secret:   key,
		Now:      h.clock.Now,
		Log:      slog.New(slog.NewTextHandler(h.log, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	h.svc = svc
	return h
}

func (h *harness) owner() string {
	h.t.Helper()
	o := store.Owner{ID: store.NewID(), Email: store.NewID() + "@example.test", CreatedAt: start}
	if err := h.db.Owners().Create(h.t.Context(), o); err != nil {
		h.t.Fatal(err)
	}
	return o.ID
}

func (h *harness) site(ownerID, name string) store.Site {
	h.t.Helper()
	s := store.Site{ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: start}
	if err := h.db.Sites().Create(h.t.Context(), s); err != nil {
		h.t.Fatal(err)
	}
	return s
}

func (h *harness) set(ownerID, site string, r access.Rule) store.Policy {
	h.t.Helper()
	p, err := h.svc.Set(h.t.Context(), ownerID, site, r)
	if err != nil {
		h.t.Fatalf("Set(%+v): %v", r, err)
	}
	return p
}

// protect makes the site's default protected with share links, as an owner
// opting in does.
func (h *harness) protect(ownerID, site string) store.Policy {
	h.t.Helper()
	return h.set(ownerID, site, access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true})
}

func (h *harness) policyFor(siteID, reqPath string) store.Policy {
	h.t.Helper()
	d, err := h.svc.Decide(h.t.Context(), access.Request{SiteID: siteID, Path: reqPath})
	if err != nil {
		h.t.Fatal(err)
	}
	return d.Policy
}

func (h *harness) session(siteID string, p store.Policy, m store.SessionMethod) string {
	h.t.Helper()
	n := access.NewSession{SiteID: siteID, Policy: p, Method: m}
	switch m {
	case store.SessionLink:
		l := store.ShareLink{ID: store.NewID(), SiteID: siteID, PublicID: strings.ToLower(store.NewID()[24:34]), TokenHash: store.NewID(), PathPrefix: "/", CreatedAt: start}
		var ownerID string
		ps, _ := h.db.Sites().GetByID(h.t.Context(), siteID)
		ownerID = ps.OwnerID
		if err := h.db.Links().Create(h.t.Context(), ownerID, l); err != nil {
			h.t.Fatal(err)
		}
		n.ShareLinkID, n.LinkTokenHash = l.ID, l.TokenHash
	case store.SessionEmail, store.SessionOAuth:
		n.Subject = "visitor@example.test"
	}
	c, err := h.svc.IssueSession(h.t.Context(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	if c.Name != access.SessionCookieName || !c.Expires.Equal(h.clock.Now().Add(access.SessionLifetime)) {
		h.t.Fatalf("cookie %s expires %v", c.Name, c.Expires)
	}
	return c.Value
}

func (h *harness) decide(r access.Request) access.Decision {
	h.t.Helper()
	d, err := h.svc.Decide(h.t.Context(), r)
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

func TestGoverning(t *testing.T) {
	policies := []store.Policy{
		{ID: "default", PathPrefix: "/"},
		{ID: "assets", PathPrefix: "/assets/"},
		{ID: "docs", PathPrefix: "/docs/"},
		{ID: "internal", PathPrefix: "/docs/internal/"},
	}
	for _, tc := range []struct{ path, want string }{
		{"/", "default"},
		{"/index.html", "default"},
		{"/assets/app.js", "assets"},
		{"/assets/", "assets"},
		{"/assets", "assets"},
		{"/assetsx/app.js", "default"},
		{"/docs/", "docs"},
		{"/docs", "docs"},
		{"/docs.html", "default"},
		{"/docs/page", "docs"},
		{"/docs/internal/", "internal"},
		{"/docs/internal", "internal"},
		{"/docs/internal/deep/file.txt", "internal"},
		{"/docs/internalx", "docs"},
		{"/Docs/page", "default"},
	} {
		got, ok := access.Governing(policies, tc.path)
		if !ok || got.ID != tc.want {
			t.Errorf("Governing(%q) = %q, %v; want %q", tc.path, got.ID, ok, tc.want)
		}
	}
	if _, ok := access.Governing(policies[1:], "/other"); ok {
		t.Error("Governing without a default matched /other")
	}
}

func TestCheckPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		ok     bool
	}{
		{"/", true},
		{"/docs/", true},
		{"/docs/internal/", true},
		{"/café/", true},
		{"/a b/", true},
		{"", false},
		{"docs/", false},
		{"/docs", false},
		{"//", false},
		{"/docs//x/", false},
		{"/./", false},
		{"/docs/../", false},
		{"/.wardlock/", false},
		{"/.WardLock/x/", false},
		{"/a\\b/", false},
		{"/a\x00b/", false},
		{"/a\u200bb/", false},
		{"/\xff/", false},
		{"/" + strings.Repeat("a", 1023) + "/", false},
		{"/" + strings.Repeat("a", 1022) + "/", true},
	} {
		err := access.CheckPrefix(tc.prefix)
		if (err == nil) != tc.ok {
			t.Errorf("CheckPrefix(%q) = %v, want ok %v", tc.prefix, err, tc.ok)
		}
		if err != nil && !errors.Is(err, access.ErrInvalidPath) {
			t.Errorf("CheckPrefix(%q) = %v, want ErrInvalidPath", tc.prefix, err)
		}
	}
}

func TestNewSiteDefaultsToPublic(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	for _, r := range []access.Request{
		{SiteID: s.ID, Path: "/", Browser: true},
		{SiteID: s.ID, Path: "/deep/page.html"},
		{SiteID: s.ID, Path: "/", Cookie: "anything"},
	} {
		d := h.decide(r)
		if d.Outcome != access.Allow || d.Protected || d.Via != access.ViaPublic || d.ClearCookie {
			t.Fatalf("%+v: %+v", r, d)
		}
		if p := d.Policy; p.PathPrefix != "/" || p.Visibility != store.Public || p.LinksEnabled ||
			p.PasswordEnabled || p.EmailEnabled || p.OAuthEnabled || p.Version != 1 {
			t.Fatalf("default policy %+v", p)
		}
	}
	list, err := h.svc.List(t.Context(), o, "docs", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != h.policyFor(s.ID, "/").ID {
		t.Fatalf("List = %+v", list)
	}
	// Opting in protects the site on the next request.
	h.protect(o, "docs")
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Browser: true}); d.Outcome != access.Login || !d.Protected {
		t.Fatalf("after protecting: %+v", d)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/"}); d.Outcome != access.Unauthorized {
		t.Fatalf("after protecting, non-browser: %+v", d)
	}
}

func TestPrecedence(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	h.protect(o, "docs")
	h.set(o, "docs", access.Rule{PathPrefix: "/assets/", Visibility: store.Public})
	h.set(o, "docs", access.Rule{PathPrefix: "/team/", Visibility: store.Public})
	h.set(o, "docs", access.Rule{PathPrefix: "/team/private/", Visibility: store.Protected, Links: true})
	for _, tc := range []struct {
		path  string
		allow bool
	}{
		{"/", false},
		{"/index.html", false},
		{"/assets/app.css", true},
		{"/assets", true},
		{"/assets-old/app.css", false},
		{"/team/", true},
		{"/team/about.html", true},
		{"/team/private/", false},
		{"/team/private", false},
		{"/team/private/plan.html", false},
	} {
		d := h.decide(access.Request{SiteID: s.ID, Path: tc.path})
		if (d.Outcome == access.Allow) != tc.allow || d.Protected == tc.allow {
			t.Errorf("%s: %+v, want allow %v", tc.path, d, tc.allow)
		}
		if tc.allow && d.Via != access.ViaPublic {
			t.Errorf("%s: via %q", tc.path, d.Via)
		}
	}
	h.set(o, "docs", access.Rule{PathPrefix: "/", Visibility: store.Public})
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/team/private/x"}); d.Outcome == access.Allow {
		t.Fatalf("protected path under a public default: %+v", d)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/x"}); d.Outcome != access.Allow || d.Protected {
		t.Fatalf("public default: %+v", d)
	}
}

func TestMethodAlternatives(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	// Password, email and OAuth are configured through A2.3-A2.5; build the
	// policies directly to exercise the decision.
	mk := func(prefix string, links, password, email, oauth bool) store.Policy {
		p := store.Policy{
			ID: store.NewID(), SiteID: s.ID, PathPrefix: prefix, Visibility: store.Protected,
			LinksEnabled: links, PasswordEnabled: password, EmailEnabled: email, OAuthEnabled: oauth,
			Version: 1, CreatedAt: start, UpdatedAt: start,
		}
		if password {
			p.PasswordHash = "$argon2id$stand-in"
		}
		if err := h.db.Policies().Create(ctx, o, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	all := mk("/", true, true, true, true)
	pw := mk("/pw/", false, true, false, false)
	mail := mk("/mail/", true, false, true, false)

	for _, tc := range []struct {
		name   string
		policy store.Policy
		method store.SessionMethod
		path   string
		allow  bool
	}{
		{"link on all", all, store.SessionLink, "/", true},
		{"password on all", all, store.SessionPassword, "/", true},
		{"email on all", all, store.SessionEmail, "/", true},
		{"oauth on all", all, store.SessionOAuth, "/", true},
		{"password on password", pw, store.SessionPassword, "/pw/x", true},
		{"email on email or links", mail, store.SessionEmail, "/mail/", true},
		{"link on email or links", mail, store.SessionLink, "/mail/", true},
		{"default session on a path policy", all, store.SessionPassword, "/pw/x", false},
		{"path session on the default", pw, store.SessionPassword, "/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cookie := h.session(s.ID, tc.policy, tc.method)
			d := h.decide(access.Request{SiteID: s.ID, Path: tc.path, Cookie: cookie, Browser: true})
			if (d.Outcome == access.Allow) != tc.allow || !d.Protected {
				t.Fatalf("%+v, want allow %v", d, tc.allow)
			}
			if tc.allow && string(d.Via) != string(tc.method) {
				t.Fatalf("via %q, want %q", d.Via, tc.method)
			}
			if !tc.allow && (d.Outcome != access.Login || d.ClearCookie) {
				// A good session for another policy is kept.
				t.Fatalf("%+v, want login without clearing", d)
			}
		})
	}
}

func TestVersionBumpEndsSessions(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	def := h.protect(o, "docs") // version 2: the public default was version 1
	docs := h.set(o, "docs", access.Rule{PathPrefix: "/docs/", Visibility: store.Protected, Links: true})
	defCookie := h.session(s.ID, def, store.SessionLink)
	docsCookie := h.session(s.ID, docs, store.SessionLink)
	allowed := func(cookie, p string) access.Decision {
		t.Helper()
		return h.decide(access.Request{SiteID: s.ID, Path: p, Cookie: cookie})
	}
	if d := allowed(defCookie, "/"); d.Outcome != access.Allow || d.Via != access.ViaLink {
		t.Fatalf("before: %+v", d)
	}

	// Identical settings: no new version, sessions survive.
	if p := h.set(o, "docs", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true}); p.Version != 2 {
		t.Fatalf("version after identical set = %d", p.Version)
	}
	if d := allowed(defCookie, "/"); d.Outcome != access.Allow {
		t.Fatalf("after identical set: %+v", d)
	}

	// Public and back: two bumps; the old session is gone for good.
	if p := h.set(o, "docs", access.Rule{PathPrefix: "/", Visibility: store.Public}); p.Version != 3 {
		t.Fatalf("version = %d, want 3", p.Version)
	}
	if p := h.set(o, "docs", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true}); p.Version != 4 {
		t.Fatalf("version = %d, want 4", p.Version)
	}
	if d := allowed(defCookie, "/"); d.Outcome == access.Allow || !d.ClearCookie {
		t.Fatalf("after version bump: %+v", d)
	}
	// The /docs/ policy did not change, so its session lives on.
	if d := allowed(docsCookie, "/docs/a"); d.Outcome != access.Allow {
		t.Fatalf("other policy's session: %+v", d)
	}

	// Deleting the path policy ends its sessions; /docs/ falls back to the
	// default, which the old cookie does not satisfy.
	if err := h.svc.Delete(t.Context(), o, "docs", docs.ID); err != nil {
		t.Fatal(err)
	}
	if d := allowed(docsCookie, "/docs/a"); d.Outcome == access.Allow || !d.ClearCookie || d.Policy.ID != def.ID {
		t.Fatalf("after delete: %+v", d)
	}
}

func TestCookieRejections(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	a := h.site(o, "alpha")
	b := h.site(o, "beta")
	pa := h.protect(o, "alpha")
	h.protect(o, "beta")
	good := h.session(a.ID, pa, store.SessionLink)
	// Same database, another server key: a cookie minted elsewhere.
	other := newHarnessKey(t, h.db, []byte("another-server-key-0123456789abcdef-0123"))
	os := other.site(o, "gamma")
	forged := other.session(os.ID, other.protect(o, "gamma"), store.SessionLink)

	enc, sig, _ := strings.Cut(good, ".")
	flip := func(s string, i int) string {
		c := byte('A')
		if s[i] == 'A' {
			c = 'B'
		}
		return s[:i] + string(c) + s[i+1:]
	}

	for _, tc := range []struct {
		name   string
		site   string
		cookie string
	}{
		{"tampered payload", a.ID, flip(enc, 10) + "." + sig},
		{"tampered signature", a.ID, enc + "." + flip(sig, 5)},
		{"truncated", a.ID, good[:len(good)-1]},
		{"no signature", a.ID, enc},
		{"garbage", a.ID, "not-a-cookie"},
		{"oversized", a.ID, strings.Repeat("A", 600) + "." + sig},
		{"forged with another key", os.ID, forged},
		{"another site's cookie", b.ID, good},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := h.decide(access.Request{SiteID: tc.site, Path: "/", Cookie: tc.cookie})
			if d.Outcome == access.Allow || !d.ClearCookie {
				t.Fatalf("%+v", d)
			}
		})
	}
	if d := h.decide(access.Request{SiteID: a.ID, Path: "/", Cookie: good}); d.Outcome != access.Allow {
		t.Fatalf("good cookie: %+v", d)
	}
}

func TestSessionExpiryAndLogout(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	p := h.protect(o, "docs")
	cookie := h.session(s.ID, p, store.SessionLink)
	h.clock.advance(access.SessionLifetime - time.Millisecond)
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: cookie}); d.Outcome != access.Allow {
		t.Fatalf("just before expiry: %+v", d)
	}
	h.clock.advance(time.Millisecond)
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: cookie}); d.Outcome == access.Allow || !d.ClearCookie {
		t.Fatalf("at expiry: %+v", d)
	}

	// NotAfter caps the lifetime.
	capped, err := h.svc.IssueSession(t.Context(), access.NewSession{
		SiteID: s.ID, Policy: p, Method: store.SessionPassword, NotAfter: h.clock.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !capped.Expires.Equal(h.clock.Now().Add(time.Hour)) {
		t.Fatalf("capped expiry %v", capped.Expires)
	}
	if _, err := h.svc.IssueSession(t.Context(), access.NewSession{
		SiteID: s.ID, Policy: p, Method: store.SessionPassword, NotAfter: h.clock.Now(),
	}); err == nil {
		t.Fatal("issued an already expired session")
	}

	// Logout revokes; a cookie for another site or garbage is ignored.
	live := h.session(s.ID, p, store.SessionLink)
	if err := h.svc.EndSession(t.Context(), store.NewID(), live); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EndSession(t.Context(), s.ID, "garbage"); err != nil {
		t.Fatal(err)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: live}); d.Outcome != access.Allow {
		t.Fatalf("after foreign logout: %+v", d)
	}
	if err := h.svc.EndSession(t.Context(), s.ID, live); err != nil {
		t.Fatal(err)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: live}); d.Outcome == access.Allow || !d.ClearCookie {
		t.Fatalf("after logout: %+v", d)
	}
}

func TestPublicRequestsStayInMemory(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	h.set(o, "docs", access.Rule{PathPrefix: "/", Visibility: store.Public})
	h.decide(access.Request{SiteID: s.ID, Path: "/"})
	before := h.policies.reads
	for range 10 {
		h.decide(access.Request{SiteID: s.ID, Path: "/a"})
	}
	if h.policies.reads != before {
		t.Fatalf("%d policy reads for cached requests", h.policies.reads-before)
	}
	h.set(o, "docs", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true})
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/a"}); d.Outcome == access.Allow {
		t.Fatalf("policy change not seen on the next request: %+v", d)
	}
}

func TestOwnerOperations(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	a, b := h.owner(), h.owner()
	h.site(a, "alpha")
	h.site(b, "beta")
	docs := h.set(a, "alpha", access.Rule{PathPrefix: "/docs/", Visibility: store.Public})
	def := h.set(a, "alpha", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true})

	for _, tc := range []struct {
		name string
		rule access.Rule
		want error
	}{
		{"public with links", access.Rule{PathPrefix: "/", Visibility: store.Public, Links: true}, access.ErrInvalidPolicy},
		{"protected without methods", access.Rule{PathPrefix: "/", Visibility: store.Protected}, access.ErrInvalidPolicy},
		{"unknown visibility", access.Rule{PathPrefix: "/", Visibility: "secret", Links: true}, access.ErrInvalidPolicy},
		{"bad prefix", access.Rule{PathPrefix: "/docs", Visibility: store.Public}, access.ErrInvalidPath},
		{"reserved prefix", access.Rule{PathPrefix: "/.wardlock/", Visibility: store.Public}, access.ErrInvalidPath},
	} {
		if _, err := h.svc.Set(ctx, a, "alpha", tc.rule); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}

	page, err := h.svc.List(ctx, a, "alpha", "", 1)
	if err != nil || len(page) != 1 || page[0].ID != def.ID {
		t.Fatalf("first page %+v, %v", page, err)
	}
	page, err = h.svc.List(ctx, a, "alpha", "/", 1)
	if err != nil || len(page) != 1 || page[0].ID != docs.ID {
		t.Fatalf("second page %+v, %v", page, err)
	}

	if err := h.svc.Delete(ctx, a, "alpha", def.ID); !errors.Is(err, access.ErrDefaultPolicy) {
		t.Fatalf("delete default: %v", err)
	}

	// Owner B cannot see or touch owner A's site or policies, and gets the
	// same error as for a missing site or policy.
	notFound := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, access.ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", name, err)
		}
	}
	_, err = h.svc.List(ctx, b, "alpha", "", 10)
	notFound("B lists A's site", err)
	_, err = h.svc.Set(ctx, b, "alpha", access.Rule{PathPrefix: "/", Visibility: store.Public})
	notFound("B sets A's site", err)
	notFound("B deletes on A's site", h.svc.Delete(ctx, b, "alpha", docs.ID))
	notFound("B deletes A's policy through B's site", h.svc.Delete(ctx, b, "beta", docs.ID))
	notFound("A deletes a missing policy", h.svc.Delete(ctx, a, "alpha", store.NewID()))
	_, err = h.svc.List(ctx, a, "missing", "", 10)
	notFound("A lists a missing site", err)

	if got := h.policyFor(h.mustSite(a, "alpha").ID, "/docs/x"); got.ID != docs.ID {
		t.Fatalf("B's attempts changed A's policy: %+v", got)
	}
	if err := h.svc.Delete(ctx, a, "alpha", docs.ID); err != nil {
		t.Fatal(err)
	}
	if got := h.policyFor(h.mustSite(a, "alpha").ID, "/docs/x"); got.ID != def.ID {
		t.Fatalf("after delete /docs/x is governed by %+v", got)
	}
}

func (h *harness) mustSite(ownerID, name string) store.Site {
	h.t.Helper()
	s, err := h.db.Sites().GetByOwnerName(h.t.Context(), ownerID, name)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func TestLogsNeverContainCookies(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	cookie := h.session(s.ID, h.policyFor(s.ID, "/"), store.SessionLink)
	h.set(o, "docs", access.Rule{PathPrefix: "/", Visibility: store.Public})
	if err := h.svc.EndSession(t.Context(), s.ID, cookie); err != nil {
		t.Fatal(err)
	}
	enc, sig, _ := strings.Cut(cookie, ".")
	if out := h.log.String(); !strings.Contains(out, "policy set") || strings.Contains(out, enc) || strings.Contains(out, sig) {
		t.Fatalf("log output:\n%s", out)
	}
}

func TestNewRequiresKey(t *testing.T) {
	if _, err := access.New(access.Config{Notifier: &notifier{}, Secret: []byte("short")}); err == nil {
		t.Fatal("New accepted a short key")
	}
}
