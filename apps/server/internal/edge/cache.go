// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// served is a site with its active manifest, keyed by path.
type served struct {
	site  ServedSite
	files map[string]ServedFile
}

// cache holds the site and active manifest for each host already served.
// Only hits are cached, so a new site or domain is found on its first
// request; changes to a known site arrive through invalidate.
type cache struct {
	src SiteSource

	mu    sync.RWMutex
	gen   uint64 // bumped by every invalidation
	hosts map[string]*served
}

func newCache(src SiteSource) *cache {
	return &cache{src: src, hosts: map[string]*served{}}
}

// lookup returns the site served at host, loading it on a miss.
func (c *cache) lookup(ctx context.Context, host string) (*served, error) {
	c.mu.RLock()
	s, ok := c.hosts[host]
	gen := c.gen
	c.mu.RUnlock()
	if ok {
		return s, nil
	}

	site, err := c.src.SiteForHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("site for host: %w", err)
	}
	s = &served{site: site, files: map[string]ServedFile{}}
	if site.ActiveDeployID != "" {
		files, err := c.src.ActiveFiles(ctx, site)
		if err != nil {
			return nil, fmt.Errorf("active files: %w", err)
		}
		for _, f := range files {
			// Upload rejects these; never serve one that got through.
			if !strings.HasPrefix(f.Path, ReservedPrefix[1:]) {
				s.files[f.Path] = f
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// An invalidation while loading may make what was read stale: serve
	// it to this request, but do not keep it.
	if c.gen == gen {
		c.hosts[host] = s
	}
	return s, nil
}

// invalidate drops every host serving siteID.
func (c *cache) invalidate(siteID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for host, s := range c.hosts {
		if s.site.ID == siteID {
			delete(c.hosts, host)
		}
	}
}
