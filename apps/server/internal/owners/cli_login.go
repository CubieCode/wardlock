// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"strings"
	"time"
	"unicode"

	"wardlock.nz/server/internal/store"
)

// The CLI device login (docs/SPEC.md "Owners and sign-in"): `wardlock login`
// starts a request and polls with a secret device code; a signed-in owner
// approves or denies it on the dashboard's CLILoginPath page by its short
// user code; the next poll after approval mints the API token and delivers
// it once.
const (
	CLILoginPath     = "/cli-login"
	CLILoginValidity = 15 * time.Minute
	// CLILoginInterval is the minimum time between polls the CLI is told.
	CLILoginInterval = 5 * time.Second
	// pollSlack forgives network jitter: a poll is too fast only when it
	// comes this much sooner than CLILoginInterval after the last one.
	pollSlack = time.Second
	// cliLoginRetention is how long a finished request is kept before
	// Start sweeps it away.
	cliLoginRetention = 24 * time.Hour
	// defaultCLITokenName names a CLI token when neither the CLI nor the
	// approving owner gave a name.
	defaultCLITokenName = "wardlock CLI"
	// userCodeChars has no vowels (no words) and no look-alikes.
	userCodeChars    = "BCDFGHJKLMNPQRSTVWXZ"
	userCodeLen      = 8
	deviceCodePrefix = "dc_"
	// codeAttempts bounds retries after a user code collision.
	codeAttempts = 5
)

var (
	// ErrLoginPending means the CLI login is still waiting for an owner.
	ErrLoginPending = errors.New("login pending")
	// ErrLoginExpired covers every CLI login poll that will never succeed:
	// unknown, expired, denied or already delivered. Callers cannot tell
	// these apart.
	ErrLoginExpired = errors.New("login expired")
	// ErrSlowDown is matched by every *SlowDownError.
	ErrSlowDown = errors.New("slow down")
)

// SlowDownError says a CLI polled sooner than the login's interval allows.
type SlowDownError struct{ RetryAfter time.Duration }

func (e *SlowDownError) Error() string { return "slow down" }

// Is makes errors.Is(err, ErrSlowDown) match.
func (e *SlowDownError) Is(target error) bool { return target == ErrSlowDown }

// CLILogin is a started CLI login. DeviceCode is secret: it exists only in
// this value and the response that carries it.
type CLILogin struct {
	DeviceCode              string
	UserCode                string
	VerificationURL         string
	VerificationURLComplete string
	ExpiresAt               time.Time
	Interval                time.Duration
}

// StartCLILogin begins a CLI login for a CLI calling itself clientName.
func (s *Tokens) StartCLILogin(ctx context.Context, c Client, clientName string) (CLILogin, error) {
	if err := s.allow("cli-start:"+ipKey(c.IP), s.limits.StartPerIP); err != nil {
		return CLILogin{}, err
	}
	now := s.now()
	if err := s.tokens.DeleteCLILoginsExpiredBefore(ctx, now.Add(-cliLoginRetention)); err != nil {
		return CLILogin{}, err
	}
	deviceCode := deviceCodePrefix + randomToken()
	req := store.CLILoginRequest{
		ID:             store.NewID(),
		DeviceCodeHash: s.deviceCodeHash(deviceCode),
		Status:         store.CLILoginPending,
		CreatedAt:      now,
		ExpiresAt:      now.Add(CLILoginValidity),
		ClientName:     cleanClientName(clientName),
	}
	for attempt := 1; ; attempt++ {
		req.UserCode = newUserCode()
		err := s.tokens.CreateCLILogin(ctx, req)
		if err == nil {
			break
		}
		if !errors.Is(err, store.ErrConflict) || attempt == codeAttempts {
			return CLILogin{}, err
		}
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "cli login started", slog.String("login_id", req.ID))
	page := *s.dash
	page.Path = CLILoginPath
	complete := page
	complete.RawQuery = url.Values{"code": {req.UserCode}}.Encode()
	return CLILogin{
		DeviceCode:              deviceCode,
		UserCode:                req.UserCode,
		VerificationURL:         page.String(),
		VerificationURLComplete: complete.String(),
		ExpiresAt:               req.ExpiresAt,
		Interval:                CLILoginInterval,
	}, nil
}

// PollCLILogin answers a CLI's poll: ErrLoginPending while it waits, the
// new token once approved (exactly once), and ErrLoginExpired otherwise.
// Polling sooner than the interval is a *SlowDownError.
func (s *Tokens) PollCLILogin(ctx context.Context, c Client, deviceCode string) (APITokenInfo, string, error) {
	if err := s.allow("cli-poll:"+ipKey(c.IP), s.limits.PollPerIP); err != nil {
		return APITokenInfo{}, "", err
	}
	if !strings.HasPrefix(deviceCode, deviceCodePrefix) || !wellFormedToken(deviceCode[len(deviceCodePrefix):]) {
		return APITokenInfo{}, "", ErrLoginExpired
	}
	hash := s.deviceCodeHash(deviceCode)
	req, err := s.tokens.GetCLILoginByDeviceCodeHash(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		return APITokenInfo{}, "", ErrLoginExpired
	}
	if err != nil {
		return APITokenInfo{}, "", err
	}
	// Only known logins get an interval window, so unknown codes cannot
	// fill the limiter's table.
	if ok, wait := s.limiter.allow("cli-poll-login:"+req.ID, Rate{N: 1, Per: CLILoginInterval - pollSlack}); !ok {
		return APITokenInfo{}, "", &SlowDownError{RetryAfter: wait + pollSlack}
	}
	now := s.now()
	if !now.Before(req.ExpiresAt) {
		return APITokenInfo{}, "", ErrLoginExpired
	}
	switch req.Status {
	case store.CLILoginPending:
		return APITokenInfo{}, "", ErrLoginPending
	case store.CLILoginApproved:
	default:
		return APITokenInfo{}, "", ErrLoginExpired
	}
	name := req.ClientName
	if name == "" {
		name = defaultCLITokenName
	}
	scopes := Scopes{Deploy: req.ScopeDeploy, Manage: req.ScopeManage}
	row, token, err := s.mint(ctx, req.OwnerID, name, scopes, time.Time{}, now, func(ctx context.Context, t store.APIToken) error {
		return s.tokens.RedeemCLILogin(ctx, req.ID, t, now)
	})
	if errors.Is(err, store.ErrNotFound) {
		return APITokenInfo{}, "", ErrLoginExpired // delivered to a concurrent poll
	}
	if err != nil {
		return APITokenInfo{}, "", fmt.Errorf("cli login %s: %w", req.ID, err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "cli login redeemed", slog.String("login_id", req.ID),
		slog.String("owner_id", row.OwnerID), slog.String("token_id", row.ID), slog.String("public_id", row.PublicID))
	return tokenInfo(row), token, nil
}

// ApproveCLILogin lets the pending login with userCode receive a token for
// ownerID with scopes, named name (or what the CLI called itself). An
// unknown, expired or already decided code, including one another owner
// decided, is ErrInvalidCode.
func (s *Tokens) ApproveCLILogin(ctx context.Context, ownerID, userCode string, scopes Scopes, name string) error {
	if err := s.allow("cli-decide:"+ownerID, s.limits.DecidePerOwner); err != nil {
		return err
	}
	if !scopes.Any() {
		return ErrInvalidScopes
	}
	if name != "" {
		var err error
		if name, err = tokenName(name); err != nil {
			return err
		}
	}
	return s.decide(ctx, store.CLILoginDecision{
		UserCode: userCode, Status: store.CLILoginApproved, OwnerID: ownerID,
		ScopeDeploy: scopes.Deploy, ScopeManage: scopes.Manage, TokenName: name,
	})
}

// DenyCLILogin ends the pending login with userCode; the CLI's next poll
// answers ErrLoginExpired. Codes fail as in ApproveCLILogin.
func (s *Tokens) DenyCLILogin(ctx context.Context, ownerID, userCode string) error {
	if err := s.allow("cli-decide:"+ownerID, s.limits.DecidePerOwner); err != nil {
		return err
	}
	return s.decide(ctx, store.CLILoginDecision{UserCode: userCode, Status: store.CLILoginDenied, OwnerID: ownerID})
}

func (s *Tokens) decide(ctx context.Context, d store.CLILoginDecision) error {
	code, ok := NormaliseUserCode(d.UserCode)
	if !ok {
		return ErrInvalidCode
	}
	d.UserCode, d.At = code, s.now()
	err := s.tokens.DecideCLILogin(ctx, d)
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidCode
	}
	if err != nil {
		return err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "cli login "+string(d.Status), slog.String("owner_id", d.OwnerID))
	return nil
}

func (s *Tokens) deviceCodeHash(code string) string {
	return HashToken(s.secret, "cli_device_code\x00"+code)
}

// NormaliseUserCode accepts a user code typed in any case, with or without
// the hyphen and spaces, and returns it as stored (XXXX-XXXX).
func NormaliseUserCode(raw string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		switch {
		case r == '-' || unicode.IsSpace(r):
		case strings.ContainsRune(userCodeChars, r) && b.Len() < userCodeLen:
			b.WriteRune(r)
		default:
			return "", false
		}
	}
	if b.Len() != userCodeLen {
		return "", false
	}
	c := b.String()
	return c[:4] + "-" + c[4:], true
}

func newUserCode() string {
	b := make([]byte, userCodeLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeChars))))
		if err != nil {
			panic(fmt.Sprintf("owners: crypto/rand: %v", err))
		}
		b[i] = userCodeChars[n.Int64()]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

// cleanClientName keeps a CLI's self-description printable and within the
// token name limit; it becomes the token's default name.
func cleanClientName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, ""))
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > maxTokenName {
		name = strings.TrimSpace(string(r[:maxTokenName]))
	}
	return name
}
