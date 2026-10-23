// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package country looks up the country of an IP address in a local
// IP-to-country database the operator downloads (V5.13): DB-IP's IP to
// Country Lite in MMDB format, or any MMDB with the same country.iso_code
// layout, such as MaxMind GeoLite2 Country. It reads only the country's
// ISO 3166-1 alpha-2 code, never a city or region, and sends nothing
// anywhere. The file is read into memory, checked, and reread when it
// changes, so the operator can replace it without a restart; a missing or
// bad file logs a warning and looks nothing up until a good one appears.
// It imports no HTTP packages: the access-log writer calls it with the IP
// as stored, off the request path.
package country

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Unknown is the country of an address the database has no country for.
const Unknown = "unknown"

const (
	// DefaultCheckInterval is how often Watch checks the file for a change.
	DefaultCheckInterval = time.Minute
	// MaxFileBytes bounds the file read into memory. A country database is
	// about 8 MB; even a city database fits well within this.
	MaxFileBytes = 512 << 20
)

// DB looks countries up in the database at its path. Lookups are safe
// from any goroutine and never touch the file.
type DB struct {
	path string
	log  *slog.Logger

	reader atomic.Pointer[maxminddb.Reader]

	// mu serialises Check; stamp is the file last checked (nil while it
	// is missing) and failed whether it was unusable.
	mu     sync.Mutex
	stamp  os.FileInfo
	failed bool
}

// Open loads the database at path. A missing or bad file is not an error:
// it logs a warning and the DB looks nothing up until Check finds a good
// file there.
func Open(path string, log *slog.Logger) *DB {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	d := &DB{path: path, log: log}
	d.Check()
	return d
}

// Loaded reports whether a database is loaded.
func (d *DB) Loaded() bool { return d.reader.Load() != nil }

// Lookup returns the ISO 3166-1 alpha-2 code, in upper case, of the
// country the database places ip in, or Unknown when it has no country for
// it. It returns "" when no database is loaded or ip is not an address,
// meaning record nothing.
func (d *DB) Lookup(ip string) string {
	r := d.reader.Load()
	if r == nil {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() && r.Metadata.IPVersion == 4 {
		return Unknown
	}
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	res := r.Lookup(addr)
	if err := res.Err(); err != nil {
		return ""
	}
	if !res.Found() {
		return Unknown
	}
	if err := res.Decode(&rec); err != nil {
		return ""
	}
	if !IsCode(rec.Country.ISOCode) {
		return Unknown
	}
	return rec.Country.ISOCode
}

// IsCode reports whether s is two upper-case letters A to Z, the shape of
// an ISO 3166-1 alpha-2 code.
func IsCode(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// Watch calls Check every interval (DefaultCheckInterval when zero or
// less) until ctx ends.
func (d *DB) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultCheckInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.Check()
		}
	}
}

// Check rereads the file when it has changed since the last check: a
// different file at the path (renamed into place), or a new size or
// modification time. A good file replaces the database in use; a missing
// or bad one stops lookups and logs a warning, once per change.
func (d *DB) Check() {
	d.mu.Lock()
	defer d.mu.Unlock()
	info, err := os.Stat(d.path)
	if err != nil {
		if d.stamp == nil && d.failed {
			return
		}
		d.stamp = nil
		d.fail(err)
		return
	}
	if d.stamp != nil && os.SameFile(d.stamp, info) && d.stamp.Size() == info.Size() && d.stamp.ModTime().Equal(info.ModTime()) {
		return
	}
	d.stamp = info
	r, err := load(d.path)
	if err != nil {
		d.fail(err)
		return
	}
	d.reader.Store(r)
	d.failed = false
	d.log.Info("country database loaded",
		slog.String("path", d.path),
		slog.String("type", r.Metadata.DatabaseType),
		slog.Time("built", r.Metadata.BuildTime().UTC()))
}

// fail stops lookups and warns. Serving carries on; entries are logged
// without a country until a good file appears.
func (d *DB) fail(err error) {
	d.reader.Store(nil)
	d.failed = true
	d.log.Warn("country database unavailable; recording no countries",
		slog.String("path", d.path), slog.Any("error", err))
}

var errTooLarge = fmt.Errorf("larger than %d bytes", MaxFileBytes)

// load reads and checks the database at path.
func load(path string) (*maxminddb.Reader, error) {
	f, err := os.Open(path) // #nosec G304 -- the operator names the file.
	if err != nil {
		return nil, fmt.Errorf("open country database: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read country database: %w", err)
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("read country database: %w", errTooLarge)
	}
	// In memory, a file replaced or truncated in place later can never
	// change what a lookup reads.
	r, err := maxminddb.OpenBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse country database: %w", err)
	}
	if v := r.Metadata.IPVersion; v != 4 && v != 6 {
		return nil, fmt.Errorf("parse country database: %w", errors.New("ip_version is neither 4 nor 6"))
	}
	if err := r.Verify(); err != nil {
		return nil, fmt.Errorf("verify country database: %w", err)
	}
	return r, nil
}
