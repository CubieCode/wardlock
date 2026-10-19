// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"os"
	"path/filepath"
	"time"
)

// DevSink is the dev:// mailer. It writes each message to a directory as an
// .eml file and logs only the recipient, subject and file. The body, which
// carries codes, never reaches the log. Because the recipient does,
// wardlockd warns at startup when it is used outside local mode.
type DevSink struct {
	dir  string
	from netmail.Address
	log  *slog.Logger
	now  func() time.Time
}

// NewDevSink returns a sink writing to cfg.DevDir, or DefaultDevDir.
func NewDevSink(cfg Config) *DevSink {
	dir := cfg.DevDir
	if dir == "" {
		dir = DefaultDevDir
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &DevSink{dir: dir, from: cfg.SMTP.From, log: log, now: now}
}

// Send writes msg to a new file named after the time and a random suffix.
func (d *DevSink) Send(ctx context.Context, msg Message) error {
	now := d.now().UTC()
	data, err := encode(d.from, msg, now)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return fmt.Errorf("dev mail sink: %w", err)
	}
	name := now.Format("20060102T150405.000Z") + "-" + rand.Text()[:8] + ".eml"
	path := filepath.Join(d.dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("dev mail sink: %w", err)
	}
	d.log.LogAttrs(ctx, slog.LevelInfo, "mail written to dev sink, not delivered",
		slog.String("to", msg.To.Address),
		slog.String("subject", msg.Subject),
		slog.String("file", path),
	)
	return nil
}
