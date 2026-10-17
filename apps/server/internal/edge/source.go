// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"context"
	"errors"
	"io"
	"time"
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

// Decision is the access hook's answer for one request.
type Decision struct {
	Allow bool
	// Protected marks a site that is not public: responses are cached
	// privately and carry the protected-site headers.
	Protected bool
}

// AccessHook decides whether a request for path on site may be served.
// Policies arrive in A2.1; until then every site is public (AllowAll).
type AccessHook func(ctx context.Context, site ServedSite, path string) Decision

// AllowAll is the access hook used until policies exist: every site is
// public.
func AllowAll(context.Context, ServedSite, string) Decision {
	return Decision{Allow: true}
}
