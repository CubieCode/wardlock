// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// Synthetic passwords for tests only.
const (
	firstPassword  = "harbour-lights-7"
	secondPassword = "tide-chart-42"
)

// racingSessions runs beforeCreate once just before the next
// CreateSession, to interleave a password change with a sign-in.
type racingSessions struct {
	store.PasswordSessions
	mu           sync.Mutex
	beforeCreate func()
}

func (r *racingSessions) CreateSession(ctx context.Context, s store.SiteSession, hash string) error {
	r.mu.Lock()
	fn := r.beforeCreate
	r.beforeCreate = nil
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return r.PasswordSessions.CreateSession(ctx, s, hash)
}

// racingPolicies runs beforeSet once just before the next Set, to
// interleave two owners' writes.
type racingPolicies struct {
	store.Policies
	mu        sync.Mutex
	beforeSet func()
}

func (r *racingPolicies) Set(ctx context.Context, actor store.Actor, p store.Policy) (store.Policy, error) {
	r.mu.Lock()
	fn := r.beforeSet
	r.beforeSet = nil
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return r.Policies.Set(ctx, actor, p)
}

type passwordHarness struct {
	*harness
	racing  *racingSessions
	writes  *racingPolicies
	limiter *access.FakeLimiter
}

// newPasswordHarness is newHarness with password limits small enough to
// reach in a test, counted by a FakeLimiter.
func newPasswordHarness(t *testing.T, limits access.Limits) *passwordHarness {
	t.Helper()
	h := newRealLimiterHarness(t, limits)
	h.limiter = access.NewFakeLimiter(h.clock.Now)
	access.SetAttemptLimiter(h.svc, h.limiter)
	return h
}

// newRealLimiterHarness counts attempts with the service's own limiters.
func newRealLimiterHarness(t *testing.T, limits access.Limits) *passwordHarness {
	t.Helper()
	db := storetest.New(t)
	h := &harness{t: t, db: db, clock: &clock{now: start}, log: &bytes.Buffer{}}
	h.policies = &countingPolicies{Policies: db.Policies()}
	rv := &racingSessions{PasswordSessions: db.PasswordSessions()}
	wr := &racingPolicies{Policies: h.policies}
	svc, err := access.New(access.Config{
		Sites: db.Sites(), Policies: wr, Sessions: db.Sessions(), Links: db.Links(), PasswordSessions: rv, EmailCodes: db.EmailCodes(),
		Notifier: &notifier{}, Secret: secret, Now: h.clock.Now,
		Log:    slog.New(slog.NewTextHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	h.svc = svc
	return &passwordHarness{harness: h, racing: rv, writes: wr}
}

func passwordRule(prefix, pw string) access.Rule {
	return access.Rule{PathPrefix: prefix, Visibility: store.Protected, Password: true, NewPassword: pw}
}

func (h *passwordHarness) login(siteID, path, ip, pw string) (access.Cookie, error) {
	return h.svc.PasswordLogin(h.t.Context(), access.PasswordAttempt{SiteID: siteID, Path: path, IP: ip, Password: pw})
}

func TestPasswordLogin(t *testing.T) {
	h := newPasswordHarness(t, access.Limits{})
	o := h.owner()
	s := h.site(o, "harbour")
	other := h.site(o, "tides")
	def := h.set(o, "harbour", passwordRule("/", firstPassword))
	if !def.PasswordEnabled || !strings.HasPrefix(def.PasswordHash, "$argon2id$") {
		t.Fatalf("policy %+v has no Argon2id password", def)
	}
	h.set(o, "harbour", access.Rule{PathPrefix: "/links/", Visibility: store.Protected, Links: true})
	h.set(o, "harbour", passwordRule("/staff/", secondPassword))
	h.set(o, "tides", passwordRule("/", secondPassword))

	c, err := h.login(s.ID, "/report/", "192.0.2.1", firstPassword)
	if err != nil {
		t.Fatalf("correct password: %v", err)
	}
	if c.Name != access.SessionCookieName || !c.Expires.Equal(start.Add(access.SessionLifetime)) {
		t.Fatalf("cookie %+v", c)
	}
	d := h.decide(access.Request{SiteID: s.ID, Path: "/report/", Cookie: c.Value})
	if d.Outcome != access.Allow || d.Via != access.ViaPassword {
		t.Fatalf("password session: %+v", d)
	}
	// The session is the default policy's: it does not open a path policy
	// with its own password, nor another site.
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/staff/", Cookie: c.Value}); d.Outcome == access.Allow {
		t.Fatalf("default's session opened /staff/: %+v", d)
	}
	if d := h.decide(access.Request{SiteID: other.ID, Path: "/", Cookie: c.Value}); d.Outcome == access.Allow || !d.ClearCookie {
		t.Fatalf("session opened another site: %+v", d)
	}

	for _, tc := range []struct{ name, site, path, pw string }{
		{"wrong password", s.ID, "/", "harbour-lights-8"},
		{"empty password", s.ID, "/", ""},
		{"a path policy's password on the default", s.ID, "/", secondPassword},
		{"policy without a password", s.ID, "/links/page", firstPassword},
		{"another site's password", other.ID, "/", firstPassword},
		{"longer than any password", s.ID, "/", firstPassword + strings.Repeat("x", access.MaxPasswordBytes)},
	} {
		if _, err := h.login(tc.site, tc.path, "192.0.2.2", tc.pw); !errors.Is(err, access.ErrWrongPassword) {
			t.Errorf("%s: %v, want ErrWrongPassword", tc.name, err)
		}
	}
	if c, err := h.login(s.ID, "/staff/handbook", "192.0.2.3", secondPassword); err != nil {
		t.Fatalf("path policy's password: %v", err)
	} else if d := h.decide(access.Request{SiteID: s.ID, Path: "/staff/handbook", Cookie: c.Value}); d.Outcome != access.Allow {
		t.Fatalf("path policy session: %+v", d)
	}
}

func TestPasswordRateLimits(t *testing.T) {
	limits := access.Limits{PasswordPerIP: access.Rate{N: 3, Per: 15 * time.Minute}, PasswordPerSite: access.Rate{N: 5, Per: 15 * time.Minute}}
	t.Run("fake limiter", func(t *testing.T) { testPasswordRateLimits(t, newPasswordHarness(t, limits)) })
	t.Run("service limiters", func(t *testing.T) { testPasswordRateLimits(t, newRealLimiterHarness(t, limits)) })
}

func testPasswordRateLimits(t *testing.T, h *passwordHarness) {
	o := h.owner()
	s := h.site(o, "harbour")
	other := h.site(o, "tides")
	h.set(o, "harbour", passwordRule("/", firstPassword))
	h.set(o, "tides", passwordRule("/", firstPassword))

	limited := func(name string, err error) {
		t.Helper()
		var rl *access.RateLimitError
		if !errors.As(err, &rl) || !errors.Is(err, access.ErrRateLimited) || rl.RetryAfter <= 0 || rl.RetryAfter > 15*time.Minute {
			t.Fatalf("%s: %v, want a RateLimitError", name, err)
		}
	}
	for i := range 3 {
		if _, err := h.login(s.ID, "/", "192.0.2.1", "wrong-guess"); !errors.Is(err, access.ErrWrongPassword) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	// Over the limit even the right password is refused, so guessing
	// cannot continue behind it.
	_, err := h.login(s.ID, "/", "192.0.2.1", firstPassword)
	limited("fourth attempt from one IP", err)
	// The per-IP limit spans sites, and an IPv6 /64 counts as one client.
	_, err = h.login(other.ID, "/", "192.0.2.1", firstPassword)
	limited("same IP on another site", err)
	for i := range 3 {
		_, _ = h.login(s.ID, "/", "2001:db8:5:6::"+string(rune('a'+i)), "wrong-guess")
	}
	_, err = h.login(other.ID, "/", "2001:db8:5:6::ff", firstPassword)
	limited("same IPv6 /64", err)

	// The site limit counts attempts from every client: 3 from 192.0.2.1,
	// 1 from the /64 (its network's share once the site was under
	// pressure) and 1 from 203.0.113.7 reach it.
	if _, err := h.login(s.ID, "/", "203.0.113.7", "wrong-guess"); !errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("fifth attempt on the site: %v", err)
	}
	_, err = h.login(s.ID, "/", "198.51.100.7", firstPassword)
	limited("site limit", err)
	if _, err := h.login(other.ID, "/", "198.51.100.8", firstPassword); err != nil {
		t.Fatalf("another site is not limited: %v", err)
	}

	// Keys name the client or the site, never the password.
	for _, k := range h.limiterKeys() {
		if !strings.HasPrefix(k, "password-ip:") && !strings.HasPrefix(k, "password-site:") && !strings.HasPrefix(k, "password-net:") || strings.Contains(k, firstPassword) {
			t.Fatalf("limiter key %q", k)
		}
	}

	// Limits reset when their window ends.
	h.clock.advance(15 * time.Minute)
	if _, err := h.login(s.ID, "/", "192.0.2.1", firstPassword); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// Attempts are counted before the (slow) check, so parallel guesses cannot
// all slip under the limit while Argon2id runs.
func TestParallelAttemptsStayWithinTheLimit(t *testing.T) {
	h := newRealLimiterHarness(t, access.Limits{PasswordPerIP: access.Rate{N: 3, Per: time.Minute}, PasswordPerSite: access.Rate{N: 100, Per: time.Minute}})
	o := h.owner()
	s := h.site(o, "harbour")
	h.set(o, "harbour", passwordRule("/", firstPassword))
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked, limited := 0, 0
	for range 20 {
		wg.Go(func() {
			_, err := h.login(s.ID, "/", "192.0.2.1", "wrong-guess")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, access.ErrWrongPassword):
				checked++
			case errors.Is(err, access.ErrRateLimited):
				limited++
			default:
				t.Errorf("attempt: %v", err)
			}
		})
	}
	wg.Wait()
	if checked != 3 || limited != 17 {
		t.Fatalf("%d checked, %d limited; want 3 and 17", checked, limited)
	}
}

func TestSetPassword(t *testing.T) {
	h := newPasswordHarness(t, access.Limits{})
	ctx := t.Context()
	a, b := h.owner(), h.owner()
	s := h.site(a, "harbour")
	h.site(b, "beta")

	for _, tc := range []struct {
		name string
		rule access.Rule
		want error
	}{
		{"password method without a password", passwordRule("/", ""), access.ErrInvalidPolicy},
		{"short password", passwordRule("/", "short"), access.ErrInvalidPolicy},
		{"long password", passwordRule("/", strings.Repeat("x", access.MaxPasswordBytes+1)), access.ErrInvalidPolicy},
		{"invalid UTF-8", passwordRule("/", "harbour\xff-lights"), access.ErrInvalidPolicy},
		{"public with a password", access.Rule{PathPrefix: "/", Visibility: store.Public, Password: true, NewPassword: firstPassword}, access.ErrInvalidPolicy},
		{"password without the method", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, NewPassword: firstPassword}, access.ErrInvalidPolicy},
	} {
		_, err := h.svc.Set(ctx, store.ActorFor(a), "harbour", tc.rule)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
		var te *access.TargetError
		if errors.As(err, &te) && tc.rule.NewPassword != "" && strings.Contains(te.Message, tc.rule.NewPassword) {
			t.Errorf("%s: message %q repeats the password", tc.name, te.Message)
		}
	}
	// Eight characters, not bytes, is the minimum.
	if _, err := h.svc.Set(ctx, store.ActorFor(a), "harbour", passwordRule("/", "ŵaŵaŵaŵ")); !errors.Is(err, access.ErrInvalidPolicy) {
		t.Fatalf("seven characters: %v", err)
	}

	v1 := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true, NewPassword: firstPassword})
	pw := h.loginCookie(s.ID, firstPassword)
	link := h.session(s.ID, v1, store.SessionLink)

	// Keeping the method without a new password keeps the hash and the
	// sessions: nothing changed.
	same := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true})
	if same.Version != v1.Version || same.PasswordHash != v1.PasswordHash {
		t.Fatalf("unchanged settings: %+v, was %+v", same, v1)
	}
	h.allowed("unchanged", s.ID, pw, true)

	// B cannot change A's password, and gets the same error as for a
	// missing site; A's password and sessions stay.
	if _, err := h.svc.Set(ctx, store.ActorFor(b), "harbour", passwordRule("/", secondPassword)); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("B sets A's password: %v", err)
	}
	if _, err := h.svc.Set(ctx, store.ActorFor(b), "beta", passwordRule("/", secondPassword)); err != nil {
		t.Fatal(err)
	}
	h.allowed("after B's attempt", s.ID, pw, true)
	if got := h.policyFor(s.ID, "/"); got.PasswordHash != v1.PasswordHash {
		t.Fatal("B's attempt changed A's password")
	}

	// A new password, even the same one again, is a change: it bumps the
	// version and revokes the policy's password sessions.
	v2 := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true, NewPassword: firstPassword})
	if v2.Version != v1.Version+1 || v2.PasswordHash == v1.PasswordHash {
		t.Fatalf("re-set password: %+v", v2)
	}
	h.allowed("old password session", s.ID, pw, false)
	h.allowed("link session across a version bump", s.ID, link, false)

	pw = h.loginCookie(s.ID, firstPassword)
	v3 := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true, NewPassword: secondPassword})
	h.allowed("session after a change", s.ID, pw, false)
	if _, err := h.login(s.ID, "/", "192.0.2.9", firstPassword); !errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("old password after a change: %v", err)
	}
	pw = h.loginCookie(s.ID, secondPassword)

	// Removing the method clears the hash and revokes too.
	off := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true})
	if off.PasswordEnabled || off.PasswordHash != "" || off.Version != v3.Version+1 {
		t.Fatalf("password removed: %+v", off)
	}
	h.allowed("after removal", s.ID, pw, false)
	if _, err := h.login(s.ID, "/", "192.0.2.9", secondPassword); !errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("password after removal: %v", err)
	}
	// Turning it back on needs a new password.
	if _, err := h.svc.Set(ctx, store.ActorFor(a), "harbour", passwordRule("/", "")); !errors.Is(err, access.ErrInvalidPolicy) {
		t.Fatalf("re-enable without a password: %v", err)
	}
}

func (h *passwordHarness) limiterKeys() []string {
	if h.limiter == nil {
		return nil
	}
	return h.limiter.Keys()
}

func (h *passwordHarness) loginCookie(siteID, pw string) string {
	h.t.Helper()
	c, err := h.login(siteID, "/", "203.0.113.1", pw)
	if err != nil {
		h.t.Fatalf("login: %v", err)
	}
	return c.Value
}

func (h *passwordHarness) allowed(name, siteID, cookie string, want bool) {
	h.t.Helper()
	d := h.decide(access.Request{SiteID: siteID, Path: "/", Cookie: cookie})
	if got := d.Outcome == access.Allow; got != want {
		h.t.Fatalf("%s: %+v, want allowed %v", name, d, want)
	}
}

// A password change that commits between a sign-in's check and its session
// insert leaves no session behind: the insert is conditional on the
// password hash and version that were checked.
func TestPasswordChangeRacingASignIn(t *testing.T) {
	h := newPasswordHarness(t, access.Limits{})
	o := h.owner()
	s := h.site(o, "harbour")
	old := h.set(o, "harbour", passwordRule("/", firstPassword))
	h.racing.mu.Lock()
	h.racing.beforeCreate = func() { h.set(o, "harbour", passwordRule("/", secondPassword)) }
	h.racing.mu.Unlock()
	if _, err := h.login(s.ID, "/", "192.0.2.1", firstPassword); !errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("sign-in raced by a change: %v, want ErrWrongPassword", err)
	}
	// Issuing directly with the stale policy is refused too.
	for name, n := range map[string]access.NewSession{
		"old hash and version":      {SiteID: s.ID, Policy: old, Method: store.SessionPassword, PasswordHash: old.PasswordHash},
		"current version, old hash": {SiteID: s.ID, Policy: h.policyFor(s.ID, "/"), Method: store.SessionPassword, PasswordHash: old.PasswordHash},
		"no hash":                   {SiteID: s.ID, Policy: h.policyFor(s.ID, "/"), Method: store.SessionPassword},
	} {
		if _, err := h.svc.IssueSession(t.Context(), n); err == nil {
			t.Errorf("%s: issued a session", name)
		}
	}
	if _, err := h.login(s.ID, "/", "192.0.2.1", secondPassword); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

// "Keep the current password" is resolved inside the store's transaction:
// a password change that lands while another write keeps the password is
// never undone, so the old password cannot come back.
func TestKeepingThePasswordNeverRevivesAnOldOne(t *testing.T) {
	h := newPasswordHarness(t, access.Limits{})
	o := h.owner()
	s := h.site(o, "harbour")
	h.set(o, "harbour", passwordRule("/", firstPassword))
	h.writes.mu.Lock()
	h.writes.beforeSet = func() { h.set(o, "harbour", passwordRule("/", secondPassword)) }
	h.writes.mu.Unlock()
	// Read the policy, then (racing) keep its password while turning links on.
	kept := h.set(o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true})
	if !kept.LinksEnabled {
		t.Fatalf("kept %+v", kept)
	}
	if _, err := h.login(s.ID, "/", "192.0.2.1", firstPassword); !errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("old password after the racing keep: %v", err)
	}
	if _, err := h.login(s.ID, "/", "192.0.2.2", secondPassword); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestPasswordLoginNeedsALimiter(t *testing.T) {
	h := newPasswordHarness(t, access.Limits{})
	o := h.owner()
	s := h.site(o, "harbour")
	h.set(o, "harbour", passwordRule("/", firstPassword))
	access.SetAttemptLimiter(h.svc, nil)
	if _, err := h.login(s.ID, "/", "192.0.2.1", firstPassword); err == nil || errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("login without a limiter: %v, want an internal error", err)
	}
}

func TestLogsNeverContainPasswords(t *testing.T) {
	h := newPasswordHarness(t, access.Limits{PasswordPerIP: access.Rate{N: 2, Per: time.Minute}, PasswordPerSite: access.Rate{N: 10, Per: time.Minute}})
	o := h.owner()
	s := h.site(o, "harbour")
	p := h.set(o, "harbour", passwordRule("/", firstPassword))
	h.set(o, "harbour", passwordRule("/", secondPassword))
	_, _ = h.svc.Set(t.Context(), store.ActorFor(o), "harbour", passwordRule("/", "short"))
	c, err := h.login(s.ID, "/", "192.0.2.1", secondPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.login(s.ID, "/", "192.0.2.1", firstPassword)
	_, _ = h.login(s.ID, "/", "192.0.2.1", firstPassword)
	out := h.log.String()
	for _, needle := range []string{"password sign-in refused", "password sign-in", "policy set"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("log lacks %q:\n%s", needle, out)
		}
	}
	enc, sig, _ := strings.Cut(c.Value, ".")
	for _, secret := range []string{firstPassword, secondPassword, "short", p.PasswordHash, enc, sig} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains a secret (%d bytes):\n%s", len(secret), out)
		}
	}
}

// However many attempts pass the limiters, at most the service's hash slots
// run Argon2id at once; an attempt that cannot get a slot in time is told
// to retry, never left waiting.
func TestPasswordChecksAreBounded(t *testing.T) {
	h := newPasswordHarness(t, access.Limits{PasswordPerIP: access.Rate{N: 100, Per: time.Minute}, PasswordPerSite: access.Rate{N: 100, Per: time.Minute}})
	o := h.owner()
	s := h.site(o, "harbour")
	h.set(o, "harbour", passwordRule("/", firstPassword))

	var mu sync.Mutex
	running, peak := 0, 0
	release := make(chan struct{})
	access.SetDerive(h.svc, func(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
		return make([]byte, n)
	})
	access.SetHashSlots(h.svc, 2, 200*time.Millisecond)

	const attempts = 12
	errs := make(chan error, attempts)
	for i := range attempts {
		go func() {
			_, err := h.login(s.ID, "/", "192.0.2."+strconv.Itoa(i+1), "wrong-guess")
			errs <- err
		}()
	}
	// Those that found no slot give up with a RateLimitError.
	busy := 0
	for range attempts - 2 {
		err := <-errs
		var rl *access.RateLimitError
		if !errors.As(err, &rl) || rl.RetryAfter != time.Second {
			t.Fatalf("attempt without a slot: %v, want RateLimitError retry 1s", err)
		}
		busy++
	}
	close(release)
	for range 2 {
		if err := <-errs; !errors.Is(err, access.ErrWrongPassword) {
			t.Fatalf("attempt with a slot: %v", err)
		}
	}
	if peak != 2 || busy != attempts-2 {
		t.Fatalf("peak %d concurrent checks, %d busy; want 2 and %d", peak, busy, attempts-2)
	}

	// A cancelled request stops waiting with its own error.
	access.SetDerive(h.svc, func(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte { return make([]byte, n) })
	access.SetHashSlots(h.svc, 0, time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.svc.PasswordLogin(ctx, access.PasswordAttempt{SiteID: s.ID, Path: "/", IP: "192.0.2.99", Password: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
}
