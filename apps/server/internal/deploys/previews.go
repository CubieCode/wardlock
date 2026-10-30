// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"wardlock.nz/server/internal/store"
)

// Preview deploys (C1.19): a deploy made with Manifest.Preview goes live at
// <site>--<name>.<base domain> instead of on the site. Deploying to the
// same name again replaces it; Promote puts its deploy live.

// Errors about previews.
var (
	// ErrInvalidPreview means a preview name breaks the rules (see
	// ValidPreviewName) or its hostname label would be too long.
	ErrInvalidPreview = errors.New("invalid preview name")
	// ErrInvalidPreviewExpiry means a preview lifetime is out of range or
	// given without a preview.
	ErrInvalidPreviewExpiry = errors.New("invalid preview expiry")
	// ErrPreviewNameTaken means a site, of any owner, holds the preview's
	// hostname label. Site names are public through DNS.
	ErrPreviewNameTaken = errors.New("preview hostname is a site's name")
)

// MaxPreviewName is the longest preview name.
const MaxPreviewName = 20

// PreviewSeparator joins a site name and a preview name in the preview's
// hostname label.
const PreviewSeparator = "--"

// ValidPreviewName reports whether name is 1 to MaxPreviewName characters
// of a-z, 0-9 and '-', not starting or ending with '-' and without "--",
// so that <site>--<name> splits at its last "--".
func ValidPreviewName(name string) bool {
	if name == "" || len(name) > MaxPreviewName || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for i := range len(name) {
		if c := name[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return !strings.Contains(name, PreviewSeparator)
}

// PreviewLabel is the hostname label a site's preview is served at.
func PreviewLabel(siteName, preview string) string { return siteName + PreviewSeparator + preview }

// checkPreview refuses a preview manifest whose name, label or lifetime
// breaks the rules, or whose label is a site's name.
func (s *Service) checkPreview(ctx context.Context, site store.Site, m *Manifest) error {
	if m.Preview == "" {
		if m.PreviewDays != nil {
			return &TargetError{Err: ErrInvalidPreviewExpiry, Target: "previewExpires", Message: "A preview lifetime needs a preview."}
		}
		return nil
	}
	if !ValidPreviewName(m.Preview) {
		return &TargetError{Err: ErrInvalidPreview, Target: "preview", Message: fmt.Sprintf(
			"Preview name %q must be 1 to %d characters of a-z, 0-9 and '-', not starting or ending with '-' and without '--'.", m.Preview, MaxPreviewName)}
	}
	label := PreviewLabel(site.Name, m.Preview)
	if len(label) > 63 {
		return &TargetError{Err: ErrInvalidPreview, Target: "preview", Message: fmt.Sprintf(
			"The preview's hostname label %q is %d characters; the limit is 63. Choose a shorter preview name.", label, len(label))}
	}
	if d := m.PreviewDays; d != nil && (*d < 0 || *d > store.MaxPreviewDays) {
		return &TargetError{Err: ErrInvalidPreviewExpiry, Target: "previewExpires", Message: fmt.Sprintf(
			"A preview lasts 1 to %d days, or never.", store.MaxPreviewDays)}
	}
	taken, err := s.c.Previews.SiteNameExists(ctx, label)
	if err != nil {
		return storeErr("check preview hostname", err)
	}
	if taken {
		return &TargetError{Err: ErrPreviewNameTaken, Target: "preview", Message: fmt.Sprintf(
			"The preview's hostname %s is a site's name. Choose another preview name.", label)}
	}
	return nil
}

// Preview returns one of the owner's site's previews that has not expired.
func (s *Service) Preview(ctx context.Context, ownerID, siteName, name string) (store.Site, store.Preview, error) {
	site, err := s.site(ctx, ownerID, siteName)
	if err != nil {
		return store.Site{}, store.Preview{}, err
	}
	p, err := s.c.Previews.Get(ctx, ownerID, site.ID, name, s.now())
	return site, p, storeErr("get preview", err)
}

// Previews returns the site and up to limit of its previews that have not
// expired, by name after afterName. A taken-down site's are listed too.
func (s *Service) Previews(ctx context.Context, ownerID, siteName, afterName string, limit int) (store.Site, []store.Preview, error) {
	site, err := s.site(ctx, ownerID, siteName)
	if err != nil {
		return store.Site{}, nil, err
	}
	ps, err := s.c.Previews.List(ctx, ownerID, site.ID, afterName, limit, s.now())
	if err != nil {
		return store.Site{}, nil, storeErr("list previews", err)
	}
	return site, ps, nil
}

// DeletePreview removes a preview, so its hostname stops serving at once;
// garbage collection removes its deploy.
func (s *Service) DeletePreview(ctx context.Context, ownerID, siteName, name string) error {
	site, err := s.changeable(ctx, ownerID, siteName)
	if err != nil {
		return err
	}
	if err := s.c.Previews.Delete(ctx, ownerID, site.ID, name, s.now()); err != nil {
		return storeErr("delete preview", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	return nil
}

// Promote activates a preview's deploy on the live site, an ordinary
// activation that enters the site's history, and keeps the preview. A path
// preview made on a live deploy the site no longer serves is
// ErrStaleBase: deploying the preview again makes it promotable.
func (s *Service) Promote(ctx context.Context, ownerID, siteName, name string) (store.Site, store.Deploy, store.Preview, error) {
	site, err := s.changeable(ctx, ownerID, siteName)
	if err != nil {
		return store.Site{}, store.Deploy{}, store.Preview{}, err
	}
	d, changed, err := s.c.Previews.Promote(ctx, ownerID, site.ID, name, s.now())
	if err != nil {
		return store.Site{}, store.Deploy{}, store.Preview{}, storeErr("promote preview", err)
	}
	p, err := s.c.Previews.Get(ctx, ownerID, site.ID, name, s.now())
	if err != nil {
		return store.Site{}, store.Deploy{}, store.Preview{}, storeErr("get preview", err)
	}
	if !changed {
		site.ActiveDeployID, site.ActivatedAt = d.ID, d.ActivatedAt
		return site, d, p, nil
	}
	return s.served(ctx, site, d), d, p, nil
}

// Deploy returns one of site's deploys, which ownerID's site must be.
func (s *Service) Deploy(ctx context.Context, ownerID string, site store.Site, deployID string) (store.Deploy, error) {
	d, err := s.c.Deploys.Get(ctx, ownerID, site.ID, deployID)
	return d, storeErr("get deploy", err)
}
