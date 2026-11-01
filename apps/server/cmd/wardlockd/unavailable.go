// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
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

// certRemover deletes a custom domain's certificate; the TLS manager in
// automatic mode satisfies it.
type certRemover interface {
	RemoveCertificate(ctx context.Context, hostname string) error
}

// unavailableWatch polls the set of unavailable sites and notifies every
// site that joined or left it, so the edge drops its cached copy and serves
// the unavailable page, or the site again, within its interval. With certs
// set it also remembers each unavailable site's custom domains, and when
// `wardlockd admin teardown-site` (which only tears down a taken-down
// site) deletes one, removes their certificates here, where the TLS
// manager runs.
type unavailableWatch struct {
	log     *slog.Logger
	sites   store.Sites
	domains store.Domains
	notify  siteNotifier
	certs   certRemover // nil outside automatic TLS mode

	last []string
	// hostnames holds each unavailable site's custom domains, by site ID.
	hostnames map[string][]string
}

// startUnavailableWatch runs w every unavailableInterval until the returned
// stop is called, which waits for it; call it before the database closes.
func startUnavailableWatch(ctx context.Context, w *unavailableWatch) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { w.run(ctx, unavailableInterval) })
	return func() {
		cancel()
		wg.Wait()
	}
}

func (w *unavailableWatch) run(ctx context.Context, every time.Duration) {
	w.hostnames = map[string][]string{}
	// Sites are loaded from the database on first use, so the first read
	// is only the baseline.
	w.tick(ctx, false)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, true)
		}
	}
}

func (w *unavailableWatch) tick(ctx context.Context, notify bool) {
	now, err := w.sites.UnavailableIDs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("list unavailable sites", slog.Any("error", err))
		}
		return
	}
	for _, id := range changed(w.last, now) {
		if notify {
			w.notify.Notify(ctx, id)
		}
		if _, found := slices.BinarySearch(now, id); found {
			w.remember(ctx, id)
		} else {
			w.forget(ctx, id)
		}
	}
	w.last = now
}

// remember records an unavailable site's custom domains.
func (w *unavailableWatch) remember(ctx context.Context, siteID string) {
	if w.certs == nil {
		return
	}
	site, err := w.sites.GetByID(ctx, siteID)
	if err != nil {
		return // torn down already: nothing to remember
	}
	var hosts []string
	for after := ""; ; {
		page, err := w.domains.List(ctx, store.ActorFor(site.OwnerID), site.ID, after, 200)
		if err != nil {
			w.log.Error("list domains of an unavailable site", slog.String("site_id", siteID), slog.Any("error", err))
			return
		}
		for _, d := range page {
			hosts = append(hosts, d.Hostname)
		}
		if len(page) < 200 {
			break
		}
		after = page[len(page)-1].Hostname
	}
	w.hostnames[siteID] = hosts
}

// forget drops a site that is available again or gone; if it is gone, its
// custom domains went with it, and so do their certificates.
func (w *unavailableWatch) forget(ctx context.Context, siteID string) {
	hosts := w.hostnames[siteID]
	delete(w.hostnames, siteID)
	if w.certs == nil || len(hosts) == 0 {
		return
	}
	if _, err := w.sites.GetByID(ctx, siteID); !errors.Is(err, store.ErrNotFound) {
		return // restored or reinstated, not torn down
	}
	for _, h := range hosts {
		if err := w.certs.RemoveCertificate(ctx, h); err != nil {
			w.log.Error("remove certificate", slog.String("hostname", h), slog.Any("error", err))
			continue
		}
		w.log.Info("certificate removed for a torn-down site", slog.String("site_id", siteID), slog.String("hostname", h))
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
