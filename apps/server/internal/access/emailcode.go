// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	netmail "net/mail"
	"strings"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store"
)

// Email-code lifetimes from SPEC "Email code".
const (
	EmailCodeValidity = 10 * time.Minute
	EmailCodeAttempts = 5
	// emailSendTimeout bounds one background send, database work and mail
	// delivery together.
	emailSendTimeout = time.Minute
)

var (
	// ErrInvalidAddress means the visitor's entry is not a plain email
	// address. It says nothing about the allowlist.
	ErrInvalidAddress = errors.New("invalid email address")
	// ErrInvalidCode covers every failed email-code sign-in alike: a wrong,
	// expired, used-up or replaced code, an address that is not allowed or
	// was sent no code, and a policy without the email method or changed
	// since the code was sent.
	ErrInvalidCode = errors.New("invalid email code")
	// ErrPolicyChanged means IssueSession refused an email session because
	// its policy changed version or dropped the method after the check.
	ErrPolicyChanged = errors.New("policy changed")
)

// Purposes keep the stored address and code hashes apart from every other
// use of the server key.
const (
	purposeEmailAddress = "visitor_email_address"
	purposeEmailCode    = "visitor_email_code"
)

// addressHash is the stored form of a visitor's address for one policy.
func (k keyed) addressHash(siteID, policyID, addr string) string {
	return hex.EncodeToString(k.mac(purposeEmailAddress, []byte(siteID), []byte(policyID), []byte(addr)))
}

// codeHash is the stored form of a code sent to addr for one policy.
func (k keyed) codeHash(siteID, policyID, addr, code string) string {
	return hex.EncodeToString(k.mac(purposeEmailCode, []byte(siteID), []byte(policyID), []byte(addr), []byte(code)))
}

// EmailCodeRequest is a visitor asking for a code, as plain values.
type EmailCodeRequest struct {
	SiteID string
	// Site is the host the visitor is on, named in the message.
	Site string
	// Path is the cleaned path the visitor is signing in for: the policy
	// governing it decides whether the address is allowed, and the code
	// opens only that policy.
	Path string
	// IP is the client address as the trusted-proxy middleware left it.
	IP      string
	Address string
}

// SendEmailCode emails a 6-digit code to r.Address if the policy governing
// r.Path enables email codes and its allowlist lets the address in. The
// answer is the same either way: after the address is read and the rate
// limits counted (per IP, per address and per site, allowed or not), the
// allowlist check, code creation and mail all run in the background, so
// allowed and disallowed addresses return after the same work. It returns
// ErrInvalidAddress for an entry that is not an address, or a
// *RateLimitError. The code is never logged or kept, only its HMAC.
func (s *Service) SendEmailCode(ctx context.Context, r EmailCodeRequest) error {
	addr, ok := NormaliseAddress(r.Address)
	if !ok {
		return ErrInvalidAddress
	}
	if err := allowAttempt(s.ipAttempts, "email-send-ip:"+ipKey(r.IP), s.limits.EmailSendPerIP); err != nil {
		return err
	}
	if err := allowAttempt(s.addressAttempts, "email-send-address:"+addr, s.limits.EmailSendPerAddress); err != nil {
		return err
	}
	if err := s.allowSiteAttempt("email-send", r.SiteID, r.IP, s.limits.EmailSendPerSite); err != nil {
		return err
	}
	s.background(ctx, addr, func(ctx context.Context) error { return s.sendEmailCode(ctx, r, addr) })
	return nil
}

func (s *Service) sendEmailCode(ctx context.Context, r EmailCodeRequest, addr string) error {
	ps, err := s.sitePolicies(ctx, r.SiteID)
	if err != nil {
		return err
	}
	p, ok := Governing(ps, r.Path)
	if !ok || !enables(p, store.SessionEmail) || !allows(p.Allowlist, addr) {
		return nil
	}
	now := s.now()
	code := sixDigits()
	err = s.c.EmailCodes.Put(ctx, store.VisitorEmailCode{
		ID: store.NewID(), SiteID: r.SiteID, PolicyID: p.ID, PolicyVersion: p.Version,
		AddressHash: s.key.addressHash(r.SiteID, p.ID, addr),
		CodeHash:    s.key.codeHash(r.SiteID, p.ID, addr, code),
		CreatedAt:   now, ExpiresAt: now.Add(EmailCodeValidity),
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil // the policy changed meanwhile: send nothing
	}
	if err != nil {
		return fmt.Errorf("policy %s: %w", p.ID, err)
	}
	if s.c.Mailer == nil {
		return fmt.Errorf("policy %s: no mailer configured", p.ID)
	}
	msg, err := mail.EmailCodeMessage(netmail.Address{Address: addr}, mail.EmailCode{Code: code, Site: r.Site, ValidFor: EmailCodeValidity})
	if err != nil {
		return fmt.Errorf("policy %s: %w", p.ID, err)
	}
	if err := s.c.Mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("policy %s: %w", p.ID, err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "email code sent", slog.String("site_id", r.SiteID), slog.String("policy_id", p.ID))
	return nil
}

// background runs fn after the request, on a context that outlives it but
// not emailSendTimeout. Close waits for it. A failure is logged without
// addr: mail errors carry only the SMTP stage and reply code, and addr is
// redacted from the text all the same in case another error repeats it.
func (s *Service) background(ctx context.Context, addr string, fn func(context.Context) error) {
	ctx = context.WithoutCancel(ctx)
	s.bg.Go(func() {
		ctx, cancel := context.WithTimeout(ctx, emailSendTimeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			msg := err.Error()
			if addr != "" {
				msg = strings.ReplaceAll(msg, addr, "[address]")
			}
			s.log.LogAttrs(ctx, slog.LevelError, "email code failed", slog.String("error", msg))
		}
	})
}

// EmailCodeAttempt is one visitor's code entry, as plain values.
type EmailCodeAttempt struct {
	SiteID string
	// Path is the cleaned path the code was sent for.
	Path    string
	IP      string
	Address string
	Code    string
}

// EmailCodeLogin checks a visitor's code and returns a site session
// cookie for the policy governing a.Path, whose subject is the verified
// address. Every failure is ErrInvalidCode; a *RateLimitError when the
// client, the address or the site has made too many attempts. Each attempt
// is counted against the code before the comparison, so concurrent
// guesses cannot exceed EmailCodeAttempts. Whether or not the address is
// allowed or has a live code, the attempt does the same lookup and the
// same conditional UPDATE (on a fresh ID when there is no usable code), so
// a refusal costs about the same either way. What remains measurable (the
// comparison and session insert of a right code) needs the code itself;
// small timing differences are further bounded by the per-IP and
// per-address verify limits.
func (s *Service) EmailCodeLogin(ctx context.Context, a EmailCodeAttempt) (Cookie, error) {
	if err := allowAttempt(s.ipAttempts, "email-verify-ip:"+ipKey(a.IP), s.limits.EmailVerifyPerIP); err != nil {
		return Cookie{}, err
	}
	if err := s.allowSiteAttempt("email-verify", a.SiteID, a.IP, s.limits.EmailVerifyPerSite); err != nil {
		return Cookie{}, err
	}
	addr, ok := NormaliseAddress(a.Address)
	if !ok {
		return Cookie{}, ErrInvalidCode
	}
	if err := allowAttempt(s.addressAttempts, "email-verify-address:"+addr, s.limits.EmailVerifyPerAddress); err != nil {
		return Cookie{}, err
	}
	if !isSixDigits(a.Code) {
		return Cookie{}, ErrInvalidCode
	}
	ps, err := s.sitePolicies(ctx, a.SiteID)
	if err != nil {
		return Cookie{}, err
	}
	p, ok := Governing(ps, a.Path)
	if !ok || !enables(p, store.SessionEmail) {
		return Cookie{}, ErrInvalidCode
	}
	refuse := func(why string) (Cookie, error) {
		s.log.LogAttrs(ctx, slog.LevelInfo, "email code refused",
			slog.String("site_id", a.SiteID), slog.String("policy_id", p.ID), slog.String("reason", why))
		return Cookie{}, ErrInvalidCode
	}
	c, err := s.c.EmailCodes.Get(ctx, a.SiteID, p.ID, s.key.addressHash(a.SiteID, p.ID, addr))
	why := ""
	switch {
	case errors.Is(err, store.ErrNotFound):
		why = "no code"
	case err != nil:
		return Cookie{}, fmt.Errorf("get email code: %w", err)
	case c.PolicyVersion != p.Version:
		why = "stale"
	}
	now := s.now()
	attemptID := c.ID
	if why != "" {
		// Count against no code at all, so the refusal does the same
		// database work as a wrong guess at a live one.
		attemptID = store.NewID()
	}
	if _, err := s.c.EmailCodes.AddAttempt(ctx, attemptID, EmailCodeAttempts, now); errors.Is(err, store.ErrNotFound) {
		if why == "" {
			why = "stale" // expired or out of attempts
		}
		return refuse(why)
	} else if err != nil {
		return Cookie{}, fmt.Errorf("count email code attempt: %w", err)
	}
	if !hmac.Equal([]byte(s.key.codeHash(a.SiteID, p.ID, addr, a.Code)), []byte(c.CodeHash)) {
		return refuse("wrong code")
	}
	// Codes are only sent to allowed addresses, and an allowlist change
	// bumps the version; check again all the same.
	if !allows(p.Allowlist, addr) {
		return refuse("not allowed")
	}
	if err := s.c.EmailCodes.Use(ctx, c.ID, now); errors.Is(err, store.ErrNotFound) {
		return refuse("used")
	} else if err != nil {
		return Cookie{}, fmt.Errorf("use email code: %w", err)
	}
	cookie, err := s.IssueSession(ctx, NewSession{SiteID: a.SiteID, Policy: p, Method: store.SessionEmail, Subject: addr})
	if errors.Is(err, ErrPolicyChanged) {
		return refuse("policy changed")
	}
	if err != nil {
		return Cookie{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "email code sign-in", slog.String("site_id", a.SiteID), slog.String("policy_id", p.ID))
	return cookie, nil
}

func sixDigits() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(fmt.Sprintf("access: crypto/rand: %v", err))
	}
	return fmt.Sprintf("%06d", n.Int64())
}

func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
