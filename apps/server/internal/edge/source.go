// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"context"
	"errors"
	"io"
	"time"

	"wardlock.nz/server/internal/access"
)

// ErrNoSite is returned by a SiteSource when no site is served at a host.
var ErrNoSite = errors.New("no site for host")

// ServedSite is what the edge needs to know about a site to serve it.
type ServedSite struct {
	ID             string
	Name           string
	ActiveDeployID string // empty while nothing is published
	// SPA is the active deploy's SPA fallback flag.
	SPA bool
	// ImmutableAssets is the active deploy's opt-in to long-term caching
	// of hashed files under /assets/ (SPEC "Serving").
	ImmutableAssets bool
	// ActivatedAt is when the active deploy was activated; it is every
	// file's Last-Modified.
	ActivatedAt time.Time
	// Unavailable means the operator has taken the site down or suspended
	// its owner (A2.13): every host answers the unavailable page.
	Unavailable bool
	// Screened means content screening applies to the site (A2.15: on,
	// and its owner not exempt): a file not screened yet is never cached
	// for long, a gzip sibling is offered only once screening has checked
	// it decompresses to exactly its file, and brotli siblings, which the
	// server cannot check, are never offered.
	Screened bool
}

// ServedFile is one entry of the active deploy's manifest.
type ServedFile struct {
	Path   string // relative, forward slashes, no leading slash
	SHA256 string // lowercase hex
	Size   int64
	// Renditions are an HTML file's AI-readable versions (A2.6) by format
	// ("md", "txt"); nil for other files and deploys activated before A2.6.
	Renditions map[string]Rendition
	// Pending means content screening has not screened it yet (set only on
	// a Screened site).
	Pending bool
	// Decoded is, for a gzip file screening checked, the SHA-256 of what
	// it decompresses to; "" otherwise.
	Decoded string
}

// Rendition is a Markdown or plain-text version of an HTML file, stored as
// a blob.
type Rendition struct {
	SHA256 string // lowercase hex
	Size   int64
}

// SiteSource looks up sites for the edge. It is instance-wide, not owner
// scoped: the host alone decides which site a visitor reaches.
type SiteSource interface {
	// SiteForHost returns the site at a canonical host: a single label
	// under the base domain, or an active custom domain. ErrNoSite when
	// there is none.
	SiteForHost(ctx context.Context, host string) (ServedSite, error)
	// ActiveFiles returns the manifest of site's active deploy, with each
	// HTML file's renditions.
	ActiveFiles(ctx context.Context, site ServedSite) ([]ServedFile, error)
}

// BlobOpener opens content-addressed file bodies of a size the manifest
// gives. The deploys BlobStore satisfies it. Open may defer its work, and
// so its errors, to the first Read.
type BlobOpener interface {
	Open(ctx context.Context, sha256 string, size int64) (io.ReadSeekCloser, error)
}

// Invalidator reports that a site's served state changed (activation,
// rollback, teardown, domain changes, takedown and suspension). The deploys Notifier satisfies it.
type Invalidator interface {
	Subscribe(fn func(siteID string)) (unsubscribe func())
}

// Withheld reports whether a content hash is quarantined (A2.15). It is
// consulted on every file served, so it must answer from memory; the
// moderation Quarantine satisfies it.
type Withheld interface {
	Has(sha256 string) bool
}

// Access decides who may read a site. *access.Service satisfies it.
type Access interface {
	// Decide applies the policy governing r.Path on r.SiteID.
	Decide(ctx context.Context, r access.Request) (access.Decision, error)
	// IssueSession stores a site session a method earned and returns its
	// cookie.
	IssueSession(ctx context.Context, n access.NewSession) (access.Cookie, error)
	// View spends a use of the link behind an interstitial ticket.
	View(ctx context.Context, siteID, ticket string) (access.Viewed, error)
	// EndSession revokes the site session a cookie value names, if any.
	EndSession(ctx context.Context, siteID, cookie string) error
	// PasswordLogin checks a password sign-in and returns the session
	// cookie it earns (A2.3).
	PasswordLogin(ctx context.Context, a access.PasswordAttempt) (access.Cookie, error)
	// SendEmailCode emails a code if the address may sign in, answering
	// the same either way (A2.4).
	SendEmailCode(ctx context.Context, r access.EmailCodeRequest) error
	// EmailCodeLogin checks an emailed code and returns the session cookie
	// it earns (A2.4).
	EmailCodeLogin(ctx context.Context, a access.EmailCodeAttempt) (access.Cookie, error)
	// OAuthProviders lists the configured OAuth and OIDC providers (A2.5).
	OAuthProviders() []access.ProviderInfo
	// StartOAuth begins a provider sign-in from the login form.
	StartOAuth(ctx context.Context, a access.OAuthStart) (access.OAuthStarted, error)
	// FinishOAuth completes it at the auth origin and returns where to go.
	FinishOAuth(ctx context.Context, cb access.OAuthCallback) (access.OAuthFinished, error)
	// RedeemOAuth exchanges a site code for the session cookie.
	RedeemOAuth(ctx context.Context, r access.OAuthRedeem) (access.OAuthRedeemed, error)
	// ReportsEnabled reports whether visitors may report sites (A2.13).
	ReportsEnabled() bool
	// SendReport checks, limits and sends a visitor's abuse report.
	SendReport(ctx context.Context, r access.Report) error
}
