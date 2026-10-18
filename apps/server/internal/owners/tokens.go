// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode"

	"wardlock.nz/server/internal/store"
)

var (
	// ErrInvalidName means a token name is empty or longer than 100
	// characters.
	ErrInvalidName = errors.New("invalid token name")
	// ErrInvalidScopes means no scope was given.
	ErrInvalidScopes = errors.New("invalid scopes")
	// ErrInvalidExpiry means a token's expiry is not in the future.
	ErrInvalidExpiry = errors.New("invalid expiry")
)

// maxTokenName is the longest token name, in characters (the API
// contract's and the schema's limit).
const maxTokenName = 100

// Scopes are what an API token may do (docs/SPEC.md "Owners and sign-in").
// Deploy creates sites and deploys, uploads, activates and rolls back;
// Manage deletes sites, manages custom domains and sets access policies.
// Either scope reads. A dashboard session has both.
type Scopes struct {
	Deploy bool
	Manage bool
}

// Any reports whether at least one scope is set.
func (s Scopes) Any() bool { return s.Deploy || s.Manage }

// TokenLimits are the API token and CLI login rate limits. Every request
// is counted per client IP or owner, successful or not, except
// FailuresPerIP, which counts only rejected tokens.
type TokenLimits struct {
	// FailuresPerIP limits bearer tokens that fail to authenticate
	// (malformed, unknown public ID or wrong secret). Once over it, every
	// bearer request from that IP is refused until the window ends.
	FailuresPerIP Rate
	// StartPerIP limits CLI logins started.
	StartPerIP Rate
	// PollPerIP limits CLI login polls, on top of the per-login interval.
	PollPerIP Rate
	// DecidePerOwner limits CLI login approvals and denials.
	DecidePerOwner Rate
}

// DefaultTokenLimits are the limits used when TokensConfig.Limits is zero.
func DefaultTokenLimits() TokenLimits {
	return TokenLimits{
		FailuresPerIP:  Rate{N: 30, Per: 15 * time.Minute},
		StartPerIP:     Rate{N: 20, Per: time.Hour},
		PollPerIP:      Rate{N: 60, Per: time.Minute},
		DecidePerOwner: Rate{N: 30, Per: 15 * time.Minute},
	}
}

// TokensConfig holds the Tokens service's dependencies.
type TokensConfig struct {
	Tokens store.APITokens
	Logger *slog.Logger
	// Secret is the server key (WARDLOCK_SECRET) that keys token and
	// device code hashes.
	Secret []byte
	// DashboardURL is the dashboard's origin, for the CLI login page URL.
	DashboardURL *url.URL
	// Now defaults to time.Now.
	Now func() time.Time
	// Limits defaults to DefaultTokenLimits().
	Limits TokenLimits
}

// Tokens is API tokens and the CLI device login (C1.7). It is safe for
// concurrent use. Raw tokens and device codes exist only in the values it
// returns to its caller; it logs public IDs and record IDs only.
type Tokens struct {
	tokens  store.APITokens
	log     *slog.Logger
	secret  []byte
	dash    *url.URL
	now     func() time.Time
	limits  TokenLimits
	limiter *limiter
}

// NewTokens returns a Tokens service.
func NewTokens(cfg TokensConfig) (*Tokens, error) {
	if len(cfg.Secret) < 32 {
		return nil, errors.New("owners: server secret must be at least 32 bytes")
	}
	if cfg.Tokens == nil || cfg.Logger == nil || cfg.DashboardURL == nil {
		return nil, errors.New("owners: missing dependency")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Limits == (TokenLimits{}) {
		cfg.Limits = DefaultTokenLimits()
	}
	return &Tokens{
		tokens:  cfg.Tokens,
		log:     cfg.Logger,
		secret:  cfg.Secret,
		dash:    cfg.DashboardURL,
		now:     cfg.Now,
		limits:  cfg.Limits,
		limiter: newLimiter(cfg.Now),
	}, nil
}

// APITokenInfo describes a token without its secret.
type APITokenInfo struct {
	ID         string
	PublicID   string
	Name       string
	Scopes     Scopes
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero: never
	LastUsedAt time.Time // zero: never used
}

func tokenInfo(t store.APIToken) APITokenInfo {
	return APITokenInfo{
		ID: t.ID, PublicID: t.PublicID, Name: t.Name, Scopes: Scopes{Deploy: t.ScopeDeploy, Manage: t.ScopeManage},
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt,
	}
}

// TokenAuth is a verified API token.
type TokenAuth struct {
	TokenID  string
	PublicID string
	OwnerID  string
	Scopes   Scopes
}

// Create makes a new API token for ownerID. The raw token is returned once
// and never stored.
func (s *Tokens) Create(ctx context.Context, ownerID, name string, scopes Scopes, expiresAt time.Time) (APITokenInfo, string, error) {
	name, err := tokenName(name)
	if err != nil {
		return APITokenInfo{}, "", err
	}
	if !scopes.Any() {
		return APITokenInfo{}, "", ErrInvalidScopes
	}
	now := s.now()
	if !expiresAt.IsZero() && !expiresAt.After(now) {
		return APITokenInfo{}, "", ErrInvalidExpiry
	}
	row, token, err := s.mint(ctx, ownerID, name, scopes, expiresAt, now, s.tokens.Create)
	if err != nil {
		return APITokenInfo{}, "", err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "api token created",
		slog.String("owner_id", ownerID), slog.String("token_id", row.ID), slog.String("public_id", row.PublicID))
	return tokenInfo(row), token, nil
}

// mintAttempts bounds retries after a public ID collision (50 random bits,
// so a second attempt is already vanishingly rare).
const mintAttempts = 3

// mint creates a token row with save, retrying on a public ID collision.
func (s *Tokens) mint(ctx context.Context, ownerID, name string, scopes Scopes, expiresAt, now time.Time,
	save func(context.Context, store.APIToken) error,
) (store.APIToken, string, error) {
	for attempt := 1; ; attempt++ {
		token, publicID := NewToken()
		row := store.APIToken{
			ID: store.NewID(), OwnerID: ownerID, PublicID: publicID, TokenHash: HashToken(s.secret, token),
			Name: name, ScopeDeploy: scopes.Deploy, ScopeManage: scopes.Manage, CreatedAt: now, ExpiresAt: expiresAt,
		}
		err := save(ctx, row)
		if err == nil {
			return row, token, nil
		}
		if !errors.Is(err, store.ErrConflict) || attempt == mintAttempts {
			return store.APIToken{}, "", err
		}
	}
}

// tokenName trims a token name and checks its length.
func tokenName(name string) (string, error) {
	name = strings.TrimSpace(strings.ToValidUTF8(name, ""))
	if name == "" || len([]rune(name)) > maxTokenName || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidName
	}
	return name, nil
}

// List returns one page of ownerID's usable tokens, newest first, and the
// cursor for the next page ("" on the last).
func (s *Tokens) List(ctx context.Context, ownerID, cursor string, limit int) ([]APITokenInfo, string, error) {
	q := store.APITokenQuery{OwnerID: ownerID, Now: s.now(), Limit: limit + 1}
	if cursor != "" {
		created, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		q.BeforeCreated, q.BeforeID = created, id
	}
	rows, err := s.tokens.List(ctx, q)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = encodeCursor(last.CreatedAt, last.ID)
	}
	out := make([]APITokenInfo, len(rows))
	for i, r := range rows {
		out[i] = tokenInfo(r)
	}
	return out, next, nil
}

// Revoke revokes ownerID's token id; it stops working on its next request.
// Another owner's token and a missing one are both ErrNotFound.
func (s *Tokens) Revoke(ctx context.Context, ownerID, id string) error {
	err := s.tokens.Revoke(ctx, ownerID, id, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "api token revoked", slog.String("owner_id", ownerID), slog.String("token_id", id))
	return nil
}

// Authenticate verifies a bearer token and records its use. A malformed,
// unknown, wrong, expired or revoked token is ErrUnauthenticated whatever
// the reason. Rejected guesses count against the client IP; once over
// TokenLimits.FailuresPerIP every token from that IP is refused with a
// *RateLimitError until the window ends, so guessing cannot continue
// behind the limit.
func (s *Tokens) Authenticate(ctx context.Context, c Client, raw string) (TokenAuth, error) {
	key := "token-fail:" + ipKey(c.IP)
	if over, wait := s.limiter.exceeded(key, s.limits.FailuresPerIP); over {
		return TokenAuth{}, &RateLimitError{RetryAfter: wait}
	}
	reject := func(publicID string) (TokenAuth, error) {
		s.limiter.allow(key, s.limits.FailuresPerIP)
		attrs := []slog.Attr{}
		if publicID != "" {
			attrs = append(attrs, slog.String("public_id", publicID))
		}
		s.log.LogAttrs(ctx, slog.LevelInfo, "api token rejected", attrs...)
		return TokenAuth{}, ErrUnauthenticated
	}
	publicID, ok := ParseToken(raw)
	if !ok {
		return reject("")
	}
	row, err := s.tokens.GetByPublicID(ctx, publicID)
	if errors.Is(err, store.ErrNotFound) {
		return reject(publicID)
	}
	if err != nil {
		return TokenAuth{}, fmt.Errorf("token %s: %w", publicID, err)
	}
	if !hmac.Equal([]byte(HashToken(s.secret, raw)), []byte(row.TokenHash)) {
		return reject(publicID)
	}
	now := s.now()
	if !row.RevokedAt.IsZero() || (!row.ExpiresAt.IsZero() && !now.Before(row.ExpiresAt)) {
		return TokenAuth{}, ErrUnauthenticated
	}
	if row.LastUsedAt.IsZero() || now.Sub(row.LastUsedAt) >= touchEvery {
		if err := s.tokens.Touch(ctx, row.ID, now); err != nil {
			return TokenAuth{}, err
		}
	}
	return TokenAuth{
		TokenID: row.ID, PublicID: row.PublicID, OwnerID: row.OwnerID,
		Scopes: Scopes{Deploy: row.ScopeDeploy, Manage: row.ScopeManage},
	}, nil
}

func (s *Tokens) allow(key string, r Rate) error {
	if ok, wait := s.limiter.allow(key, r); !ok {
		return &RateLimitError{RetryAfter: wait}
	}
	return nil
}
