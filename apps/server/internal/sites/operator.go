// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"wardlock.nz/server/internal/store"
)

// Operator controls (A2.13): `wardlockd admin takedown-site`,
// `restore-site`, `teardown-site` and `list-sites`. They are instance-wide,
// address a site by name or by any hostname it answers on, and never back
// an owner API handler. Each is logged with IDs only.

// Find returns the site called ref, or served at the hostname ref: its
// base-domain host or an active custom domain.
func (s *Service) Find(ctx context.Context, ref string) (store.Site, error) {
	ref = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(ref)), ".")
	name := ref
	if label, ok := strings.CutSuffix(ref, "."+s.c.BaseDomain); ok {
		name = label
	}
	if ValidName(name) {
		site, err := s.c.Sites.GetByName(ctx, name)
		if !errors.Is(err, store.ErrNotFound) {
			return site, storeErr("get site", err)
		}
	}
	if h, err := NormaliseHostname(ref); err == nil {
		ref = h
	}
	d, err := s.c.Domains.GetActiveByHostname(ctx, ref)
	if err != nil {
		return store.Site{}, storeErr("get domain", err)
	}
	site, err := s.c.Sites.GetByID(ctx, d.SiteID)
	return site, storeErr("get site", err)
}

// TakeDown takes the site down: every host it answers on serves the
// unavailable page, and its deploys and files are kept, safe from garbage
// collection and from its owner, until it is restored or torn down. Taking
// down a taken-down site changes nothing.
func (s *Service) TakeDown(ctx context.Context, ref string) (store.Site, error) {
	site, err := s.Find(ctx, ref)
	if err != nil {
		return store.Site{}, err
	}
	if err := s.c.Sites.TakeDown(ctx, site.ID, s.now()); err != nil {
		return store.Site{}, storeErr("take down site", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	s.log().LogAttrs(ctx, slog.LevelInfo, "operator took down site", slog.String("site_id", site.ID), slog.String("owner_id", site.OwnerID))
	site, err = s.c.Sites.GetByID(ctx, site.ID)
	return site, storeErr("get site", err)
}

// Restore serves a taken-down site again, from the deploy it had.
func (s *Service) Restore(ctx context.Context, ref string) (store.Site, error) {
	site, err := s.Find(ctx, ref)
	if err != nil {
		return store.Site{}, err
	}
	if err := s.c.Sites.Restore(ctx, site.ID); err != nil {
		return store.Site{}, storeErr("restore site", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	s.log().LogAttrs(ctx, slog.LevelInfo, "operator restored site", slog.String("site_id", site.ID), slog.String("owner_id", site.OwnerID))
	site, err = s.c.Sites.GetByID(ctx, site.ID)
	return site, storeErr("get site", err)
}

// TearDown deletes the site as its owner's Delete would, whatever its
// state: its domains and their certificates, deploys, policies and links
// go, and garbage collection later removes files nothing else uses. Only
// a taken-down site may be torn down, so a mistyped name cannot delete a
// live site.
func (s *Service) TearDown(ctx context.Context, ref string) (store.Site, error) {
	site, err := s.Find(ctx, ref)
	if err != nil {
		return store.Site{}, err
	}
	if site.TakenDownAt.IsZero() {
		return store.Site{}, ErrNotTakenDown
	}
	hostnames, err := s.hostnames(ctx, site)
	if err != nil {
		return store.Site{}, err
	}
	if err := s.c.Sites.TearDown(ctx, site.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Restored (or torn down) since it was read.
			return store.Site{}, ErrNotTakenDown
		}
		return store.Site{}, storeErr("tear down site", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	for _, h := range hostnames {
		s.removeCert(ctx, h)
	}
	s.log().LogAttrs(ctx, slog.LevelInfo, "operator tore down site", slog.String("site_id", site.ID), slog.String("owner_id", site.OwnerID))
	return site, nil
}

// ErrNotTakenDown means TearDown was asked for a site that is still up.
var ErrNotTakenDown = errors.New("site is not taken down")

// ListAll calls fn for every site on the instance, ordered by name.
func (s *Service) ListAll(ctx context.Context, fn func(store.OperatorSite) error) error {
	for after := ""; ; {
		page, err := s.c.Sites.ListAll(ctx, after, checkPage)
		if err != nil {
			return fmt.Errorf("list sites: %w", err)
		}
		for _, site := range page {
			if err := fn(site); err != nil {
				return err
			}
		}
		if len(page) < checkPage {
			return nil
		}
		after = page[len(page)-1].Name
	}
}

// hostnames lists the site's custom domains when certificates must go
// with them.
func (s *Service) hostnames(ctx context.Context, site store.Site) ([]string, error) {
	var hostnames []string
	if s.c.Certs == nil {
		return nil, nil
	}
	for after := ""; ; {
		ds, err := s.c.Domains.List(ctx, site.OwnerID, site.ID, after, checkPage)
		if err != nil {
			return nil, fmt.Errorf("list domains: %w", err)
		}
		for _, d := range ds {
			hostnames = append(hostnames, d.Hostname)
		}
		if len(ds) < checkPage {
			return hostnames, nil
		}
		after = ds[len(ds)-1].Hostname
	}
}
