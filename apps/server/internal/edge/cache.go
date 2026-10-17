// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"context"
	"fmt"
	"sync"
)

// served is a site with its active manifest, keyed by path.
type served struct {
	site  ServedSite
	files map[string]ServedFile
}

// load is one in-flight lookup that concurrent misses on a host share.
type load struct {
	done chan struct{} // closed once s and err are set
	s    *served
	err  error
}

// cache holds the site and active manifest for each host already served.
// Only hits are cached, so a new site or domain is found on its first
// request; changes to a known site arrive through invalidate.
type cache struct {
	src SiteSource

	mu      sync.RWMutex
	gen     uint64 // bumped by every invalidation
	hosts   map[string]*served
	loading map[string]*load
}

func newCache(src SiteSource) *cache {
	return &cache{src: src, hosts: map[string]*served{}, loading: map[string]*load{}}
}

// lookup returns the site served at host, loading it on a miss. Concurrent
// misses on one host wait for a single load.
func (c *cache) lookup(ctx context.Context, host string) (*served, error) {
	c.mu.RLock()
	s, ok := c.hosts[host]
	c.mu.RUnlock()
	if ok {
		return s, nil
	}

	c.mu.Lock()
	if s, ok := c.hosts[host]; ok {
		c.mu.Unlock()
		return s, nil
	}
	if l, ok := c.loading[host]; ok {
		c.mu.Unlock()
		select {
		case <-l.done:
			return l.s, l.err
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for site load: %w", ctx.Err())
		}
	}
	l := &load{done: make(chan struct{})}
	c.loading[host] = l
	gen := c.gen
	c.mu.Unlock()

	// Waiters share this load, so it must not end with the first request.
	l.s, l.err = c.fetch(context.WithoutCancel(ctx), host)

	c.mu.Lock()
	delete(c.loading, host)
	// An invalidation while loading may make what was read stale: serve
	// it to the waiting requests, but do not keep it.
	if l.err == nil && c.gen == gen {
		c.hosts[host] = l.s
	}
	c.mu.Unlock()
	close(l.done)
	return l.s, l.err
}

func (c *cache) fetch(ctx context.Context, host string) (*served, error) {
	site, err := c.src.SiteForHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("site for host: %w", err)
	}
	s := &served{site: site, files: map[string]ServedFile{}}
	if site.ActiveDeployID == "" {
		return s, nil
	}
	files, err := c.src.ActiveFiles(ctx, site)
	if err != nil {
		return nil, fmt.Errorf("active files: %w", err)
	}
	for _, f := range files {
		// Upload rejects these in any case; never serve one that got through.
		if !reserved(f.Path) {
			s.files[f.Path] = f
		}
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
