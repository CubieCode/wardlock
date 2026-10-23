// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// slowAppend delays every batch before the real store writes it and its
// rollup counts.
type slowAppend struct {
	store.AccessLog
	delay time.Duration
}

func (s slowAppend) Append(ctx context.Context, entries []store.AccessEntry) error {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.AccessLog.Append(ctx, entries)
}

// TestSlowRollupDoesNotSlowServingAndMatchesTheLog serves through the real
// edge and writer into SQLite, with every batch (log rows and rollup
// counts together) taking a second: requests stay fast, and afterwards the
// rollup equals a recount of the logged entries.
func TestSlowRollupDoesNotSlowServingAndMatchesTheLog(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	// oneSite serves the site with this ID.
	if err := db.Sites().Create(ctx, store.Site{ID: "site-docs", OwnerID: owner.ID, Name: "docs", CreatedAt: time.Now()}, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	if err := accesslog.Backfill(ctx, db.Traffic(), time.Now, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	w := accesslog.Start(accesslog.Config{Repo: slowAppend{db.AccessLog(), time.Second}, FlushInterval: time.Millisecond})
	handler := newTestEdge(t, w)

	agents := []string{"curl/8", "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", ""}
	paths := []string{"/", "/missing", "/?t=wl_abcdefgh23_c2VjcmV0LXRva2Vu"}
	const n = 150
	began := time.Now()
	for i := range n {
		req := httptest.NewRequest(http.MethodGet, "http://docs.wardlock.localhost"+paths[i%len(paths)], nil)
		req.Header.Set("User-Agent", agents[i%len(agents)])
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	// Waiting on even one batch would take a second.
	if took := time.Since(began); took > 500*time.Millisecond {
		t.Fatalf("%d requests took %v against a slow store", n, took)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); err != nil || w.Dropped() != 0 {
		t.Fatalf("Close() = %v with %d dropped", err, w.Dropped())
	}

	logged, err := db.AccessLog().ListBySite(ctx, owner.ID, "site-docs", store.AccessCursor{}, store.MaxAccessListLimit)
	if err != nil || len(logged) != n {
		t.Fatalf("ListBySite() = %d entries, %v; want %d", len(logged), err, n)
	}
	var want store.TrafficCount
	browsers := map[store.Browser]store.TrafficCount{}
	pathCounts := map[string]store.TrafficCount{}
	for _, e := range logged {
		add := func(c store.TrafficCount) store.TrafficCount {
			if e.Status >= 400 {
				c.Refused++
			} else {
				c.Allowed++
			}
			return c
		}
		want = add(want)
		b := accesslog.Classify(e.UserAgent)
		browsers[b] = add(browsers[b])
		pathCounts[e.Path] = add(pathCounts[e.Path])
	}
	from := time.Now().Add(-2 * time.Hour)
	r, err := db.Traffic().Report(ctx, owner.ID, store.TrafficScope{}, from, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var got store.TrafficCount
	for _, h := range r.Hours {
		got.Allowed += h.Allowed
		got.Refused += h.Refused
	}
	if got != want || want.Refused == 0 {
		t.Fatalf("rollup total = %+v, log recount = %+v", got, want)
	}
	for _, b := range r.Browsers {
		if browsers[b.Browser] != b.TrafficCount {
			t.Errorf("browser %s = %+v, log recount = %+v", b.Browser, b.TrafficCount, browsers[b.Browser])
		}
	}
	if len(r.Browsers) != len(browsers) {
		t.Errorf("Browsers = %+v, recount %v", r.Browsers, browsers)
	}
	for _, p := range r.Paths {
		if pathCounts[p.Path] != p.TrafficCount {
			t.Errorf("path %s = %+v, log recount = %+v", p.Path, p.TrafficCount, pathCounts[p.Path])
		}
	}
	if len(r.Paths) != len(pathCounts) {
		t.Errorf("Paths = %+v, recount %v", r.Paths, pathCounts)
	}
}
