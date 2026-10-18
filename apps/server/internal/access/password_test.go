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

// Synthetic passwords for tests only.
const (
	firstPassword  = "harbour-lights-7"
	secondPassword = "tide-chart-42"
)

// revocations records RevokeForPolicy calls on top of the real repository.
type revocations struct {
	store.PasswordSessions
	mu    sync.Mutex
	calls []string
	n     int
}

func (r *revocations) RevokeForPolicy(ctx context.Context, siteID, policyID string, at time.Time) (int, error) {
	n, err := r.PasswordSessions.RevokeForPolicy(ctx, siteID, policyID, at)
	r.mu.Lock()
	r.calls = append(r.calls, siteID+" "+policyID)
	r.n += n
	r.mu.Unlock()
	return n, err
}

func (r *revocations) take() (calls []string, revoked int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls, revoked = r.calls, r.n
	r.calls, r.n = nil, 0
	return calls, revoked
}

type passwordHarness struct {
	*harness
	revoked *revocations
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
	rv := &revocations{PasswordSessions: db.PasswordSessions()}
	svc, err := access.New(access.Config{
		Sites: db.Sites(), Policies: h.policies, Sessions: db.Sessions(), Links: db.Links(), PasswordSessions: rv,
		Notifier: &notifier{}, Secret: secret, Now: h.clock.Now,
		Log:    slog.New(slog.NewTextHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	h.svc = svc
	return &passwordHarness{harness: h, revoked: rv}
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

	// The site limit counts attempts from every client: 3 from 192.0.2.1
	// and 3 from the /64 already passed it.
	_, err = h.login(s.ID, "/", "198.51.100.7", firstPassword)
	limited("site limit", err)
	if _, err := h.login(other.ID, "/", "198.51.100.8", firstPassword); err != nil {
		t.Fatalf("another site is not limited: %v", err)
	}

	// Keys name the client or the site, never the password.
	for _, k := range h.limiterKeys() {
		if !strings.HasPrefix(k, "password-ip:") && !strings.HasPrefix(k, "password-site:") || strings.Contains(k, firstPassword) {
			t.Fatalf("limiter key %q", k)
		}
	}

	// Limits reset when their window ends.
	h.clock.advance(15 * time.Minute)
	if _, err := h.login(s.ID, "/", "192.0.2.1", firstPassword); err != nil {
		t.Fatalf("after the window: %v", err)
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
		_, err := h.svc.Set(ctx, a, "harbour", tc.rule)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
		var te *access.TargetError
		if errors.As(err, &te) && tc.rule.NewPassword != "" && strings.Contains(te.Message, tc.rule.NewPassword) {
			t.Errorf("%s: message %q repeats the password", tc.name, te.Message)
		}
	}
	// Eight characters, not bytes, is the minimum.
	if _, err := h.svc.Set(ctx, a, "harbour", passwordRule("/", "ŵaŵaŵaŵ")); !errors.Is(err, access.ErrInvalidPolicy) {
		t.Fatalf("seven characters: %v", err)
	}

	v1 := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true, NewPassword: firstPassword})
	pw := h.loginCookie(s.ID, firstPassword)
	link := h.session(s.ID, v1, store.SessionLink)
	if calls, _ := h.revoked.take(); len(calls) != 0 {
		t.Fatalf("setting the first password revoked %v", calls)
	}

	// Keeping the method without a new password keeps the hash and the
	// sessions: nothing changed.
	same := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true})
	if same.Version != v1.Version || same.PasswordHash != v1.PasswordHash {
		t.Fatalf("unchanged settings: %+v, was %+v", same, v1)
	}
	h.allowed("unchanged", s.ID, pw, true)

	// B cannot change A's password, and gets the same error as for a
	// missing site; A's password and sessions stay.
	if _, err := h.svc.Set(ctx, b, "harbour", passwordRule("/", secondPassword)); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("B sets A's password: %v", err)
	}
	if _, err := h.svc.Set(ctx, b, "beta", passwordRule("/", secondPassword)); err != nil {
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
	if calls, n := h.revoked.take(); len(calls) != 1 || calls[0] != s.ID+" "+v1.ID || n != 1 {
		t.Fatalf("revocations %v (%d sessions), want one for the policy", calls, n)
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
	if calls, _ := h.revoked.take(); len(calls) != 1 {
		t.Fatalf("revocations %v", calls)
	}

	// Removing the method clears the hash and revokes too.
	off := h.set(a, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, Links: true})
	if off.PasswordEnabled || off.PasswordHash != "" || off.Version != v3.Version+1 {
		t.Fatalf("password removed: %+v", off)
	}
	if calls, _ := h.revoked.take(); len(calls) != 1 {
		t.Fatalf("revocations %v", calls)
	}
	h.allowed("after removal", s.ID, pw, false)
	if _, err := h.login(s.ID, "/", "192.0.2.9", secondPassword); !errors.Is(err, access.ErrWrongPassword) {
		t.Fatalf("password after removal: %v", err)
	}
	// Turning it back on needs a new password.
	if _, err := h.svc.Set(ctx, a, "harbour", passwordRule("/", "")); !errors.Is(err, access.ErrInvalidPolicy) {
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
	_, _ = h.svc.Set(t.Context(), o, "harbour", passwordRule("/", "short"))
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
