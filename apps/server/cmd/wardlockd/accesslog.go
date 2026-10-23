// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/country"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

// startAccessLog backfills the traffic rollup the first time and starts
// the access-log writer, with the country lookup when WARDLOCK_COUNTRY_DB
// names a file (V5.13). The returned stop flushes what is buffered within
// platform.ShutdownTimeout; call it after the HTTP server has shut down and
// before the database closes.
func startAccessLog(ctx context.Context, cfg platform.Config, log *slog.Logger, db *store.DB) (accessRecorder, func(), error) {
	if err := accesslog.Backfill(ctx, db.Traffic(), time.Now, log); err != nil {
		return accessRecorder{}, nil, err
	}
	cfgLog := accesslog.Config{
		Repo: db.AccessLog(),
		Log:  log,
		Now:  time.Now,
		Truncation: accesslog.Truncation{
			IPv4Prefix: cfg.AccessLogIPv4Prefix,
			IPv6Prefix: cfg.AccessLogIPv6Prefix,
		},
		BufferSize:         cfg.AccessLogBuffer,
		Retention:          cfg.AccessLogRetention,
		Traffic:            db.Traffic(),
		AnalyticsRetention: cfg.AnalyticsRetention,
	}
	// The watcher stops with the writer; lookups after that find the
	// database still loaded.
	watchCtx, stopWatch := context.WithCancel(context.WithoutCancel(ctx))
	if countries := startCountries(watchCtx, cfg, log); countries != nil {
		cfgLog.Countries = countries
	}
	w := accesslog.Start(cfgLog)
	stop := func() {
		defer stopWatch()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), platform.ShutdownTimeout)
		defer cancel()
		if err := w.Close(ctx); err != nil {
			log.Error("flush access log", slog.Any("error", err))
		}
		if n := w.Dropped(); n > 0 {
			log.Warn("access log entries dropped since start", slog.Int64("dropped_total", n))
		}
	}
	return accessRecorder{w}, stop, nil
}

// startCountries opens the country database and rechecks the file every
// country.DefaultCheckInterval until ctx ends, or returns nil when the
// lookup is off. A missing or bad file only warns: serving goes on and
// entries are logged without a country until a good file appears.
func startCountries(ctx context.Context, cfg platform.Config, log *slog.Logger) *country.DB {
	if cfg.Country.DB == "" {
		return nil
	}
	db := country.Open(cfg.Country.DB, cfg.Country.Attribution == platform.DefaultCountryAttribution, log)
	go db.Watch(ctx, country.DefaultCheckInterval)
	return db
}

// countryLookup is what the API says about the lookup: nil while it is off.
func countryLookup(cfg platform.Config) *api.CountryLookupConfig {
	if cfg.Country.DB == "" {
		return nil
	}
	return &api.CountryLookupConfig{Attribution: cfg.Country.Attribution, ServerCountry: cfg.Country.ServerCountry}
}

// accessRecorder adapts the writer to the edge's AccessRecorder: the edge
// and access never import each other.
type accessRecorder struct{ w *accesslog.Writer }

func (a accessRecorder) Record(r edge.AccessRecord) {
	a.w.Record(accesslog.Entry{
		Time:         r.Time,
		SiteID:       r.SiteID,
		Host:         r.Host,
		Path:         r.Path,
		Status:       r.Status,
		Via:          accesslog.ParseVia(string(r.Via)),
		LinkPublicID: r.LinkPublicID,
		IP:           r.RemoteIP,
		UserAgent:    r.UserAgent,
	})
}
