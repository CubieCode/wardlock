// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package accesslog_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

func TestClassify(t *testing.T) {
	for _, tt := range []struct {
		name, ua string
		want     store.Browser
	}{
		{"Chrome on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", store.BrowserChrome},
		{"Chrome on Android", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.6668.81 Mobile Safari/537.36", store.BrowserChrome},
		{"Chrome on iOS", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/129.0.6668.69 Mobile/15E148 Safari/604.1", store.BrowserChrome},
		{"Brave, which sends Chrome's", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", store.BrowserChrome},
		{"Safari on macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15", store.BrowserSafari},
		{"Safari on iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1", store.BrowserSafari},
		{"Firefox on Linux", "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", store.BrowserFirefox},
		{"Firefox on iOS", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/131.0 Mobile/15E148 Safari/605.1.15", store.BrowserFirefox},
		{"Edge on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0", store.BrowserEdge},
		{"Edge on Android", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36 EdgA/129.0.0.0", store.BrowserEdge},
		{"Edge on iOS", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 EdgiOS/129.0.0.0 Mobile/15E148 Safari/605.1.15", store.BrowserEdge},
		{"legacy Edge", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.102 Safari/537.36 Edge/18.19041", store.BrowserEdge},
		{"Opera", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 OPR/114.0.0.0", store.BrowserOther},
		{"Samsung Internet", "Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36", store.BrowserOther},
		{"an app's web view", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148", store.BrowserOther},
		{"unknown", "SomethingElse/1.0", store.BrowserOther},
		{"curl", "curl/8.10.1", store.BrowserTool},
		{"wget", "Wget/1.24.5", store.BrowserTool},
		{"Python requests", "python-requests/2.32.3", store.BrowserTool},
		{"Go", "Go-http-client/2.0", store.BrowserTool},
		{"Node fetch", "node-fetch/1.0 (+https://github.com/bitinn/node-fetch)", store.BrowserTool},
		{"Googlebot claiming Chrome", "Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.6668.89 Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", store.BrowserTool},
		{"Bingbot", "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)", store.BrowserTool},
		{"an AI fetcher", "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ClaudeBot/1.0; +claudebot@anthropic.com", store.BrowserTool},
		{"a link preview", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)", store.BrowserTool},
		{"headless Chrome", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/129.0.0.0 Safari/537.36", store.BrowserTool},
		{"the Wardlock CLI's HTTP library", "Go-http-client/1.1", store.BrowserTool},
		{"no User-Agent", "", store.BrowserTool},
		{"blank User-Agent", "   ", store.BrowserTool},
		{"case does not matter", "CURL/8", store.BrowserTool},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := accesslog.Classify(tt.ua); got != tt.want {
				t.Fatalf("Classify(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}

// TestBatchesCarryBrowsers checks the writer classifies each entry's
// User-Agent, as stored, before the batch reaches the store.
func TestBatchesCarryBrowsers(t *testing.T) {
	repo := &fakeRepo{}
	w := accesslog.Start(accesslog.Config{Repo: repo, FlushInterval: time.Hour})
	for _, ua := range []string{"curl/8", "Mozilla/5.0 Firefox/131.0", ""} {
		e := entry("/")
		e.UserAgent = ua
		w.Record(e)
	}
	closeWriter(t, w)
	var got []store.Browser
	for _, e := range repo.entries() {
		got = append(got, e.Browser)
	}
	if want := []store.Browser{store.BrowserTool, store.BrowserFirefox, store.BrowserTool}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("browsers = %v, want %v", got, want)
	}
}

// fakeTraffic counts PruneHour calls and pretends hours are expired.
type fakeTraffic struct {
	mu      sync.Mutex
	cutoffs []time.Time
	hours   int
}

func (f *fakeTraffic) PruneHour(_ context.Context, cutoff time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	if f.hours == 0 {
		return false, nil
	}
	f.hours--
	return true, nil
}

func (f *fakeTraffic) calls() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.cutoffs...)
}

func TestAnalyticsRetention(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	t.Run("prunes hour by hour, independent of the log", func(t *testing.T) {
		repo, traffic := &fakeRepo{}, &fakeTraffic{hours: 3}
		start(t, accesslog.Config{
			Repo: repo, Now: func() time.Time { return now },
			Traffic: traffic, AnalyticsRetention: 400 * 24 * time.Hour, PruneInterval: time.Hour,
		})
		// Three hours, then the call that finds none left.
		waitFor(t, func() bool { return len(traffic.calls()) >= 4 })
		for _, cutoff := range traffic.calls() {
			if want := now.Add(-400 * 24 * time.Hour); !cutoff.Equal(want) {
				t.Fatalf("pruned before %v, want %v", cutoff, want)
			}
		}
		if n := len(repo.pruned()); n != 0 {
			t.Fatalf("pruned the log %d times with no log retention", n)
		}
	})
	t.Run("zero keeps every hour", func(t *testing.T) {
		traffic := &fakeTraffic{hours: 3}
		w := accesslog.Start(accesslog.Config{Repo: &fakeRepo{}, Traffic: traffic, Retention: time.Hour, PruneInterval: time.Millisecond})
		time.Sleep(20 * time.Millisecond) // proving an absence
		closeWriter(t, w)
		if n := len(traffic.calls()); n != 0 {
			t.Fatalf("pruned the rollup %d times with retention 0", n)
		}
	})
}

// TestBackfillIsIdempotent backfills a real store twice: the first run
// counts the log that was there and logs it, the second changes nothing,
// and entries written after count once.
func TestBackfillIsIdempotent(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(ctx, store.ActorFor(site.OwnerID), site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := func(uas ...string) {
		t.Helper()
		w := accesslog.Start(accesslog.Config{Repo: db.AccessLog(), FlushInterval: time.Hour})
		for _, ua := range uas {
			e := entry("/")
			e.SiteID, e.Time, e.UserAgent = site.ID, now, ua
			w.Record(e)
		}
		closeWriter(t, w)
	}
	// Written before the rollup existed: logged only.
	record("curl/8", "Mozilla/5.0 Firefox/131.0")

	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	for range 2 {
		if err := accesslog.Backfill(ctx, db.Traffic(), time.Now, log); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(logs.String(), "backfilled traffic rollup"); n != 1 || !strings.Contains(logs.String(), "entries=2") {
		t.Fatalf("logs = %q, want one backfill of 2 entries", logs.String())
	}
	record("Mozilla/5.0 Firefox/131.0")
	r, err := db.Traffic().Report(ctx, store.ActorFor(owner.ID), owner.ID, store.TrafficScope{}, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []store.TrafficBrowser{
		{Browser: store.BrowserFirefox, TrafficCount: store.TrafficCount{Allowed: 2}},
		{Browser: store.BrowserTool, TrafficCount: store.TrafficCount{Allowed: 1}},
	}
	if fmt.Sprint(r.Browsers) != fmt.Sprint(want) {
		t.Fatalf("Browsers = %+v, want %+v", r.Browsers, want)
	}
}

// TestRollupHoldsNoSecrets writes entries carrying a token in the path and
// User-Agent through the real store: the rollup keeps the redacted path,
// the link's public ID and a browser family, never the token, the IP or
// the User-Agent.
func TestRollupHoldsNoSecrets(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(ctx, store.ActorFor(site.OwnerID), site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	if err := accesslog.Backfill(ctx, db.Traffic(), time.Now, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	w := accesslog.Start(accesslog.Config{Repo: db.AccessLog(), FlushInterval: time.Hour})
	e := entry("/guide/" + token + "?t=" + token)
	e.SiteID, e.Time, e.Via, e.LinkPublicID, e.UserAgent = site.ID, now, store.ViaLink, "abcdefgh23", "curl/8 "+token
	w.Record(e)
	closeWriter(t, w)
	r, err := db.Traffic().Report(ctx, store.ActorFor(owner.ID), owner.ID, store.TrafficScope{}, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if dump := fmt.Sprintf("%+v", r); strings.Contains(dump, secret) || strings.Contains(dump, "192.0.2") || strings.Contains(dump, "curl") {
		t.Fatalf("rollup holds a secret, IP or User-Agent: %s", dump)
	}
	if len(r.Paths) != 1 || r.Paths[0].Path != "/guide/wl_abcdefgh23_[redacted]" || len(r.Links) != 1 || r.Links[0].PublicID != "abcdefgh23" {
		t.Fatalf("Paths = %+v, Links = %+v", r.Paths, r.Links)
	}
}
