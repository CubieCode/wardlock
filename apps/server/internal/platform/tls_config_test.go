// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform_test

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/server/internal/platform"
)

func TestLoadTLSConfig(t *testing.T) {
	// The DNS token is synthetic; no real credential belongs in tests.
	auto := map[string]string{
		"WARDLOCK_TLS_MODE":       "auto",
		"WARDLOCK_BASE_DOMAIN":    "sites.example",
		"WARDLOCK_DASHBOARD_HOST": "wardlock.example",
		"WARDLOCK_DNS_PROVIDER":   "Cloudflare",
		"WARDLOCK_DNS_API_TOKEN":  "test-token-not-real",
	}
	with := func(base map[string]string, kv ...string) map[string]string {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			env[kv[i]] = kv[i+1]
		}
		return env
	}
	public := map[string]string{"WARDLOCK_BASE_DOMAIN": "sites.example"}

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string // a substring of the error; "" means success
		check   func(platform.Config) bool
	}{
		{"localhost defaults to local", nil, "", func(c platform.Config) bool {
			return c.TLS.Mode == platform.TLSLocal && c.HTTPAddr == "127.0.0.1:8080"
		}},
		{"public hosts default to proxy", public, "", func(c platform.Config) bool {
			return c.TLS.Mode == platform.TLSProxy && c.TLS.TrustedProxies == nil && c.HTTPAddr == "127.0.0.1:8080"
		}},
		{"a public dashboard makes it proxy", map[string]string{"WARDLOCK_DASHBOARD_HOST": "wardlock.example"}, "", func(c platform.Config) bool {
			return c.TLS.Mode == platform.TLSProxy
		}},
		{"HTTPS address ignored outside auto", with(nil, "WARDLOCK_HTTPS_ADDR", "127.0.0.1:8443"), "", func(c platform.Config) bool {
			return c.TLS.HTTPSAddr == ""
		}},
		{"trusted proxies", with(public, "WARDLOCK_TRUSTED_PROXIES", " 127.0.0.1, 10.0.0.0/8 ,::1,fd00::/8, 192.168.1.7/16"), "", func(c platform.Config) bool {
			return slices.Equal(c.TLS.TrustedProxies, []netip.Prefix{
				netip.MustParsePrefix("127.0.0.1/32"),
				netip.MustParsePrefix("10.0.0.0/8"),
				netip.MustParsePrefix("::1/128"),
				netip.MustParsePrefix("fd00::/8"),
				netip.MustParsePrefix("192.168.0.0/16"),
			})
		}},
		{"auto defaults", auto, "", func(c platform.Config) bool {
			return c.TLS.Mode == platform.TLSAuto && c.HTTPAddr == ":80" && c.TLS.HTTPSAddr == ":443" &&
				c.TLS.ACMECA == platform.LetsEncrypt && c.TLS.DNSProvider == "cloudflare" &&
				c.TLS.DNSAPIToken == "test-token-not-real" && c.TLS.ACMEEmail == ""
		}},
		{"auto settings", with(auto,
			"WARDLOCK_HTTP_ADDR", "0.0.0.0:8080", "WARDLOCK_HTTPS_ADDR", "0.0.0.0:8443",
			"WARDLOCK_ACME_CA", "https://acme-staging-v02.api.letsencrypt.org/directory",
			"WARDLOCK_ACME_EMAIL", "ops@example.com", "WARDLOCK_ACME_CA_ROOT", "/etc/wardlock/ca.pem"), "",
			func(c platform.Config) bool {
				return c.HTTPAddr == "0.0.0.0:8080" && c.TLS.HTTPSAddr == "0.0.0.0:8443" &&
					strings.Contains(c.TLS.ACMECA, "staging") && c.TLS.ACMEEmail == "ops@example.com" &&
					c.TLS.ACMECARoot == "/etc/wardlock/ca.pem"
			}},

		{"unknown mode", with(nil, "WARDLOCK_TLS_MODE", "https"), "WARDLOCK_TLS_MODE must be local, proxy or auto", nil},
		{"local with public hosts", with(public, "WARDLOCK_TLS_MODE", "local"), "WARDLOCK_TLS_MODE=local needs localhost", nil},
		{"auto on localhost", with(auto, "WARDLOCK_BASE_DOMAIN", "wardlock.localhost", "WARDLOCK_DASHBOARD_HOST", ""), "needs public DNS names", nil},
		{"auto with an IP dashboard", with(auto, "WARDLOCK_DASHBOARD_HOST", "192.0.2.1"), "needs public DNS names", nil},
		{"auto without provider", with(auto, "WARDLOCK_DNS_PROVIDER", ""), "WARDLOCK_DNS_PROVIDER is required", nil},
		{"auto without token", with(auto, "WARDLOCK_DNS_API_TOKEN", ""), "WARDLOCK_DNS_API_TOKEN is required", nil},
		{"auto with plain HTTP CA", with(auto, "WARDLOCK_ACME_CA", "http://ca.example/dir"), "WARDLOCK_ACME_CA must be an https://", nil},
		{"auto with bad email", with(auto, "WARDLOCK_ACME_EMAIL", "not an address"), "WARDLOCK_ACME_EMAIL", nil},
		{"auto with bad HTTPS address", with(auto, "WARDLOCK_HTTPS_ADDR", "443"), "WARDLOCK_HTTPS_ADDR", nil},
		{"auto with trusted proxies", with(auto, "WARDLOCK_TRUSTED_PROXIES", "127.0.0.1"), "WARDLOCK_TRUSTED_PROXIES is only used when WARDLOCK_TLS_MODE=proxy", nil},
		{"DNS provider without auto", with(public, "WARDLOCK_DNS_PROVIDER", "cloudflare"), "WARDLOCK_DNS_PROVIDER is only used when WARDLOCK_TLS_MODE=auto", nil},
		{"ACME email in local mode", with(nil, "WARDLOCK_ACME_EMAIL", "ops@example.com"), "WARDLOCK_ACME_EMAIL is only used when WARDLOCK_TLS_MODE=auto", nil},
		{"trusted proxies in local mode", with(nil, "WARDLOCK_TRUSTED_PROXIES", "127.0.0.1"), "only used when WARDLOCK_TLS_MODE=proxy", nil},
		{"IPv4-mapped proxies become IPv4", with(public, "WARDLOCK_TRUSTED_PROXIES", "::ffff:10.1.2.3,::ffff:192.168.0.0/112"), "", func(c platform.Config) bool {
			return slices.Equal(c.TLS.TrustedProxies, []netip.Prefix{
				netip.MustParsePrefix("10.1.2.3/32"),
				netip.MustParsePrefix("192.168.0.0/16"),
			})
		}},
		{"every IPv4 address", with(public, "WARDLOCK_TRUSTED_PROXIES", "127.0.0.1,0.0.0.0/0"), `"0.0.0.0/0" trusts every address`, nil},
		{"every IPv6 address", with(public, "WARDLOCK_TRUSTED_PROXIES", "::/0"), `"::/0" trusts every address`, nil},
		{"every IPv4-mapped address", with(public, "WARDLOCK_TRUSTED_PROXIES", "::ffff:0:0/96"), "trusts every address", nil},
		{"IPv4-mapped range wider than IPv4", with(public, "WARDLOCK_TRUSTED_PROXIES", "::ffff:0:0/80"), "mixes IPv4-mapped and IPv6", nil},
		{"zoned address", with(public, "WARDLOCK_TRUSTED_PROXIES", "fe80::1%eth0"), "IPv6 zone", nil},
		{"bad trusted proxy", with(public, "WARDLOCK_TRUSTED_PROXIES", "127.0.0.1,proxy.internal"), `"proxy.internal" is not an IP address or CIDR range`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := platform.LoadConfig(func(k string) string { return tt.env[k] })
			if tt.wantErr != "" {
				if !errors.Is(err, platform.ErrInvalidConfig) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want ErrInvalidConfig containing %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "test-token-not-real") {
					t.Fatalf("error leaks the DNS token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(cfg) {
				t.Fatalf("unexpected config %+v", cfg.TLS)
			}
		})
	}
}
