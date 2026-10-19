// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

// certExpiries is the part of the certificate manager readiness and
// metrics read; nil outside automatic TLS mode.
type certExpiries interface {
	Expiries() map[string]time.Time
}

// newReadiness checks the database, the blob directory and, in automatic
// TLS mode, that every managed name has an unexpired certificate.
func newReadiness(cfg platform.Config, log *slog.Logger, db *store.DB, certs certExpiries) *platform.Readiness {
	checks := []platform.Check{
		{Name: "database", Run: db.Check},
		{Name: "blobs", Run: platform.DirWritable(cfg.BlobDir)},
	}
	if certs != nil {
		checks = append(checks, platform.Check{Name: "certificates", Run: func(context.Context) error {
			return certsReady(certs.Expiries(), time.Now())
		}})
	}
	return platform.NewReadiness(log, checks...)
}

func certsReady(expiries map[string]time.Time, now time.Time) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(expiries)) {
		switch at := expiries[name]; {
		case at.IsZero():
			errs = append(errs, fmt.Errorf("no certificate yet for %s", name))
		case !at.After(now):
			errs = append(errs, fmt.Errorf("certificate for %s expired at %s", name, at.UTC().Format(time.RFC3339)))
		}
	}
	return errors.Join(errs...)
}

// collectServerMetrics adds the families only serve knows about: dropped
// access-log entries, the database's size on disk and certificate expiry.
func collectServerMetrics(m *platform.Metrics, cfg platform.Config, w *accesslog.Writer, certs certExpiries) {
	m.Collect(func(_ context.Context, e *platform.Exposition) {
		e.Counter("wardlock_access_log_dropped_total", "Access-log entries dropped because the database fell behind or a write failed.",
			platform.Sample{Value: float64(w.Dropped())})
		if size, err := databaseSize(cfg.DataDir); err == nil {
			e.Gauge("wardlock_database_size_bytes", "Size of the SQLite database and its write-ahead log on disk.", platform.Sample{Value: float64(size)})
		}
		if certs == nil {
			return
		}
		var samples []platform.Sample
		expiries := certs.Expiries()
		for _, name := range slices.Sorted(maps.Keys(expiries)) {
			if at := expiries[name]; !at.IsZero() {
				samples = append(samples, platform.Sample{Labels: []string{"name", name}, Value: float64(at.Unix())})
			}
		}
		e.Gauge("wardlock_certificate_expiry_timestamp_seconds", "When each managed certificate expires, in Unix seconds.", samples...)
	})
}

func databaseSize(dataDir string) (int64, error) {
	var size int64
	for _, suffix := range []string{"", "-wal"} {
		info, err := os.Stat(filepath.Join(dataDir, "wardlock.db"+suffix))
		switch {
		case err == nil:
			size += info.Size()
		case !errors.Is(err, fs.ErrNotExist):
			return 0, err
		}
	}
	return size, nil
}
