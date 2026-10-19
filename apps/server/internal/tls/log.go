// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"unicode/utf8"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// zapLogger returns a zap logger (what certmagic takes) that writes to log,
// so certificate events appear in the server's own format and level.
//
// Only fields named in safeFields pass. certmagic and acmez log, mostly at
// debug level, the ACME contact address, storage keys that embed it, every
// ACME request's and response's headers, DNS-01 record values, HTTP-01
// request paths carrying the challenge token, and client addresses; a new
// release may add more. An allowlist keeps whatever they add out of the log
// until it is reviewed, where a denylist would pass it. The names of the
// fields dropped from an entry are logged as `dropped`.
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
	attrs := make([]slog.Attr, 0, len(enc.Fields)+2)
	if e.LoggerName != "" {
		attrs = append(attrs, slog.String("logger", e.LoggerName))
	}
	keys := make([]string, 0, len(enc.Fields))
	for k := range enc.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var dropped []string
	for _, k := range keys {
		switch {
		case freeTextFields[k]:
			attrs = append(attrs, slog.String(k, scrubText(fmt.Sprint(enc.Fields[k]))))
		case safeFields[k]:
			attrs = append(attrs, slog.Any(k, enc.Fields[k]))
		default:
			dropped = append(dropped, k)
		}
	}
	if len(dropped) > 0 {
		attrs = append(attrs, slog.Any("dropped", dropped))
	}
	c.log.LogAttrs(context.Background(), slogLevel(e.Level), e.Message, attrs...)
	return nil
}

// safeFields are the certmagic and acmez field names that carry names,
// times, counts, states and public ACME resource URLs, never a credential
// or an address. Review a new name against the library source before
// adding it.
var safeFields = setOf(
	// Hostnames and the DNS zone.
	"identifier", "identifiers", "identifier_type", "names", "sans", "san_dns_names",
	"subject", "subjects", "common_name", "domain", "dns_name", "server_name", "host",
	"fqdn", "zone", "entry", "original", "transformed", "record_name", "record_type",
	"record_ttl", "resolvers",
	// ACME issuance: the CA, public resource URLs, challenge types, states.
	"ca", "issuer", "issuer_key", "challenge", "challenge_type", "status", "authz_status",
	"order", "authz", "authz_url", "url", "chain_url", "first_url", "explanation_url",
	"method", "status_code", "type", "preference", "cert_id", "ari_cert_id", "ari_unique_id",
	"cert_hash", "hash", "verification_method", "managed", "on_demand", "distributed",
	"revoked", "ocsp_status", "chain", "num_choices", "load_or_obtain_if_necessary",
	// Retries, cache maintenance and timing.
	"attempt", "max_attempts", "count", "cache_size", "cache_capacity", "cert_cache_fill",
	"elapsed", "remaining", "retrying_in", "try_again", "try_again_in", "grace_period",
	"max_duration", "renew_check_interval", "cert_expiry", "expiration", "expired",
	"expired_for", "expires", "not_after", "new_expiration", "renewal_cutoff",
	"renewal_time", "revoked_at", "this_update", "next_update", "next_refresh",
	"next_ari_update", "recheck_after", "selected_time", "prev_selected_time",
	"next_selected_time", "ephemeral_selected_time", "window_start", "window_end",
	"start", "end", "from", "to", "prev_start", "prev_end", "next_start", "next_end",
)

// freeTextFields carry a CA's or DNS provider's own words: an error, which
// can embed a provider's whole response body or a storage path naming the
// ACME contact, or an ACME problem document.
// They pass through scrubText.
var freeTextFields = setOf("error", "problem", "subproblem", "subproblems", "detail", "title")

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// maxFreeText bounds a free-text value in bytes.
const maxFreeText = 512

// credentialText matches what a provider might echo in an error: a PEM
// private key, or an Authorization scheme and its credential.
var credentialText = regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)|\b(bearer|token)(\s+|\s*[:=]\s*)[A-Za-z0-9._~+/=-]{8,}`)

// emailText matches an email address, which certmagic puts in the storage
// paths its errors name.
var emailText = regexp.MustCompile(`[^\s@/\\"'<>:]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+`)

// scrubText removes credentials and email addresses from s and bounds its
// length.
func scrubText(s string) string {
	s = emailText.ReplaceAllString(s, "[redacted email]")
	s = credentialText.ReplaceAllStringFunc(s, func(m string) string {
		if sub := credentialText.FindStringSubmatch(m); sub[1] != "" {
			return sub[1] + " [redacted]"
		}
		return "[redacted private key]"
	})
	if len(s) <= maxFreeText {
		return s
	}
	cut := maxFreeText
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated]"
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
