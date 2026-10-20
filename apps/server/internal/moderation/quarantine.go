// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Quarantine is the set of withheld content hashes the edge consults on
// every request. It is held in memory so serving never waits on the
// database: Watch reloads it from the store, and the worker adds a hash the
// moment it quarantines one.
type Quarantine struct {
	mu  sync.RWMutex
	set map[string]struct{}
	// gen counts loads begun; added holds each hash add put in the set
	// with the gen current then, so a load that began before the add
	// keeps it (the list it read may predate the verdict).
	gen   uint64
	added map[string]uint64
}

// NewQuarantine returns an empty set.
func NewQuarantine() *Quarantine {
	return &Quarantine{set: map[string]struct{}{}, added: map[string]uint64{}}
}

// Has reports whether sha256 is withheld.
func (q *Quarantine) Has(sha256 string) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	_, ok := q.set[sha256]
	return ok
}

// Len counts the withheld hashes.
func (q *Quarantine) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.set)
}

func (q *Quarantine) add(sha256 string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.set[sha256] = struct{}{}
	q.added[sha256] = q.gen
}

// begin starts a load and returns its generation, for replace.
func (q *Quarantine) begin() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.gen++
	return q.gen
}

// replace makes the set hashes, read by the load begun at gen, plus every
// hash added since that load began, which hashes may not show yet.
func (q *Quarantine) replace(gen uint64, hashes []string) {
	set := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		set[h] = struct{}{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for h, g := range q.added {
		if g >= gen {
			set[h] = struct{}{}
		} else {
			// Every load from now on began after the add was stored.
			delete(q.added, h)
		}
	}
	q.set = set
}

// Load makes the set what list returns, keeping hashes added while it ran.
// serve calls it before listening, so a restarted server withholds every
// quarantined file from its first request.
func (q *Quarantine) Load(ctx context.Context, list func(context.Context) ([]string, error)) error {
	gen := q.begin()
	hs, err := list(ctx)
	if err != nil {
		return fmt.Errorf("load quarantined files: %w", err)
	}
	q.replace(gen, hs)
	return nil
}

// QuarantineInterval is how often Watch reloads the set, so the operator's
// releases and a rescan's verdict reach a running server.
const QuarantineInterval = 5 * time.Second

// Watch reloads the set from list every interval until ctx ends; Load
// first. A failed reload keeps the previous set and is logged.
func (q *Quarantine) Watch(ctx context.Context, list func(context.Context) ([]string, error), interval time.Duration, log *slog.Logger) {
	load := func() {
		if err := q.Load(ctx, list); err != nil && ctx.Err() == nil {
			log.LogAttrs(ctx, slog.LevelError, "reload quarantined files", slog.String("error", err.Error()))
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			load()
		}
	}
}
