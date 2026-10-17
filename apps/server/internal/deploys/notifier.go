// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"sync"
)

// Notifier carries cache invalidations from the API to the edge.
type Notifier interface {
	// Notify tells subscribers that siteID's served state changed
	// (activation, rollback, teardown, site deletion).
	Notify(ctx context.Context, siteID string)
	// Subscribe registers fn; the returned func unsubscribes. fn must not
	// block.
	Subscribe(fn func(siteID string)) (unsubscribe func())
}

// LocalNotifier delivers notifications in process, synchronously, to every
// current subscriber.
type LocalNotifier struct {
	mu   sync.RWMutex
	next int
	subs map[int]func(siteID string)
}

var _ Notifier = (*LocalNotifier)(nil)

// NewLocalNotifier returns a Notifier for a single server process.
func NewLocalNotifier() *LocalNotifier {
	return &LocalNotifier{subs: map[int]func(string){}}
}

// Notify calls every subscriber with siteID before returning. Subscribers
// run without the lock held, so they may subscribe or unsubscribe.
func (n *LocalNotifier) Notify(_ context.Context, siteID string) {
	n.mu.RLock()
	subs := make([]func(string), 0, len(n.subs))
	for _, fn := range n.subs {
		subs = append(subs, fn)
	}
	n.mu.RUnlock()
	for _, fn := range subs {
		fn(siteID)
	}
}

// Subscribe registers fn until the returned func is called.
func (n *LocalNotifier) Subscribe(fn func(siteID string)) func() {
	n.mu.Lock()
	defer n.mu.Unlock()
	id := n.next
	n.next++
	n.subs[id] = fn
	return sync.OnceFunc(func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		delete(n.subs, id)
	})
}
