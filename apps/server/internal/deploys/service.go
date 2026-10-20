// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"wardlock.nz/server/internal/store"
)

// Errors returned by Service. Input errors arrive as *TargetError wrapping
// ErrInvalidPath, ErrInvalidManifest or ErrLimitExceeded.
var (
	// ErrNotFound covers a missing site or deploy and one that belongs to
	// another owner alike.
	ErrNotFound         = errors.New("not found")
	ErrInvalidPath      = errors.New("invalid path")
	ErrInvalidManifest  = errors.New("invalid manifest")
	ErrLimitExceeded    = errors.New("limit exceeded")
	ErrUnexpectedBlob   = errors.New("blob not in the deploy's manifest")
	ErrBlobsMissing     = errors.New("deploy blobs missing")
	ErrNotPending       = errors.New("deploy is not pending")
	ErrNotActivated     = errors.New("deploy was never activated")
	ErrNoPreviousDeploy = errors.New("no previous deploy")
)

// TargetError is an input error about one manifest path or field. Message
// is safe to show the owner.
type TargetError struct {
	Err     error
	Target  string
	Message string
}

func (e *TargetError) Error() string { return e.Message }
func (e *TargetError) Unwrap() error { return e.Err }

// Status is a deploy's place in its site's history.
type Status string

const (
	StatusPending  Status = "pending"  // never activated
	StatusActive   Status = "active"   // the site's active deploy
	StatusInactive Status = "inactive" // activated before, since replaced
)

// StatusOf returns d's status on site.
func StatusOf(site store.Site, d store.Deploy) Status {
	switch {
	case d.ID == site.ActiveDeployID:
		return StatusActive
	case d.ActivatedAt.IsZero():
		return StatusPending
	default:
		return StatusInactive
	}
}

// Config holds a Service's dependencies.
type Config struct {
	Sites    store.Sites
	Deploys  store.Deploys
	Blobs    BlobStore
	Notifier Notifier
	Limits   Limits
	Now      func() time.Time
	// Renditions records the Markdown and plain-text renditions generated
	// for each HTML file at activation (A2.6); nil generates none.
	Renditions store.Renditions
	// SiteOrigin returns a site's address (such as https://docs.example.com)
	// from its name, which renditions make links absolute against; nil
	// leaves them root-relative.
	SiteOrigin func(siteName string) string
	// MaxRenditionSource is the largest HTML file, in bytes, that gets
	// renditions; 0 means DefaultMaxRenditionSource.
	MaxRenditionSource int64
}

// Service creates, uploads, activates, lists and rolls back deploys. Every
// method takes the verified owner ID and addresses sites by name; another
// owner's site is ErrNotFound.
type Service struct {
	c     Config
	slots chan struct{} // render slots (renditions.go)
}

// NewService returns a Service over c.
func NewService(c Config) *Service { return &Service{c: c, slots: make(chan struct{}, renderSlots)} }

func (s *Service) site(ctx context.Context, ownerID, name string) (store.Site, error) {
	site, err := s.c.Sites.GetByOwnerName(ctx, ownerID, name)
	return site, storeErr("find site", err)
}

// Create records a pending deploy from m and returns it with the hashes the
// owner still has to upload, each once. A hash counts as held only when
// this owner uploaded it and the blob is still stored, so the answer never
// reveals another owner's content.
func (s *Service) Create(ctx context.Context, ownerID, siteName string, m Manifest) (store.Deploy, []string, error) {
	site, err := s.site(ctx, ownerID, siteName)
	if err != nil {
		return store.Deploy{}, nil, err
	}
	if err := s.c.Limits.validate(&m); err != nil {
		return store.Deploy{}, nil, err
	}
	d := store.Deploy{
		ID:              store.NewID(),
		SiteID:          site.ID,
		Message:         m.Message,
		FileCount:       len(m.Files),
		SPA:             m.SPA,
		ImmutableAssets: m.ImmutableAssets,
		CreatedAt:       s.now(),
	}
	for _, f := range m.Files {
		d.TotalSize += f.Size
	}
	if err := s.c.Deploys.Create(ctx, ownerID, d, m.Files); err != nil {
		return store.Deploy{}, nil, storeErr("create deploy", err)
	}
	missing, err := s.missing(ctx, ownerID, site.ID, d.ID)
	if err != nil {
		return store.Deploy{}, nil, err
	}
	return d, missing, nil
}

// missing returns the deploy's hashes the owner has not uploaded or whose
// blob is no longer stored, in hash order.
func (s *Service) missing(ctx context.Context, ownerID, siteID, deployID string) ([]string, error) {
	unclaimed, err := s.c.Deploys.MissingBlobs(ctx, ownerID, siteID, deployID)
	if err != nil {
		return nil, storeErr("list missing blobs", err)
	}
	hashes, err := s.c.Deploys.Hashes(ctx, ownerID, siteID, deployID)
	if err != nil {
		return nil, storeErr("list manifest hashes", err)
	}
	isUnclaimed := make(map[string]bool, len(unclaimed))
	for _, h := range unclaimed {
		isUnclaimed[h] = true
	}
	missing := make([]string, 0, len(unclaimed))
	for _, h := range hashes {
		if !isUnclaimed[h] {
			ok, err := s.c.Blobs.Has(ctx, h)
			if err != nil {
				return nil, fmt.Errorf("check blob: %w", err)
			}
			if ok {
				continue
			}
		}
		missing = append(missing, h)
	}
	return missing, nil
}

// Upload stores one blob listed in a deploy's manifest and records that the
// owner holds it. The body must be exactly the listed size and hash; one
// longer than the per-file limit is ErrLimitExceeded. Re-uploading a blob
// already stored verifies it again without rewriting it.
func (s *Service) Upload(ctx context.Context, ownerID, siteName, deployID, hash string, body io.Reader) error {
	site, err := s.site(ctx, ownerID, siteName)
	if err != nil {
		return err
	}
	if _, err := s.c.Deploys.Get(ctx, ownerID, site.ID, deployID); err != nil {
		return storeErr("find deploy", err)
	}
	if !ValidHash(hash) {
		return ErrUnexpectedBlob
	}
	size, err := s.c.Deploys.ManifestSize(ctx, ownerID, site.ID, deployID, hash)
	if errors.Is(err, store.ErrNotFound) {
		return ErrUnexpectedBlob
	}
	if err != nil {
		return fmt.Errorf("find manifest blob: %w", err)
	}
	limit := s.c.Limits.MaxFileSize
	if size > limit {
		return &TargetError{Err: ErrLimitExceeded, Target: hash, Message: fmt.Sprintf("The blob is %d bytes; the limit is %d.", size, limit)}
	}
	cr := &countingReader{r: body}
	err = s.c.Blobs.Put(ctx, hash, size, cr)
	if errors.Is(err, ErrHashMismatch) && cr.n > size {
		// Read on, up to the limit, to tell an oversized body from a wrong one.
		rest, _ := io.Copy(io.Discard, io.LimitReader(body, limit-cr.n+1))
		if cr.n+rest > limit {
			return &TargetError{Err: ErrLimitExceeded, Target: hash, Message: fmt.Sprintf("The body is over the %d-byte limit.", limit)}
		}
	}
	if err != nil {
		return fmt.Errorf("store blob: %w", err)
	}
	err = s.c.Deploys.RecordUpload(ctx, ownerID, store.Blob{SHA256: hash, Size: size, CreatedAt: s.now()})
	return storeErr("record upload", err)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Activate generates the renditions of the deploy's HTML files, then makes
// it the site's active deploy atomically and notifies subscribers. It fails with ErrBlobsMissing until the owner has
// uploaded every blob, and ErrNotPending once activated (see Rollback).
func (s *Service) Activate(ctx context.Context, ownerID, siteName, deployID string) (store.Site, store.Deploy, error) {
	site, err := s.site(ctx, ownerID, siteName)
	if err != nil {
		return store.Site{}, store.Deploy{}, err
	}
	d, err := s.c.Deploys.Get(ctx, ownerID, site.ID, deployID)
	if err != nil {
		return store.Site{}, store.Deploy{}, storeErr("find deploy", err)
	}
	if !d.ActivatedAt.IsZero() {
		return store.Site{}, store.Deploy{}, ErrNotPending
	}
	missing, err := s.missing(ctx, ownerID, site.ID, deployID)
	if err != nil {
		return store.Site{}, store.Deploy{}, err
	}
	if len(missing) > 0 {
		return store.Site{}, store.Deploy{}, &TargetError{
			Err: ErrBlobsMissing, Target: missing[0],
			Message: fmt.Sprintf("%d blobs have not been uploaded.", len(missing)),
		}
	}
	if err := s.render(ctx, ownerID, site, deployID); err != nil {
		return store.Site{}, store.Deploy{}, err
	}
	// The store re-checks the owner's uploads inside the transaction.
	d, err = s.c.Deploys.Activate(ctx, ownerID, site.ID, deployID, s.now())
	if err != nil {
		return store.Site{}, store.Deploy{}, storeErr("activate deploy", err)
	}
	return s.served(ctx, site, d), d, nil
}

// Rollback reactivates deployID, or with "" the deploy active before the
// current one, and notifies subscribers.
func (s *Service) Rollback(ctx context.Context, ownerID, siteName, deployID string) (store.Site, store.Deploy, error) {
	site, err := s.site(ctx, ownerID, siteName)
	if err != nil {
		return store.Site{}, store.Deploy{}, err
	}
	d, changed, err := s.c.Deploys.Rollback(ctx, ownerID, site.ID, deployID, s.now())
	if err != nil {
		return store.Site{}, store.Deploy{}, storeErr("rollback", err)
	}
	if !changed {
		// Already active when the transaction ran: nothing to tell the edge.
		site.ActiveDeployID = d.ID
		site.ActivatedAt = d.ActivatedAt
		return site, d, nil
	}
	return s.served(ctx, site, d), d, nil
}

// served notifies subscribers that site now serves d and returns the site
// as it is now.
func (s *Service) served(ctx context.Context, site store.Site, d store.Deploy) store.Site {
	s.c.Notifier.Notify(ctx, site.ID)
	site.ActiveDeployID, site.ActivatedAt = d.ID, d.ActivatedAt
	return site
}

// List returns the site and up to limit of its deploys, newest first,
// strictly older than before.
func (s *Service) List(ctx context.Context, ownerID, siteName string, before store.DeployCursor, limit int) (store.Site, []store.Deploy, error) {
	site, err := s.site(ctx, ownerID, siteName)
	if err != nil {
		return store.Site{}, nil, err
	}
	deploys, err := s.c.Deploys.List(ctx, ownerID, site.ID, before, limit)
	if err != nil {
		return store.Site{}, nil, storeErr("list deploys", err)
	}
	return site, deploys, nil
}

func (s *Service) now() time.Time { return s.c.Now().UTC().Truncate(time.Millisecond) }

// storeErr translates repository errors into this package's sentinels.
func storeErr(what string, err error) error {
	for _, m := range []struct{ from, to error }{
		{store.ErrNotFound, ErrNotFound},
		{store.ErrBlobsMissing, ErrBlobsMissing},
		{store.ErrDeployNotPending, ErrNotPending},
		{store.ErrDeployNotActivated, ErrNotActivated},
		{store.ErrNoPreviousDeploy, ErrNoPreviousDeploy},
	} {
		if errors.Is(err, m.from) {
			return fmt.Errorf("%s: %w", what, m.to)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}
