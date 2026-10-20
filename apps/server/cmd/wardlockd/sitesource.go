// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"

	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/store"
)

// siteSource adapts the store's instance-wide routing reads to
// edge.SiteSource, so the edge never imports store.
type siteSource struct {
	baseDomain string
	owners     store.Owners
	sites      store.Sites
	domains    store.Domains
	deploys    store.Deploys
	renditions store.Renditions
	// moderation is set when content screening is on (A2.15): sites of
	// owners who are not exempt are Screened, with each file's state.
	moderation store.Moderation
}

func (s siteSource) SiteForHost(ctx context.Context, host string) (edge.ServedSite, error) {
	site, err := s.find(ctx, host)
	if errors.Is(err, store.ErrNotFound) {
		return edge.ServedSite{}, edge.ErrNoSite
	}
	if err != nil {
		return edge.ServedSite{}, err
	}
	served := edge.ServedSite{
		ID:             site.ID,
		Name:           site.Name,
		ActiveDeployID: site.ActiveDeployID,
		ActivatedAt:    site.ActivatedAt,
		Unavailable:    !site.TakenDownAt.IsZero(),
	}
	if !served.Unavailable {
		// A suspended owner's sites are unavailable too (A2.13).
		o, err := s.owners.Get(ctx, site.OwnerID)
		if err != nil {
			return edge.ServedSite{}, fmt.Errorf("get owner: %w", err)
		}
		served.Unavailable = !o.SuspendedAt.IsZero()
	}
	if served.Unavailable || site.ActiveDeployID == "" {
		return served, nil
	}
	// The owner comes from the site row: this is routing, not an owner request.
	d, err := s.deploys.Get(ctx, site.OwnerID, site.ID, site.ActiveDeployID)
	if err != nil {
		return edge.ServedSite{}, fmt.Errorf("get active deploy: %w", err)
	}
	served.SPA = d.SPA
	served.ImmutableAssets = d.ImmutableAssets
	if s.moderation != nil {
		exempt, err := s.moderation.Exempt(ctx, site.OwnerID)
		if err != nil {
			return edge.ServedSite{}, fmt.Errorf("get screening exemption: %w", err)
		}
		served.Screened = !exempt
	}
	return served, nil
}

// find resolves a base-domain label by site name and any other host as an
// active custom domain.
func (s siteSource) find(ctx context.Context, host string) (store.Site, error) {
	if name, ok := edge.SiteLabel(host, s.baseDomain); ok {
		return s.sites.GetByName(ctx, name)
	}
	d, err := s.domains.GetActiveByHostname(ctx, host)
	if err != nil {
		return store.Site{}, err
	}
	return s.sites.GetByID(ctx, d.SiteID)
}

func (s siteSource) ActiveFiles(ctx context.Context, site edge.ServedSite) ([]edge.ServedFile, error) {
	files, err := s.deploys.Files(ctx, site.ActiveDeployID)
	if err != nil {
		return nil, err
	}
	rs, err := s.renditions.List(ctx, site.ActiveDeployID)
	if err != nil {
		return nil, err
	}
	byPath := map[string]map[string]edge.Rendition{}
	for _, r := range rs {
		if byPath[r.Path] == nil {
			byPath[r.Path] = map[string]edge.Rendition{}
		}
		byPath[r.Path][r.Format] = edge.Rendition{SHA256: r.SHA256, Size: r.Size}
	}
	screening := map[string]store.ModerationState{}
	if site.Screened && s.moderation != nil {
		states, err := s.moderation.DeployScreening(ctx, site.ActiveDeployID)
		if err != nil {
			return nil, err
		}
		for _, st := range states {
			screening[st.SHA256] = st
		}
	}
	out := make([]edge.ServedFile, len(files))
	for i, f := range files {
		out[i] = edge.ServedFile{Path: f.Path, SHA256: f.SHA256, Size: f.Size, Renditions: byPath[f.Path]}
		if site.Screened {
			st, ok := screening[f.SHA256]
			out[i].Pending = !ok || st.Status == store.ModerationPending
			out[i].Decoded = st.Decoded
		}
	}
	return out, nil
}
