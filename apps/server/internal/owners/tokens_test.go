// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// tokenHarness is a Tokens service over a real database with a settable
// clock and captured logs.
type tokenHarness struct {
	t      *testing.T
	svc    *Tokens
	db     *store.DB
	logs   *syncBuffer
	mu     sync.Mutex
	now    time.Time
	owners int
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var testSecret = bytes.Repeat([]byte("k"), 32)

func newTokenHarness(t *testing.T, limits TokenLimits) *tokenHarness {
	t.Helper()
	h := &tokenHarness{t: t, db: storetest.New(t), logs: &syncBuffer{}, now: time.UnixMilli(1_800_000_000_000).UTC()}
	svc, err := NewTokens(TokensConfig{
		Tokens:       h.db.APITokens(),
		Logger:       slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Secret:       testSecret,
		DashboardURL: &url.URL{Scheme: "https", Host: "dash.example.test"},
		Now:          h.clock,
		Limits:       limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	return h
}

func (h *tokenHarness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *tokenHarness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *tokenHarness) owner() string {
	h.t.Helper()
	h.owners++
	o := store.Owner{ID: store.NewID(), Email: fmt.Sprintf("owner%d@example.test", h.owners), CreatedAt: h.clock()}
	if err := h.db.Owners().Create(h.t.Context(), o); err != nil {
		h.t.Fatal(err)
	}
	return o.ID
}

// assertNoSecrets fails if any secret appears in the captured logs.
func (h *tokenHarness) assertNoSecrets(secrets ...string) {
	h.t.Helper()
	logs := h.logs.String()
	for _, s := range secrets {
		if s != "" && strings.Contains(logs, s) {
			h.t.Fatalf("logs contain a secret:\n%s", logs)
		}
	}
}

var client = Client{IP: "192.0.2.10"}

func TestTokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		tok, pid := NewToken()
		if got, ok := ParseToken(tok); !ok || got != pid {
			t.Fatalf("ParseToken(%q) = %q, %v; want %q", tok, got, ok, pid)
		}
		if len(tok) != 57 || !strings.HasPrefix(tok, "wl_"+pid+"_") {
			t.Fatalf("token %q", tok)
		}
		if seen[pid] {
			t.Fatalf("public ID %q repeated", pid)
		}
		seen[pid] = true
	}
	tok, _ := NewToken()
	for _, bad := range []string{
		"", "wl_", tok[:len(tok)-1], tok + "A", strings.Replace(tok, "wl_", "wx_", 1),
		"wl_ABCDEFGHIJ" + tok[13:], "wl_abcdefgh1j" + tok[13:], strings.Replace(tok, tok[13:14], "-", 1),
		tok[:20] + "!" + tok[21:],
	} {
		if _, ok := ParseToken(bad); ok {
			t.Errorf("ParseToken(%q) accepted", bad)
		}
	}
}

func FuzzParseToken(f *testing.F) {
	tok, _ := NewToken()
	f.Add(tok)
	f.Add("")
	f.Add("wl_aaaaaaaaaa_" + strings.Repeat("A", 43))
	f.Add("wl_aaaaaaaaaa_" + strings.Repeat("A", 42) + "B") // trailing bits set
	f.Fuzz(func(t *testing.T, s string) {
		pid, ok := ParseToken(s)
		if !ok {
			return
		}
		if len(s) != 57 || len(pid) != 10 || !strings.HasPrefix(s, "wl_"+pid+"_") {
			t.Fatalf("accepted %q as %q", s, pid)
		}
		// One secret, one spelling: the secret re-encodes to itself.
		secret, err := base64.RawURLEncoding.DecodeString(s[14:])
		if err != nil || base64.RawURLEncoding.EncodeToString(secret) != s[14:] {
			t.Fatalf("accepted the non-canonical %q", s)
		}
	})
}

func TestHashToken(t *testing.T) {
	tok, _ := NewToken()
	h := HashToken(testSecret, tok)
	if len(h) != 64 || strings.Contains(h, tok) {
		t.Fatalf("hash %q", h)
	}
	if HashToken(testSecret, tok) != h {
		t.Fatal("hash is not deterministic")
	}
	if HashToken(bytes.Repeat([]byte("x"), 32), tok) == h {
		t.Fatal("hash ignores the server secret")
	}
	other, _ := NewToken()
	if HashToken(testSecret, other) == h {
		t.Fatal("different tokens share a hash")
	}
}

func TestCreateAndAuthenticate(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	info, tok, err := h.svc.Create(t.Context(), owner, "  CI deploys ", Scopes{Deploy: true}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "CI deploys" || info.Scopes != (Scopes{Deploy: true}) || !info.ExpiresAt.IsZero() {
		t.Fatalf("created %+v", info)
	}
	row, err := h.db.APITokens().GetByPublicID(t.Context(), info.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if row.TokenHash != HashToken(testSecret, tok) || strings.Contains(row.TokenHash, tok) {
		t.Fatal("stored hash is not the HMAC of the token")
	}

	auth, err := h.svc.Authenticate(t.Context(), client, tok)
	if err != nil {
		t.Fatal(err)
	}
	want := TokenAuth{TokenID: info.ID, PublicID: info.PublicID, OwnerID: owner, Scopes: Scopes{Deploy: true}}
	if auth != want {
		t.Fatalf("Authenticate() = %+v, want %+v", auth, want)
	}
	page, _, err := h.svc.List(t.Context(), owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || !page[0].LastUsedAt.Equal(h.clock()) {
		t.Fatalf("last use not recorded: %+v", page)
	}
	h.assertNoSecrets(tok, tok[len(tok)-43:])
	if !strings.Contains(h.logs.String(), info.PublicID) {
		t.Fatal("creation log does not name the public ID")
	}
}

func TestCreateValidation(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	tests := []struct {
		name    string
		tname   string
		scopes  Scopes
		expires time.Time
		want    error
	}{
		{"empty name", " ", Scopes{Deploy: true}, time.Time{}, ErrInvalidName},
		{"long name", strings.Repeat("n", 101), Scopes{Deploy: true}, time.Time{}, ErrInvalidName},
		{"control character", "a\nb", Scopes{Deploy: true}, time.Time{}, ErrInvalidName},
		{"no scopes", "CI", Scopes{}, time.Time{}, ErrInvalidScopes},
		{"expired", "CI", Scopes{Manage: true}, h.clock(), ErrInvalidExpiry},
		{"100 characters", strings.Repeat("é", 100), Scopes{Manage: true}, h.clock().Add(time.Hour), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := h.svc.Create(t.Context(), owner, tt.tname, tt.scopes, tt.expires)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Create() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAuthenticateRejects(t *testing.T) {
	h := newTokenHarness(t, withLimits(func(l *TokenLimits) { l.FailuresPerIP = Rate{N: 1000, Per: time.Hour} }))
	owner := h.owner()
	_, valid, err := h.svc.Create(t.Context(), owner, "valid", Scopes{Deploy: true}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	_, expTok, err := h.svc.Create(t.Context(), owner, "expiring", Scopes{Deploy: true}, h.clock().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	revoked, revTok, err := h.svc.Create(t.Context(), owner, "revoked", Scopes{Manage: true}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Revoke(t.Context(), owner, revoked.ID); err != nil {
		t.Fatal(err)
	}
	unknown, _ := NewToken()
	// Same public ID, different secret: a guessed secret for a real token.
	wrongSecret := valid[:len(valid)-43] + strings.Repeat("A", 43)
	h.advance(time.Hour) // expiring is now expired

	tests := map[string]string{
		"empty":        "",
		"malformed":    "not-a-token",
		"unknown":      unknown,
		"wrong secret": wrongSecret,
		"expired":      expTok,
		"revoked":      revTok,
		"session-like": randomToken(),
	}
	for name, tok := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := h.svc.Authenticate(t.Context(), client, tok); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("Authenticate() = %v, want ErrUnauthenticated", err)
			}
		})
	}
	if _, err := h.svc.Authenticate(t.Context(), client, valid); err != nil {
		t.Fatalf("valid token after rejections: %v", err)
	}
	h.assertNoSecrets(valid, expTok, revTok, unknown, wrongSecret)
}

func TestAuthenticateRateLimitsGuesses(t *testing.T) {
	h := newTokenHarness(t, withLimits(func(l *TokenLimits) { l.FailuresPerIP = Rate{N: 3, Per: time.Minute} }))
	owner := h.owner()
	_, valid, err := h.svc.Create(t.Context(), owner, "valid", Scopes{Deploy: true}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Successes do not count.
	for range 5 {
		if _, err := h.svc.Authenticate(t.Context(), client, valid); err != nil {
			t.Fatal(err)
		}
	}
	guess := valid[:len(valid)-43] + strings.Repeat("B", 43)
	for i := range 3 {
		if _, err := h.svc.Authenticate(t.Context(), client, guess); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("guess %d = %v", i+1, err)
		}
	}
	// Over the limit, even the right token is refused from that IP...
	_, err = h.svc.Authenticate(t.Context(), client, valid)
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 || rl.RetryAfter > time.Minute {
		t.Fatalf("after guesses = %v, want *RateLimitError", err)
	}
	// ...but not from another.
	if _, err := h.svc.Authenticate(t.Context(), Client{IP: "198.51.100.7"}, valid); err != nil {
		t.Fatalf("other IP: %v", err)
	}
	// The limit resets with the window.
	h.advance(time.Minute)
	if _, err := h.svc.Authenticate(t.Context(), client, valid); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// With the limiter's table full, a rejected token from an address it does
// not hold is counted in the overflow sketch: the address is held to the
// same limit, and other new addresses keep working.
func TestAuthenticateCountsNewAddressesWhenTheTableIsFull(t *testing.T) {
	h := newTokenHarness(t, withLimits(func(l *TokenLimits) { l.FailuresPerIP = Rate{N: 3, Per: time.Minute} }))
	h.svc.limiter.maxKeys = 1
	owner := h.owner()
	_, valid, err := h.svc.Create(t.Context(), owner, "valid", Scopes{Deploy: true}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	guess := valid[:len(valid)-43] + strings.Repeat("B", 43)
	// The first address takes the table's only slot.
	if _, err := h.svc.Authenticate(t.Context(), client, guess); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("first guess = %v", err)
	}
	other := Client{IP: "198.51.100.7"}
	for i := range 3 {
		if _, err := h.svc.Authenticate(t.Context(), other, guess); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("guess %d from a new address with the table full = %v", i+1, err)
		}
	}
	for _, raw := range []string{guess, valid} {
		_, err := h.svc.Authenticate(t.Context(), other, raw)
		var rl *RateLimitError
		if !errors.As(err, &rl) || rl.RetryAfter <= 0 {
			t.Fatalf("a new address over its limit = %v, want *RateLimitError", err)
		}
	}
	if _, held := h.svc.limiter.windows["token-fail:"+ipKey(other.IP)]; held {
		t.Fatal("the full table took a new key")
	}
	if _, err := h.svc.Authenticate(t.Context(), Client{IP: "2001:db8:5::1"}, valid); err != nil {
		t.Fatalf("valid token from another new address: %v", err)
	}
	h.advance(time.Minute)
	if _, err := h.svc.Authenticate(t.Context(), other, guess); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after the window = %v", err)
	}
}

func TestListAndRevokeAreOwnerScoped(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	a, b := h.owner(), h.owner()
	var ids []string
	for range 3 {
		info, _, err := h.svc.Create(t.Context(), a, "a", Scopes{Deploy: true}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, info.ID)
		h.advance(time.Second)
	}
	bInfo, bTok, err := h.svc.Create(t.Context(), b, "b", Scopes{Manage: true}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	first, next, err := h.svc.List(t.Context(), a, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	second, last, err := h.svc.List(t.Context(), a, next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != ids[2] || first[1].ID != ids[1] || len(second) != 1 || second[0].ID != ids[0] || last != "" {
		t.Fatalf("pages %+v / %+v (next %q)", first, second, last)
	}
	if _, _, err := h.svc.List(t.Context(), a, "bogus!", 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("bad cursor = %v", err)
	}

	// Owner A cannot revoke B's token: the same ErrNotFound as a missing one.
	if err := h.svc.Revoke(t.Context(), a, bInfo.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign revoke = %v, want ErrNotFound", err)
	}
	if err := h.svc.Revoke(t.Context(), a, store.NewID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing revoke = %v, want ErrNotFound", err)
	}
	if _, err := h.svc.Authenticate(t.Context(), client, bTok); err != nil {
		t.Fatalf("B's token after A's attempt: %v", err)
	}
	if err := h.svc.Revoke(t.Context(), b, bInfo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authenticate(t.Context(), client, bTok); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked token = %v", err)
	}
	page, _, err := h.svc.List(t.Context(), b, "", 10)
	if err != nil || len(page) != 0 {
		t.Fatalf("revoked token still listed: %+v, %v", page, err)
	}
}

// withLimits returns the default limits changed by set.
func withLimits(set func(*TokenLimits)) TokenLimits {
	l := DefaultTokenLimits()
	set(&l)
	return l
}
