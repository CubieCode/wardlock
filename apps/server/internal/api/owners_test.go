// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

const (
	dashHost = "wardlock.localhost"
	dashOrig = "http://wardlock.localhost"
)

// ownerHarness is the real API handler (with request logging) over a fresh
// SQLite database, a fake clock and a mailer that captures messages.
type ownerHarness struct {
	t     testing.TB
	h     http.Handler
	svc   *owners.Service
	db    *store.DB
	clock *clock
	mail  chan mail.Message
	logs  *syncBuffer
	// secrets are every code, link code and cookie value seen, for the log
	// assertion at cleanup.
	mu      sync.Mutex
	secrets []string
}

func newOwnerHarness(t *testing.T, limits owners.Limits) *ownerHarness {
	t.Helper()
	return newOwnerHarnessWith(t, storetest.New(t), limits, nil)
}

// newOwnerHarnessWith lets extend add other lanes' services to the server.
func newOwnerHarnessWith(t testing.TB, db *store.DB, limits owners.Limits, extend func(*api.Server)) *ownerHarness {
	t.Helper()
	h := &ownerHarness{
		t:     t,
		db:    db,
		clock: &clock{now: time.UnixMilli(1_800_000_000_000).UTC()},
		mail:  make(chan mail.Message, 100),
		logs:  &syncBuffer{},
	}
	log := slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc, err := owners.New(owners.Config{
		Owners:       db.Owners(),
		Sessions:     db.Sessions(),
		Mailer:       captureMailer(h.mail),
		Logger:       log,
		Secret:       bytes.Repeat([]byte("k"), 32),
		DashboardURL: &url.URL{Scheme: "http", Host: dashHost},
		Now:          h.clock.Now,
		Limits:       limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	srv := &api.Server{Version: "test", Owners: svc, Logger: log}
	if extend != nil {
		extend(srv)
	}
	h.h = platform.LogRequests(log, srv.Handler())
	t.Cleanup(func() {
		svc.Close()
		h.assertLogsClean()
	})
	return h
}

// assertLogsClean fails the test if any credential seen during it reached
// the log.
func (h *ownerHarness) assertLogsClean() {
	h.t.Helper()
	logs := h.logs.String()
	if logs == "" {
		h.t.Error("no log output captured")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.secrets {
		found := strings.Contains(logs, s)
		if sixDigits.MatchString(s) && len(s) == 6 {
			// A 6-digit email code also occurs by chance inside the hex of
			// logged IDs; only a standalone run of digits is a leak.
			found = regexp.MustCompile(`(^|[^0-9a-f])` + s + `([^0-9a-f]|$)`).MatchString(logs)
		}
		if found {
			h.t.Errorf("log output contains a credential %q", s)
		}
	}
}

func (h *ownerHarness) secret(s string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.secrets = append(h.secrets, s)
	return s
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

type captureMailer chan mail.Message

func (m captureMailer) Send(_ context.Context, msg mail.Message) error {
	m <- msg
	return nil
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// reset discards what was written so far.
func (b *syncBuffer) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// req describes one request; zero values are the dashboard defaults.
type req struct {
	method, path string
	body         any
	cookie       string // session token, sent as __Host-wardlock_session
	ip           string // client IP; default 192.0.2.1
	// headers replace the default Origin for cookie-bearing unsafe
	// requests; set to an empty map to send no Origin at all.
	headers map[string]string
}

func (h *ownerHarness) do(r req) *httptest.ResponseRecorder {
	h.t.Helper()
	var body io.Reader
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			h.t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	hr := httptest.NewRequest(r.method, r.path, body)
	hr.Host = dashHost
	if r.body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.ip != "" {
		hr.RemoteAddr = net.JoinHostPort(r.ip, "4321")
	}
	if r.cookie != "" {
		hr.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: r.cookie})
	}
	if r.headers == nil && r.cookie != "" && r.method != http.MethodGet {
		hr.Header.Set("Origin", dashOrig)
	}
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, hr)
	for _, c := range rec.Result().Cookies() {
		if c.Value != "" {
			h.secret(c.Value)
		}
	}
	return rec
}

// nextMail waits for the next captured message.
func (h *ownerHarness) nextMail() mail.Message {
	h.t.Helper()
	select {
	case m := <-h.mail:
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("no mail sent")
		return mail.Message{}
	}
}

// noMail checks nothing is queued once background sends have finished.
func (h *ownerHarness) noMail() {
	h.t.Helper()
	h.svc.Close()
	select {
	case m := <-h.mail:
		h.t.Fatalf("unexpected mail to %s: %q", m.To.Address, m.Subject)
	default:
	}
}

var (
	sixDigits = regexp.MustCompile(`\b[0-9]{6}\b`)
	linkCode  = regexp.MustCompile(`code=([A-Za-z0-9_-]+)`)
)

// emailCode sends a code to email and returns it from the captured mail.
func (h *ownerHarness) emailCode(email string) string {
	h.t.Helper()
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code", body: map[string]string{"email": email}})
	wantStatus(h.t, rec, http.StatusAccepted)
	m := h.nextMail()
	if m.To.Address != strings.ToLower(email) {
		h.t.Fatalf("mail to %s, want %s", m.To.Address, email)
	}
	code := sixDigits.FindString(m.Text)
	if code == "" {
		h.t.Fatalf("no code in mail:\n%s", m.Text)
	}
	return h.secret(code)
}

// verify posts an email code and returns the response.
func (h *ownerHarness) verify(email, code string, ip string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code/verify", ip: ip, body: map[string]string{"email": email, "code": code}})
}

// signIn creates (if needed) and signs in an owner by email code,
// returning the session token.
func (h *ownerHarness) signIn(email string) string {
	h.t.Helper()
	rec := h.verify(email, h.emailCode(email), "")
	wantStatus(h.t, rec, http.StatusOK)
	return sessionToken(h.t, rec)
}

// adminCreate creates an owner as `wardlockd admin create` does and
// returns the sign-in link code.
func (h *ownerHarness) adminCreate(email string) string {
	h.t.Helper()
	_, link, err := h.svc.CreateOwner(h.t.Context(), email)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.secret(codeOf(h.t, link))
}

// owner signs in a new owner and returns their session token.
func (h *ownerHarness) owner(email string) string {
	h.t.Helper()
	h.adminCreate(email)
	return h.signIn(email)
}

func codeOf(t testing.TB, link string) string {
	t.Helper()
	m := linkCode.FindStringSubmatch(link)
	if m == nil {
		t.Fatalf("no code in %q", link)
	}
	return m[1]
}

func wantStatus(t testing.TB, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d; body %s", rec.Code, want, rec.Body)
	}
}

func wantError(t testing.TB, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	wantStatus(t, rec, status)
	var e api.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body, err)
	}
	if e.Code != code {
		t.Fatalf("error code %q, want %q; body %s", e.Code, code, rec.Body)
	}
}

// sessionToken returns the session cookie set by rec, checking its
// attributes against the contract.
func sessionToken(t testing.TB, rec *httptest.ResponseRecorder) string {
	t.Helper()
	raw := rec.Header().Values("Set-Cookie")
	if len(raw) != 1 {
		t.Fatalf("Set-Cookie = %q, want one session cookie", raw)
	}
	c, err := http.ParseSetCookie(raw[0])
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != api.SessionCookie || !strings.HasPrefix(c.Name, "__Host-") {
		t.Errorf("cookie name %q", c.Name)
	}
	if c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie attributes %q: want Path=/, no Domain, Secure, HttpOnly, SameSite=Lax", raw[0])
	}
	if c.MaxAge != int(owners.SessionIdle/time.Second) {
		t.Errorf("cookie Max-Age %d, want the 14-day idle timeout", c.MaxAge)
	}
	if len(c.Value) < 43 {
		t.Errorf("cookie value too short for 256 bits")
	}
	return c.Value
}

func me(t *testing.T, h *ownerHarness, token string) api.Me {
	t.Helper()
	rec := h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: token})
	wantStatus(t, rec, http.StatusOK)
	var m api.Me
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSetup(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	setup := func() string {
		t.Helper()
		link, err := h.svc.PrepareSetup(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(link, dashOrig+"/setup?code=") {
			t.Fatalf("setup URL %q", link)
		}
		return h.secret(codeOf(t, link))
	}
	complete := func(code, email string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/setup", body: map[string]string{"code": code, "email": email}})
	}

	expired := setup()
	h.clock.advance(owners.SetupValidity)
	wantError(t, complete(expired, "first@example.test"), http.StatusBadRequest, "invalid_code")

	// Each start prints a new URL and retires the previous one.
	stale := setup()
	code := setup()
	wantError(t, complete(stale, "first@example.test"), http.StatusBadRequest, "invalid_code")
	wantError(t, complete("wrong", "first@example.test"), http.StatusBadRequest, "invalid_code")
	wantError(t, complete(code, "not an address"), http.StatusBadRequest, "invalid_request")

	rec := complete(code, "First@Example.test")
	wantStatus(t, rec, http.StatusCreated)
	token := sessionToken(t, rec)
	got := me(t, h, token)
	if got.Owner.Email != "first@example.test" || got.Credential != api.MeCredentialSession || len(got.Scopes) != 2 {
		t.Fatalf("me = %+v", got)
	}

	// Single use, and no further setup once an owner exists.
	wantError(t, complete(code, "second@example.test"), http.StatusBadRequest, "invalid_code")
	if link, err := h.svc.PrepareSetup(t.Context()); err != nil || link != "" {
		t.Fatalf("PrepareSetup with an owner = %q, %v; want none", link, err)
	}
}

func TestSignInLink(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	redeem := func(code string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": code}})
	}
	code := h.adminCreate("admin@example.test")
	if _, _, err := h.svc.CreateOwner(t.Context(), "Admin@example.test"); !errors.Is(err, owners.ErrOwnerExists) {
		t.Fatalf("CreateOwner twice = %v, want ErrOwnerExists", err)
	}

	rec := redeem(code)
	wantStatus(t, rec, http.StatusOK)
	if got := me(t, h, sessionToken(t, rec)); got.Owner.Email != "admin@example.test" {
		t.Fatalf("me = %+v", got)
	}
	wantError(t, redeem(code), http.StatusBadRequest, "invalid_code")

	late := h.adminCreate("late@example.test")
	h.clock.advance(owners.SignInURLValidity)
	wantError(t, redeem(late), http.StatusBadRequest, "invalid_code")
	wantError(t, redeem(""), http.StatusBadRequest, "invalid_request")
}

func TestEmailCode(t *testing.T) {
	const email = "owner@example.test"
	harness := func(t *testing.T) *ownerHarness {
		h := newOwnerHarness(t, owners.Limits{})
		h.adminCreate(email)
		return h
	}

	t.Run("wrong then right", func(t *testing.T) {
		h := harness(t)
		code := h.emailCode(email)
		wrong := "000000"
		if code == wrong {
			wrong = "111111"
		}
		wantError(t, h.verify(email, wrong, ""), http.StatusBadRequest, "invalid_code")
		wantError(t, h.verify(email, "12ab56", ""), http.StatusBadRequest, "invalid_code")
		rec := h.verify(email, code, "")
		wantStatus(t, rec, http.StatusOK)
		sessionToken(t, rec)
		wantError(t, h.verify(email, code, ""), http.StatusBadRequest, "invalid_code")
	})
	t.Run("expiry", func(t *testing.T) {
		h := harness(t)
		code := h.emailCode(email)
		h.clock.advance(owners.EmailCodeValidity)
		wantError(t, h.verify(email, code, ""), http.StatusBadRequest, "invalid_code")
	})
	t.Run("five attempts", func(t *testing.T) {
		h := harness(t)
		code := h.emailCode(email)
		wrong := "000000"
		if code == wrong {
			wrong = "111111"
		}
		for range owners.EmailCodeAttempts {
			wantError(t, h.verify(email, wrong, ""), http.StatusBadRequest, "invalid_code")
		}
		wantError(t, h.verify(email, code, ""), http.StatusBadRequest, "invalid_code")
	})
	t.Run("a new code retires the old one", func(t *testing.T) {
		h := harness(t)
		old := h.emailCode(email)
		code := h.emailCode(email)
		if old != code {
			wantError(t, h.verify(email, old, ""), http.StatusBadRequest, "invalid_code")
		}
		wantStatus(t, h.verify(email, code, ""), http.StatusOK)
	})
	t.Run("addresses are case-insensitive", func(t *testing.T) {
		h := harness(t)
		code := h.emailCode("Owner@Example.TEST")
		wantStatus(t, h.verify("OWNER@example.test", code, ""), http.StatusOK)
	})
}

func TestNeutralResponses(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	h.adminCreate("known@example.test")
	inviter := h.signIn("known@example.test")
	h.secret(inviter)

	send := func(email string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code", body: map[string]string{"email": email}})
	}
	same := func(a, b *httptest.ResponseRecorder) {
		t.Helper()
		if a.Code != b.Code || a.Body.String() != b.Body.String() || len(a.Header()) != len(b.Header()) {
			t.Fatalf("responses differ:\n%d %v %q\n%d %v %q", a.Code, a.Header(), a.Body, b.Code, b.Header(), b.Body)
		}
	}

	known, unknown := send("known@example.test"), send("nobody@example.test")
	wantStatus(t, known, http.StatusAccepted)
	same(known, unknown)
	code := h.secret(sixDigits.FindString(h.nextMail().Text))
	h.noMail()

	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	same(h.verify("known@example.test", wrong, ""), h.verify("nobody@example.test", wrong, ""))

	invite := func(email string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/owners", cookie: inviter, body: map[string]string{"email": email}})
	}
	existing, fresh := invite("known@example.test"), invite("new@example.test")
	wantStatus(t, existing, http.StatusAccepted)
	same(existing, fresh)
	if m := h.nextMail(); m.To.Address != "new@example.test" {
		t.Fatalf("invitation went to %s", m.To.Address)
	}
	h.noMail()
}

func TestInvite(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	a := h.owner("a@example.test")
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/owners", cookie: a, body: map[string]string{"email": "B@example.test"}})
	wantStatus(t, rec, http.StatusAccepted)
	m := h.nextMail()
	if m.To.Address != "b@example.test" || !strings.Contains(m.Text, "a@example.test") {
		t.Fatalf("invitation %+v", m)
	}
	code := h.secret(codeOf(t, m.Text))
	rec = h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": code}})
	wantStatus(t, rec, http.StatusOK)
	if got := me(t, h, sessionToken(t, rec)); got.Owner.Email != "b@example.test" {
		t.Fatalf("invited owner me = %+v", got)
	}
	// An invited owner can also sign in by email code.
	h.signIn("b@example.test")

	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/owners", body: map[string]string{"email": "c@example.test"}}),
		http.StatusUnauthorized, "unauthenticated")
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/owners", cookie: a, body: map[string]string{"email": ""}}),
		http.StatusBadRequest, "invalid_request")
}

func TestRateLimits(t *testing.T) {
	t.Run("sends per address", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		limit := owners.DefaultLimits().SendPerAddress
		send := func(ip string) *httptest.ResponseRecorder {
			return h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code", ip: ip, body: map[string]string{"email": "nobody@example.test"}})
		}
		for i := range limit.N {
			wantStatus(t, send("192.0.2."+strconv.Itoa(i+1)), http.StatusAccepted)
		}
		rec := send("192.0.2.200")
		wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
		if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 || ra > int(limit.Per/time.Second) {
			t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
		}
		h.clock.advance(limit.Per)
		wantStatus(t, send("192.0.2.200"), http.StatusAccepted)
	})
	t.Run("sends per IP", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		limit := owners.DefaultLimits().SendPerIP
		send := func(n int) *httptest.ResponseRecorder {
			return h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code", body: map[string]string{"email": "user" + strconv.Itoa(n) + "@example.test"}})
		}
		for i := range limit.N {
			wantStatus(t, send(i), http.StatusAccepted)
		}
		wantError(t, send(limit.N), http.StatusTooManyRequests, "rate_limited")
		h.clock.advance(limit.Per)
		wantStatus(t, send(limit.N+1), http.StatusAccepted)
	})
	t.Run("verifications per IP", func(t *testing.T) {
		limits := owners.DefaultLimits()
		limits.VerifyPerIP = owners.Rate{N: 3, Per: time.Minute}
		h := newOwnerHarness(t, limits)
		h.adminCreate("owner@example.test")
		code := h.emailCode("owner@example.test")
		for range 3 {
			wantError(t, h.verify("nobody@example.test", "123456", "198.51.100.7"), http.StatusBadRequest, "invalid_code")
		}
		// The right code is refused while the IP is limited...
		wantError(t, h.verify("owner@example.test", code, "198.51.100.7"), http.StatusTooManyRequests, "rate_limited")
		// ...works from another IP, and from this one after the window.
		h.clock.advance(time.Minute)
		wantStatus(t, h.verify("owner@example.test", code, "198.51.100.7"), http.StatusOK)
	})
	t.Run("redemptions per IP", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		limit := owners.DefaultLimits().RedeemPerIP
		for range limit.N {
			wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": "guess"}}),
				http.StatusBadRequest, "invalid_code")
		}
		code := h.adminCreate("owner@example.test")
		wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": code}}),
			http.StatusTooManyRequests, "rate_limited")
		wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/setup", body: map[string]string{"code": "guess", "email": "x@example.test"}}),
			http.StatusTooManyRequests, "rate_limited")
		h.clock.advance(limit.Per)
		wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": code}}), http.StatusOK)
	})
	t.Run("invites per owner", func(t *testing.T) {
		limits := owners.DefaultLimits()
		limits.InvitePerOwner = owners.Rate{N: 2, Per: time.Hour}
		h := newOwnerHarness(t, limits)
		a := h.owner("a@example.test")
		invite := func(n int) *httptest.ResponseRecorder {
			return h.do(req{method: http.MethodPost, path: "/api/v0/owners", cookie: a, body: map[string]string{"email": "i" + strconv.Itoa(n) + "@example.test"}})
		}
		wantStatus(t, invite(1), http.StatusAccepted)
		wantStatus(t, invite(2), http.StatusAccepted)
		wantError(t, invite(3), http.StatusTooManyRequests, "rate_limited")
		h.clock.advance(time.Hour)
		wantStatus(t, invite(4), http.StatusAccepted)
	})
}

func TestSignOut(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	token := h.owner("owner@example.test")
	other := h.signIn("owner@example.test")

	// Old enough that any other request would refresh the cookie.
	h.clock.advance(time.Hour)
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-out", cookie: token})
	wantStatus(t, rec, http.StatusNoContent)
	if n := len(rec.Header().Values("Set-Cookie")); n != 1 {
		t.Fatalf("sign-out sent %d Set-Cookie headers %q, want only the clearing one", n, rec.Header().Values("Set-Cookie"))
	}
	c, err := http.ParseSetCookie(rec.Header().Get("Set-Cookie"))
	if err != nil || c.Name != api.SessionCookie || c.Value != "" || c.MaxAge >= 0 || !c.Secure || !c.HttpOnly || c.Path != "/" {
		t.Fatalf("sign-out cookie %q (%v), want a cleared session cookie", rec.Header().Get("Set-Cookie"), err)
	}

	rec = h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: token})
	wantError(t, rec, http.StatusUnauthorized, "unauthenticated")
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("a dead session cookie is not cleared: %q", rec.Header().Get("Set-Cookie"))
	}
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-out", cookie: token}), http.StatusUnauthorized, "unauthenticated")
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-out"}), http.StatusUnauthorized, "unauthenticated")
	// Other sessions are unaffected.
	me(t, h, other)
}

func listSessions(t *testing.T, h *ownerHarness, token, query string) api.SessionPage {
	t.Helper()
	rec := h.do(req{method: http.MethodGet, path: "/api/v0/sessions" + query, cookie: token})
	wantStatus(t, rec, http.StatusOK)
	var page api.SessionPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestSessions(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	first := h.owner("a@example.test")
	h.clock.advance(time.Hour)
	second := h.signIn("a@example.test")
	h.clock.advance(time.Hour)
	third := h.signIn("a@example.test")

	page := listSessions(t, h, second, "")
	if len(page.Items) != 3 || page.NextCursor != nil {
		t.Fatalf("sessions = %+v", page)
	}
	current := 0
	for i, it := range page.Items {
		if it.Current {
			current++
		}
		if i > 0 && it.LastUsedAt > page.Items[i-1].LastUsedAt {
			t.Errorf("not most recently used first: %+v", page.Items)
		}
	}
	if current != 1 || page.Items[2].Current {
		t.Fatalf("current flags = %+v", page.Items)
	}
	for _, it := range page.Items {
		if it.ExpiresAt <= it.LastUsedAt || it.ExpiresAt-it.LastUsedAt > owners.SessionIdle.Milliseconds() {
			t.Errorf("expiresAt %d not the idle expiry of lastUsedAt %d", it.ExpiresAt, it.LastUsedAt)
		}
	}

	// Pagination walks every session once.
	p1 := listSessions(t, h, second, "?limit=2")
	if len(p1.Items) != 2 || p1.NextCursor == nil {
		t.Fatalf("page 1 = %+v", p1)
	}
	p2 := listSessions(t, h, second, "?limit=2&cursor="+url.QueryEscape(*p1.NextCursor))
	if len(p2.Items) != 1 || p2.NextCursor != nil {
		t.Fatalf("page 2 = %+v", p2)
	}
	seen := map[string]bool{}
	for _, it := range append(p1.Items, p2.Items...) {
		seen[it.Id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("pages covered %d sessions, want 3", len(seen))
	}
	wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/sessions?cursor=bogus", cookie: second}), http.StatusBadRequest, "invalid_request")
	wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/sessions?limit=0", cookie: second}), http.StatusBadRequest, "invalid_request")
	wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/sessions"}), http.StatusUnauthorized, "unauthenticated")

	// Revoking the first session ends it on its next request.
	firstID := page.Items[2].Id // created first and unused since
	wantStatus(t, h.do(req{method: http.MethodDelete, path: "/api/v0/sessions/" + firstID, cookie: third}), http.StatusNoContent)
	wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: first}), http.StatusUnauthorized, "unauthenticated")
	if got := listSessions(t, h, third, ""); len(got.Items) != 2 {
		t.Fatalf("after revoke, sessions = %+v", got)
	}
	wantError(t, h.do(req{method: http.MethodDelete, path: "/api/v0/sessions/" + firstID}), http.StatusUnauthorized, "unauthenticated")
}

func TestSessionIsolation(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	a := h.owner("a@example.test")
	b := h.owner("b@example.test")

	aPage := listSessions(t, h, a, "")
	bPage := listSessions(t, h, b, "")
	if len(aPage.Items) != 1 || len(bPage.Items) != 1 || aPage.Items[0].Id == bPage.Items[0].Id {
		t.Fatalf("A sees %+v, B sees %+v", aPage.Items, bPage.Items)
	}
	aID := aPage.Items[0].Id

	foreign := h.do(req{method: http.MethodDelete, path: "/api/v0/sessions/" + aID, cookie: b})
	missing := h.do(req{method: http.MethodDelete, path: "/api/v0/sessions/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e0f", cookie: b})
	wantError(t, foreign, http.StatusNotFound, "not_found")
	if foreign.Body.String() != missing.Body.String() || missing.Code != foreign.Code {
		t.Fatalf("foreign %d %q vs missing %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
	if got := me(t, h, a); got.Owner.Email != "a@example.test" {
		t.Fatalf("A's session after B's revoke attempt: %+v", got)
	}
}

func TestSessionExpiry(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		token := h.owner("owner@example.test")
		h.clock.advance(owners.SessionIdle - time.Minute)
		me(t, h, token)
		// The use moved the idle expiry and re-sent the cookie.
		h.clock.advance(owners.SessionIdle - time.Minute)
		rec := h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: token})
		wantStatus(t, rec, http.StatusOK)
		if c, err := http.ParseSetCookie(rec.Header().Get("Set-Cookie")); err != nil || c.Value != token || c.MaxAge <= 0 || !c.Secure || !c.HttpOnly {
			t.Errorf("refresh cookie %q", rec.Header().Get("Set-Cookie"))
		}
		h.clock.advance(owners.SessionIdle)
		wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: token}), http.StatusUnauthorized, "unauthenticated")
	})
	t.Run("absolute", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		token := h.owner("owner@example.test")
		var used time.Duration
		for used+13*24*time.Hour < owners.SessionAbsolute {
			h.clock.advance(13 * 24 * time.Hour)
			used += 13 * 24 * time.Hour
			me(t, h, token)
		}
		h.clock.advance(owners.SessionAbsolute - used)
		wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: token}), http.StatusUnauthorized, "unauthenticated")
	})
}

func TestCSRF(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	token := h.owner("owner@example.test")
	invite := func(headers map[string]string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/owners", cookie: token, headers: headers, body: map[string]string{"email": "x@example.test"}})
	}
	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"same Origin", map[string]string{"Origin": dashOrig}, http.StatusAccepted},
		{"same Origin and Sec-Fetch-Site", map[string]string{"Origin": dashOrig, "Sec-Fetch-Site": "same-origin"}, http.StatusAccepted},
		{"Sec-Fetch-Site same-origin only", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusAccepted},
		{"neither header", map[string]string{}, http.StatusForbidden},
		{"other Origin", map[string]string{"Origin": "http://evil.example.test"}, http.StatusForbidden},
		{"site Origin", map[string]string{"Origin": "http://docs.wardlock.localhost"}, http.StatusForbidden},
		{"other port", map[string]string{"Origin": "http://wardlock.localhost:8081"}, http.StatusForbidden},
		{"null Origin", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site fetch", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"same Origin but cross-site fetch", map[string]string{"Origin": dashOrig, "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := invite(tt.headers)
			if tt.want == http.StatusForbidden {
				wantError(t, rec, http.StatusForbidden, "forbidden")
				return
			}
			wantStatus(t, rec, tt.want)
		})
	}
	// Sign-out and revocation are covered too; safe methods are not.
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-out", cookie: token, headers: map[string]string{}}), http.StatusForbidden, "forbidden")
	me(t, h, token)
	// A request with neither header and no cookie is outside the check.
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code", headers: map[string]string{}, body: map[string]string{"email": "x@example.test"}}), http.StatusAccepted)
}

// TestLoginCSRF checks the rule also holds without a session cookie, so a
// cross-site page cannot sign a visitor in to the attacker's account.
func TestLoginCSRF(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	link := h.adminCreate("owner@example.test")
	setupLink, err := h.svc.PrepareSetup(t.Context())
	if err != nil || setupLink != "" {
		t.Fatalf("PrepareSetup = %q, %v", setupLink, err)
	}
	bodies := []struct {
		name, path string
		body       map[string]string
	}{
		{"verifyEmailCode", "/api/v0/auth/email-code/verify", map[string]string{"email": "owner@example.test", "code": "123456"}},
		{"redeemSignInLink", "/api/v0/auth/sign-in-link", map[string]string{"code": link}},
		{"completeSetup", "/api/v0/setup", map[string]string{"code": "anything", "email": "x@example.test"}},
		{"sendEmailCode", "/api/v0/auth/email-code", map[string]string{"email": "owner@example.test"}},
	}
	crossSite := []map[string]string{
		{"Origin": "http://evil.example.test"},
		{"Origin": "http://docs.wardlock.localhost"},
		{"Origin": "null"},
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site"},
		{"Origin": dashOrig, "Sec-Fetch-Site": "cross-site"},
	}
	for _, b := range bodies {
		for _, hdr := range crossSite {
			t.Run(b.name+fmt.Sprint(hdr), func(t *testing.T) {
				wantError(t, h.do(req{method: http.MethodPost, path: b.path, headers: hdr, body: b.body}), http.StatusForbidden, "forbidden")
			})
		}
	}
	// The link was not spent by the refused attempts, and works same-origin.
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", headers: map[string]string{"Origin": dashOrig, "Sec-Fetch-Site": "same-origin"}, body: map[string]string{"code": link}})
	wantStatus(t, rec, http.StatusOK)
	sessionToken(t, rec)
}

func TestOnlyTheSessionCookieAuthenticates(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	token := h.owner("owner@example.test")
	for _, name := range []string{"wardlock_session", "__Host-wardlock_site", "__Secure-wardlock_session"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v0/me", nil)
		r.Host = dashHost
		r.AddCookie(&http.Cookie{Name: name, Value: token})
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, r)
		wantError(t, rec, http.StatusUnauthorized, "unauthenticated")
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 43), token + "x"} {
		wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: bad}), http.StatusUnauthorized, "unauthenticated")
	}
	if bad := strings.Repeat("A", 43); bad == token {
		t.Fatal("unlucky token")
	}
}

func TestProtectedOperationsNeedASession(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	for _, r := range []req{
		{method: http.MethodGet, path: "/api/v0/me"},
		{method: http.MethodPost, path: "/api/v0/auth/sign-out"},
		{method: http.MethodPost, path: "/api/v0/owners", body: map[string]string{"email": "x@example.test"}},
		{method: http.MethodGet, path: "/api/v0/sessions"},
		{method: http.MethodDelete, path: "/api/v0/sessions/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e0f"},
	} {
		wantError(t, h.do(r), http.StatusUnauthorized, "unauthenticated")
	}
	// Seed a log line so the cleanup assertion has output to check.
	h.do(req{method: http.MethodGet, path: "/api/v0/health"})
}

func TestConcurrentGuessesCountAtMostFive(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	h.adminCreate("owner@example.test")
	code := h.emailCode("owner@example.test")
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	const n = 20
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := range n {
		wg.Go(func() {
			// Distinct IPs keep the per-IP limit out of the way.
			statuses[i] = h.verify("owner@example.test", wrong, "198.51.100."+strconv.Itoa(i+1)).Code
		})
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusBadRequest {
			t.Fatalf("guess %d: status %d", i, st)
		}
	}
	o, err := h.db.Owners().GetByEmail(t.Context(), "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	lc, err := h.db.Owners().LatestLoginCode(t.Context(), o.ID, store.LoginCodeEmail)
	if err != nil {
		t.Fatal(err)
	}
	if lc.Attempts != owners.EmailCodeAttempts {
		t.Fatalf("%d attempts counted, want exactly %d", lc.Attempts, owners.EmailCodeAttempts)
	}
	wantError(t, h.verify("owner@example.test", code, "203.0.113.1"), http.StatusBadRequest, "invalid_code")
}

func TestConcurrentRedemptionsSucceedOnce(t *testing.T) {
	const n = 20
	race := func(t *testing.T, h *ownerHarness, path string, body map[string]string) {
		t.Helper()
		var wg sync.WaitGroup
		statuses := make([]int, n)
		for i := range n {
			wg.Go(func() {
				statuses[i] = h.do(req{method: http.MethodPost, path: path, ip: "198.51.100." + strconv.Itoa(i+1), body: body}).Code
			})
		}
		wg.Wait()
		ok := 0
		for _, st := range statuses {
			switch st {
			case http.StatusOK, http.StatusCreated:
				ok++
			case http.StatusBadRequest:
			default:
				t.Fatalf("status %d", st)
			}
		}
		if ok != 1 {
			t.Fatalf("%d of %d concurrent redemptions succeeded, want exactly 1 (%v)", ok, n, statuses)
		}
	}
	t.Run("sign-in link", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		race(t, h, "/api/v0/auth/sign-in-link", map[string]string{"code": h.adminCreate("owner@example.test")})
	})
	t.Run("email code", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		h.adminCreate("owner@example.test")
		race(t, h, "/api/v0/auth/email-code/verify", map[string]string{"email": "owner@example.test", "code": h.emailCode("owner@example.test")})
	})
	t.Run("setup", func(t *testing.T) {
		h := newOwnerHarness(t, owners.Limits{})
		link, err := h.svc.PrepareSetup(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		code := h.secret(codeOf(t, link))
		var wg sync.WaitGroup
		statuses := make([]int, n)
		for i := range n {
			wg.Go(func() {
				statuses[i] = h.do(req{method: http.MethodPost, path: "/api/v0/setup", ip: "198.51.100." + strconv.Itoa(i+1),
					body: map[string]string{"code": code, "email": "owner" + strconv.Itoa(i) + "@example.test"}}).Code
			})
		}
		wg.Wait()
		created := 0
		for _, st := range statuses {
			if st == http.StatusCreated {
				created++
			}
		}
		if n, err := h.db.Owners().Count(t.Context()); created != 1 || err != nil || n != 1 {
			t.Fatalf("%d setups succeeded, %d owners (%v); want exactly one", created, n, err)
		}
	})
}

func TestReinviteUnacceptedOwner(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	a := h.owner("a@example.test")
	invite := func() *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/owners", cookie: a, body: map[string]string{"email": "b@example.test"}})
	}
	redeem := func(code string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/auth/sign-in-link", body: map[string]string{"code": code}})
	}
	first := invite()
	wantStatus(t, first, http.StatusAccepted)
	expired := h.secret(codeOf(t, h.nextMail().Text))
	h.clock.advance(owners.InviteValidity)
	wantError(t, redeem(expired), http.StatusBadRequest, "invalid_code")

	// Re-inviting before the first sign-in sends a fresh link.
	again := invite()
	if again.Code != first.Code || again.Body.String() != first.Body.String() {
		t.Fatalf("re-invite answered %d %q, first %d %q", again.Code, again.Body, first.Code, first.Body)
	}
	fresh := h.secret(codeOf(t, h.nextMail().Text))
	// A third invite retires the second link.
	wantStatus(t, invite(), http.StatusAccepted)
	latest := h.secret(codeOf(t, h.nextMail().Text))
	wantError(t, redeem(fresh), http.StatusBadRequest, "invalid_code")
	wantStatus(t, redeem(latest), http.StatusOK)

	// Once B has signed in, inviting them sends nothing.
	wantStatus(t, invite(), http.StatusAccepted)
	h.noMail()
}

func TestIPv6ClientsShareTheirSlash64(t *testing.T) {
	limits := owners.DefaultLimits()
	limits.VerifyPerIP = owners.Rate{N: 2, Per: time.Minute}
	h := newOwnerHarness(t, limits)
	for _, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2::ffff"} {
		wantError(t, h.verify("nobody@example.test", "123456", ip), http.StatusBadRequest, "invalid_code")
	}
	wantError(t, h.verify("nobody@example.test", "123456", "2001:db8:1:2:abcd::9"), http.StatusTooManyRequests, "rate_limited")
	wantError(t, h.verify("nobody@example.test", "123456", "2001:db8:1:3::1"), http.StatusBadRequest, "invalid_code")
}

// repeatReader yields c forever, so oversize bodies need no buffer.
type repeatReader byte

func (c repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(c)
	}
	return len(p), nil
}

func TestRequestBodyCap(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	oversize := func(method, path string, declareLength bool) *httptest.ResponseRecorder {
		t.Helper()
		size := int64(api.MaxBodyBytes + 1)
		body := io.MultiReader(strings.NewReader(`{"email":"`), io.LimitReader(repeatReader('a'), size))
		r := httptest.NewRequest(method, path, body)
		r.Host = dashHost
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = -1
		if declareLength {
			r.ContentLength = size + 10
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, r)
		return rec
	}
	wantError(t, oversize(http.MethodPost, "/api/v0/auth/email-code", true), http.StatusRequestEntityTooLarge, "limit_exceeded")
	wantError(t, oversize(http.MethodPost, "/api/v0/auth/email-code", false), http.StatusRequestEntityTooLarge, "limit_exceeded")
	wantError(t, oversize(http.MethodPost, "/api/v0/sites", false), http.StatusRequestEntityTooLarge, "limit_exceeded")
	// Blob uploads are exempt: the request reaches the operation (501 until
	// C1.2), whatever its size.
	r := httptest.NewRequest(http.MethodPut, "/api/v0/sites/docs/deploys/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e10/blobs/"+strings.Repeat("ab", 32),
		io.LimitReader(repeatReader('x'), api.MaxBodyBytes+1))
	r.Host = dashHost
	r.Header.Set("Content-Type", "application/octet-stream")
	r.ContentLength = api.MaxBodyBytes + 1
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("blob upload refused by the JSON body cap: %s", rec.Body)
	}
	// A body under the cap is unaffected.
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code", body: map[string]string{"email": "x@example.test"}}), http.StatusAccepted)
}

func TestSessionErrorsAreLogged(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	token := h.owner("owner@example.test")
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	rec := h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: token})
	wantError(t, rec, http.StatusInternalServerError, "internal")
	logs := h.logs.String()
	if !strings.Contains(logs, `"msg":"internal error"`) || !strings.Contains(logs, `"route":"session middleware"`) ||
		!strings.Contains(logs, `"request_id":"`+rec.Header().Get("X-Request-Id")+`"`) {
		t.Fatalf("no internal error line with the request ID:\n%s", logs)
	}
}

func TestSessionsRecordTheUserAgent(t *testing.T) {
	h := newOwnerHarness(t, owners.Limits{})
	h.adminCreate("owner@example.test")
	code := h.emailCode("owner@example.test")
	long := "TestAgent/1.0 " + strings.Repeat("x", 300)
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/auth/email-code/verify", headers: map[string]string{"User-Agent": long},
		body: map[string]string{"email": "owner@example.test", "code": code}})
	wantStatus(t, rec, http.StatusOK)
	page := listSessions(t, h, sessionToken(t, rec), "")
	if len(page.Items) != 1 || page.Items[0].UserAgent == nil || *page.Items[0].UserAgent != long[:200] {
		t.Fatalf("sessions = %+v, want the User-Agent truncated to 200 characters", page.Items)
	}
}
