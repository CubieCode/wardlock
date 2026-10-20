// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	netmail "net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store"
)

// Lifetimes from docs/SPEC.md "Owners and sign-in".
const (
	SetupValidity     = 60 * time.Minute
	SignInURLValidity = 60 * time.Minute // `wardlockd admin create`
	InviteValidity    = 7 * 24 * time.Hour
	EmailCodeValidity = 10 * time.Minute
	EmailCodeAttempts = 5
	SessionIdle       = 14 * 24 * time.Hour
	SessionAbsolute   = 90 * 24 * time.Hour
	// touchEvery is how stale a session's last use may get before a request
	// records it, so not every request writes to the database.
	touchEvery = 5 * time.Minute
	// bgTimeout bounds one background send, including its database work.
	bgTimeout = time.Minute
)

// Paths on the dashboard host that one-time URLs open.
const (
	SetupPath  = "/setup"
	SignInPath = "/sign-in"
)

var (
	// ErrInvalidCode covers every failed setup, sign-in-link or email-code
	// redemption: wrong, used, expired, exhausted, or for an address that
	// is not an owner's. Callers cannot tell these apart.
	ErrInvalidCode = errors.New("invalid code")
	// ErrInvalidEmail means the address is not a plain email address.
	ErrInvalidEmail = errors.New("invalid email address")
	// ErrUnauthenticated means the session token is unknown, expired or
	// revoked.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrNotFound means the resource does not exist or belongs to another
	// owner.
	ErrNotFound = errors.New("not found")
	// ErrOwnerExists means `wardlockd admin create` named an existing owner.
	ErrOwnerExists = errors.New("an owner with this email address exists")
	// ErrInvalidCursor means a list cursor was not one this package issued.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrRateLimited is matched by every *RateLimitError.
	ErrRateLimited = errors.New("rate limited")
	// ErrInvitesRestricted means owners may not invite: the operator set
	// WARDLOCK_INVITES=operator, so only `wardlockd admin create` adds
	// owners (A2.13).
	ErrInvitesRestricted = errors.New("invitations are restricted to the operator")
)

// Invitation modes (WARDLOCK_INVITES).
const (
	// InvitesOwners lets every owner invite others (the default).
	InvitesOwners = "owners"
	// InvitesOperator leaves inviting to `wardlockd admin create`.
	InvitesOperator = "operator"
)

// RateLimitError says a limit was hit and when to retry.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "rate limited" }

// Is makes errors.Is(err, ErrRateLimited) match.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Config holds the Service's dependencies.
type Config struct {
	Owners   store.Owners
	Sessions store.Sessions
	Mailer   mail.Mailer
	Logger   *slog.Logger
	// Secret is the server key (WARDLOCK_SECRET) for every stored hash.
	Secret []byte
	// DashboardURL is the dashboard's origin, for setup and sign-in URLs.
	DashboardURL *url.URL
	// Now defaults to time.Now.
	Now func() time.Time
	// Limits defaults to DefaultLimits().
	Limits Limits
	// Invites is InvitesOwners (the default when empty) or
	// InvitesOperator.
	Invites string
}

// Service is owner authentication. It is safe for concurrent use.
type Service struct {
	owners   store.Owners
	sessions store.Sessions
	mailer   mail.Mailer
	log      *slog.Logger
	secret   []byte
	dash     *url.URL
	now      func() time.Time
	limits   Limits
	invites  string
	limiter  *limiter
	// addressLimiter counts email-code sends per address in a bounded
	// table of its own: addresses cost nothing to invent, so filling it
	// must never refuse sign-in from IPs the limiter has not yet seen.
	addressLimiter *limiter
	bg             sync.WaitGroup
}

// New returns a Service. Call Close on shutdown to finish queued mail.
func New(cfg Config) (*Service, error) {
	if len(cfg.Secret) < 32 {
		return nil, errors.New("owners: server secret must be at least 32 bytes")
	}
	if cfg.Owners == nil || cfg.Sessions == nil || cfg.Mailer == nil || cfg.Logger == nil || cfg.DashboardURL == nil {
		return nil, errors.New("owners: missing dependency")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits()
	}
	switch cfg.Invites {
	case "":
		cfg.Invites = InvitesOwners
	case InvitesOwners, InvitesOperator:
	default:
		return nil, fmt.Errorf("owners: unknown invitation mode %q", cfg.Invites)
	}
	return &Service{
		owners:         cfg.Owners,
		sessions:       cfg.Sessions,
		mailer:         cfg.Mailer,
		log:            cfg.Logger,
		secret:         cfg.Secret,
		dash:           cfg.DashboardURL,
		now:            cfg.Now,
		limits:         cfg.Limits,
		invites:        cfg.Invites,
		limiter:        newLimiter(cfg.Now),
		addressLimiter: newLimiter(cfg.Now),
	}, nil
}

// Close waits for background sends to finish.
func (s *Service) Close() { s.bg.Wait() }

// Client describes who is making a request: its IP for rate limits and
// its User-Agent for the session list.
type Client struct {
	IP        string
	UserAgent string
}

// maxUserAgent is the longest User-Agent kept, in characters (the API
// contract's limit).
const maxUserAgent = 200

func truncateUserAgent(ua string) string {
	ua = strings.ToValidUTF8(ua, "")
	if r := []rune(ua); len(r) > maxUserAgent {
		return string(r[:maxUserAgent])
	}
	return ua
}

// Session is a verified dashboard session.
type Session struct {
	ID      string
	OwnerID string
	// ExpiresAt is the earlier of the idle and absolute expiry.
	ExpiresAt time.Time
	// TTL is ExpiresAt from now: the cookie's Max-Age.
	TTL time.Duration
	// Refreshed reports that this request moved the idle expiry forward,
	// so the cookie should be re-sent with the new TTL.
	Refreshed bool
}

// NewSession is a session that was just created. Token is the raw cookie
// value; it exists only in this value and the response that sets it.
type NewSession struct {
	Session
	Token string
}

// NormaliseEmail trims and ASCII-lower-cases a plain address (no display
// name), matching the owners.email CHECK.
func NormaliseEmail(raw string) (string, error) {
	e := asciiLower(strings.TrimSpace(raw))
	if len(e) < 3 || len(e) > 254 {
		return "", ErrInvalidEmail
	}
	a, err := netmail.ParseAddress(e)
	if err != nil || a.Name != "" || a.Address != e {
		return "", ErrInvalidEmail
	}
	return e, nil
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// PrepareSetup returns a fresh setup URL while the instance has no owner,
// retiring any earlier one, or "" once an owner exists. `wardlockd serve`
// calls it on every start.
func (s *Service) PrepareSetup(ctx context.Context) (string, error) {
	n, err := s.owners.Count(ctx)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	now := s.now()
	if err := s.owners.RetireLoginCodes(ctx, "", store.LoginCodeSetup, now); err != nil {
		return "", err
	}
	code := randomToken()
	if err := s.owners.CreateLoginCode(ctx, store.OwnerLoginCode{
		ID:        store.NewID(),
		Purpose:   store.LoginCodeSetup,
		CodeHash:  s.hash(store.LoginCodeSetup, code),
		CreatedAt: now,
		ExpiresAt: now.Add(SetupValidity),
	}); err != nil {
		return "", err
	}
	return s.oneTimeURL(SetupPath, code), nil
}

// CompleteSetup redeems the setup code, creates the first owner with email
// and signs them in.
func (s *Service) CompleteSetup(ctx context.Context, c Client, code, email string) (store.Owner, NewSession, error) {
	if err := s.allow("redeem:"+ipKey(c.IP), s.limits.RedeemPerIP); err != nil {
		return store.Owner{}, NewSession{}, err
	}
	email, err := NormaliseEmail(email)
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	lc, err := s.validCode(ctx, store.LoginCodeSetup, code)
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	now := s.now()
	o := store.Owner{ID: store.NewID(), Email: email, CreatedAt: now, AcceptedAt: now}
	if err := s.owners.CompleteSetup(ctx, lc.ID, o); err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
			return store.Owner{}, NewSession{}, ErrInvalidCode
		}
		return store.Owner{}, NewSession{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "first owner created", slog.String("owner_id", o.ID))
	sess, err := s.startSession(ctx, c, o.ID, "setup")
	return o, sess, err
}

// CreateOwner creates an owner offline (`wardlockd admin create`) and
// returns a one-time sign-in URL for them.
func (s *Service) CreateOwner(ctx context.Context, email string) (store.Owner, string, error) {
	email, err := NormaliseEmail(email)
	if err != nil {
		return store.Owner{}, "", err
	}
	if _, err := s.owners.GetByEmail(ctx, email); err == nil {
		return store.Owner{}, "", ErrOwnerExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Owner{}, "", err
	}
	o, url, err := s.createWithLink(ctx, email, "", SignInURLValidity)
	if errors.Is(err, store.ErrConflict) {
		return store.Owner{}, "", ErrOwnerExists
	}
	if err != nil {
		return store.Owner{}, "", err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "owner created", slog.String("owner_id", o.ID))
	return o, url, nil
}

// createWithLink creates an owner and their one-time sign-in URL together.
func (s *Service) createWithLink(ctx context.Context, email, invitedBy string, validFor time.Duration) (store.Owner, string, error) {
	now := s.now()
	o := store.Owner{ID: store.NewID(), Email: email, InvitedBy: invitedBy, CreatedAt: now}
	code := randomToken()
	err := s.owners.CreateWithLoginCode(ctx, o, store.OwnerLoginCode{
		ID:        store.NewID(),
		OwnerID:   o.ID,
		Purpose:   store.LoginCodeSignInURL,
		CodeHash:  s.hash(store.LoginCodeSignInURL, code),
		CreatedAt: now,
		ExpiresAt: now.Add(validFor),
	})
	if err != nil {
		return store.Owner{}, "", err
	}
	return o, s.oneTimeURL(SignInPath, code), nil
}

// RedeemSignInLink spends a one-time sign-in URL code and signs its owner in.
func (s *Service) RedeemSignInLink(ctx context.Context, c Client, code string) (store.Owner, NewSession, error) {
	if err := s.allow("redeem:"+ipKey(c.IP), s.limits.RedeemPerIP); err != nil {
		return store.Owner{}, NewSession{}, err
	}
	lc, err := s.validCode(ctx, store.LoginCodeSignInURL, code)
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	if err := s.owners.UseLoginCode(ctx, lc.ID, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Owner{}, NewSession{}, ErrInvalidCode
		}
		return store.Owner{}, NewSession{}, err
	}
	return s.signIn(ctx, c, lc.OwnerID, "signin_url", ErrInvalidCode)
}

// validCode finds an unused, unexpired high-entropy code of purpose.
func (s *Service) validCode(ctx context.Context, purpose store.LoginCodePurpose, code string) (store.OwnerLoginCode, error) {
	if code == "" || len(code) > 128 {
		return store.OwnerLoginCode{}, ErrInvalidCode
	}
	lc, err := s.owners.GetLoginCodeByHash(ctx, purpose, s.hash(purpose, code))
	if errors.Is(err, store.ErrNotFound) {
		return store.OwnerLoginCode{}, ErrInvalidCode
	}
	if err != nil {
		return store.OwnerLoginCode{}, err
	}
	if !lc.UsedAt.IsZero() || !s.now().Before(lc.ExpiresAt) {
		return store.OwnerLoginCode{}, ErrInvalidCode
	}
	return lc, nil
}

// SendEmailCode emails a sign-in code if email belongs to an owner. The
// result is the same either way: after validation and the rate limits,
// the owner lookup, code creation and mail all run in the background, so
// known and unknown addresses return after the same work.
func (s *Service) SendEmailCode(ctx context.Context, c Client, email string) error {
	email, err := NormaliseEmail(email)
	if err != nil {
		return err
	}
	if err := s.allow("send-ip:"+ipKey(c.IP), s.limits.SendPerIP); err != nil {
		return err
	}
	if ok, wait := s.addressLimiter.allow("send-address:"+email, s.limits.SendPerAddress); !ok {
		return &RateLimitError{RetryAfter: wait}
	}
	s.background(ctx, "email code", func(ctx context.Context) error { return s.sendEmailCode(ctx, email) })
	return nil
}

func (s *Service) sendEmailCode(ctx context.Context, email string) error {
	o, err := s.owners.GetByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !o.SuspendedAt.IsZero() {
		// As for an unknown address: nothing is sent (A2.13).
		return nil
	}
	now := s.now()
	code := sixDigits()
	if err := s.owners.ReplaceLoginCode(ctx, store.OwnerLoginCode{
		ID:        store.NewID(),
		OwnerID:   o.ID,
		Purpose:   store.LoginCodeEmail,
		CodeHash:  s.emailCodeHash(o.ID, code),
		CreatedAt: now,
		ExpiresAt: now.Add(EmailCodeValidity),
	}); err != nil {
		return fmt.Errorf("owner %s: %w", o.ID, err)
	}
	msg, err := mail.SignInMessage(netmail.Address{Address: o.Email}, mail.SignIn{
		Code: code, Host: s.dash.Hostname(), ValidFor: EmailCodeValidity,
	})
	if err != nil {
		return fmt.Errorf("owner %s: %w", o.ID, err)
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("owner %s: %w", o.ID, err)
	}
	return nil
}

// VerifyEmailCode checks an emailed code and signs its owner in. Each
// attempt is counted before the comparison, so concurrent guesses cannot
// exceed EmailCodeAttempts.
func (s *Service) VerifyEmailCode(ctx context.Context, c Client, email, code string) (store.Owner, NewSession, error) {
	email, err := NormaliseEmail(email)
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	if err := s.allow("verify-ip:"+ipKey(c.IP), s.limits.VerifyPerIP); err != nil {
		return store.Owner{}, NewSession{}, err
	}
	if !isSixDigits(code) {
		return store.Owner{}, NewSession{}, ErrInvalidCode
	}
	o, err := s.owners.GetByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) || err == nil && !o.SuspendedAt.IsZero() {
		return store.Owner{}, NewSession{}, ErrInvalidCode
	}
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	lc, err := s.owners.LatestLoginCode(ctx, o.ID, store.LoginCodeEmail)
	if errors.Is(err, store.ErrNotFound) {
		return store.Owner{}, NewSession{}, ErrInvalidCode
	}
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	now := s.now()
	if !lc.UsedAt.IsZero() || !now.Before(lc.ExpiresAt) || lc.Attempts >= EmailCodeAttempts {
		return store.Owner{}, NewSession{}, ErrInvalidCode
	}
	attempts, err := s.owners.AddLoginCodeAttempt(ctx, lc.ID, EmailCodeAttempts)
	if errors.Is(err, store.ErrNotFound) {
		return store.Owner{}, NewSession{}, ErrInvalidCode
	}
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	if !hmac.Equal([]byte(s.emailCodeHash(o.ID, code)), []byte(lc.CodeHash)) {
		s.log.LogAttrs(ctx, slog.LevelInfo, "email code rejected", slog.String("owner_id", o.ID), slog.Int("attempts", attempts))
		return store.Owner{}, NewSession{}, ErrInvalidCode
	}
	if err := s.owners.UseLoginCode(ctx, lc.ID, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Owner{}, NewSession{}, ErrInvalidCode
		}
		return store.Owner{}, NewSession{}, err
	}
	return s.signIn(ctx, c, o.ID, "email_code", ErrInvalidCode)
}

// signIn records the owner's first sign-in and starts a session. A
// suspended owner is refused with the method's own rejection, so sign-in
// says no more than a wrong code or passkey does (A2.13).
func (s *Service) signIn(ctx context.Context, c Client, ownerID, method string, refused error) (store.Owner, NewSession, error) {
	o, err := s.owners.Get(ctx, ownerID)
	if err != nil {
		return store.Owner{}, NewSession{}, err
	}
	if !o.SuspendedAt.IsZero() {
		s.log.LogAttrs(ctx, slog.LevelInfo, "suspended owner refused sign-in", slog.String("owner_id", ownerID), slog.String("method", method))
		return store.Owner{}, NewSession{}, refused
	}
	if err := s.owners.MarkAccepted(ctx, ownerID, s.now()); err != nil {
		return store.Owner{}, NewSession{}, err
	}
	if o.AcceptedAt.IsZero() {
		if o, err = s.owners.Get(ctx, ownerID); err != nil {
			return store.Owner{}, NewSession{}, err
		}
	}
	sess, err := s.startSession(ctx, c, ownerID, method)
	if errors.Is(err, errSuspended) {
		return store.Owner{}, NewSession{}, refused
	}
	return o, sess, err
}

// errSuspended means the owner was suspended while a session was being
// started; callers answer with their method's own rejection.
var errSuspended = errors.New("owner suspended")

func (s *Service) startSession(ctx context.Context, c Client, ownerID, method string) (NewSession, error) {
	now := s.now()
	token := randomToken()
	row := store.OwnerSession{
		ID:         store.NewID(),
		OwnerID:    ownerID,
		TokenHash:  s.hash(purposeSession, token),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionAbsolute),
		UserAgent:  truncateUserAgent(c.UserAgent),
	}
	if err := s.sessions.CreateOwner(ctx, row); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Suspended since the sign-in was checked (A2.13).
			return NewSession{}, errSuspended
		}
		return NewSession{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "owner signed in",
		slog.String("owner_id", ownerID), slog.String("session_id", row.ID), slog.String("method", method))
	return NewSession{Session: s.view(row, now, true), Token: token}, nil
}

// view describes a stored session as of now.
func (s *Service) view(row store.OwnerSession, now time.Time, refreshed bool) Session {
	exp := sessionExpiry(row)
	return Session{ID: row.ID, OwnerID: row.OwnerID, ExpiresAt: exp, TTL: exp.Sub(now), Refreshed: refreshed}
}

func sessionExpiry(row store.OwnerSession) time.Time {
	idle := row.LastSeenAt.Add(SessionIdle)
	if idle.Before(row.ExpiresAt) {
		return idle
	}
	return row.ExpiresAt
}

// Authenticate verifies a session cookie token and records its use.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if !wellFormedToken(token) {
		return Session{}, ErrUnauthenticated
	}
	row, err := s.sessions.GetOwnerByTokenHash(ctx, s.hash(purposeSession, token))
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	if !row.RevokedAt.IsZero() || !now.Before(sessionExpiry(row)) {
		return Session{}, ErrUnauthenticated
	}
	refreshed := false
	if now.Sub(row.LastSeenAt) >= touchEvery {
		if err := s.sessions.TouchOwner(ctx, row.ID, now); err != nil {
			return Session{}, err
		}
		row.LastSeenAt = now
		refreshed = true
	}
	return s.view(row, now, refreshed), nil
}

// SignOut revokes the owner's current session.
func (s *Service) SignOut(ctx context.Context, ownerID, sessionID string) error {
	if err := s.RevokeSession(ctx, ownerID, sessionID); err != nil {
		return err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "owner signed out", slog.String("owner_id", ownerID), slog.String("session_id", sessionID))
	return nil
}

// Me returns the signed-in owner.
func (s *Service) Me(ctx context.Context, ownerID string) (store.Owner, error) {
	o, err := s.owners.Get(ctx, ownerID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Owner{}, ErrUnauthenticated
	}
	return o, err
}

// Invite emails a one-time sign-in link to email, invited by inviterID,
// creating the owner if needed. An owner who has never signed in gets a
// fresh link (retiring any earlier one); an owner who has signed in gets
// nothing. The result is the same in every case: after validation and the
// rate limit, everything runs in the background.
func (s *Service) Invite(ctx context.Context, inviterID, email string) error {
	if s.invites == InvitesOperator {
		return ErrInvitesRestricted
	}
	email, err := NormaliseEmail(email)
	if err != nil {
		return err
	}
	if err := s.allow("invite:"+inviterID, s.limits.InvitePerOwner); err != nil {
		return err
	}
	s.background(ctx, "invitation", func(ctx context.Context) error { return s.invite(ctx, inviterID, email) })
	return nil
}

func (s *Service) invite(ctx context.Context, inviterID, email string) error {
	inviter, err := s.owners.Get(ctx, inviterID)
	if err != nil {
		return fmt.Errorf("inviter %s: %w", inviterID, err)
	}
	var ownerID, link string
	o, err := s.owners.GetByEmail(ctx, email)
	switch {
	case err == nil && (!o.AcceptedAt.IsZero() || !o.SuspendedAt.IsZero()):
		return nil
	case err == nil:
		ownerID = o.ID
		code := randomToken()
		now := s.now()
		if err := s.owners.ReplaceLoginCode(ctx, store.OwnerLoginCode{
			ID:        store.NewID(),
			OwnerID:   o.ID,
			Purpose:   store.LoginCodeSignInURL,
			CodeHash:  s.hash(store.LoginCodeSignInURL, code),
			CreatedAt: now,
			ExpiresAt: now.Add(InviteValidity),
		}); err != nil {
			return fmt.Errorf("owner %s: %w", o.ID, err)
		}
		link = s.oneTimeURL(SignInPath, code)
	case errors.Is(err, store.ErrNotFound):
		created, l, err := s.createWithLink(ctx, email, inviterID, InviteValidity)
		if errors.Is(err, store.ErrConflict) {
			return nil // invited concurrently
		}
		if err != nil {
			return err
		}
		ownerID, link = created.ID, l
	default:
		return err
	}
	msg, err := mail.InviteMessage(netmail.Address{Address: email}, mail.Invite{
		URL: link, Host: s.dash.Hostname(), InvitedBy: inviter.Email, ValidFor: InviteValidity,
	})
	if err != nil {
		return fmt.Errorf("owner %s: %w", ownerID, err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "owner invited", slog.String("owner_id", ownerID), slog.String("invited_by", inviterID))
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("owner %s: %w", ownerID, err)
	}
	return nil
}

// SessionInfo is one of an owner's sessions as the dashboard lists it.
type SessionInfo struct {
	ID         string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	UserAgent  string // empty if none was sent
	Current    bool
}

// ListSessions returns one page of ownerID's active sessions, most recently
// used first, and the cursor for the next page ("" on the last).
func (s *Service) ListSessions(ctx context.Context, ownerID, currentID, cursor string, limit int) ([]SessionInfo, string, error) {
	now := s.now()
	q := store.OwnerSessionQuery{OwnerID: ownerID, Now: now, IdleSince: now.Add(-SessionIdle), Limit: limit + 1}
	if cursor != "" {
		seen, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		q.AfterSeen, q.AfterID = seen, id
	}
	rows, err := s.sessions.ListOwner(ctx, q)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = encodeCursor(last.LastSeenAt, last.ID)
	}
	out := make([]SessionInfo, len(rows))
	for i, r := range rows {
		out[i] = SessionInfo{
			ID: r.ID, CreatedAt: r.CreatedAt, LastUsedAt: r.LastSeenAt, ExpiresAt: sessionExpiry(r), UserAgent: r.UserAgent, Current: r.ID == currentID,
		}
	}
	return out, next, nil
}

// RevokeSession revokes ownerID's session id. Another owner's session and
// a missing one are both ErrNotFound.
func (s *Service) RevokeSession(ctx context.Context, ownerID, id string) error {
	err := s.sessions.RevokeOwner(ctx, ownerID, id, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// background runs fn after the request returns, bounded by bgTimeout.
// Errors are logged; they never carry codes, links or message bodies.
func (s *Service) background(ctx context.Context, kind string, fn func(context.Context) error) {
	ctx = context.WithoutCancel(ctx)
	s.bg.Go(func() {
		ctx, cancel := context.WithTimeout(ctx, bgTimeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			s.log.LogAttrs(ctx, slog.LevelError, kind+" failed", slog.String("error", err.Error()))
		}
	})
}

func (s *Service) allow(key string, r Rate) error {
	if ok, wait := s.limiter.allow(key, r); !ok {
		return &RateLimitError{RetryAfter: wait}
	}
	return nil
}

func (s *Service) oneTimeURL(path, code string) string {
	u := *s.dash
	u.Path = path
	u.RawQuery = url.Values{"code": {code}}.Encode()
	return u.String()
}

// purposeSession namespaces session token hashes apart from login codes.
const purposeSession = "owner_session"

// hash is the stored form of a credential: HMAC-SHA-256 keyed by the server
// secret over the purpose and value, hex-encoded.
func (s *Service) hash(purpose store.LoginCodePurpose, value string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(purpose))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

// emailCodeHash binds a 6-digit code to its owner, since codes are short
// enough to collide across owners.
func (s *Service) emailCodeHash(ownerID, code string) string {
	return s.hash(store.LoginCodeEmail, ownerID+":"+code)
}

// tokenBytes is the entropy of setup codes, sign-in URL codes and session
// tokens: 256 bits.
const tokenBytes = 32

func randomToken() string {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails since Go 1.24.
	return base64.RawURLEncoding.EncodeToString(b)
}

// wellFormedToken reports whether t has the shape of a session token,
// setup or sign-in code, or device code secret: canonical base64url of
// tokenBytes, so one value has exactly one spelling.
func wellFormedToken(t string) bool {
	if len(t) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	// Strict still skips CR and LF, so the length is checked too.
	b, err := base64.RawURLEncoding.Strict().DecodeString(t)
	return err == nil && len(b) == tokenBytes
}

func sixDigits() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(fmt.Sprintf("owners: crypto/rand: %v", err))
	}
	return fmt.Sprintf("%06d", n.Int64())
}

func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Cursors are base64url("<last seen ms>.<session id>").

func encodeCursor(seen time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(seen.UnixMilli(), 10) + "." + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", ErrInvalidCursor
	}
	ms, id, ok := strings.Cut(string(raw), ".")
	n, err := strconv.ParseInt(ms, 10, 64)
	if !ok || err != nil || id == "" {
		return time.Time{}, "", ErrInvalidCursor
	}
	return time.UnixMilli(n).UTC(), id, nil
}

// Suspend suspends the owner with email (`wardlockd admin suspend-owner`,
// A2.13): their dashboard sessions are revoked, approved CLI logins not yet
// collected are denied, their API tokens are refused while the suspension
// lasts and sign-in is refused like a wrong code. Their sites stop serving
// once the server notices (see store.Sites.UnavailableIDs). Suspending a
// suspended owner changes nothing. An unknown address is ErrNotFound.
func (s *Service) Suspend(ctx context.Context, email string) (store.Owner, error) {
	o, err := s.operatorOwner(ctx, email)
	if err != nil {
		return store.Owner{}, err
	}
	if err := s.owners.Suspend(ctx, o.ID, s.now()); err != nil {
		return store.Owner{}, fmt.Errorf("suspend owner: %w", err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "operator suspended owner", slog.String("owner_id", o.ID))
	return s.owners.Get(ctx, o.ID)
}

// Reinstate lifts the owner's suspension (`wardlockd admin
// reinstate-owner`): their API tokens and sites work again; they sign in
// afresh. An unknown address is ErrNotFound.
func (s *Service) Reinstate(ctx context.Context, email string) (store.Owner, error) {
	o, err := s.operatorOwner(ctx, email)
	if err != nil {
		return store.Owner{}, err
	}
	if err := s.owners.Reinstate(ctx, o.ID); err != nil {
		return store.Owner{}, fmt.Errorf("reinstate owner: %w", err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "operator reinstated owner", slog.String("owner_id", o.ID))
	return s.owners.Get(ctx, o.ID)
}

// ListAll calls fn for every owner on the instance, ordered by address
// (`wardlockd admin list-owners`).
func (s *Service) ListAll(ctx context.Context, fn func(store.Owner) error) error {
	for after := ""; ; {
		page, err := s.owners.ListAll(ctx, after, operatorPage)
		if err != nil {
			return fmt.Errorf("list owners: %w", err)
		}
		for _, o := range page {
			if err := fn(o); err != nil {
				return err
			}
		}
		if len(page) < operatorPage {
			return nil
		}
		after = page[len(page)-1].Email
	}
}

// Quotas returns the quota overrides of the owner with email and what
// counts against them now (`wardlockd admin quota`, A2.14). An unknown
// address is ErrNotFound.
func (s *Service) Quotas(ctx context.Context, email string) (store.Owner, store.QuotaOverrides, store.OwnerUsage, error) {
	o, err := s.operatorOwner(ctx, email)
	if err != nil {
		return store.Owner{}, store.QuotaOverrides{}, store.OwnerUsage{}, err
	}
	over, usage, err := s.owners.Quotas(ctx, o.ID, s.now())
	if err != nil {
		return store.Owner{}, store.QuotaOverrides{}, store.OwnerUsage{}, fmt.Errorf("get owner quotas: %w", err)
	}
	return o, over, usage, nil
}

// SetQuotas replaces the quota overrides of the owner with email (A2.14).
// The next deploy or new site they make is checked against them. An
// unknown address is ErrNotFound.
func (s *Service) SetQuotas(ctx context.Context, email string, over store.QuotaOverrides) (store.Owner, error) {
	o, err := s.operatorOwner(ctx, email)
	if err != nil {
		return store.Owner{}, err
	}
	if err := s.owners.SetQuotas(ctx, o.ID, over); err != nil {
		return store.Owner{}, fmt.Errorf("set owner quotas: %w", err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "operator set owner quotas", slog.String("owner_id", o.ID))
	return o, nil
}

// Lookup returns the owner with email for an operator command (A2.15). An
// unknown address is ErrNotFound; a malformed one ErrInvalidEmail.
func (s *Service) Lookup(ctx context.Context, email string) (store.Owner, error) {
	return s.operatorOwner(ctx, email)
}

// operatorPage is how many rows the operator listings read at a time.
const operatorPage = 500

func (s *Service) operatorOwner(ctx context.Context, email string) (store.Owner, error) {
	email, err := NormaliseEmail(email)
	if err != nil {
		return store.Owner{}, err
	}
	o, err := s.owners.GetByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return store.Owner{}, ErrNotFound
	}
	return o, err
}
