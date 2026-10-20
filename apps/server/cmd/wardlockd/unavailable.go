// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"wardlock.nz/server/internal/store"
)

// unavailableInterval is how often serve looks for sites an operator
// command (run in another process) has taken down or restored, or whose
// owner it suspended or reinstated.
const unavailableInterval = 2 * time.Second

// siteNotifier is the part of deploys.Notifier the watch needs.
type siteNotifier interface {
	Notify(ctx context.Context, siteID string)
}

// startUnavailableWatch polls the set of unavailable sites and notifies
// every site that joined or left it, so the edge drops its cached copy and
// serves the unavailable page, or the site again, within
// unavailableInterval. The returned stop waits for it; call it before the
// database closes.
func startUnavailableWatch(ctx context.Context, log *slog.Logger, sites store.Sites, n siteNotifier) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { watchUnavailable(ctx, log, sites, n, unavailableInterval) })
	return func() {
		cancel()
		wg.Wait()
	}
}

func watchUnavailable(ctx context.Context, log *slog.Logger, sites store.Sites, n siteNotifier, every time.Duration) {
	// Sites are loaded from the database on first use, so the first read
	// is only the baseline.
	last, err := sites.UnavailableIDs(ctx)
	if err != nil && ctx.Err() == nil {
		log.Error("list unavailable sites", slog.Any("error", err))
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now, err := sites.UnavailableIDs(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("list unavailable sites", slog.Any("error", err))
			}
			continue
		}
		for _, id := range changed(last, now) {
			n.Notify(ctx, id)
		}
		last = now
	}
}

// changed returns the IDs in exactly one of two sorted lists.
func changed(a, b []string) []string {
	var out []string
	for _, id := range a {
		if _, found := slices.BinarySearch(b, id); !found {
			out = append(out, id)
		}
	}
	for _, id := range b {
		if _, found := slices.BinarySearch(a, id); !found {
			out = append(out, id)
		}
	}
	return out
}
