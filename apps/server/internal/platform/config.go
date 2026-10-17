// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package platform holds process-wide concerns: configuration, logging,
// health and graceful shutdown. It imports no other internal package.
package platform

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the server configuration, read once at startup from WARDLOCK_*
// environment variables (see .env.example for every variable).
type Config struct {
	// DataDir holds the SQLite database and, later, blobs and certificates.
	DataDir string
	// HTTPAddr is the listen address for plain HTTP (edge and API).
	HTTPAddr string
	// BaseDomain is the domain sites are published under: <site>.<BaseDomain>.
	BaseDomain string
	// DashboardHost serves the dashboard and the owner API.
	DashboardHost string
	// DevDashboardURL, when set, proxies dashboard pages to a Vite dev server.
	DevDashboardURL *url.URL
	LogLevel        slog.Level
	LogFormat       string
	// SMTP is outgoing mail: WARDLOCK_SMTP_URL and WARDLOCK_SMTP_FROM.
	SMTP SMTPConfig
	// BlobDir holds deploy file bodies; <DataDir>/blobs by default.
	BlobDir string
	// Deploy limits: bytes per file, files per deploy, bytes per deploy.
	MaxFileSize    int64
	MaxDeployFiles int
	MaxDeploySize  int64
	// Secret is the decoded WARDLOCK_SECRET: the server key for token,
	// code and session hashes. Empty when unset; commands that need it
	// call RequireSecret.
	Secret []byte
}

// MinSecretBytes is the shortest WARDLOCK_SECRET accepted, after decoding.
const MinSecretBytes = 32

// ErrNoSecret means WARDLOCK_SECRET is unset where it is required.
var ErrNoSecret = fmt.Errorf("%w: WARDLOCK_SECRET is required: at least %d random bytes, base64url-encoded (for example `openssl rand -base64 32 | tr '+/' '-_'`)", ErrInvalidConfig, MinSecretBytes)

// RequireSecret returns ErrNoSecret when WARDLOCK_SECRET is unset.
func (c Config) RequireSecret() error {
	if len(c.Secret) == 0 {
		return ErrNoSecret
	}
	return nil
}

// ErrInvalidConfig wraps every configuration error.
var ErrInvalidConfig = errors.New("invalid configuration")

// LoadConfig reads the configuration through getenv (os.Getenv in main;
// a map lookup in tests) and validates it.
func LoadConfig(getenv func(string) string) (Config, error) {
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	cfg := Config{
		DataDir:    get("WARDLOCK_DATA_DIR", "wardlock-data"),
		HTTPAddr:   get("WARDLOCK_HTTP_ADDR", "127.0.0.1:8080"),
		BaseDomain: normaliseHost(get("WARDLOCK_BASE_DOMAIN", "wardlock.localhost")),
		LogFormat:  get("WARDLOCK_LOG_FORMAT", "json"),
	}
	cfg.DashboardHost = normaliseHost(get("WARDLOCK_DASHBOARD_HOST", cfg.BaseDomain))

	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("%w: WARDLOCK_HTTP_ADDR %q: %w", ErrInvalidConfig, cfg.HTTPAddr, err)
	}
	if !validHost(cfg.BaseDomain) {
		return Config{}, fmt.Errorf("%w: WARDLOCK_BASE_DOMAIN %q is not a hostname", ErrInvalidConfig, cfg.BaseDomain)
	}
	if !validHost(cfg.DashboardHost) {
		return Config{}, fmt.Errorf("%w: WARDLOCK_DASHBOARD_HOST %q is not a hostname", ErrInvalidConfig, cfg.DashboardHost)
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(get("WARDLOCK_LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("%w: WARDLOCK_LOG_LEVEL: %w", ErrInvalidConfig, err)
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return Config{}, fmt.Errorf("%w: WARDLOCK_LOG_FORMAT must be json or text", ErrInvalidConfig)
	}
	if raw := get("WARDLOCK_DEV_DASHBOARD_URL", ""); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("%w: WARDLOCK_DEV_DASHBOARD_URL must be an http(s) URL", ErrInvalidConfig)
		}
		cfg.DevDashboardURL = u
	}
	smtp, err := parseSMTPConfig(get("WARDLOCK_SMTP_URL", "dev://"), get("WARDLOCK_SMTP_FROM", ""), cfg.DashboardHost)
	if err != nil {
		return Config{}, err
	}
	cfg.SMTP = smtp
	cfg.BlobDir = get("WARDLOCK_BLOB_DIR", filepath.Join(cfg.DataDir, "blobs"))
	for _, l := range []struct {
		key      string
		fallback int64
		dst      func(int64)
	}{
		{"WARDLOCK_MAX_FILE_SIZE", 25 << 20, func(v int64) { cfg.MaxFileSize = v }},
		{"WARDLOCK_MAX_DEPLOY_FILES", 20_000, func(v int64) { cfg.MaxDeployFiles = int(v) }},
		{"WARDLOCK_MAX_DEPLOY_SIZE", 1 << 30, func(v int64) { cfg.MaxDeploySize = v }},
	} {
		v, err := positiveInt(get(l.key, ""), l.fallback)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s must be a positive whole number: %w", ErrInvalidConfig, l.key, err)
		}
		l.dst(v)
	}
	if raw := get("WARDLOCK_SECRET", ""); raw != "" {
		key, err := decodeSecret(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.Secret = key
	}
	return cfg, nil
}

// positiveInt parses s as an integer of at least 1, or returns fallback
// for "".
func positiveInt(s string, fallback int64) (int64, error) {
	if s == "" {
		return fallback, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if v < 1 {
		return 0, errors.New("below 1")
	}
	return v, nil
}

// decodeSecret accepts base64url or standard base64, padded or not. The
// error never includes the value.
func decodeSecret(raw string) ([]byte, error) {
	raw = strings.TrimRight(raw, "=")
	key, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: WARDLOCK_SECRET is not base64url", ErrInvalidConfig)
	}
	if len(key) < MinSecretBytes {
		return nil, fmt.Errorf("%w: WARDLOCK_SECRET must decode to at least %d bytes, got %d", ErrInvalidConfig, MinSecretBytes, len(key))
	}
	return key, nil
}

func normaliseHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}
