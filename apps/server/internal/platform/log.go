// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// NewLogger returns the process logger in the configured format.
func NewLogger(w io.Writer, cfg Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// Secret wraps a value that must never reach a log line. Logging it with
// slog prints "[redacted]" instead of the value.
type Secret string

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue("[redacted]") }

// sensitiveParams are query parameters that can carry credentials. Share
// links put their token in `t`; never log it.
var sensitiveParams = []string{"t", "token", "code", "state", "password"}

// tokenPattern matches a share-link or API token (wl_<public id>_<secret>)
// anywhere, in any letter case, as the access log's scrubber does.
var tokenPattern = regexp.MustCompile(`(?i)wl_([a-z2-7]{10})_[A-Za-z0-9_-]+`)

// RedactURL returns the path and query of u with credential-bearing query
// values replaced, suitable for logs. A token pasted anywhere else, in the
// path or another parameter, keeps only its public ID.
func RedactURL(u *url.URL) string {
	out := u.EscapedPath()
	if u.RawQuery != "" {
		q := u.Query()
		for _, key := range sensitiveParams {
			if q.Has(key) {
				q.Set(key, "[redacted]")
			}
		}
		out += "?" + q.Encode()
	}
	return tokenPattern.ReplaceAllString(out, "wl_${1}_[redacted]")
}

// LogRequests logs one line per request with credentials redacted.
func LogRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("host", r.Host),
			slog.String("path", RedactURL(r.URL)),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the underlying writer
// (flushing and hijacking for proxied websocket upgrades).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
