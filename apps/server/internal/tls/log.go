// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"context"
	"log/slog"
	"slices"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// zapLogger returns a zap logger (what certmagic takes) that writes to log,
// so certificate events appear in the server's own format and level.
func zapLogger(log *slog.Logger) *zap.Logger {
	return zap.New(slogCore{log: log})
}

// slogCore is a zapcore.Core that forwards entries to a slog.Logger.
type slogCore struct {
	log    *slog.Logger
	fields []zapcore.Field
}

func (c slogCore) Enabled(l zapcore.Level) bool {
	return c.log.Enabled(context.Background(), slogLevel(l))
}

func (c slogCore) With(fields []zapcore.Field) zapcore.Core {
	return slogCore{log: c.log, fields: append(slices.Clip(c.fields), fields...)}
}

func (c slogCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}
	return ce
}

func (c slogCore) Write(e zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range append(slices.Clip(c.fields), fields...) {
		f.AddTo(enc)
	}
	attrs := make([]slog.Attr, 0, len(enc.Fields)+1)
	if e.LoggerName != "" {
		attrs = append(attrs, slog.String("logger", e.LoggerName))
	}
	keys := make([]string, 0, len(enc.Fields))
	for k := range enc.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		attrs = append(attrs, slog.Any(k, enc.Fields[k]))
	}
	c.log.LogAttrs(context.Background(), slogLevel(e.Level), e.Message, attrs...)
	return nil
}

func (slogCore) Sync() error { return nil }

func slogLevel(l zapcore.Level) slog.Level {
	switch {
	case l <= zapcore.DebugLevel:
		return slog.LevelDebug
	case l == zapcore.InfoLevel:
		return slog.LevelInfo
	case l == zapcore.WarnLevel:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}
