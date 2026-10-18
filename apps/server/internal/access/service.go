// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"wardlock.nz/server/internal/store"
)

// Errors returned by Service.
var (
	// ErrNotFound covers a missing site or policy and another owner's alike.
	ErrNotFound = errors.New("not found")
	// ErrInvalidPath means a policy path prefix is not one CheckPrefix
	// accepts.
	ErrInvalidPath = errors.New("invalid policy path")
	// ErrInvalidPolicy means the visibility and methods do not fit together.
	ErrInvalidPolicy = errors.New("invalid policy")
	// ErrDefaultPolicy means the request would delete a site's default
	// policy, which every site keeps.
	ErrDefaultPolicy = errors.New("default policy cannot be deleted")
)

// TargetError carries the field an error is about and a message safe to
// show the owner. It wraps one of the errors above.
type TargetError struct {
	Err     error
	Target  string
	Message string
}

func (e *TargetError) Error() string { return e.Message }
func (e *TargetError) Unwrap() error { return e.Err }

type timeFunc func() time.Time

// notifier is the part of deploys.Notifier the service uses: policy changes
// are announced like deploy changes, and every announcement drops the
// site's cached policies.
type notifier interface {
	Notify(ctx context.Context, siteID string)
	Subscribe(fn func(siteID string)) (unsubscribe func())
}

// Config holds a Service's dependencies.
type Config struct {
	Sites    store.Sites
	Policies store.Policies
	Sessions store.Sessions
	Links    store.Links
	// PasswordSessions stores password sessions only while the password
	// they were checked against still holds (A2.3).
	PasswordSessions store.PasswordSessions
	// AccessLog backs LinkAccessLog (D3.3); nil when the caller never reads
	// the access log.
	AccessLog store.AccessLog
	Notifier  notifier
	// Secret is the server key (WARDLOCK_SECRET): it signs session cookies
	// and keys the stored session token hashes.
	Secret []byte
	// Limits are the visitor rate limits; zero means DefaultLimits.
	Limits Limits
	Now    func() time.Time
	Log    *slog.Logger
}

// Service evaluates site policies and share-link tokens for the edge,
// issues and ends site sessions, and serves the owner's policy and
// share-link operations.
type Service struct {
	c           Config
	key         keyed
	now         timeFunc
	log         *slog.Logger
	limits      Limits
	limiter     *limiter // client keys (IPs), bounded
	siteLimiter *limiter // server keys (sites, policies), unbounded
	// ipAttempts and siteAttempts count password attempts: limiter and
	// siteLimiter, or fakes in tests.
	ipAttempts, siteAttempts attemptLimiter
	derive                   deriveFunc
	// hashSlots bounds concurrent password checks (each holds 19 MiB);
	// hashWait is how long an attempt waits for a slot.
	hashSlots   chan struct{}
	hashWait    time.Duration
	unsubscribe func()

	mu       sync.Mutex
	gen      uint64                    // bumped by every invalidation
	policies map[string][]store.Policy // by site ID
}

// New returns a Service subscribed to cfg.Notifier until Close.
func New(cfg Config) (*Service, error) {
	if len(cfg.Secret) < 32 {
		return nil, errors.New("access: the server key must be at least 32 bytes")
	}
	if cfg.Links == nil {
		return nil, errors.New("access: Config.Links is required")
	}
	if cfg.PasswordSessions == nil {
		return nil, errors.New("access: Config.PasswordSessions is required")
	}
	s := &Service{
		c:      cfg,
		key:    keyed{secret: cfg.Secret},
		now:    func() time.Time { return cfg.Now().UTC().Truncate(time.Millisecond) },
		log:    cfg.Log,
		derive: argon2.IDKey,
		// Half the CPUs, at least two: enough to keep sign-ins moving
		// while bounding memory to slots × 19 MiB.
		hashSlots: make(chan struct{}, max(2, runtime.GOMAXPROCS(0)/2)),
		hashWait:  time.Second,
		policies:  map[string][]store.Policy{},
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	s.limits = cfg.Limits
	if s.limits == (Limits{}) {
		s.limits = DefaultLimits()
	}
	s.limiter = newLimiter(s.now)
	s.siteLimiter = newServerKeyLimiter(s.now)
	s.ipAttempts, s.siteAttempts = s.limiter, s.siteLimiter
	s.unsubscribe = cfg.Notifier.Subscribe(s.invalidate)
	return s, nil
}

// Close stops listening for invalidations.
func (s *Service) Close() { s.unsubscribe() }

func (s *Service) invalidate(siteID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	delete(s.policies, siteID)
}

// sitePolicies returns siteID's policies ordered by prefix, from memory when
// it can, so a public request never touches the database. A site without a
// default (created before A2.1 or never configured) gets one first.
func (s *Service) sitePolicies(ctx context.Context, siteID string) ([]store.Policy, error) {
	s.mu.Lock()
	ps, ok := s.policies[siteID]
	gen := s.gen
	s.mu.Unlock()
	if ok {
		return ps, nil
	}
	ps, err := s.loadPolicies(ctx, siteID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// An invalidation while loading may make what was read stale: use it
	// for this request, but do not keep it.
	if s.gen == gen {
		s.policies[siteID] = ps
	}
	s.mu.Unlock()
	return ps, nil
}

func (s *Service) loadPolicies(ctx context.Context, siteID string) ([]store.Policy, error) {
	ps, err := s.c.Policies.ForSite(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("load policies: %w", err)
	}
	if len(ps) > 0 && ps[0].PathPrefix == DefaultPrefix {
		return ps, nil
	}
	if err := s.c.Policies.EnsureDefault(ctx, newDefault(siteID, s.now)); err != nil {
		return nil, fmt.Errorf("create default policy: %w", err)
	}
	ps, err = s.c.Policies.ForSite(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("load policies: %w", err)
	}
	return ps, nil
}

// Request is what the edge knows about one request, as plain values.
type Request struct {
	SiteID string
	// Path is the cleaned, absolute request path (or the path of the file
	// that will answer it).
	Path string
	// Cookie is the value of the SessionCookieName cookie; "" without one.
	Cookie string
	// Browser reports that the client accepts HTML, so it is shown the
	// login page rather than a bare 401.
	Browser bool
	// Token is the share-link token from ?t= or Authorization: Bearer; ""
	// without one. It is checked on every request that carries it.
	Token string
	// Return is the request's path and query without the token: where a
	// use-limited link's interstitial sends the visitor back to.
	Return string
	// ClientIP is the client address (after trusted-proxy resolution),
	// the key for token-guess limits.
	ClientIP string
}

// Outcome is what the edge does with a request.
type Outcome int

const (
	// Allow serves the request.
	Allow Outcome = iota + 1
	// Login answers 401 with the site's login page.
	Login
	// Unauthorized answers a bare 401.
	Unauthorized
	// Interstitial shows a use-limited link's "Click to view" page, whose
	// form posts Decision.Ticket to /.wardlock/view. Nothing is spent.
	Interstitial
	// RateLimited answers 429: too many tokens were refused for this IP or
	// site. Decision.RetryAfter says for how long.
	RateLimited
)

// Via says how a request was let in, in the access log's terms: its values
// are exactly those the access_log.via column accepts (A2.8).
type Via string

const (
	ViaPublic   Via = "public"
	ViaLink     Via = "link"
	ViaPassword Via = "password"
	ViaEmail    Via = "email"
	ViaOAuth    Via = "oauth"
	// ViaNone marks a request that was not let in.
	ViaNone Via = "none"
)

// viaSession maps a session's method to how it lets requests in.
func viaSession(m store.SessionMethod) Via {
	switch m {
	case store.SessionLink:
		return ViaLink
	case store.SessionPassword:
		return ViaPassword
	case store.SessionEmail:
		return ViaEmail
	case store.SessionOAuth:
		return ViaOAuth
	}
	return ViaNone
}

// Decision is the answer for one request.
type Decision struct {
	Outcome Outcome
	// Protected marks a response governed by a protected policy: it is
	// cached privately and carries the protected-site headers.
	Protected bool
	// Policy is the policy that governed the request.
	Policy store.Policy
	// Via is ViaPublic for a public policy, ViaNone for a request not let
	// in, and otherwise the method of the session that allowed it.
	Via Via
	// LinkPublicID is the share link's public ID, set only when Via is
	// ViaLink.
	LinkPublicID string
	// TokenAccepted reports that the request's share-link token is valid
	// here, so HTML responses strip it from the address bar.
	TokenAccepted bool
	// Issue, when set on an allowed request, is the link session the
	// request's token has earned; the edge issues it when the token came
	// in the URL (a browser following a link) and sets its cookie.
	Issue *NewSession
	// Ticket is the interstitial's form value (Outcome Interstitial).
	Ticket string
	// RetryAfter is how long a RateLimited client must wait.
	RetryAfter time.Duration
	// ClearCookie asks the edge to delete the visitor's session cookie: it
	// is forged, tampered with, for another site, expired, revoked or bound
	// to a policy version that no longer exists.
	ClearCookie bool
}

// Decide applies the governing policy to r. A public policy allows anyone
// and ignores tokens. A protected one allows a valid share-link token in
// its scope when the policy enables links (a use-limited link shows the
// interstitial instead), or a valid site session bound to that policy's
// current version and created by a method it still enables (methods are
// alternatives); otherwise browsers get the login page and other clients a
// bare 401.
func (s *Service) Decide(ctx context.Context, r Request) (Decision, error) {
	ps, err := s.sitePolicies(ctx, r.SiteID)
	if err != nil {
		return Decision{}, err
	}
	p, ok := Governing(ps, r.Path)
	if !ok {
		return Decision{}, fmt.Errorf("site %s has no default policy", r.SiteID)
	}
	if p.Visibility == store.Public {
		return Decision{Outcome: Allow, Policy: p, Via: ViaPublic}, nil
	}
	d := Decision{Outcome: Unauthorized, Protected: true, Policy: p, Via: ViaNone}
	if r.Browser {
		d.Outcome = Login
	}
	var sess session
	if r.Cookie != "" {
		if sess, err = s.checkSession(ctx, r, ps, p); err != nil {
			return Decision{}, err
		}
		// Anything but a good session (for this or another of the site's
		// policies) is cleared.
		d.ClearCookie = !sess.valid
	}
	if r.Token != "" {
		return s.decideToken(ctx, r, p, sess, d)
	}
	if sess.allows {
		d.Outcome, d.Via, d.LinkPublicID = Allow, sess.via, sess.linkPublicID
	}
	return d, nil
}

// session is what checkSession found in a cookie.
type session struct {
	// valid: a live session of the site, bound to the current version of
	// one of its policies. Keep the cookie.
	valid bool
	// allows: valid, and it lets the request in under the governing
	// policy.
	allows bool
	via    Via
	// linkPublicID names the link behind a link session.
	linkPublicID string
}

// checkSession verifies r's cookie against p, the governing policy. A
// session allows r only under the policy it was created under, while p
// still enables its method; a link session also needs its link unrevoked,
// unexpired and scoped to r.Path, checked on every request.
func (s *Service) checkSession(ctx context.Context, r Request, ps []store.Policy, p store.Policy) (session, error) {
	c, ok := s.key.decode(r.Cookie)
	if !ok || c.SiteID != r.SiteID || !s.now().Before(c.ExpiresAt) || !current(ps, c.PolicyID, c.PolicyVersion) {
		return session{}, nil
	}
	if c.PolicyID != p.ID {
		// Valid for another policy of the site; no need to look it up.
		return session{valid: true}, nil
	}
	row, err := s.c.Sessions.GetSiteByTokenHash(ctx, s.key.tokenHash(c.Token))
	if errors.Is(err, store.ErrNotFound) {
		return session{}, nil
	}
	if err != nil {
		return session{}, fmt.Errorf("get site session: %w", err)
	}
	if row.SiteID != c.SiteID || row.PolicyID != c.PolicyID || row.PolicyVersion != c.PolicyVersion ||
		!row.RevokedAt.IsZero() || !s.now().Before(row.ExpiresAt) {
		return session{}, nil
	}
	sess := session{valid: true, via: viaSession(row.Method)}
	if !enables(p, row.Method) {
		return sess, nil
	}
	if row.Method == store.SessionLink {
		l, err := s.c.Links.Get(ctx, r.SiteID, row.ShareLinkID)
		if errors.Is(err, store.ErrNotFound) {
			return session{}, nil
		}
		if err != nil {
			return session{}, fmt.Errorf("get share link: %w", err)
		}
		if !l.RevokedAt.IsZero() || expired(l, s.now()) {
			return session{}, nil
		}
		sess.linkPublicID = l.PublicID
		if !inScope(l.PathPrefix, r.Path) {
			return sess, nil
		}
		s.touchLink(ctx, l)
	}
	sess.allows = true
	return sess, nil
}

// current reports whether policy id exists in ps at version.
func current(ps []store.Policy, id string, version int) bool {
	for _, p := range ps {
		if p.ID == id {
			return p.Version == version
		}
	}
	return false
}

// NewSession describes a site session a method has just earned.
type NewSession struct {
	SiteID string
	// Policy is the policy the visitor satisfied; the session is bound to
	// its ID and current version.
	Policy store.Policy
	Method store.SessionMethod
	// ShareLinkID is set exactly when Method is store.SessionLink.
	ShareLinkID string
	// LinkTokenHash is the stored hash of the link token that earned a
	// link session. The session is stored only if the link still has it
	// (and is unrevoked and unexpired), so a rotation or revocation that
	// races the issue cannot leave a live session behind (ErrLinkChanged).
	LinkTokenHash string
	// PasswordHash is the policy password hash a password session was
	// checked against. The session is stored only if the policy still has
	// it at the same version (ErrPasswordChanged), so a password change
	// that races the sign-in cannot leave a live session behind.
	PasswordHash string
	// Subject is the verified email for email-code and OAuth sessions.
	Subject string
	// NotAfter caps the session's lifetime (a link's expiry); zero means
	// SessionLifetime.
	NotAfter time.Time
}

// IssueSession stores a site session and returns its cookie. It is the
// primitive the share link, password, email-code and OAuth methods call
// once a visitor has satisfied them.
func (s *Service) IssueSession(ctx context.Context, n NewSession) (Cookie, error) {
	now := s.now()
	expires := now.Add(SessionLifetime)
	if !n.NotAfter.IsZero() && n.NotAfter.Before(expires) {
		expires = n.NotAfter.UTC().Truncate(time.Millisecond)
	}
	if !now.Before(expires) {
		if n.Method == store.SessionLink {
			return Cookie{}, ErrLinkChanged // the link expired after the check
		}
		return Cookie{}, errors.New("issue site session: already expired")
	}
	token := newToken()
	row := store.SiteSession{
		ID:            store.NewID(),
		SiteID:        n.SiteID,
		TokenHash:     s.key.tokenHash(token),
		Method:        n.Method,
		ShareLinkID:   n.ShareLinkID,
		PolicyID:      n.Policy.ID,
		PolicyVersion: n.Policy.Version,
		Subject:       n.Subject,
		CreatedAt:     now,
		ExpiresAt:     expires,
	}
	var err error
	switch n.Method {
	case store.SessionLink:
		if n.LinkTokenHash == "" {
			return Cookie{}, errors.New("issue site session: a link session needs the link's token hash")
		}
		err = s.c.Links.CreateSession(ctx, row, n.LinkTokenHash)
		if errors.Is(err, store.ErrNotFound) {
			return Cookie{}, ErrLinkChanged
		}
	case store.SessionPassword:
		if n.PasswordHash == "" {
			return Cookie{}, errors.New("issue site session: a password session needs the password hash")
		}
		err = s.c.PasswordSessions.CreateSession(ctx, row, n.PasswordHash)
		if errors.Is(err, store.ErrNotFound) {
			return Cookie{}, ErrPasswordChanged
		}
	default:
		err = s.c.Sessions.CreateSite(ctx, row)
	}
	if err != nil {
		return Cookie{}, fmt.Errorf("issue site session: %w", err)
	}
	value := s.key.encode(sessionClaims{
		SiteID: n.SiteID, PolicyID: n.Policy.ID, PolicyVersion: n.Policy.Version, ExpiresAt: expires, Token: token,
	})
	return Cookie{Name: SessionCookieName, Value: value, Expires: expires}, nil
}

// EndSession revokes the site session a cookie value names, if it is a
// genuine cookie for siteID. Anything else ends nothing and is not an
// error: the visitor is signed out either way.
func (s *Service) EndSession(ctx context.Context, siteID, cookie string) error {
	c, ok := s.key.decode(cookie)
	if !ok || c.SiteID != siteID {
		return nil
	}
	row, err := s.c.Sessions.GetSiteByTokenHash(ctx, s.key.tokenHash(c.Token))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get site session: %w", err)
	}
	if row.SiteID != siteID {
		return nil
	}
	if err := s.c.Sessions.RevokeSite(ctx, siteID, row.ID, s.now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("revoke site session: %w", err)
	}
	return nil
}

// ownedSite finds ownerID's site called name.
func (s *Service) ownedSite(ctx context.Context, ownerID, name string) (store.Site, error) {
	site, err := s.c.Sites.GetByOwnerName(ctx, ownerID, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Site{}, fmt.Errorf("get site: %w", ErrNotFound)
	}
	if err != nil {
		return store.Site{}, fmt.Errorf("get site: %w", err)
	}
	return site, nil
}

// List returns up to limit of the policies of ownerID's site called name,
// ordered by path prefix after afterPrefix ("" for the first page). The
// site default is always among them.
func (s *Service) List(ctx context.Context, ownerID, siteName, afterPrefix string, limit int) ([]store.Policy, error) {
	site, err := s.ownedSite(ctx, ownerID, siteName)
	if err != nil {
		return nil, err
	}
	if _, err := s.sitePolicies(ctx, site.ID); err != nil {
		return nil, err
	}
	ps, err := s.c.Policies.List(ctx, ownerID, site.ID, afterPrefix, limit)
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	return ps, nil
}

// Set makes r the policy for its path prefix on ownerID's site called
// name, creating it or replacing its settings. A change increments the
// policy's version, which ends every site session bound to it on the next
// request; setting identical settings changes nothing. A new password
// always counts as a change, and changing or removing the password also
// revokes the policy's password sessions outright.
func (s *Service) Set(ctx context.Context, ownerID, siteName string, r Rule) (store.Policy, error) {
	if err := checkRule(r); err != nil {
		return store.Policy{}, err
	}
	site, err := s.ownedSite(ctx, ownerID, siteName)
	if err != nil {
		return store.Policy{}, err
	}
	if _, err := s.sitePolicies(ctx, site.ID); err != nil {
		return store.Policy{}, err
	}
	hash, err := s.passwordHash(r)
	if err != nil {
		return store.Policy{}, err
	}
	p, err := s.c.Policies.Set(ctx, ownerID, store.Policy{
		ID: store.NewID(), SiteID: site.ID, PathPrefix: r.PathPrefix,
		Visibility: r.Visibility, LinksEnabled: r.Links,
		PasswordEnabled: r.Password, PasswordHash: hash,
		UpdatedAt: s.now(),
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.Policy{}, fmt.Errorf("set policy: %w", ErrNotFound)
	}
	if errors.Is(err, store.ErrNoPasswordToKeep) {
		return store.Policy{}, errNeedsPassword
	}
	if err != nil {
		return store.Policy{}, fmt.Errorf("set policy: %w", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	s.log.LogAttrs(ctx, slog.LevelInfo, "policy set",
		slog.String("site_id", site.ID), slog.String("policy_id", p.ID),
		slog.String("path", p.PathPrefix), slog.String("visibility", string(p.Visibility)),
		slog.Bool("password", p.PasswordEnabled), slog.Int("version", p.Version))
	return p, nil
}

// Delete removes path policy id from ownerID's site called name, ending
// the sessions bound to it; its paths fall back to the next longest prefix.
// The site default cannot be deleted (ErrDefaultPolicy).
func (s *Service) Delete(ctx context.Context, ownerID, siteName, id string) error {
	site, err := s.ownedSite(ctx, ownerID, siteName)
	if err != nil {
		return err
	}
	ps, err := s.loadPolicies(ctx, site.ID)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.ID == id && p.PathPrefix == DefaultPrefix {
			return ErrDefaultPolicy
		}
	}
	err = s.c.Policies.Delete(ctx, ownerID, site.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("delete policy: %w", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("delete policy: %w", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	s.log.LogAttrs(ctx, slog.LevelInfo, "policy deleted", slog.String("site_id", site.ID), slog.String("policy_id", id))
	return nil
}
