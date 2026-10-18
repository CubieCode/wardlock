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
	// ActivatedAt is when the active deploy was activated; it is every
	// file's Last-Modified.
	ActivatedAt time.Time
}

// ServedFile is one entry of the active deploy's manifest.
type ServedFile struct {
	Path   string // relative, forward slashes, no leading slash
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
	// ActiveFiles returns the manifest of site's active deploy.
	ActiveFiles(ctx context.Context, site ServedSite) ([]ServedFile, error)
}

// BlobOpener opens content-addressed file bodies. The deploys BlobStore
// satisfies it.
type BlobOpener interface {
	Open(ctx context.Context, sha256 string) (io.ReadSeekCloser, error)
}

// Invalidator reports that a site's served state changed (activation,
// rollback, teardown, domain changes). The deploys Notifier satisfies it.
type Invalidator interface {
	Subscribe(fn func(siteID string)) (unsubscribe func())
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
}
