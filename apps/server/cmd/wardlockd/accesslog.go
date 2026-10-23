// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

// startAccessLog backfills the traffic rollup the first time and starts
// the access-log writer. The returned stop flushes what is buffered within
// platform.ShutdownTimeout; call it after the HTTP server has shut down and
// before the database closes.
func startAccessLog(ctx context.Context, cfg platform.Config, log *slog.Logger, db *store.DB) (accessRecorder, func(), error) {
	if err := accesslog.Backfill(ctx, db.Traffic(), time.Now, log); err != nil {
		return accessRecorder{}, nil, err
	}
	w := accesslog.Start(accesslog.Config{
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
	})
	stop := func() {
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
