// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"wardlock.nz/server/internal/store"
)

var (
	// ErrWrongPassword covers every failed password sign-in alike: a wrong
	// password, and a path whose policy has no password at all.
	ErrWrongPassword = errors.New("wrong password")
	// ErrPasswordChanged means IssueSession refused a password session
	// because the policy's password or version changed after the check.
	ErrPasswordChanged = errors.New("policy password changed")
	// ErrRateLimited is matched by every *RateLimitError.
	ErrRateLimited = errors.New("rate limited")
)

// RateLimitError says a limit was hit and when to retry.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "rate limited" }

// Is makes errors.Is(err, ErrRateLimited) match.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Password length bounds for owners setting one. The maximum also caps what
// a sign-in attempt hashes.
const (
	MinPasswordChars = 8
	MaxPasswordBytes = 1024
)

// attemptLimiter is the part of the service's in-memory limiters
// (limit.go) that password sign-in uses: allow counts one attempt for key
// under r, or returns false and how long until the window resets.
type attemptLimiter interface {
	allow(key string, r Rate) (bool, time.Duration)
}

// Argon2id parameters for new hashes: the OWASP baseline of 19 MiB, two
// passes, one lane. Stored hashes carry their own parameters, so these can
// rise without invalidating existing passwords.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
	// argonMaxMemory bounds what a stored hash can make a sign-in cost.
	argonMaxMemory = 256 * 1024
	argonMaxTime   = 16
)

// deriveFunc is argon2.IDKey's shape; tests count calls through it.
type deriveFunc func(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte

type argonHash struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

// hashPassword returns the PHC-format Argon2id hash of pw with a fresh
// random salt: $argon2id$v=19$m=…,t=…,p=…$<salt>$<key> (unpadded base64).
func hashPassword(derive deriveFunc, pw string) string {
	salt := make([]byte, argonSaltLen)
	_, _ = rand.Read(salt) // crypto/rand.Read never fails since Go 1.24.
	key := derive([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// parseHash reads a hash hashPassword wrote. ok is false for anything else,
// or for parameters outside the bounds a sign-in may cost.
func parseHash(encoded string) (argonHash, bool) {
	f := strings.Split(encoded, "$")
	if len(f) != 6 || f[0] != "" || f[1] != "argon2id" || f[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return argonHash{}, false
	}
	var h argonHash
	if n, err := fmt.Sscanf(f[3], "m=%d,t=%d,p=%d", &h.memory, &h.time, &h.threads); err != nil || n != 3 ||
		h.memory < 8 || h.memory > argonMaxMemory || h.time < 1 || h.time > argonMaxTime || h.threads < 1 {
		return argonHash{}, false
	}
	var err error
	if h.salt, err = base64.RawStdEncoding.Strict().DecodeString(f[4]); err != nil || len(h.salt) < 8 {
		return argonHash{}, false
	}
	if h.key, err = base64.RawStdEncoding.Strict().DecodeString(f[5]); err != nil || len(h.key) != argonKeyLen {
		return argonHash{}, false
	}
	return h, true
}

// dummySalt stands in for a missing hash so a failed check costs the same.
var dummySalt = make([]byte, argonSaltLen)

// verifyPassword reports whether pw matches encoded. It always derives one
// key, with encoded's parameters or the defaults when encoded is empty or
// unreadable, and compares in constant time, so neither the answer nor the
// presence of a password shows in how long it takes.
func verifyPassword(derive deriveFunc, encoded, pw string) bool {
	h, ok := parseHash(encoded)
	if !ok {
		h = argonHash{memory: argonMemory, time: argonTime, threads: argonThreads, salt: dummySalt, key: make([]byte, argonKeyLen)}
	}
	got := derive([]byte(pw), h.salt, h.time, h.memory, h.threads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, h.key) == 1 && ok
}

// checkPassword validates a password an owner is setting. Its messages
// never repeat the password.
func checkPassword(pw string) error {
	invalid := func(msg string) error {
		return &TargetError{Err: ErrInvalidPolicy, Target: "password", Message: msg}
	}
	switch {
	case !utf8.ValidString(pw):
		return invalid("The password is not valid UTF-8.")
	case utf8.RuneCountInString(pw) < MinPasswordChars:
		return invalid(fmt.Sprintf("The password needs at least %d characters.", MinPasswordChars))
	case len(pw) > MaxPasswordBytes:
		return invalid(fmt.Sprintf("The password can be at most %d bytes.", MaxPasswordBytes))
	}
	return nil
}

// passwordHash works out the hash a Set stores for r, given cur, the
// policy at r's prefix ("" hash when there is none): a new password is
// hashed, an enabled password without a new one keeps cur's, and a policy
// without the method stores none.
func (s *Service) passwordHash(r Rule, cur store.Policy) (string, error) {
	switch {
	case !r.Password && r.NewPassword != "":
		return "", &TargetError{Err: ErrInvalidPolicy, Target: "password", Message: "A password is set only with the password method."}
	case !r.Password:
		return "", nil
	case r.NewPassword != "":
		if err := checkPassword(r.NewPassword); err != nil {
			return "", err
		}
		return hashPassword(s.derive, r.NewPassword), nil
	case cur.PasswordEnabled && cur.PasswordHash != "":
		return cur.PasswordHash, nil
	}
	return "", &TargetError{Err: ErrInvalidPolicy, Target: "password", Message: "The password method needs a password."}
}

// PasswordAttempt is one visitor's password sign-in, as plain values.
type PasswordAttempt struct {
	SiteID string
	// Path is the cleaned path the visitor is signing in for: the password
	// of the policy governing it is the one checked, and the session is
	// bound to that policy.
	Path string
	// IP is the client address as the trusted-proxy middleware left it.
	IP       string
	Password string
}

// PasswordLogin checks a visitor's password and returns a site session
// cookie for the policy governing a.Path. Every failure is
// ErrWrongPassword, whether the password was wrong or the policy has none;
// a *RateLimitError when the client or the site has made too many attempts.
// The password is never logged or kept.
func (s *Service) PasswordLogin(ctx context.Context, a PasswordAttempt) (Cookie, error) {
	if err := allowAttempt(s.ipAttempts, "password-ip:"+ipKey(a.IP), s.limits.PasswordPerIP); err != nil {
		return Cookie{}, err
	}
	if err := allowAttempt(s.siteAttempts, "password-site:"+a.SiteID, s.limits.PasswordPerSite); err != nil {
		return Cookie{}, err
	}
	ps, err := s.sitePolicies(ctx, a.SiteID)
	if err != nil {
		return Cookie{}, err
	}
	p, ok := Governing(ps, a.Path)
	hash := ""
	if ok && enables(p, store.SessionPassword) {
		hash = p.PasswordHash
	}
	pw := a.Password
	if len(pw) > MaxPasswordBytes {
		// Longer than any password an owner can set: still hash (a
		// bounded prefix) so the reply takes the usual time.
		pw, hash = pw[:MaxPasswordBytes], ""
	}
	if !verifyPassword(s.derive, hash, pw) {
		s.log.LogAttrs(ctx, slog.LevelInfo, "password sign-in refused", slog.String("site_id", a.SiteID), slog.String("policy_id", p.ID))
		return Cookie{}, ErrWrongPassword
	}
	c, err := s.IssueSession(ctx, NewSession{SiteID: a.SiteID, Policy: p, Method: store.SessionPassword, PasswordHash: hash})
	if errors.Is(err, ErrPasswordChanged) {
		// Changed while checking: the password just tried is no longer
		// the policy's, so it is as wrong as any other.
		s.log.LogAttrs(ctx, slog.LevelInfo, "password sign-in refused", slog.String("site_id", a.SiteID), slog.String("policy_id", p.ID))
		return Cookie{}, ErrWrongPassword
	}
	if err != nil {
		return Cookie{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "password sign-in", slog.String("site_id", a.SiteID), slog.String("policy_id", p.ID))
	return c, nil
}

// allowAttempt counts one password attempt for key on l, or returns a
// *RateLimitError. Without a limiter it refuses: attempts are never
// unlimited.
func allowAttempt(l attemptLimiter, key string, r Rate) error {
	if l == nil {
		return errors.New("password sign-in: no attempt limiter")
	}
	if ok, wait := l.allow(key, r); !ok {
		return &RateLimitError{RetryAfter: wait}
	}
	return nil
}
