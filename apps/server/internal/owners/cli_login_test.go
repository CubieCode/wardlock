// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

var userCodePattern = regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`)

func TestStartCLILogin(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	l, err := h.svc.StartCLILogin(t.Context(), client, "wardlock 0.1.0 on build-host\x07")
	if err != nil {
		t.Fatal(err)
	}
	if !userCodePattern.MatchString(l.UserCode) || !strings.HasPrefix(l.DeviceCode, "dc_") || len(l.DeviceCode) < 32 {
		t.Fatalf("codes %q / %q", l.UserCode, l.DeviceCode)
	}
	if l.VerificationURL != "https://dash.example.test/cli-login" ||
		l.VerificationURLComplete != "https://dash.example.test/cli-login?code="+l.UserCode {
		t.Fatalf("URLs %q, %q", l.VerificationURL, l.VerificationURLComplete)
	}
	if l.Interval != 5*time.Second || !l.ExpiresAt.Equal(h.clock().Add(15*time.Minute)) {
		t.Fatalf("interval %v, expiry %v", l.Interval, l.ExpiresAt)
	}
	req, err := h.db.APITokens().GetCLILoginByDeviceCodeHash(t.Context(), h.svc.deviceCodeHash(l.DeviceCode))
	if err != nil {
		t.Fatal(err)
	}
	if req.ClientName != "wardlock 0.1.0 on build-host" || strings.Contains(req.DeviceCodeHash, l.DeviceCode) {
		t.Fatalf("stored %+v", req)
	}
	h.assertNoSecrets(l.DeviceCode)
}

// approvedLogin starts a login and approves it for owner.
func approvedLogin(t *testing.T, h *tokenHarness, owner string, scopes Scopes, name string) CLILogin {
	t.Helper()
	l, err := h.svc.StartCLILogin(t.Context(), client, "wardlock dev on build-host")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ApproveCLILogin(t.Context(), owner, l.UserCode, scopes, name); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCLILoginApproveAndPoll(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	l, err := h.svc.StartCLILogin(t.Context(), client, "wardlock dev on build-host")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode); !errors.Is(err, ErrLoginPending) {
		t.Fatalf("first poll = %v, want ErrLoginPending", err)
	}
	// Lower case and no hyphen are accepted.
	typed := strings.ToLower(strings.ReplaceAll(l.UserCode, "-", " "))
	if err := h.svc.ApproveCLILogin(t.Context(), owner, typed, Scopes{Deploy: true, Manage: true}, ""); err != nil {
		t.Fatal(err)
	}
	h.advance(5 * time.Second)
	info, tok, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "wardlock dev on build-host" || info.Scopes != (Scopes{Deploy: true, Manage: true}) || !info.ExpiresAt.IsZero() {
		t.Fatalf("token %+v", info)
	}
	auth, err := h.svc.Authenticate(t.Context(), client, tok)
	if err != nil || auth.OwnerID != owner || auth.TokenID != info.ID {
		t.Fatalf("Authenticate(new token) = %+v, %v", auth, err)
	}
	// Delivered once.
	h.advance(5 * time.Second)
	if _, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("second delivery = %v, want ErrLoginExpired", err)
	}
	h.assertNoSecrets(tok, l.DeviceCode)
}

func TestCLILoginApprovedName(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	l := approvedLogin(t, h, owner, Scopes{Deploy: true}, "Laptop")
	info, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode)
	if err != nil || info.Name != "Laptop" || info.Scopes != (Scopes{Deploy: true}) {
		t.Fatalf("token %+v, %v", info, err)
	}
	// A CLI that sent no name gets the default.
	l2, err := h.svc.StartCLILogin(t.Context(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ApproveCLILogin(t.Context(), owner, l2.UserCode, Scopes{Manage: true}, ""); err != nil {
		t.Fatal(err)
	}
	info, _, err = h.svc.PollCLILogin(t.Context(), client, l2.DeviceCode)
	if err != nil || info.Name != "wardlock CLI" {
		t.Fatalf("token %+v, %v", info, err)
	}
}

func TestCLILoginDenied(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	l, err := h.svc.StartCLILogin(t.Context(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DenyCLILogin(t.Context(), owner, l.UserCode); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("poll after denial = %v", err)
	}
	if err := h.svc.ApproveCLILogin(t.Context(), owner, l.UserCode, Scopes{Deploy: true}, ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approve after denial = %v", err)
	}
}

func TestCLILoginExpires(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	pending, err := h.svc.StartCLILogin(t.Context(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	approved := approvedLogin(t, h, owner, Scopes{Deploy: true}, "")
	h.advance(CLILoginValidity)
	if err := h.svc.ApproveCLILogin(t.Context(), owner, pending.UserCode, Scopes{Deploy: true}, ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approve expired = %v", err)
	}
	if err := h.svc.DenyCLILogin(t.Context(), owner, pending.UserCode); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("deny expired = %v", err)
	}
	for name, l := range map[string]CLILogin{"pending": pending, "approved": approved} {
		if _, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode); !errors.Is(err, ErrLoginExpired) {
			t.Fatalf("%s poll after expiry = %v", name, err)
		}
	}
	// A day later the next start sweeps them away.
	h.advance(cliLoginRetention + time.Minute)
	if _, err := h.svc.StartCLILogin(t.Context(), client, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.APITokens().GetCLILoginByDeviceCodeHash(t.Context(), h.svc.deviceCodeHash(pending.DeviceCode)); err == nil {
		t.Fatal("expired request not swept")
	}
}

func TestCLILoginUnknownCodes(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	for _, code := range []string{"", "dc_short", "dc_" + randomToken(), randomToken()} {
		if _, _, err := h.svc.PollCLILogin(t.Context(), client, code); !errors.Is(err, ErrLoginExpired) {
			t.Errorf("poll %q = %v, want ErrLoginExpired", code, err)
		}
	}
	for _, code := range []string{"", "BCDF-GHJ", "AEIO-UBCD", "BCDF-GHJKL", "BCDF-GHJK"} {
		if err := h.svc.ApproveCLILogin(t.Context(), owner, code, Scopes{Deploy: true}, ""); !errors.Is(err, ErrInvalidCode) {
			t.Errorf("approve %q = %v, want ErrInvalidCode", code, err)
		}
	}
}

// A login another owner already decided answers exactly as a missing one.
func TestCLILoginOwnerIsolation(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	a, b := h.owner(), h.owner()
	l := approvedLogin(t, h, a, Scopes{Deploy: true}, "")
	errMissing := h.svc.ApproveCLILogin(t.Context(), b, "ZZZZ-ZZZZ", Scopes{Deploy: true}, "")
	for name, err := range map[string]error{
		"approve": h.svc.ApproveCLILogin(t.Context(), b, l.UserCode, Scopes{Manage: true}, "stolen"),
		"deny":    h.svc.DenyCLILogin(t.Context(), b, l.UserCode),
	} {
		if !errors.Is(err, ErrInvalidCode) || err.Error() != errMissing.Error() {
			t.Fatalf("owner B %s = %v, want the same error as a missing code (%v)", name, err, errMissing)
		}
	}
	info, tok, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode)
	if err != nil || info.Scopes != (Scopes{Deploy: true}) {
		t.Fatalf("token %+v, %v", info, err)
	}
	auth, err := h.svc.Authenticate(t.Context(), client, tok)
	if err != nil || auth.OwnerID != a {
		t.Fatalf("token belongs to %q (%v), want owner A", auth.OwnerID, err)
	}
	if err := h.svc.Revoke(t.Context(), b, info.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner B revoking A's CLI token = %v", err)
	}
}

func TestCLILoginApproveValidation(t *testing.T) {
	h := newTokenHarness(t, TokenLimits{})
	owner := h.owner()
	l, err := h.svc.StartCLILogin(t.Context(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ApproveCLILogin(t.Context(), owner, l.UserCode, Scopes{}, ""); !errors.Is(err, ErrInvalidScopes) {
		t.Fatalf("no scopes = %v", err)
	}
	if err := h.svc.ApproveCLILogin(t.Context(), owner, l.UserCode, Scopes{Deploy: true}, strings.Repeat("n", 101)); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("long name = %v", err)
	}
	// Neither failure decided the login.
	if err := h.svc.ApproveCLILogin(t.Context(), owner, l.UserCode, Scopes{Deploy: true}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestCLILoginPollRateLimits(t *testing.T) {
	h := newTokenHarness(t, withLimits(func(l *TokenLimits) { l.PollPerIP = Rate{N: 4, Per: time.Minute} }))
	l, err := h.svc.StartCLILogin(t.Context(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode); !errors.Is(err, ErrLoginPending) {
		t.Fatal(err)
	}
	// Too soon after the last poll: slow down, and the wait covers the
	// rest of the interval.
	h.advance(2 * time.Second)
	_, _, err = h.svc.PollCLILogin(t.Context(), client, l.DeviceCode)
	var sd *SlowDownError
	if !errors.As(err, &sd) || !errors.Is(err, ErrSlowDown) || sd.RetryAfter != 3*time.Second {
		t.Fatalf("fast poll = %v, want slow down after 3s", err)
	}
	// Just inside the interval's slack is fine.
	h.advance(2 * time.Second)
	if _, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode); !errors.Is(err, ErrLoginPending) {
		t.Fatalf("poll after 4s = %v", err)
	}
	// The per-IP limit covers every poll, including unknown codes.
	h.advance(5 * time.Second)
	_, _, err = h.svc.PollCLILogin(t.Context(), client, "dc_"+randomToken())
	if !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("unknown code = %v", err)
	}
	_, _, err = h.svc.PollCLILogin(t.Context(), client, l.DeviceCode)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("fifth poll in a minute = %v, want *RateLimitError", err)
	}
	h.advance(time.Minute)
	if _, _, err := h.svc.PollCLILogin(t.Context(), client, l.DeviceCode); !errors.Is(err, ErrLoginPending) {
		t.Fatalf("after the window = %v", err)
	}
}

func TestCLILoginStartAndDecideRateLimits(t *testing.T) {
	h := newTokenHarness(t, withLimits(func(l *TokenLimits) {
		l.StartPerIP = Rate{N: 2, Per: time.Hour}
		l.DecidePerOwner = Rate{N: 2, Per: time.Hour}
	}))
	owner := h.owner()
	var logins []CLILogin
	for range 2 {
		l, err := h.svc.StartCLILogin(t.Context(), client, "")
		if err != nil {
			t.Fatal(err)
		}
		logins = append(logins, l)
	}
	var rl *RateLimitError
	if _, err := h.svc.StartCLILogin(t.Context(), client, ""); !errors.As(err, &rl) {
		t.Fatalf("third start = %v", err)
	}
	if _, err := h.svc.StartCLILogin(t.Context(), Client{IP: "198.51.100.7"}, ""); err != nil {
		t.Fatalf("start from another IP: %v", err)
	}
	if err := h.svc.DenyCLILogin(t.Context(), owner, "ZZZZ-ZZZZ"); !errors.Is(err, ErrInvalidCode) {
		t.Fatal(err)
	}
	if err := h.svc.DenyCLILogin(t.Context(), owner, logins[0].UserCode); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ApproveCLILogin(t.Context(), owner, logins[1].UserCode, Scopes{Deploy: true}, ""); !errors.As(err, &rl) {
		t.Fatalf("third decision = %v", err)
	}
}

func TestNormaliseUserCode(t *testing.T) {
	tests := map[string]string{
		"BDFH-KMPR":   "BDFH-KMPR",
		"bdfhkmpr":    "BDFH-KMPR",
		" bdfh kmpr ": "BDFH-KMPR",
		"BDFH-KMP":    "",
		"BDFH-KMPRS":  "",
		"ADFH-KMPR":   "",
		"BDF1-KMPR":   "",
	}
	for in, want := range tests {
		got, ok := NormaliseUserCode(in)
		if got != want || ok != (want != "") {
			t.Errorf("NormaliseUserCode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for range 100 {
		if c := newUserCode(); !userCodePattern.MatchString(c) {
			t.Fatalf("newUserCode() = %q", c)
		}
	}
}
