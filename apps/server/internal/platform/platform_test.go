// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/platform"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(platform.Config) bool
	}{
		{"defaults", nil, false, func(c platform.Config) bool {
			return c.BaseDomain == "wardlock.localhost" && c.DashboardHost == "wardlock.localhost" && c.DevDashboardURL == nil
		}},
		{"hosts are normalised", map[string]string{"WARDLOCK_BASE_DOMAIN": "Sites.Example.", "WARDLOCK_DASHBOARD_HOST": "Admin.Example"}, false,
			func(c platform.Config) bool {
				return c.BaseDomain == "sites.example" && c.DashboardHost == "admin.example"
			}},
		{"dev dashboard URL", map[string]string{"WARDLOCK_DEV_DASHBOARD_URL": "http://127.0.0.1:5173"}, false,
			func(c platform.Config) bool {
				return c.DevDashboardURL != nil && c.DevDashboardURL.Host == "127.0.0.1:5173"
			}},
		{"deploy defaults", map[string]string{"WARDLOCK_DATA_DIR": "/srv/wl"}, false, func(c platform.Config) bool {
			return c.BlobDir == filepath.Join("/srv/wl", "blobs") && c.MaxFileSize == 25<<20 && c.MaxDeployFiles == 20_000 && c.MaxDeploySize == 1<<30
		}},
		{"deploy settings", map[string]string{
			"WARDLOCK_BLOB_DIR": "/mnt/blobs", "WARDLOCK_MAX_FILE_SIZE": "1048576",
			"WARDLOCK_MAX_DEPLOY_FILES": "100", "WARDLOCK_MAX_DEPLOY_SIZE": "10485760",
		}, false, func(c platform.Config) bool {
			return c.BlobDir == "/mnt/blobs" && c.MaxFileSize == 1<<20 && c.MaxDeployFiles == 100 && c.MaxDeploySize == 10<<20
		}},
		{"zero file size", map[string]string{"WARDLOCK_MAX_FILE_SIZE": "0"}, true, nil},
		{"negative file count", map[string]string{"WARDLOCK_MAX_DEPLOY_FILES": "-1"}, true, nil},
		{"file count too large", map[string]string{"WARDLOCK_MAX_DEPLOY_FILES": "99999999999999999999"}, true, nil},
		{"size with unit", map[string]string{"WARDLOCK_MAX_DEPLOY_SIZE": "1GiB"}, true, nil},
		{"access log defaults", nil, false, func(c platform.Config) bool {
			return c.AccessLogIPv4Prefix == 24 && c.AccessLogIPv6Prefix == 48 &&
				c.AccessLogRetention == 90*24*time.Hour && c.AccessLogBuffer == 10_000
		}},
		{"access log settings", map[string]string{
			"WARDLOCK_ACCESS_LOG_IPV4_PREFIX": "32", "WARDLOCK_ACCESS_LOG_IPV6_PREFIX": "0",
			"WARDLOCK_ACCESS_LOG_RETENTION_DAYS": "0", "WARDLOCK_ACCESS_LOG_BUFFER": "50",
		}, false, func(c platform.Config) bool {
			return c.AccessLogIPv4Prefix == 32 && c.AccessLogIPv6Prefix == 0 && c.AccessLogRetention == 0 && c.AccessLogBuffer == 50
		}},
		{"IPv4 prefix too long", map[string]string{"WARDLOCK_ACCESS_LOG_IPV4_PREFIX": "33"}, true, nil},
		{"IPv6 prefix too long", map[string]string{"WARDLOCK_ACCESS_LOG_IPV6_PREFIX": "129"}, true, nil},
		{"negative prefix", map[string]string{"WARDLOCK_ACCESS_LOG_IPV4_PREFIX": "-1"}, true, nil},
		{"retention with unit", map[string]string{"WARDLOCK_ACCESS_LOG_RETENTION_DAYS": "30d"}, true, nil},
		{"zero buffer", map[string]string{"WARDLOCK_ACCESS_LOG_BUFFER": "0"}, true, nil},
		{"buffer too large", map[string]string{"WARDLOCK_ACCESS_LOG_BUFFER": "1000001"}, true, nil},
		{"gc defaults", nil, false, func(c platform.Config) bool {
			return c.GCInterval == time.Hour && c.GCGrace == 24*time.Hour && c.DeployHistory == 50
		}},
		{"gc settings", map[string]string{
			"WARDLOCK_GC_INTERVAL_MINUTES": "0", "WARDLOCK_GC_GRACE_HOURS": "2", "WARDLOCK_DEPLOY_HISTORY": "5",
		}, false, func(c platform.Config) bool {
			return c.GCInterval == 0 && c.GCGrace == 2*time.Hour && c.DeployHistory == 5
		}},
		{"zero grace", map[string]string{"WARDLOCK_GC_GRACE_HOURS": "0"}, true, nil},
		{"zero history", map[string]string{"WARDLOCK_DEPLOY_HISTORY": "0"}, true, nil},
		{"negative interval", map[string]string{"WARDLOCK_GC_INTERVAL_MINUTES": "-1"}, true, nil},
		{"interval with unit", map[string]string{"WARDLOCK_GC_INTERVAL_MINUTES": "1h"}, true, nil},
		{"system DNS resolvers by default", nil, false, func(c platform.Config) bool { return c.DNSResolvers == nil }},
		{"DNS resolvers", map[string]string{"WARDLOCK_DNS_RESOLVERS": "9.9.9.9:53, [2620:fe::fe]:53,ns.example.net:5353"}, false,
			func(c platform.Config) bool {
				return slices.Equal(c.DNSResolvers, []string{"9.9.9.9:53", "[2620:fe::fe]:53", "ns.example.net:5353"})
			}},
		{"DNS resolver without a port", map[string]string{"WARDLOCK_DNS_RESOLVERS": "9.9.9.9"}, true, nil},
		{"DNS resolver port 0", map[string]string{"WARDLOCK_DNS_RESOLVERS": "9.9.9.9:0"}, true, nil},
		{"empty DNS resolver", map[string]string{"WARDLOCK_DNS_RESOLVERS": "9.9.9.9:53,,1.1.1.1:53"}, true, nil},
		{"DNS resolvers outside auto mode are fine", map[string]string{
			"WARDLOCK_TLS_MODE": "proxy", "WARDLOCK_BASE_DOMAIN": "sites.example", "WARDLOCK_DNS_RESOLVERS": "1.1.1.1:53",
		}, false, func(c platform.Config) bool { return len(c.DNSResolvers) == 1 }},
		{"bad address", map[string]string{"WARDLOCK_HTTP_ADDR": "8080"}, true, nil},
		{"bad base domain", map[string]string{"WARDLOCK_BASE_DOMAIN": "under_score.example"}, true, nil},
		{"bad log level", map[string]string{"WARDLOCK_LOG_LEVEL": "loud"}, true, nil},
		{"bad log format", map[string]string{"WARDLOCK_LOG_FORMAT": "xml"}, true, nil},
		{"bad dev URL", map[string]string{"WARDLOCK_DEV_DASHBOARD_URL": "ftp://x"}, true, nil},
		{"no secret", nil, false, func(c platform.Config) bool {
			return c.Secret == nil && errors.Is(c.RequireSecret(), platform.ErrNoSecret)
		}},
		{"base64url secret", map[string]string{"WARDLOCK_SECRET": strings.Repeat("A-_w", 11)}, false, func(c platform.Config) bool {
			return len(c.Secret) == 33 && c.RequireSecret() == nil
		}},
		{"padded standard base64 secret", map[string]string{"WARDLOCK_SECRET": strings.Repeat("A+/w", 11) + "=="}, false, func(c platform.Config) bool {
			return len(c.Secret) == 33
		}},
		{"no oauth providers", nil, false, func(c platform.Config) bool {
			return c.OAuth == (platform.OAuthConfig{OIDCName: "single sign-on"}) && c.AuthHost() == "auth.wardlock.localhost"
		}},
		{"oauth providers", map[string]string{
			"WARDLOCK_GITHUB_CLIENT_ID": "gh-id", "WARDLOCK_GITHUB_CLIENT_SECRET": "gh-secret",
			"WARDLOCK_GOOGLE_CLIENT_ID": "g-id", "WARDLOCK_GOOGLE_CLIENT_SECRET": "g-secret",
			"WARDLOCK_OIDC_ISSUER": "https://sso.example.test/realm", "WARDLOCK_OIDC_CLIENT_ID": "o-id",
			"WARDLOCK_OIDC_CLIENT_SECRET": "o-secret", "WARDLOCK_OIDC_NAME": "Harbour SSO",
		}, false, func(c platform.Config) bool {
			o := c.OAuth
			return o.GitHubClientID == "gh-id" && o.GitHubClientSecret == "gh-secret" && o.GoogleClientID == "g-id" &&
				o.OIDCIssuer == "https://sso.example.test/realm" && o.OIDCClientSecret == "o-secret" && o.OIDCName == "Harbour SSO"
		}},
		{"local OIDC issuer", map[string]string{
			"WARDLOCK_OIDC_ISSUER": "http://sso.localhost:8081", "WARDLOCK_OIDC_CLIENT_ID": "o-id", "WARDLOCK_OIDC_CLIENT_SECRET": "o-secret",
		}, false, func(c platform.Config) bool { return c.OAuth.OIDCIssuer == "http://sso.localhost:8081" }},
		{"client ID without secret", map[string]string{"WARDLOCK_GITHUB_CLIENT_ID": "gh-id"}, true, nil},
		{"secret without client ID", map[string]string{"WARDLOCK_GOOGLE_CLIENT_SECRET": "g-secret"}, true, nil},
		{"OIDC client without issuer", map[string]string{"WARDLOCK_OIDC_CLIENT_ID": "o-id", "WARDLOCK_OIDC_CLIENT_SECRET": "o-secret"}, true, nil},
		{"plain HTTP OIDC issuer", map[string]string{
			"WARDLOCK_OIDC_ISSUER": "http://sso.example.test", "WARDLOCK_OIDC_CLIENT_ID": "o-id", "WARDLOCK_OIDC_CLIENT_SECRET": "o-secret",
		}, true, nil},
		{"dashboard on the auth origin", map[string]string{"WARDLOCK_DASHBOARD_HOST": "auth.wardlock.localhost"}, true, nil},
		{"short secret", map[string]string{"WARDLOCK_SECRET": "c2hvcnQ"}, true, nil},
		{"secret not base64", map[string]string{"WARDLOCK_SECRET": strings.Repeat("!", 44)}, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := platform.LoadConfig(func(k string) string { return tt.env[k] })
			if tt.wantErr {
				if !errors.Is(err, platform.ErrInvalidConfig) {
					t.Fatalf("error = %v, want ErrInvalidConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(cfg) {
				t.Fatalf("unexpected config %+v", cfg)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	u, _ := url.Parse("/report?t=wl_abc.secret&format=md")
	got := platform.RedactURL(u)
	if strings.Contains(got, "secret") || !strings.Contains(got, "format=md") {
		t.Fatalf("RedactURL = %q", got)
	}
}

func TestLogsNeverContainSecrets(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("x", slog.Any("token", platform.Secret("wl_live_value")))
	h := platform.LogRequests(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/p?t=wl_live_value", nil))
	if strings.Contains(buf.String(), "wl_live_value") {
		t.Fatalf("log leaked a secret: %s", buf.String())
	}
}
