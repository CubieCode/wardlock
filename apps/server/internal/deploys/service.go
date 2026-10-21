// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"wardlock.nz/server/internal/store"
)

// Errors returned by Service. Input errors arrive as *TargetError wrapping
// ErrInvalidPath, ErrInvalidManifest, ErrLimitExceeded or ErrBlockedType.
var (
	// ErrNotFound covers a missing site or deploy and one that belongs to
	// another owner alike.
	ErrNotFound        = errors.New("not found")
	ErrInvalidPath     = errors.New("invalid path")
	ErrInvalidManifest = errors.New("invalid manifest")
	ErrLimitExceeded   = errors.New("limit exceeded")
	// ErrBlockedType means the manifest lists a file whose extension the
	// operator has blocked (A2.14).
	ErrBlockedType      = errors.New("file type blocked")
	ErrUnexpectedBlob   = errors.New("blob not in the deploy's manifest")
	ErrBlobsMissing     = errors.New("deploy blobs missing")
	ErrNotPending       = errors.New("deploy is not pending")
	ErrNotActivated     = errors.New("deploy was never activated")
	ErrNoPreviousDeploy = errors.New("no previous deploy")
	// ErrSiteUnavailable means the operator has taken the site down: its
	// deploys are kept as they are until it is restored (A2.13).
	ErrSiteUnavailable = errors.New("site taken down by the operator")
	// ErrStaleBase means a path deploy's base is no longer the site's
	// active deploy, so activating it would drop what went live since; the
	// client creates the deploy again (C1.13).
	ErrStaleBase = errors.New("path deploy base is no longer active")
	// ErrWithheld means content screening has withheld a file on every
	// site (A2.15), so it is not downloadable either (C1.14).
	ErrWithheld = errors.New("file withheld by content screening")
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
	// Quotas are the instance's per-owner limits (A2.14); Create checks
	// StorageBytes and DeploysPerDay, with the owner's own overrides in
	// their place when set.
	Quotas store.Quotas
	Now    func() time.Time
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
	// Withheld lists the hashes content screening withholds (A2.15), which
	// Open refuses; nil withholds none.
	Withheld Withheld
}

// Withheld reports whether a content hash is quarantined. The moderation
// Quarantine satisfies it.
type Withheld interface {
	Has(sha256 string) bool
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

// changeable returns the site unless the operator has taken it down, when
// deploys, activations and rollbacks are refused with ErrSiteUnavailable,
// and so is reading its files (Files, Open): they are kept for the
// operator, never handed out for rehosting.
func (s *Service) changeable(ctx context.Context, ownerID, name string) (store.Site, error) {
	site, err := s.site(ctx, ownerID, name)
	if err == nil && !site.TakenDownAt.IsZero() {
		return store.Site{}, ErrSiteUnavailable
	}
	return site, err
}

// Create records a pending deploy from m and returns it with the hashes the
// owner still has to upload, each once. A deploy past the owner's storage
// or daily deploy quota is ErrLimitExceeded. A hash counts as held only when
// this owner uploaded it and the blob is still stored, so the answer never
// reveals another owner's content.
//
// With m.Path, the deploy is the site's active deploy (its base) with only
// that prefix replaced by m.Files; limits and quotas apply to the merged
// deploy exactly as to a full one.
func (s *Service) Create(ctx context.Context, ownerID, siteName string, m Manifest) (store.Deploy, []string, error) {
	site, err := s.changeable(ctx, ownerID, siteName)
	if err != nil {
		return store.Deploy{}, nil, err
	}
	d := store.Deploy{ID: store.NewID(), SiteID: site.ID, Message: m.Message}
	if m.Path != "" {
		if err := s.merge(ctx, ownerID, site, &m, &d); err != nil {
			return store.Deploy{}, nil, err
		}
	}
	if err := s.c.Limits.validate(&m); err != nil {
		return store.Deploy{}, nil, err
	}
	if err := s.checkHeldSizes(ctx, ownerID, &m); err != nil {
		return store.Deploy{}, nil, err
	}
	d.FileCount = len(m.Files)
	d.SPA = m.SPA != nil && *m.SPA
	d.ImmutableAssets = m.ImmutableAssets != nil && *m.ImmutableAssets
	d.CreatedAt = s.now()
	for _, f := range m.Files {
		d.TotalSize += f.Size
	}
	if err := s.c.Deploys.Create(ctx, ownerID, d, m.Files, s.c.Quotas); err != nil {
		if qe := (*store.QuotaError)(nil); errors.As(err, &qe) {
			return store.Deploy{}, nil, quotaErr(qe)
		}
		return store.Deploy{}, nil, storeErr("create deploy", err)
	}
	missing, err := s.missing(ctx, ownerID, site.ID, d.ID)
	if err != nil {
		return store.Deploy{}, nil, err
	}
	return d, missing, nil
}

// merge turns a path deploy's manifest into the whole site's: the files of
// the site's active deploy, pinned as d's base, outside the prefix, as they
// are, and m's files under it. SPA and ImmutableAssets left unset follow
// the base. A site with nothing live takes m's files alone.
func (s *Service) merge(ctx context.Context, ownerID string, site store.Site, m *Manifest, d *store.Deploy) error {
	prefix, err := NormalisePrefix(m.Path)
	if err != nil {
		return err
	}
	m.Path, d.PathPrefix = prefix, prefix
	dir := prefix[1:]
	files := make([]store.DeployFile, 0, len(m.Files))
	for _, f := range m.Files {
		p, err := NormalisePath(f.Path)
		if err != nil {
			return err
		}
		f.Path = dir + p
		files = append(files, f)
	}
	if site.ActiveDeployID != "" {
		base, err := s.c.Deploys.Get(ctx, ownerID, site.ID, site.ActiveDeployID)
		if errors.Is(err, store.ErrNotFound) {
			// Replaced and collected since the site was read.
			return ErrStaleBase
		}
		if err != nil {
			return storeErr("find base deploy", err)
		}
		kept, err := s.c.Deploys.Manifest(ctx, ownerID, site.ID, base.ID)
		if err != nil {
			return storeErr("list base files", err)
		}
		for _, f := range kept {
			if !strings.HasPrefix(f.Path, dir) {
				files = append(files, f)
			}
		}
		d.BaseDeployID = base.ID
		if m.SPA == nil {
			m.SPA = &base.SPA
		}
		if m.ImmutableAssets == nil {
			m.ImmutableAssets = &base.ImmutableAssets
		}
	}
	m.Files = files
	return nil
}

// checkHeldSizes refuses a manifest that lists a hash the owner already
// holds with a size other than the stored blob's: the hash is not uploaded
// again, so nothing else would catch it, and the edge, limits and quotas
// all trust the listed size. Only the owner's own blobs are compared, so the
// answer says nothing about other owners'. A path deploy checks only the
// files under its prefix; the base's were checked when the base was created,
// and validate has made any base file sharing one of their hashes list the
// same size.
func (s *Service) checkHeldSizes(ctx context.Context, ownerID string, m *Manifest) error {
	dir := ""
	if m.Path != "" {
		dir = m.Path[1:]
	}
	var hashes []string
	seen := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		if strings.HasPrefix(f.Path, dir) && !seen[f.SHA256] {
			seen[f.SHA256] = true
			hashes = append(hashes, f.SHA256)
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	held, err := s.c.Deploys.HeldBlobSizes(ctx, ownerID, hashes)
	if err != nil {
		return storeErr("find held blobs", err)
	}
	for _, f := range m.Files {
		size, ok := held[f.SHA256]
		if ok && size != f.Size {
			return &TargetError{Err: ErrInvalidManifest, Target: f.Path, Message: fmt.Sprintf("%q is listed with size %d, but its sha256 matches a file you have already uploaded of size %d.", f.Path, f.Size, size)}
		}
	}
	return nil
}

// quotaErr explains a refused deploy to its owner.
func quotaErr(qe *store.QuotaError) error {
	te := &TargetError{Err: ErrLimitExceeded, Target: "files"}
	switch qe.Quota {
	case store.QuotaDeploysPerDay:
		te.Target = "deploys"
		deploys := "deploys"
		if qe.Limit == 1 {
			deploys = "deploy"
		}
		te.Message = fmt.Sprintf("You have created %d %s in the last 24 hours, the most this server allows you. Try again later or ask the operator for a higher limit.", qe.Limit, deploys)
	default:
		te.Message = fmt.Sprintf("This deploy adds %s of new files to the %s you already store; your storage limit is %s. Delete a site you no longer need or ask the operator for a higher limit.",
			FormatBytes(qe.Adding), FormatBytes(qe.Used), FormatBytes(qe.Limit))
	}
	return te
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
// already stored verifies it again without rewriting it. A taken-down site
// takes no uploads (ErrSiteUnavailable).
func (s *Service) Upload(ctx context.Context, ownerID, siteName, deployID, hash string, body io.Reader) error {
	site, err := s.changeable(ctx, ownerID, siteName)
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
// uploaded every blob, ErrNotPending once activated (see Rollback) and
// ErrStaleBase for a path deploy whose base the site no longer serves.
func (s *Service) Activate(ctx context.Context, ownerID, siteName, deployID string) (store.Site, store.Deploy, error) {
	site, err := s.changeable(ctx, ownerID, siteName)
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
	if d.PathPrefix != "" && d.BaseDeployID != site.ActiveDeployID {
		// Checked again in the activation transaction; this spares the
		// renditions.
		return store.Site{}, store.Deploy{}, ErrStaleBase
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
	site, err := s.changeable(ctx, ownerID, siteName)
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

// Files returns up to limit of one of the site's deploys' files, ordered
// by path, with paths after after ("" for the first page). Any deploy in
// the site's history can be read, but not on a taken-down site
// (ErrSiteUnavailable).
func (s *Service) Files(ctx context.Context, ownerID, siteName, deployID, after string, limit int) ([]store.DeployFile, error) {
	site, err := s.changeable(ctx, ownerID, siteName)
	if err != nil {
		return nil, err
	}
	files, err := s.c.Deploys.FilesPage(ctx, ownerID, site.ID, deployID, after, limit)
	return files, storeErr("list deploy files", err)
}

// Open opens a file body the deploy lists for download and returns it with
// its size. The owner must have uploaded the hash too: a pending deploy can
// list any hash, and content addressing must never hand one owner another's
// file. A hash the deploy does not list, one the owner never uploaded and a
// body garbage collection has removed are all ErrNotFound, like a missing
// site or deploy. A withheld file is ErrWithheld; a taken-down site,
// ErrSiteUnavailable.
func (s *Service) Open(ctx context.Context, ownerID, siteName, deployID, hash string) (io.ReadSeekCloser, int64, error) {
	site, err := s.changeable(ctx, ownerID, siteName)
	if err != nil {
		return nil, 0, err
	}
	if !ValidHash(hash) {
		return nil, 0, ErrNotFound
	}
	size, err := s.c.Deploys.HeldSize(ctx, ownerID, site.ID, deployID, hash)
	if err != nil {
		return nil, 0, storeErr("find held blob", err)
	}
	if s.c.Withheld != nil && s.c.Withheld.Has(hash) {
		return nil, 0, ErrWithheld
	}
	// A bucket reports a missing object only on the first read, so ask
	// first; a body removed between here and that read cuts the download
	// short, which the client's hash check catches.
	ok, err := s.c.Blobs.Has(ctx, hash)
	if err != nil {
		return nil, 0, fmt.Errorf("check blob: %w", err)
	}
	if !ok {
		return nil, 0, ErrNotFound
	}
	body, err := s.c.Blobs.Open(ctx, hash, size)
	if errors.Is(err, ErrBlobNotFound) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("open blob: %w", err)
	}
	return body, size, nil
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
		{store.ErrStaleBase, ErrStaleBase},
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
