// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package platform holds process-wide concerns: configuration, logging,
// health and graceful shutdown. It imports no other internal package.
package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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
	return cfg, nil
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
