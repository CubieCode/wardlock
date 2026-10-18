// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/store"
)

// Share links (A2.2): owner operations, the token check the edge runs on
// every request that carries one, and the use-limited interstitial.

// Errors returned by the share-link operations.
var (
	// ErrInvalidLink means a new link's label, expiry or use limit is not
	// acceptable. It arrives inside a TargetError.
	ErrInvalidLink = errors.New("invalid share link")
	// ErrLinkChanged means IssueSession refused a link session because
	// the link was rotated, revoked or expired after the token was checked.
	ErrLinkChanged = errors.New("share link changed since it was checked")
	// ErrTicket means an interstitial ticket is forged, expired or for
	// another site, or its link can no longer be used (revoked, rotated,
	// expired or out of uses).
	ErrTicket = errors.New("share link cannot be used")
)

const (
	// maxLabelChars matches the share_links.label CHECK.
	maxLabelChars = 200
	// MaxLinkUses bounds a link's use limit.
	MaxLinkUses = 1_000_000
	// TicketLifetime is how long the "Click to view" button works after
	// the interstitial is shown.
	TicketLifetime = 15 * time.Minute
	// touchEvery limits last-access writes to one per link per minute.
	touchEvery = time.Minute
	// maxTicketBytes bounds the work done on a ticket before its MAC is
	// checked.
	maxTicketBytes = 8192
	// purposeTicketMAC keeps ticket MACs apart from every other use of the
	// server key.
	purposeTicketMAC = "share_link_view_ticket"
)

// errTokenRefused is checkToken's answer for any token that does not let
// the request in, whatever the reason.
var errTokenRefused = errors.New("share link refused")

// limitedError is checkToken's answer for a refused guess that went over a
// limit (or found the limiter full): RetryAfter says for how long.
type limitedError struct{ RetryAfter time.Duration }

func (e *limitedError) Error() string { return "share link guesses limited" }

// NewLink is what an owner asks for when creating a share link.
type NewLink struct {
	// Label is free text shown to the owner (up to 200 characters); it is
	// never part of the URL.
	Label string
	// PathPrefix scopes the link: "/" for the whole site or a directory
	// prefix such as "/docs/". Empty means "/".
	PathPrefix string
	// ExpiresAt is when the link stops working; zero means never.
	ExpiresAt time.Time
	// MaxUses limits how many times the link can be opened (1 for a
	// one-time link); zero means unlimited.
	MaxUses int
}

// LinkStatus says whether a listed link still works.
type LinkStatus string

const (
	LinkActive  LinkStatus = "active"
	LinkExpired LinkStatus = "expired"
	LinkUsedUp  LinkStatus = "used_up"
)

// Status reports whether l still works now.
func (s *Service) Status(l store.ShareLink) LinkStatus {
	switch {
	case expired(l, s.now()):
		return LinkExpired
	case usedUp(l):
		return LinkUsedUp
	}
	return LinkActive
}

func expired(l store.ShareLink, now time.Time) bool {
	return !l.ExpiresAt.IsZero() && !now.Before(l.ExpiresAt)
}

func usedUp(l store.ShareLink) bool { return l.MaxUses > 0 && l.UseCount >= l.MaxUses }

// inScope reports whether a link scoped to prefix covers reqPath, with the
// same rule as policies: "/docs/" covers everything under it and "/docs".
func inScope(prefix, reqPath string) bool {
	return strings.HasPrefix(reqPath, prefix) || reqPath+"/" == prefix
}

// checkNewLink validates n against SPEC "Share links".
func (s *Service) checkNewLink(n *NewLink) error {
	invalid := func(target, msg string) error {
		return &TargetError{Err: ErrInvalidLink, Target: target, Message: msg}
	}
	if n.PathPrefix == "" {
		n.PathPrefix = DefaultPrefix
	}
	if err := CheckPrefix(n.PathPrefix); err != nil {
		return err
	}
	if !utf8.ValidString(n.Label) || utf8.RuneCountInString(n.Label) > maxLabelChars ||
		strings.ContainsFunc(n.Label, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return invalid("label", fmt.Sprintf("label must be at most %d characters, without control or invisible characters.", maxLabelChars))
	}
	if !n.ExpiresAt.IsZero() && !n.ExpiresAt.After(s.now()) {
		return invalid("expiresAt", "expiresAt must be in the future; omit it for a link that never expires.")
	}
	if n.MaxUses < 0 || n.MaxUses > MaxLinkUses {
		return invalid("maxUses", fmt.Sprintf("maxUses must be 1 to %d; omit it for unlimited uses.", MaxLinkUses))
	}
	return nil
}

// CreateLink creates a share link on ownerID's site called siteName and
// returns it with its token, which is never available again.
func (s *Service) CreateLink(ctx context.Context, ownerID, siteName string, n NewLink) (store.ShareLink, string, error) {
	if err := s.checkNewLink(&n); err != nil {
		return store.ShareLink{}, "", err
	}
	site, err := s.ownedSite(ctx, ownerID, siteName)
	if err != nil {
		return store.ShareLink{}, "", err
	}
	token, publicID := newLinkToken()
	l := store.ShareLink{
		ID: store.NewID(), SiteID: site.ID, PublicID: publicID, TokenHash: s.key.linkTokenHash(token),
		Label: n.Label, PathPrefix: n.PathPrefix, CreatedAt: s.now(), MaxUses: n.MaxUses,
	}
	if !n.ExpiresAt.IsZero() {
		l.ExpiresAt = n.ExpiresAt.UTC().Truncate(time.Millisecond)
	}
	err = s.c.Links.Create(ctx, ownerID, l)
	if errors.Is(err, store.ErrNotFound) {
		return store.ShareLink{}, "", fmt.Errorf("create share link: %w", ErrNotFound)
	}
	if err != nil {
		return store.ShareLink{}, "", fmt.Errorf("create share link: %w", err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "share link created",
		slog.String("site_id", site.ID), slog.String("public_id", l.PublicID), slog.String("path", l.PathPrefix))
	return l, token, nil
}

// ListLinks returns up to limit of the unrevoked links of ownerID's site
// called siteName, newest first, after the link (beforeCreated, beforeID);
// a zero beforeID starts at the newest.
func (s *Service) ListLinks(ctx context.Context, ownerID, siteName string, beforeCreated time.Time, beforeID string, limit int) ([]store.ShareLink, error) {
	site, err := s.ownedSite(ctx, ownerID, siteName)
	if err != nil {
		return nil, err
	}
	ls, err := s.c.Links.List(ctx, store.ShareLinkQuery{
		OwnerID: ownerID, SiteID: site.ID, BeforeCreated: beforeCreated, BeforeID: beforeID, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list share links: %w", err)
	}
	return ls, nil
}

// RevokeLink revokes the link publicID on ownerID's site called siteName.
// The token and every session it created stop working on their next
// request. A missing, already revoked or another owner's link is
// ErrNotFound.
func (s *Service) RevokeLink(ctx context.Context, ownerID, siteName, publicID string) error {
	site, err := s.ownedSite(ctx, ownerID, siteName)
	if err != nil {
		return err
	}
	if !validPublicID(publicID) {
		return fmt.Errorf("revoke share link: %w", ErrNotFound)
	}
	err = s.c.Links.Revoke(ctx, ownerID, site.ID, publicID, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("revoke share link: %w", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("revoke share link: %w", err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "share link revoked", slog.String("site_id", site.ID), slog.String("public_id", publicID))
	return nil
}

// RotateLink gives the link publicID on ownerID's site called siteName a new
// secret, keeping its public ID, label, scope, expiry and use count, and
// returns the new token. The old token and every session it created stop
// working on their next request.
func (s *Service) RotateLink(ctx context.Context, ownerID, siteName, publicID string) (store.ShareLink, string, error) {
	site, err := s.ownedSite(ctx, ownerID, siteName)
	if err != nil {
		return store.ShareLink{}, "", err
	}
	if !validPublicID(publicID) {
		return store.ShareLink{}, "", fmt.Errorf("rotate share link: %w", ErrNotFound)
	}
	token := withSecret(publicID)
	l, err := s.c.Links.Rotate(ctx, ownerID, site.ID, publicID, s.key.linkTokenHash(token), s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.ShareLink{}, "", fmt.Errorf("rotate share link: %w", ErrNotFound)
	}
	if err != nil {
		return store.ShareLink{}, "", fmt.Errorf("rotate share link: %w", err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "share link rotated", slog.String("site_id", site.ID), slog.String("public_id", publicID))
	return l, token, nil
}

// decideToken lets r in with its share-link token, under protected policy
// p. sess is r's cookie as checkSession found it; d is the refusal so far.
// A refused token falls back to the session, so a stale ?t= in a bookmark
// does not undo a good cookie.
func (s *Service) decideToken(ctx context.Context, r Request, p store.Policy, sess session, d Decision) (Decision, error) {
	bySession := func() Decision {
		if sess.allows {
			d.Outcome, d.Via, d.LinkPublicID = Allow, sess.via, sess.linkPublicID
		}
		return d
	}
	if over, wait := s.overTokenLimit(r); over {
		if sess.allows {
			return bySession(), nil
		}
		d.Outcome, d.RetryAfter = RateLimited, wait
		return d, nil
	}
	l, err := s.checkToken(ctx, r)
	if errors.Is(err, errTokenRefused) {
		return bySession(), nil
	}
	if le, ok := errors.AsType[*limitedError](err); ok {
		if sess.allows {
			return bySession(), nil
		}
		d.Outcome, d.RetryAfter = RateLimited, le.RetryAfter
		return d, nil
	}
	if err != nil {
		return Decision{}, err
	}
	if !p.LinksEnabled || !inScope(l.PathPrefix, r.Path) {
		return bySession(), nil
	}
	d.TokenAccepted = true
	if sess.allows {
		// Already in, through this link or another way: no new session,
		// and a use-limited link spends nothing.
		return bySession(), nil
	}
	if l.MaxUses > 0 {
		d.Outcome = Interstitial
		d.Ticket = s.key.encodeTicket(viewTicket{
			SiteID: r.SiteID, LinkID: l.ID, Path: r.Path, Return: r.Return,
			ExpiresAt: s.now().Add(TicketLifetime),
		}, l.TokenHash)
		return d, nil
	}
	s.touchLink(ctx, l)
	d.Outcome, d.Via, d.LinkPublicID = Allow, ViaLink, l.PublicID
	d.Issue = &NewSession{
		SiteID: r.SiteID, Policy: p, Method: store.SessionLink, ShareLinkID: l.ID, LinkTokenHash: l.TokenHash, NotAfter: l.ExpiresAt,
	}
	return d, nil
}

func tokenIPKey(ip string) string       { return "link-fail-ip:" + ipKey(ip) }
func tokenSiteKey(siteID string) string { return "link-fail-site:" + siteID }

// overTokenLimit reports whether r's IP or site has had too many tokens
// refused, and how long until the later window resets.
func (s *Service) overTokenLimit(r Request) (bool, time.Duration) {
	ipOver, ipWait := s.limiter.exceeded(tokenIPKey(r.ClientIP), s.limits.TokenFailuresPerIP)
	siteOver, siteWait := s.limiter.exceeded(tokenSiteKey(r.SiteID), s.limits.TokenFailuresPerSite)
	return ipOver || siteOver, max(ipWait, siteWait)
}

// checkToken finds the usable link r.Token names on r's site. Every other
// token is errTokenRefused, whatever the reason; malformed, unknown, wrong
// and other sites' tokens also count against the IP's and site's limits,
// and the guess that goes over one (or finds the limiter full) is a
// *limitedError instead.
// Only public IDs are logged, never tokens.
func (s *Service) checkToken(ctx context.Context, r Request) (store.ShareLink, error) {
	refuse := func(publicID string, guess bool) (store.ShareLink, error) {
		attrs := []slog.Attr{slog.String("site_id", r.SiteID)}
		if publicID != "" {
			attrs = append(attrs, slog.String("public_id", publicID))
		}
		s.log.LogAttrs(ctx, slog.LevelInfo, "share link refused", attrs...)
		if guess {
			ipOK, ipWait := s.limiter.allow(tokenIPKey(r.ClientIP), s.limits.TokenFailuresPerIP)
			siteOK, siteWait := s.limiter.allow(tokenSiteKey(r.SiteID), s.limits.TokenFailuresPerSite)
			if !ipOK || !siteOK {
				return store.ShareLink{}, &limitedError{RetryAfter: max(ipWait, siteWait)}
			}
		}
		return store.ShareLink{}, errTokenRefused
	}
	publicID, ok := parseLinkToken(r.Token)
	if !ok {
		return refuse("", true)
	}
	l, err := s.c.Links.GetByPublicID(ctx, publicID)
	if errors.Is(err, store.ErrNotFound) {
		return refuse(publicID, true)
	}
	if err != nil {
		return store.ShareLink{}, fmt.Errorf("share link %s: %w", publicID, err)
	}
	if !hmac.Equal([]byte(s.key.linkTokenHash(r.Token)), []byte(l.TokenHash)) || l.SiteID != r.SiteID {
		return refuse(publicID, true)
	}
	if !l.RevokedAt.IsZero() || expired(l, s.now()) || usedUp(l) {
		return refuse(publicID, false)
	}
	return l, nil
}

// touchLink records an access to l at most once per touchEvery. A failure
// is logged, never served: the last access is informational.
func (s *Service) touchLink(ctx context.Context, l store.ShareLink) {
	now := s.now()
	if !l.LastAccessedAt.IsZero() && now.Sub(l.LastAccessedAt) < touchEvery {
		return
	}
	if err := s.c.Links.Touch(ctx, l.ID, now); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn, "touch share link", slog.String("public_id", l.PublicID), slog.Any("error", err))
	}
}

// viewTicket is what the "Click to view" form posts back to
// /.wardlock/view instead of the token, so the interstitial never echoes
// the token. Its MAC also covers the link's token hash, so rotating or
// revoking the link voids it.
type viewTicket struct {
	SiteID string `json:"s"`
	LinkID string `json:"l"`
	// Path is the cleaned request path the session is earned for.
	Path string `json:"p"`
	// Return is where the visitor goes after pressing the button.
	Return    string    `json:"r"`
	ExpiresAt time.Time `json:"e"`
}

func (k keyed) encodeTicket(t viewTicket, tokenHash string) string {
	payload, _ := json.Marshal(t) // only strings and a time: cannot fail
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(k.mac(purposeTicketMAC, payload, []byte(tokenHash)))
}

// decodeTicket parses a ticket without checking its MAC, which needs the
// link's token hash; verifyTicket checks it.
func decodeTicket(value string) (t viewTicket, payload, mac []byte, ok bool) {
	if len(value) > maxTicketBytes {
		return viewTicket{}, nil, nil, false
	}
	enc, sig, found := strings.Cut(value, ".")
	if !found {
		return viewTicket{}, nil, nil, false
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(enc)
	if err != nil {
		return viewTicket{}, nil, nil, false
	}
	mac, err = base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil || json.Unmarshal(payload, &t) != nil || !validID(t.SiteID) || !validID(t.LinkID) {
		return viewTicket{}, nil, nil, false
	}
	return t, payload, mac, true
}

// Viewed is the result of pressing "Click to view".
type Viewed struct {
	// Cookie is the new site session; empty when the path no longer needs
	// one (its policy became public), in which case no use was spent.
	Cookie Cookie
	// Return is where to send the visitor: the path and query they asked
	// for, without the token.
	Return string
	// LinkPublicID names the link that was used.
	LinkPublicID string
}

// View spends one use of the link behind an interstitial ticket on siteID
// and issues a site session for it. A forged, expired or foreign ticket,
// or a link that can no longer be used, is ErrTicket and spends nothing.
func (s *Service) View(ctx context.Context, siteID, ticket string) (Viewed, error) {
	t, payload, mac, ok := decodeTicket(ticket)
	if !ok || t.SiteID != siteID || !s.now().Before(t.ExpiresAt) {
		return Viewed{}, ErrTicket
	}
	l, err := s.c.Links.Get(ctx, siteID, t.LinkID)
	if errors.Is(err, store.ErrNotFound) {
		return Viewed{}, ErrTicket
	}
	if err != nil {
		return Viewed{}, fmt.Errorf("get share link: %w", err)
	}
	if !hmac.Equal(mac, s.key.mac(purposeTicketMAC, payload, []byte(l.TokenHash))) {
		return Viewed{}, ErrTicket
	}
	ps, err := s.sitePolicies(ctx, siteID)
	if err != nil {
		return Viewed{}, err
	}
	p, ok := Governing(ps, t.Path)
	if !ok {
		return Viewed{}, fmt.Errorf("site %s has no default policy", siteID)
	}
	if p.Visibility == store.Public {
		return Viewed{Return: t.Return, LinkPublicID: l.PublicID}, nil
	}
	if !p.LinksEnabled || !inScope(l.PathPrefix, t.Path) {
		return Viewed{}, ErrTicket
	}
	err = s.c.Links.Use(ctx, l.ID, l.TokenHash, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return Viewed{}, ErrTicket
	}
	if err != nil {
		return Viewed{}, fmt.Errorf("use share link: %w", err)
	}
	c, err := s.IssueSession(ctx, NewSession{
		SiteID: siteID, Policy: p, Method: store.SessionLink, ShareLinkID: l.ID, LinkTokenHash: l.TokenHash, NotAfter: l.ExpiresAt,
	})
	if errors.Is(err, ErrLinkChanged) {
		// Rotated or revoked between spending the use and issuing.
		return Viewed{}, ErrTicket
	}
	if err != nil {
		return Viewed{}, err
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "share link used",
		slog.String("site_id", siteID), slog.String("public_id", l.PublicID), slog.Int("uses", l.UseCount+1))
	return Viewed{Cookie: c, Return: t.Return, LinkPublicID: l.PublicID}, nil
}
