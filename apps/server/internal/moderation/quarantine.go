// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"context"
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
}

// NewQuarantine returns an empty set.
func NewQuarantine() *Quarantine { return &Quarantine{set: map[string]struct{}{}} }

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
}

// Replace makes the set exactly hashes.
func (q *Quarantine) Replace(hashes []string) {
	set := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		set[h] = struct{}{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.set = set
}

// QuarantineInterval is how often Watch reloads the set, so the operator's
// releases and a rescan's verdict reach a running server.
const QuarantineInterval = 5 * time.Second

// Watch loads the set from list now and every interval until ctx ends. A
// failed load keeps the previous set and is logged.
func (q *Quarantine) Watch(ctx context.Context, list func(context.Context) ([]string, error), interval time.Duration, log *slog.Logger) {
	load := func() {
		hs, err := list(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.LogAttrs(ctx, slog.LevelError, "load quarantined files", slog.String("error", err.Error()))
			}
			return
		}
		q.Replace(hs)
	}
	load()
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
