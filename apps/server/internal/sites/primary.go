// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"wardlock.nz/server/internal/store"
)

// ErrDomainNotActive means the hostname is one of the site's custom domains
// but is pending or lapsed, so it is not served and cannot be the primary
// (C1.18).
var ErrDomainNotActive = errors.New("custom domain not active")

// PrimaryHost returns the hostname site redirects its other hostnames to
// (C1.18), or "" when it has none in effect: the base-domain hostname
// (<name>.<base domain>) when the owner chose it, or the chosen custom
// domain only while it is active. A lapsed primary is suspended rather
// than cleared, so it resumes when the domain passes its checks again; it
// never names a hostname that is not served.
func PrimaryHost(site store.Site, baseDomain string) string {
	switch {
	case site.PrimaryBase:
		return site.Name + "." + baseDomain
	case site.PrimaryDomain != "" && site.PrimaryDomainActive:
		return site.PrimaryDomain
	}
	return ""
}

// PrimaryHost returns site's primary hostname in effect, or "".
func (s *Service) PrimaryHost(site store.Site) string { return PrimaryHost(site, s.c.BaseDomain) }

// SetPrimaryHost makes host, the site's base-domain hostname or one of its
// active custom domains in any spelling, the primary of the actor's site
// and returns the site as it is now. Any other hostname, including one of
// another site or another owner, is ErrNotFound like a missing one; a
// pending or lapsed custom domain of the site is ErrDomainNotActive. A
// taken-down site's hostnames are frozen (ErrUnavailable).
func (s *Service) SetPrimaryHost(ctx context.Context, actor store.Actor, siteName, host string) (store.Site, error) {
	site, err := s.editable(ctx, actor, siteName)
	if err != nil {
		return store.Site{}, err
	}
	if !site.TakenDownAt.IsZero() {
		return store.Site{}, ErrUnavailable
	}
	h, err := NormaliseHostname(host)
	if err != nil {
		// Not a hostname at all, so not one of the site's.
		return store.Site{}, fmt.Errorf("set primary hostname: %w", ErrNotFound)
	}
	if h == s.Hostname(site.Name) {
		err = s.c.Sites.SetPrimaryBase(ctx, actor, site.ID)
	} else {
		var d store.Domain
		d, err = s.c.Domains.Get(ctx, actor, site.ID, h)
		if err != nil {
			return store.Site{}, storeErr("get domain", err)
		}
		if d.Status != store.DomainActive {
			return store.Site{}, ErrDomainNotActive
		}
		// A domain that lapsed or was removed since is ErrNotFound here.
		err = s.c.Sites.SetPrimaryDomain(ctx, actor, site.ID, d.ID)
	}
	if err != nil {
		return store.Site{}, storeErr("set primary hostname", err)
	}
	return s.primaryChanged(ctx, actor, site)
}

// ClearPrimaryHost leaves the actor's site with no primary, so every
// hostname serves it again, and returns the site as it is now. Clearing a
// site with none changes nothing. A taken-down site is ErrUnavailable.
func (s *Service) ClearPrimaryHost(ctx context.Context, actor store.Actor, siteName string) (store.Site, error) {
	site, err := s.editable(ctx, actor, siteName)
	if err != nil {
		return store.Site{}, err
	}
	if !site.TakenDownAt.IsZero() {
		return store.Site{}, ErrUnavailable
	}
	if err := s.c.Sites.ClearPrimary(ctx, actor, site.ID); err != nil {
		return store.Site{}, storeErr("clear primary hostname", err)
	}
	return s.primaryChanged(ctx, actor, site)
}

// primaryChanged tells the edge, so the redirect starts or stops at once,
// and rereads the site.
func (s *Service) primaryChanged(ctx context.Context, actor store.Actor, site store.Site) (store.Site, error) {
	s.c.Notifier.Notify(ctx, site.ID)
	now, err := s.c.Sites.Get(ctx, actor, site.ID)
	if err != nil {
		return store.Site{}, storeErr("get site", err)
	}
	s.log().LogAttrs(ctx, slog.LevelInfo, "primary hostname changed",
		slog.String("site_id", site.ID), slog.String("primary", s.PrimaryHost(now)))
	return now, nil
}
