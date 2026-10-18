// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"sync"
	"time"
)

// pending is a bounded in-memory table of single-use entries that expire,
// keyed by a hash: OAuth flows and site codes. It holds at most max live
// entries, and at most perSite for any one site, so a flood aimed at one
// site cannot crowd out the others. Expired entries are dropped in expiry
// order from a queue, a few at a time on each call, so no call scans the
// table: every entry of one table has the same lifetime, so insertion
// order is expiry order.
type pending[V any] struct {
	max, perSite int

	mu      sync.Mutex
	entries map[string]pendingEntry[V]
	sites   map[string]int // live entries per site
	queue   []queued       // insertion (so expiry) order, from head
	head    int
}

type pendingEntry[V any] struct {
	value   V
	site    string
	expires time.Time
}

type queued struct {
	key     string
	expires time.Time
}

func newPending[V any](maxEntries, perSite int) *pending[V] {
	return &pending[V]{max: maxEntries, perSite: perSite, entries: map[string]pendingEntry[V]{}, sites: map[string]int{}}
}

// expire drops the entries that have expired by now, oldest first.
func (p *pending[V]) expire(now time.Time) {
	for p.head < len(p.queue) && !now.Before(p.queue[p.head].expires) {
		q := p.queue[p.head]
		p.queue[p.head] = queued{}
		p.head++
		// The key may have been taken already; only an entry with this
		// expiry is the one queued here.
		if e, ok := p.entries[q.key]; ok && e.expires.Equal(q.expires) {
			p.remove(q.key, e)
		}
	}
	// Reclaim the queue's dead prefix once it is most of the slice.
	if p.head > 1024 && p.head > len(p.queue)/2 {
		p.queue = append(p.queue[:0:0], p.queue[p.head:]...)
		p.head = 0
	}
}

func (p *pending[V]) remove(key string, e pendingEntry[V]) {
	delete(p.entries, key)
	if p.sites[e.site]--; p.sites[e.site] <= 0 {
		delete(p.sites, e.site)
	}
}

// put stores v under key for site until expires, unless the table or the
// site's share of it is full. Queued keys already taken still count
// against the queue's own bound (twice max), so it cannot grow without
// limit either.
func (p *pending[V]) put(key, site string, v V, expires, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire(now)
	if len(p.entries) >= p.max || p.sites[site] >= p.perSite || len(p.queue)-p.head >= 2*p.max {
		return false
	}
	if old, ok := p.entries[key]; ok {
		p.remove(key, old)
	}
	p.entries[key] = pendingEntry[V]{value: v, site: site, expires: expires}
	p.sites[site]++
	p.queue = append(p.queue, queued{key: key, expires: expires})
	return true
}

// take removes and returns the entry under key if it is there and live.
func (p *pending[V]) take(key string, now time.Time) (V, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire(now)
	e, ok := p.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	p.remove(key, e)
	if !now.Before(e.expires) {
		var zero V
		return zero, false
	}
	return e.value, true
}
