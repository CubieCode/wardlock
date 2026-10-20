// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform_test

import (
	"bytes"
	"errors"
	"log/slog"
	"maps"
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
		{"filesystem blobs by default", nil, false, func(c platform.Config) bool {
			return c.BlobBackend == platform.BlobFS && c.S3 == (platform.S3Config{})
		}},
		{"s3 blobs", s3Env(map[string]string{
			"WARDLOCK_S3_PREFIX": "/wardlock/prod/", "WARDLOCK_S3_PATH_STYLE": "true",
			"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "example-secret", "AWS_SESSION_TOKEN": "example-session",
			"AWS_PROFILE": "wardlock", "AWS_SHARED_CREDENTIALS_FILE": "/etc/wardlock/aws",
		}), false, func(c platform.Config) bool {
			s := c.S3
			return c.BlobBackend == platform.BlobS3 && c.BlobDir == "" && s.Endpoint.String() == "https://s3.eu-west-1.amazonaws.com" &&
				s.Bucket == "wardlock-blobs" && s.Region == "eu-west-1" && s.Prefix == "wardlock/prod/" &&
				s.Addressing == platform.S3AddressPath && s.AccessKeyID == "AKIAEXAMPLE" && s.SecretAccessKey == "example-secret" &&
				s.SessionToken == "example-session" && s.Profile == "wardlock" && s.CredentialsFile == "/etc/wardlock/aws"
		}},
		{"s3 defaults", s3Env(map[string]string{"WARDLOCK_BLOB_BACKEND": "S3", "WARDLOCK_S3_ENDPOINT": "http://127.0.0.1:9000/"}), false,
			func(c platform.Config) bool {
				s := c.S3
				return s.Endpoint.String() == "http://127.0.0.1:9000" && s.Prefix == "" && s.Addressing == platform.S3AddressAuto && s.AccessKeyID == ""
			}},
		{"s3 virtual-hosted", s3Env(map[string]string{"WARDLOCK_S3_PATH_STYLE": "false"}), false, func(c platform.Config) bool {
			return c.S3.Addressing == platform.S3AddressVirtual
		}},
		{"s3 file size at the limit", s3Env(map[string]string{"WARDLOCK_MAX_FILE_SIZE": "5368709120"}), false, func(c platform.Config) bool {
			return c.MaxFileSize == 5<<30
		}},
		{"operator control defaults", nil, false, func(c platform.Config) bool {
			return c.Invites == "owners" && c.AbuseEmail == ""
		}},
		{"operator controls", map[string]string{"WARDLOCK_INVITES": "Operator", "WARDLOCK_ABUSE_EMAIL": "abuse@example.test"}, false,
			func(c platform.Config) bool {
				return c.Invites == "operator" && c.AbuseEmail == "abuse@example.test"
			}},
		{"unknown invitation mode", map[string]string{"WARDLOCK_INVITES": "nobody"}, true, nil},
		{"abuse address with a name", map[string]string{"WARDLOCK_ABUSE_EMAIL": "Abuse <abuse@example.test>"}, true, nil},
		{"abuse address not an address", map[string]string{"WARDLOCK_ABUSE_EMAIL": "abuse"}, true, nil},
		{"unknown blob backend", map[string]string{"WARDLOCK_BLOB_BACKEND": "gcs"}, true, nil},
		{"s3 setting with filesystem blobs", map[string]string{"WARDLOCK_S3_BUCKET": "wardlock-blobs"}, true, nil},
		{"blob directory with s3", s3Env(map[string]string{"WARDLOCK_BLOB_DIR": "/mnt/blobs"}), true, nil},
		{"s3 without endpoint", s3Env(map[string]string{"WARDLOCK_S3_ENDPOINT": ""}), true, nil},
		{"s3 endpoint without scheme", s3Env(map[string]string{"WARDLOCK_S3_ENDPOINT": "s3.example.test"}), true, nil},
		{"s3 endpoint with a path", s3Env(map[string]string{"WARDLOCK_S3_ENDPOINT": "https://s3.example.test/wardlock-blobs"}), true, nil},
		{"s3 endpoint with credentials", s3Env(map[string]string{"WARDLOCK_S3_ENDPOINT": "https://key:secret@s3.example.test"}), true, nil},
		{"s3 endpoint with a query", s3Env(map[string]string{"WARDLOCK_S3_ENDPOINT": "https://s3.example.test/?x=1"}), true, nil},
		{"s3 without bucket", s3Env(map[string]string{"WARDLOCK_S3_BUCKET": ""}), true, nil},
		{"s3 bucket with capitals", s3Env(map[string]string{"WARDLOCK_S3_BUCKET": "Wardlock"}), true, nil},
		{"s3 bucket too short", s3Env(map[string]string{"WARDLOCK_S3_BUCKET": "wl"}), true, nil},
		{"s3 bucket ending in a hyphen", s3Env(map[string]string{"WARDLOCK_S3_BUCKET": "wardlock-"}), true, nil},
		{"s3 without region", s3Env(map[string]string{"WARDLOCK_S3_REGION": ""}), true, nil},
		{"s3 prefix with dot-dot", s3Env(map[string]string{"WARDLOCK_S3_PREFIX": "a/../b"}), true, nil},
		{"s3 prefix with an empty segment", s3Env(map[string]string{"WARDLOCK_S3_PREFIX": "a//b"}), true, nil},
		{"s3 path style not boolean", s3Env(map[string]string{"WARDLOCK_S3_PATH_STYLE": "sometimes"}), true, nil},
		{"s3 file size over the limit", s3Env(map[string]string{"WARDLOCK_MAX_FILE_SIZE": "5368709121"}), true, nil},
		{"access key without secret", s3Env(map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE"}), true, nil},
		{"secret without access key", s3Env(map[string]string{"AWS_SECRET_ACCESS_KEY": "example-secret"}), true, nil},
		{"session token alone", s3Env(map[string]string{"AWS_SESSION_TOKEN": "example-session"}), true, nil},
		{"no blob cache by default", nil, false, func(c platform.Config) bool {
			return c.BlobCacheDir == "" && c.BlobCacheMaxBytes == 0
		}},
		{"blob cache", s3Env(map[string]string{"WARDLOCK_BLOB_CACHE_DIR": "/var/cache/wardlock", "WARDLOCK_BLOB_CACHE_MAX_BYTES": "2147483648"}), false,
			func(c platform.Config) bool {
				return c.BlobCacheDir == "/var/cache/wardlock" && c.BlobCacheMaxBytes == 2<<30
			}},
		{"blob cache beside filesystem blobs", map[string]string{"WARDLOCK_DATA_DIR": "/srv/wl", "WARDLOCK_BLOB_CACHE_DIR": "/srv/wl/blob-cache", "WARDLOCK_BLOB_CACHE_MAX_BYTES": "1"}, false,
			func(c platform.Config) bool { return c.BlobCacheDir == "/srv/wl/blob-cache" }},
		{"blob cache without a size", map[string]string{"WARDLOCK_BLOB_CACHE_DIR": "/var/cache/wardlock"}, true, nil},
		{"blob cache size without a directory", map[string]string{"WARDLOCK_BLOB_CACHE_MAX_BYTES": "1048576"}, true, nil},
		{"blob cache size with unit", map[string]string{"WARDLOCK_BLOB_CACHE_DIR": "/var/cache/wardlock", "WARDLOCK_BLOB_CACHE_MAX_BYTES": "2GiB"}, true, nil},
		{"blob cache size zero", map[string]string{"WARDLOCK_BLOB_CACHE_DIR": "/var/cache/wardlock", "WARDLOCK_BLOB_CACHE_MAX_BYTES": "0"}, true, nil},
		{"blob cache in the blob directory", map[string]string{"WARDLOCK_BLOB_DIR": "/srv/blobs", "WARDLOCK_BLOB_CACHE_DIR": "/srv/blobs/cache", "WARDLOCK_BLOB_CACHE_MAX_BYTES": "1"}, true, nil},
		{"blob cache is the blob directory", map[string]string{"WARDLOCK_BLOB_DIR": "/srv/blobs", "WARDLOCK_BLOB_CACHE_DIR": "/srv/blobs/", "WARDLOCK_BLOB_CACHE_MAX_BYTES": "1"}, true, nil},
		{"blob cache around the blob directory", map[string]string{"WARDLOCK_BLOB_DIR": "/srv/blobs", "WARDLOCK_BLOB_CACHE_DIR": "/srv", "WARDLOCK_BLOB_CACHE_MAX_BYTES": "1"}, true, nil},
		{"no operator listener by default", nil, false, func(c platform.Config) bool { return c.OperatorAddr == "" }},
		{"operator listener", map[string]string{"WARDLOCK_OPERATOR_ADDR": "127.0.0.1:9740"}, false, func(c platform.Config) bool {
			return c.OperatorAddr == "127.0.0.1:9740"
		}},
		{"operator address without a port", map[string]string{"WARDLOCK_OPERATOR_ADDR": "127.0.0.1"}, true, nil},
		{"operator address shared with the edge", map[string]string{"WARDLOCK_OPERATOR_ADDR": "127.0.0.1:8080"}, true, nil},
		{"quota defaults", nil, false, func(c platform.Config) bool {
			return c.MaxVideoSize == 10<<20 && c.BlockedExtensions == nil &&
				c.MaxOwnerStorage == 0 && c.MaxOwnerSites == 0 && c.MaxOwnerDeploysPerDay == 0
		}},
		{"quota settings", map[string]string{
			"WARDLOCK_MAX_VIDEO_SIZE": "0", "WARDLOCK_BLOCKED_EXTENSIONS": " EXE, .msi,,dmg ,exe,app-image ",
			"WARDLOCK_MAX_OWNER_STORAGE": "5368709120", "WARDLOCK_MAX_OWNER_SITES": "25", "WARDLOCK_MAX_OWNER_DEPLOYS_PER_DAY": "200",
		}, false, func(c platform.Config) bool {
			return c.MaxVideoSize == 0 && slices.Equal(c.BlockedExtensions, []string{".app-image", ".dmg", ".exe", ".msi"}) &&
				c.MaxOwnerStorage == 5<<30 && c.MaxOwnerSites == 25 && c.MaxOwnerDeploysPerDay == 200
		}},
		{"negative video size", map[string]string{"WARDLOCK_MAX_VIDEO_SIZE": "-1"}, true, nil},
		{"video size with unit", map[string]string{"WARDLOCK_MAX_VIDEO_SIZE": "10MiB"}, true, nil},
		{"negative storage quota", map[string]string{"WARDLOCK_MAX_OWNER_STORAGE": "-5"}, true, nil},
		{"site quota not a number", map[string]string{"WARDLOCK_MAX_OWNER_SITES": "many"}, true, nil},
		{"deploy quota fraction", map[string]string{"WARDLOCK_MAX_OWNER_DEPLOYS_PER_DAY": "1.5"}, true, nil},
		{"blocked extension with a path", map[string]string{"WARDLOCK_BLOCKED_EXTENSIONS": "exe,bin/sh"}, true, nil},
		{"blocked extension with an inner dot", map[string]string{"WARDLOCK_BLOCKED_EXTENSIONS": "tar.gz"}, true, nil},
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

// s3Env is a valid S3 blob configuration with overrides; an empty
// override removes the variable.
func s3Env(over map[string]string) map[string]string {
	env := map[string]string{
		"WARDLOCK_BLOB_BACKEND": "s3",
		"WARDLOCK_S3_ENDPOINT":  "https://s3.eu-west-1.amazonaws.com",
		"WARDLOCK_S3_BUCKET":    "wardlock-blobs",
		"WARDLOCK_S3_REGION":    "eu-west-1",
	}
	maps.Copy(env, over)
	return env
}

// TestS3ConfigErrorsNeverContainSecrets: a rejected endpoint is not echoed,
// since it may carry a key, and credentials are redacted in logs.
func TestS3ConfigErrorsNeverContainSecrets(t *testing.T) {
	_, err := platform.LoadConfig(func(k string) string {
		return s3Env(map[string]string{"WARDLOCK_S3_ENDPOINT": "https://AKIAEXAMPLE:leaked-secret@s3.example.test"})[k]
	})
	if err == nil || strings.Contains(err.Error(), "leaked-secret") || strings.Contains(err.Error(), "AKIAEXAMPLE") {
		t.Fatalf("error = %v", err)
	}
	cfg, err := platform.LoadConfig(func(k string) string {
		return s3Env(map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "leaked-secret", "AWS_SESSION_TOKEN": "leaked-session"})[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", slog.Any("s3", cfg.S3))
	if strings.Contains(buf.String(), "leaked-") {
		t.Fatalf("log leaked a credential: %s", buf.String())
	}
}

func TestRedactURL(t *testing.T) {
	u, _ := url.Parse("/report?t=wl_abc.secret&format=md")
	got := platform.RedactURL(u)
	if strings.Contains(got, "secret") || !strings.Contains(got, "format=md") {
		t.Fatalf("RedactURL = %q", got)
	}
}

// Every credential-bearing parameter is redacted however the client
// spells it; malformed pairs that might hold one are dropped, never kept.
func TestRedactURLCredentialParameters(t *testing.T) {
	const secret = "c2VjcmV0LXZhbHVl"
	for _, raw := range []string{
		"/.wardlock/callback?code=" + secret,
		"/callback/github?code=" + secret + "&state=" + secret,
		"/.wardlock/login?password=" + secret + "&next=/docs/",
		"/docs/?%74=" + secret,
		"/docs/?T=1&t=" + secret,
		"/docs/?t=" + secret + "&t=" + secret,
		"/docs/?t=" + secret + "%zz",
		"/docs/?x=1;t=" + secret,
		"/docs/?token=" + secret,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := platform.RedactURL(u); strings.Contains(got, secret) {
			t.Errorf("RedactURL(%q) = %q", raw, got)
		}
	}
}

func TestRedactURLTokensAnywhere(t *testing.T) {
	for _, raw := range []string{
		"/wl_abcdefghij_c2VjcmV0LXZhbHVlLWhlcmU/page",
		"/p?ref=WL_ABCDEFGHIJ_c2VjcmV0LXZhbHVlLWhlcmU",
		"/p?token=wl_abcdefghij_c2VjcmV0LXZhbHVlLWhlcmU&x=1",
	} {
		u, _ := url.Parse(raw)
		got := platform.RedactURL(u)
		if strings.Contains(got, "c2VjcmV0") || !strings.Contains(strings.ToLower(got), "wl_abcdefghij_") && !strings.Contains(got, "token=%5Bredacted%5D") {
			t.Errorf("RedactURL(%q) = %q", raw, got)
		}
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
