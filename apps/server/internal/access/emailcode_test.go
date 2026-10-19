// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// fakeMailer keeps every message it is given.
type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (m *fakeMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

// take returns and forgets the messages sent so far.
func (m *fakeMailer) take() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.sent
	m.sent = nil
	return out
}

// racingEmailCodes records the codes and sessions stored through it, and
// runs beforeCreate once just before the next CreateSession, to interleave
// a policy change with a sign-in.
type racingEmailCodes struct {
	store.EmailCodes
	mu           sync.Mutex
	beforeCreate func()
	puts         []store.VisitorEmailCode
	sessions     []store.SiteSession // stored ones only
}

func (r *racingEmailCodes) Put(ctx context.Context, c store.VisitorEmailCode) error {
	err := r.EmailCodes.Put(ctx, c)
	if err == nil {
		r.mu.Lock()
		r.puts = append(r.puts, c)
		r.mu.Unlock()
	}
	return err
}

func (r *racingEmailCodes) CreateSession(ctx context.Context, s store.SiteSession) error {
	r.mu.Lock()
	fn := r.beforeCreate
	r.beforeCreate = nil
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	err := r.EmailCodes.CreateSession(ctx, s)
	if err == nil {
		r.mu.Lock()
		r.sessions = append(r.sessions, s)
		r.mu.Unlock()
	}
	return err
}

// lastPut is the code stored most recently.
func (r *racingEmailCodes) lastPut(t *testing.T) store.VisitorEmailCode {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.puts) == 0 {
		t.Fatal("no code stored")
	}
	return r.puts[len(r.puts)-1]
}

// stored reports the codes and sessions stored so far.
func (r *racingEmailCodes) stored() (int, []store.SiteSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.puts), append([]store.SiteSession(nil), r.sessions...)
}

// live reports whether the stored session ss is not revoked.
func (h *emailHarness) live(ss store.SiteSession) bool {
	h.t.Helper()
	got, err := h.db.Sessions().GetSiteByTokenHash(h.t.Context(), ss.TokenHash)
	if err != nil {
		h.t.Fatal(err)
	}
	return got.RevokedAt.IsZero()
}

// count is the number of reads so far.
func (c *countingPolicies) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

type emailHarness struct {
	*harness
	mailer *fakeMailer
	codes  *racingEmailCodes
}

// newEmailHarness is newHarness with a fake mailer and the given limits
// (zero for the defaults), counted by the service's own limiters.
func newEmailHarness(t *testing.T, limits access.Limits) *emailHarness {
	t.Helper()
	db := storetest.New(t)
	h := &harness{t: t, db: db, clock: &clock{now: start}, log: &bytes.Buffer{}}
	h.policies = &countingPolicies{Policies: db.Policies()}
	codes := &racingEmailCodes{EmailCodes: db.EmailCodes()}
	m := &fakeMailer{}
	svc, err := access.New(access.Config{
		Sites: db.Sites(), Policies: h.policies, Sessions: db.Sessions(), Links: db.Links(),
		PasswordSessions: db.PasswordSessions(), EmailCodes: codes, Mailer: m,
		Notifier: &notifier{}, Secret: secret, Now: h.clock.Now,
		Log:    slog.New(slog.NewTextHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	h.svc = svc
	return &emailHarness{harness: h, mailer: m, codes: codes}
}

// Synthetic visitor addresses for tests only.
const (
	allowedAddr  = "ana@example.test"
	disallowAddr = "eve@elsewhere.test"
)

func emailRule(prefix string, entries store.Allowlist) access.Rule {
	return access.Rule{PathPrefix: prefix, Visibility: store.Protected, Email: true, Allowlist: &entries}
}

// send asks for a code and waits for the background send.
func (h *emailHarness) send(siteID, path, ip, addr string) error {
	h.t.Helper()
	err := h.svc.SendEmailCode(h.t.Context(), access.EmailCodeRequest{SiteID: siteID, Site: "harbour.wardlock.test", Path: path, IP: ip, Address: addr})
	access.WaitSends(h.svc)
	return err
}

// codeSent sends a code to addr and returns it from the message.
func (h *emailHarness) codeSent(siteID, path, addr string) string {
	h.t.Helper()
	if err := h.send(siteID, path, "192.0.2.10", addr); err != nil {
		h.t.Fatalf("send to %s: %v", addr, err)
	}
	msgs := h.mailer.take()
	if len(msgs) != 1 || msgs[0].To.Address != strings.ToLower(strings.TrimSpace(addr)) {
		h.t.Fatalf("sent %+v, want one message to %s", msgs, addr)
	}
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(msgs[0].Text)
	if code == "" || strings.Contains(msgs[0].Subject, code) {
		h.t.Fatalf("message %q / %q has no code, or the code in its subject", msgs[0].Subject, msgs[0].Text)
	}
	return code
}

func (h *emailHarness) login(siteID, path, ip, addr, code string) (access.Cookie, error) {
	return h.svc.EmailCodeLogin(h.t.Context(), access.EmailCodeAttempt{SiteID: siteID, Path: path, IP: ip, Address: addr, Code: code})
}

// otherCode returns a well-formed code that is not code.
func otherCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}

func TestEmailCodeLogin(t *testing.T) {
	h := newEmailHarness(t, access.Limits{})
	o := h.owner()
	s := h.site(o, "harbour")
	other := h.site(o, "tides")
	def := h.set(o, "harbour", emailRule("/", store.Allowlist{
		Emails: []string{"Ana@Example.test"}, Domains: []string{"crew.test", "*.fleet.test"},
	}))
	if !def.EmailEnabled || !def.Allowlist.Equal(store.Allowlist{Emails: []string{allowedAddr}, Domains: []string{"*.fleet.test", "crew.test"}}) {
		t.Fatalf("policy %+v", def)
	}
	h.set(o, "harbour", emailRule("/staff/", store.Allowlist{Emails: []string{"bo@example.test"}}))
	h.set(o, "harbour", access.Rule{PathPrefix: "/links/", Visibility: store.Protected, Links: true})
	h.set(o, "tides", emailRule("/", store.Allowlist{Emails: []string{allowedAddr}}))

	// The address is matched in any case and spacing; the session's
	// subject is the verified, normalised address.
	code := h.codeSent(s.ID, "/report/", "  ANA@example.TEST ")
	c, err := h.login(s.ID, "/report/", "192.0.2.1", allowedAddr, code)
	if err != nil {
		t.Fatalf("right code: %v", err)
	}
	d := h.decide(access.Request{SiteID: s.ID, Path: "/report/", Cookie: c.Value})
	if d.Outcome != access.Allow || d.Via != access.ViaEmail {
		t.Fatalf("email session: %+v", d)
	}
	if _, ss := h.codes.stored(); len(ss) != 1 || ss[0].Method != store.SessionEmail || ss[0].Subject != allowedAddr ||
		ss[0].PolicyID != def.ID || ss[0].PolicyVersion != def.Version {
		t.Fatalf("stored sessions %+v, want one email session for %s bound to %+v", ss, allowedAddr, def)
	}
	// The session is the default policy's only, and this site's only.
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/staff/", Cookie: c.Value}); d.Outcome == access.Allow {
		t.Fatalf("default's session opened /staff/: %+v", d)
	}
	if d := h.decide(access.Request{SiteID: other.ID, Path: "/", Cookie: c.Value}); d.Outcome == access.Allow || !d.ClearCookie {
		t.Fatalf("session opened another site: %+v", d)
	}
	// A code works once.
	if _, err := h.login(s.ID, "/report/", "192.0.2.1", allowedAddr, code); !errors.Is(err, access.ErrInvalidCode) {
		t.Fatalf("second use: %v", err)
	}

	// Domain entries: example.com is that domain only; *.example.com its
	// subdomains, at any depth, not the domain itself.
	for _, tc := range []struct {
		addr string
		in   bool
	}{
		{"cook@crew.test", true},
		{"cook@deck.crew.test", false},
		{"cook@north.fleet.test", true},
		{"cook@a.b.fleet.test", true},
		{"cook@fleet.test", false},
		{"cook@notfleet.test", false},
		{"cook@crew.test.evil.test", false},
		{"ana@example.test.evil.test", false},
	} {
		if err := h.send(s.ID, "/", "192.0.2.11", tc.addr); err != nil {
			t.Fatalf("%s: %v", tc.addr, err)
		}
		if got := len(h.mailer.take()); got != map[bool]int{true: 1, false: 0}[tc.in] {
			t.Errorf("%s: %d messages, want allowed %v", tc.addr, got, tc.in)
		}
	}

	// A path policy's code opens that policy only, and another policy's
	// allowlist decides there.
	staffCode := h.codeSent(s.ID, "/staff/rota", "bo@example.test")
	if _, err := h.login(s.ID, "/", "192.0.2.1", "bo@example.test", staffCode); !errors.Is(err, access.ErrInvalidCode) {
		t.Fatalf("a /staff/ code on /: %v", err)
	}
	if c, err := h.login(s.ID, "/staff/rota", "192.0.2.1", "bo@example.test", staffCode); err != nil {
		t.Fatalf("path policy code: %v", err)
	} else if d := h.decide(access.Request{SiteID: s.ID, Path: "/staff/", Cookie: c.Value}); d.Outcome != access.Allow {
		t.Fatalf("path policy session: %+v", d)
	}

	// Every failure is the same error.
	code = h.codeSent(s.ID, "/", allowedAddr)
	for _, tc := range []struct{ name, site, path, addr, code string }{
		{"wrong code", s.ID, "/", allowedAddr, otherCode(code)},
		{"not six digits", s.ID, "/", allowedAddr, code[:5]},
		{"letters", s.ID, "/", allowedAddr, "12345a"},
		{"another address", s.ID, "/", "cook@crew.test", code},
		{"not an address", s.ID, "/", "ana", code},
		{"disallowed address", s.ID, "/", disallowAddr, code},
		{"policy without the method", s.ID, "/links/x", allowedAddr, code},
		{"another site", other.ID, "/", allowedAddr, code},
	} {
		if _, err := h.login(tc.site, tc.path, "192.0.2.2", tc.addr, tc.code); !errors.Is(err, access.ErrInvalidCode) {
			t.Errorf("%s: %v, want ErrInvalidCode", tc.name, err)
		}
	}
	if _, err := h.login(s.ID, "/", "192.0.2.2", allowedAddr, code); err != nil {
		t.Fatalf("right code after one wrong attempt: %v", err)
	}
}

// TestEmailCodeNeutral shows the send answer is the same for allowed and
// disallowed addresses, and for a policy without the method: the same
// result, no message and nothing stored for the others, the same limiter
// keys counted, and the same work on the request path.
func TestEmailCodeNeutral(t *testing.T) {
	h := newEmailHarness(t, access.Limits{})
	limiter := access.NewFakeLimiter(h.clock.Now)
	access.SetAttemptLimiter(h.svc, limiter)
	o := h.owner()
	s := h.site(o, "harbour")
	h.set(o, "harbour", emailRule("/", store.Allowlist{Emails: []string{allowedAddr}}))
	h.set(o, "harbour", access.Rule{PathPrefix: "/links/", Visibility: store.Protected, Links: true})
	h.set(o, "harbour", access.Rule{PathPrefix: "/open/", Visibility: store.Public})

	type result struct {
		err   error
		keys  []string
		reads int
	}
	try := func(path, addr string) result {
		reads := h.policies.count()
		// The request path alone: the background send's policy read waits
		// until the request path's reads are counted, so it cannot land in
		// them (or not) depending on scheduling.
		release := h.policies.hold()
		defer release()
		done := make(chan error, 1)
		go func() {
			done <- h.svc.SendEmailCode(t.Context(), access.EmailCodeRequest{SiteID: s.ID, Site: "harbour.wardlock.test", Path: path, IP: "192.0.2.1", Address: addr})
		}()
		var err error
		select {
		case err = <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the request path waited for a policy read", addr)
		}
		r := result{err: err, keys: limiter.Keys(), reads: h.policies.count() - reads}
		release()
		access.WaitSends(h.svc)
		return r
	}
	allowed := try("/", allowedAddr)
	if allowed.err != nil || len(h.mailer.take()) != 1 {
		t.Fatalf("allowed: %+v", allowed)
	}
	for _, tc := range []struct{ name, path, addr string }{
		{"disallowed address", "/", disallowAddr},
		{"policy without the method", "/links/", allowedAddr},
		{"public policy", "/open/", allowedAddr},
	} {
		got := try(tc.path, tc.addr)
		if got.err != nil || got.reads != allowed.reads || len(got.keys) != len(allowed.keys) {
			t.Errorf("%s: %+v, want like allowed %+v", tc.name, got, allowed)
		}
		for i, k := range got.keys {
			// Only the address part of the key differs.
			if want := strings.Replace(allowed.keys[i], allowedAddr, tc.addr, 1); k != want {
				t.Errorf("%s: key %q, want %q", tc.name, k, want)
			}
		}
		if msgs := h.mailer.take(); len(msgs) != 0 {
			t.Errorf("%s: sent %+v", tc.name, msgs)
		}
	}
	if n, _ := h.codes.stored(); n != 1 {
		t.Fatalf("%d codes stored, want only the allowed address's", n)
	}
	// Not an address: refused before any limit or work.
	if err := h.send(s.ID, "/", "192.0.2.1", "not an address"); !errors.Is(err, access.ErrInvalidAddress) {
		t.Fatalf("bad address: %v", err)
	}
	if k := limiter.Keys(); len(k) != 0 {
		t.Fatalf("bad address counted %v", k)
	}
}

func TestEmailCodeExpiryAttemptsAndReplacement(t *testing.T) {
	// Visitor limits out of the way: this is about each code's own limits.
	limits := access.DefaultLimits()
	limits.EmailVerifyPerIP.N, limits.EmailVerifyPerAddress.N, limits.EmailSendPerAddress.N = 1000, 1000, 1000
	h := newEmailHarness(t, limits)
	o := h.owner()
	s := h.site(o, "harbour")
	h.set(o, "harbour", emailRule("/", store.Allowlist{Emails: []string{allowedAddr}}))

	// Valid until just before 10 minutes.
	code := h.codeSent(s.ID, "/", allowedAddr)
	h.clock.advance(access.EmailCodeValidity - time.Millisecond)
	if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, code); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	code = h.codeSent(s.ID, "/", allowedAddr)
	h.clock.advance(access.EmailCodeValidity)
	if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, code); !errors.Is(err, access.ErrInvalidCode) {
		t.Fatalf("at expiry: %v", err)
	}

	// Five attempts per code: after five wrong ones the right one fails.
	code = h.codeSent(s.ID, "/", allowedAddr)
	for i := range access.EmailCodeAttempts {
		if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, otherCode(code)); !errors.Is(err, access.ErrInvalidCode) {
			t.Fatalf("wrong attempt %d: %v", i+1, err)
		}
	}
	if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, code); !errors.Is(err, access.ErrInvalidCode) {
		t.Fatalf("right code after %d wrong: %v", access.EmailCodeAttempts, err)
	}

	// Concurrent guesses never exceed the attempts.
	code = h.codeSent(s.ID, "/", allowedAddr)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { _, _ = h.login(s.ID, "/", "192.0.2.1", allowedAddr, otherCode(code)) })
	}
	wg.Wait()
	last := h.codes.lastPut(t)
	if got, err := h.db.EmailCodes().Get(t.Context(), last.SiteID, last.PolicyID, last.AddressHash); err != nil || got.Attempts != access.EmailCodeAttempts {
		t.Fatalf("after 20 parallel guesses: %+v, %v", got, err)
	}

	// A new code replaces the old one, and its attempts start again.
	old := h.codeSent(s.ID, "/", allowedAddr)
	fresh := h.codeSent(s.ID, "/", allowedAddr)
	if old != fresh {
		if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, old); !errors.Is(err, access.ErrInvalidCode) {
			t.Fatalf("replaced code: %v", err)
		}
	}
	if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, fresh); err != nil {
		t.Fatalf("new code: %v", err)
	}
}

// TestEmailCodePolicyChanges shows codes and sessions are bound to the
// policy version: an allowlist change ends sessions (revoked and stale)
// and kills codes sent before it, as does turning the method off.
func TestEmailCodePolicyChanges(t *testing.T) {
	h := newEmailHarness(t, access.Limits{})
	o := h.owner()
	s := h.site(o, "harbour")
	list := store.Allowlist{Emails: []string{allowedAddr}, Domains: []string{"crew.test"}}
	first := h.set(o, "harbour", emailRule("/", list)).Version

	code := h.codeSent(s.ID, "/", allowedAddr)
	c, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, code)
	if err != nil {
		t.Fatal(err)
	}
	pending := h.codeSent(s.ID, "/", "cook@crew.test")

	// The same entries in another spelling and order change nothing.
	same := h.set(o, "harbour", emailRule("/", store.Allowlist{Domains: []string{"CREW.test."}, Emails: []string{allowedAddr, allowedAddr}}))
	if same.Version != first {
		t.Fatalf("same allowlist bumped the version: %+v", same)
	}
	// Keeping the allowlist (nil) changes nothing either.
	if kept := h.set(o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Email: true}); kept.Version != first || !kept.Allowlist.Equal(list) {
		t.Fatalf("keep: %+v", kept)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: c.Value}); d.Outcome != access.Allow {
		t.Fatalf("session after an unchanged set: %+v", d)
	}

	// Adding an entry is a change: the session ends and so does the code
	// sent before it, even for an address still allowed.
	changed := h.set(o, "harbour", emailRule("/", store.Allowlist{Emails: []string{allowedAddr, "bo@example.test"}, Domains: []string{"crew.test"}}))
	if changed.Version != first+1 {
		t.Fatalf("change: %+v", changed)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: c.Value}); d.Outcome == access.Allow || !d.ClearCookie {
		t.Fatalf("session after an allowlist change: %+v", d)
	}
	if _, ss := h.codes.stored(); len(ss) != 1 || h.live(ss[0]) {
		t.Fatalf("the email session was not revoked by the change: %+v", ss)
	}
	if _, err := h.login(s.ID, "/", "192.0.2.1", "cook@crew.test", pending); !errors.Is(err, access.ErrInvalidCode) {
		t.Fatalf("a code sent before the change: %v", err)
	}

	// Turning the method off keeps the allowlist (OAuth shares it) and is a
	// change; a code sent before it no longer works.
	code = h.codeSent(s.ID, "/", allowedAddr)
	off := h.set(o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true})
	if off.Version != first+2 || off.EmailEnabled || off.Allowlist.Len() != 3 {
		t.Fatalf("email off: %+v", off)
	}
	if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, code); !errors.Is(err, access.ErrInvalidCode) {
		t.Fatalf("code after the method was turned off: %v", err)
	}
	// Turning it back on with the kept allowlist works again.
	h.set(o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Email: true})
	code = h.codeSent(s.ID, "/", "bo@example.test")

	// A change that commits between the check and the session insert
	// leaves no session: the sign-in is refused like a wrong code.
	h.codes.beforeCreate = func() {
		h.set(o, "harbour", emailRule("/", store.Allowlist{Emails: []string{"bo@example.test"}}))
	}
	if _, err := h.login(s.ID, "/", "192.0.2.1", "bo@example.test", code); !errors.Is(err, access.ErrInvalidCode) {
		t.Fatalf("sign-in racing a change: %v", err)
	}
	if _, ss := h.codes.stored(); len(ss) != 1 {
		t.Fatalf("a session was stored by the racing sign-in: %+v", ss)
	}

	// Making the policy public clears the allowlist.
	pub := h.set(o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Public})
	if pub.Allowlist.Len() != 0 {
		t.Fatalf("public policy kept %+v", pub.Allowlist)
	}
	if _, err := h.svc.Set(t.Context(), o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Email: true}); !errors.Is(err, access.ErrInvalidPolicy) {
		t.Fatalf("email with the cleared allowlist kept: %v", err)
	}
}

func TestEmailCodeSetValidation(t *testing.T) {
	h := newEmailHarness(t, access.Limits{})
	a, b := h.owner(), h.owner()
	h.site(a, "harbour")
	list := func(entries ...string) *store.Allowlist {
		l := &store.Allowlist{}
		for _, e := range entries {
			if strings.Contains(e, "@") {
				l.Emails = append(l.Emails, e)
			} else {
				l.Domains = append(l.Domains, e)
			}
		}
		return l
	}
	for _, tc := range []struct {
		name   string
		rule   access.Rule
		target string
	}{
		{"email with no allowlist", access.Rule{PathPrefix: "/", Visibility: store.Protected, Email: true, Allowlist: list()}, "allowlist"},
		{"email keeping none", access.Rule{PathPrefix: "/", Visibility: store.Protected, Email: true}, "allowlist"},
		{"email with GitHub orgs only", access.Rule{PathPrefix: "/", Visibility: store.Protected, Email: true, Allowlist: &store.Allowlist{GitHubOrgs: []string{"example-org"}}}, "allowlist"},
		{"public with email", access.Rule{PathPrefix: "/", Visibility: store.Public, Email: true}, "methods"},
		{"public with an allowlist", access.Rule{PathPrefix: "/", Visibility: store.Public, Allowlist: list("crew.test")}, "allowlist"},
		{"display name", emailRule("/", *list("Ana <ana@example.test>")), "allowlist"},
		{"no domain", emailRule("/", *list("ana@")), "allowlist"},
		{"single-label domain", emailRule("/", *list("localhost")), "allowlist"},
		{"IP address", emailRule("/", *list("192.0.2.1")), "allowlist"},
		{"Unicode domain", emailRule("/", *list("bücher.example")), "allowlist"},
		{"bare wildcard", emailRule("/", *list("*.test")), "allowlist"},
		{"inner wildcard", emailRule("/", *list("a.*.test")), "allowlist"},
		{"hyphen edge", emailRule("/", *list("-crew.test")), "allowlist"},
		{"empty label", emailRule("/", *list("crew..test")), "allowlist"},
		{"long label", emailRule("/", *list(strings.Repeat("a", 64) + ".test")), "allowlist"},
		{"long wildcard", emailRule("/", *list("*." + longDomain)), "allowlist"},
		{"long domain", emailRule("/", *list("a" + longDomain)), "allowlist"},
	} {
		_, err := h.svc.Set(t.Context(), a, "harbour", tc.rule)
		var te *access.TargetError
		if !errors.Is(err, access.ErrInvalidPolicy) || !errors.As(err, &te) || te.Target != tc.target {
			t.Errorf("%s: %v, want invalid_policy on %s", tc.name, err, tc.target)
		}
	}
	tooMany := &store.Allowlist{}
	for i := range access.MaxAllowlistEntries + 1 {
		tooMany.Emails = append(tooMany.Emails, "v"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+"@example.test")
	}
	if _, err := h.svc.Set(t.Context(), a, "harbour", emailRule("/", *tooMany)); !errors.Is(err, access.ErrInvalidPolicy) {
		t.Errorf("too many entries: %v", err)
	}

	// Entries are normalised: lower case, one trailing dot dropped,
	// sorted, duplicates merged; A-labels are accepted.
	p := h.set(a, "harbour", emailRule("/", *list("Bo@Example.TEST", "ana@example.test", "bo@example.test", "Crew.Test.", "*.XN--BCHER-KVA.example")))
	want := store.Allowlist{Emails: []string{"ana@example.test", "bo@example.test"}, Domains: []string{"*.xn--bcher-kva.example", "crew.test"}}
	if !p.Allowlist.Equal(want) {
		t.Fatalf("normalised %+v, want %+v", p.Allowlist, want)
	}

	// The longest names that fit are stored: 253 bytes with or without
	// the wildcard.
	longest := h.set(a, "harbour", emailRule("/", *list(longDomain, "*."+longDomain[2:])))
	if len(longest.Allowlist.Domains) != 2 || len(longest.Allowlist.Domains[0]) != 253 || len(longest.Allowlist.Domains[1]) != 253 {
		t.Fatalf("longest domains: %+v", longest.Allowlist)
	}
	p = h.set(a, "harbour", emailRule("/", *list("Bo@Example.TEST", "ana@example.test", "bo@example.test", "Crew.Test.", "*.XN--BCHER-KVA.example")))

	// Owner isolation: owner B gets the same not-found for A's site as for
	// a missing one, and A's allowlist is untouched.
	for _, site := range []string{"harbour", "missing"} {
		if _, err := h.svc.Set(t.Context(), b, site, emailRule("/", *list("eve@example.test"))); !errors.Is(err, access.ErrNotFound) {
			t.Errorf("owner B on %s: %v, want ErrNotFound", site, err)
		}
	}
	ps, err := h.svc.List(t.Context(), a, "harbour", "", 10)
	if err != nil || len(ps) != 1 || !ps[0].Allowlist.Equal(want) {
		t.Fatalf("after owner B: %+v, %v", ps, err)
	}
	if ps, err := h.svc.List(t.Context(), b, "harbour", "", 10); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("owner B lists %+v, %v", ps, err)
	}
}

// longDomain is a 253-byte domain, the longest DNS name.
var longDomain = strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 56) + ".test"

// TestEmailCodeAddressTable shows per-address keys live in a bounded
// table of their own: inventing addresses until it is full refuses only
// new addresses' email codes, never password attempts, per-IP limits or
// addresses already counted.
func TestEmailCodeAddressTable(t *testing.T) {
	h := newEmailHarness(t, access.Limits{})
	access.SetAddressTableSize(h.svc, 3)
	o := h.owner()
	s := h.site(o, "harbour")
	other := h.site(o, "tides")
	h.set(o, "harbour", emailRule("/", store.Allowlist{Domains: []string{"crew.test"}}))
	h.set(o, "tides", passwordRule("/", firstPassword))
	limited := func(name string, err error) {
		t.Helper()
		var rl *access.RateLimitError
		if !errors.As(err, &rl) {
			t.Fatalf("%s: %v, want a RateLimitError", name, err)
		}
	}
	for i, addr := range []string{"a@crew.test", "b@crew.test", "c@crew.test"} {
		if err := h.send(s.ID, "/", "192.0.2."+string(rune('1'+i)), addr); err != nil {
			t.Fatalf("send to %s: %v", addr, err)
		}
	}
	// The table is full: a new address is refused, sending and verifying.
	limited("send to a new address", h.send(s.ID, "/", "192.0.2.9", "d@crew.test"))
	_, err := h.login(s.ID, "/", "192.0.2.9", "d@crew.test", "123456")
	limited("verify for a new address", err)
	// Counted addresses, per-IP keys and password attempts are unaffected.
	if err := h.send(s.ID, "/", "198.51.100.1", "a@crew.test"); err != nil {
		t.Fatalf("send to a counted address: %v", err)
	}
	for _, ip := range []string{"198.51.100.2", "198.51.100.3", "2001:db8:9::1"} {
		c, err := h.svc.PasswordLogin(t.Context(), access.PasswordAttempt{SiteID: other.ID, Path: "/", IP: ip, Password: firstPassword})
		if err != nil || c.Value == "" {
			t.Fatalf("password from %s with the address table full: %v", ip, err)
		}
	}
	if _, err := h.svc.PasswordLogin(t.Context(), access.PasswordAttempt{SiteID: other.ID, Path: "/", IP: "198.51.100.4", Password: "wrong-guess"}); !errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("wrong password with the address table full: %v", err)
	}
}

func TestEmailCodeRateLimits(t *testing.T) {
	limits := access.DefaultLimits()
	limits.EmailSendPerIP = access.Rate{N: 3, Per: time.Hour}
	limits.EmailSendPerAddress = access.Rate{N: 2, Per: time.Hour}
	limits.EmailSendPerSite = access.Rate{N: 5, Per: time.Hour}
	limits.EmailVerifyPerIP = access.Rate{N: 3, Per: 15 * time.Minute}
	limits.EmailVerifyPerAddress = access.Rate{N: 4, Per: 15 * time.Minute}
	limits.EmailVerifyPerSite = access.Rate{N: 6, Per: 15 * time.Minute}
	limited := func(t *testing.T, name string, err error) {
		t.Helper()
		var rl *access.RateLimitError
		if !errors.As(err, &rl) || rl.RetryAfter <= 0 {
			t.Fatalf("%s: %v, want a RateLimitError", name, err)
		}
	}
	setup := func(t *testing.T) (*emailHarness, store.Site, store.Site) {
		h := newEmailHarness(t, limits)
		o := h.owner()
		s, other := h.site(o, "harbour"), h.site(o, "tides")
		h.set(o, "harbour", emailRule("/", store.Allowlist{Domains: []string{"crew.test"}}))
		h.set(o, "tides", emailRule("/", store.Allowlist{Domains: []string{"crew.test"}}))
		return h, s, other
	}

	t.Run("send per IP", func(t *testing.T) {
		h, s, other := setup(t)
		// Allowed and disallowed addresses count alike.
		for _, addr := range []string{"a@crew.test", disallowAddr, "b@crew.test"} {
			if err := h.send(s.ID, "/", "2001:db8:1:2::1", addr); err != nil {
				t.Fatal(err)
			}
		}
		// Across sites, and an IPv6 /64 is one client.
		limited(t, "fourth send", h.send(other.ID, "/", "2001:db8:1:2::ff", "c@crew.test"))
		if err := h.send(s.ID, "/", "2001:db8:1:3::1", "c@crew.test"); err != nil {
			t.Fatalf("another /64: %v", err)
		}
		h.clock.advance(time.Hour)
		if err := h.send(s.ID, "/", "2001:db8:1:2::1", "d@crew.test"); err != nil {
			t.Fatalf("after the window: %v", err)
		}
	})
	t.Run("send per address", func(t *testing.T) {
		h, s, other := setup(t)
		for i, ip := range []string{"192.0.2.1", "192.0.2.2"} {
			if err := h.send(s.ID, "/", ip, "a@crew.test"); err != nil {
				t.Fatalf("send %d: %v", i+1, err)
			}
		}
		// The address is counted across sites and spellings, so no inbox
		// is flooded.
		limited(t, "third send to one address", h.send(other.ID, "/", "192.0.2.3", " A@Crew.Test"))
		h.mailer.take()
		if err := h.send(s.ID, "/", "192.0.2.3", "b@crew.test"); err != nil {
			t.Fatal(err)
		}
		h.clock.advance(time.Hour)
		if err := h.send(s.ID, "/", "192.0.2.4", "a@crew.test"); err != nil || len(h.mailer.take()) != 2 {
			t.Fatalf("after the window: %v", err)
		}
	})
	t.Run("send per site", func(t *testing.T) {
		h, s, other := setup(t)
		for i := range 5 {
			if err := h.send(s.ID, "/", "192.0.2."+string(rune('1'+i)), string(rune('a'+i))+"@crew.test"); err != nil {
				t.Fatal(err)
			}
		}
		limited(t, "sixth send to the site", h.send(s.ID, "/", "198.51.100.1", "z@crew.test"))
		if err := h.send(other.ID, "/", "198.51.100.1", "z@crew.test"); err != nil {
			t.Fatalf("another site: %v", err)
		}
	})
	t.Run("verify per IP and address", func(t *testing.T) {
		h, s, _ := setup(t)
		code := h.codeSent(s.ID, "/", "a@crew.test")
		for range 3 {
			_, _ = h.login(s.ID, "/", "192.0.2.1", "a@crew.test", otherCode(code))
		}
		// Over the limit even the right code is refused.
		_, err := h.login(s.ID, "/", "192.0.2.1", "a@crew.test", code)
		limited(t, "fourth verify from one IP", err)
		_, err = h.login(s.ID, "/", "192.0.2.2", "a@crew.test", otherCode(code))
		if !errors.Is(err, access.ErrInvalidCode) {
			t.Fatalf("fourth for the address: %v", err)
		}
		// The address's fifth attempt, from a fresh IP, is over its limit.
		_, err = h.login(s.ID, "/", "192.0.2.3", "a@crew.test", code)
		limited(t, "fifth verify for one address", err)
		h.clock.advance(15 * time.Minute)
		if _, err := h.login(s.ID, "/", "192.0.2.1", "b@crew.test", "123456"); !errors.Is(err, access.ErrInvalidCode) {
			t.Fatalf("after the window: %v", err)
		}
	})
	t.Run("verify per site", func(t *testing.T) {
		h, s, other := setup(t)
		for i := range 6 {
			_, _ = h.login(s.ID, "/", "192.0.2."+string(rune('1'+i)), string(rune('a'+i))+"@crew.test", "123456")
		}
		_, err := h.login(s.ID, "/", "198.51.100.1", "z@crew.test", "123456")
		limited(t, "seventh verify on the site", err)
		if _, err := h.login(other.ID, "/", "198.51.100.1", "z@crew.test", "123456"); !errors.Is(err, access.ErrInvalidCode) {
			t.Fatalf("another site: %v", err)
		}
	})
}

// TestEmailCodeNeverLogged checks that neither codes nor addresses reach
// the log, and that only HMACs are stored.
func TestEmailCodeNeverLogged(t *testing.T) {
	h := newEmailHarness(t, access.Limits{})
	o := h.owner()
	s := h.site(o, "harbour")
	h.set(o, "harbour", emailRule("/", store.Allowlist{Emails: []string{allowedAddr}}))
	code := h.codeSent(s.ID, "/", allowedAddr)
	_, _ = h.login(s.ID, "/", "192.0.2.1", allowedAddr, otherCode(code))
	if _, err := h.login(s.ID, "/", "192.0.2.1", allowedAddr, code); err != nil {
		t.Fatal(err)
	}
	// A server error that echoes the recipient, as RCPT TO replies often
	// do, still leaves the address out of the log.
	h.mailer.err = errors.New("rcpt to: 550 5.1.1 <" + allowedAddr + ">: Recipient address rejected")
	if err := h.send(s.ID, "/", "192.0.2.1", allowedAddr); err != nil {
		t.Fatal(err)
	}
	logs := h.log.String()
	for _, secret := range []string{code, otherCode(code), allowedAddr} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains %q:\n%s", secret, logs)
		}
	}
	for _, want := range []string{"email code sent", "email code refused", "email code sign-in", "email code failed"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
	h.codes.mu.Lock()
	defer h.codes.mu.Unlock()
	for _, c := range h.codes.puts {
		if len(c.CodeHash) != 64 || len(c.AddressHash) != 64 || strings.Contains(c.CodeHash, code) || strings.Contains(c.AddressHash, "example") {
			t.Errorf("stored code %+v is not HMACs only", c)
		}
	}
}
