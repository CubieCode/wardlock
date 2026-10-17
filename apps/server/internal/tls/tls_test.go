// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"go.uber.org/zap"

	"wardlock.nz/server/internal/platform"
)

func TestManagedNames(t *testing.T) {
	tests := []struct {
		base, dashboard string
		wantDNS         []string
		wantHTTP        []string
	}{
		{"sites.example", "wardlock.example", []string{"*.sites.example"}, []string{"wardlock.example"}},
		{"sites.example", "sites.example", []string{"*.sites.example", "sites.example"}, nil},
		{"sites.example", "dash.sites.example", []string{"*.sites.example"}, nil},
		{"sites.example", "a.dash.sites.example", []string{"*.sites.example", "a.dash.sites.example"}, nil},
		{"sites.example", "notsites.example", []string{"*.sites.example"}, []string{"notsites.example"}},
	}
	for _, tt := range tests {
		dnsNames, httpNames := managedNames(tt.base, tt.dashboard)
		if !slices.Equal(dnsNames, tt.wantDNS) || !slices.Equal(httpNames, tt.wantHTTP) {
			t.Errorf("managedNames(%q, %q) = %v, %v; want %v, %v", tt.base, tt.dashboard, dnsNames, httpNames, tt.wantDNS, tt.wantHTTP)
		}
	}
}

func TestDNSProvider(t *testing.T) {
	const token = platform.Secret("test-token-not-real")
	if p, err := DNSProvider("cloudflare", token); err != nil || p == nil {
		t.Fatalf("cloudflare: %v", err)
	}
	if p, err := DNSProvider("desec", token); err != nil || p == nil {
		t.Fatalf("desec: %v", err)
	}
	for _, tt := range []struct {
		name  string
		token platform.Secret
		want  string
	}{
		{"route53", token, `"route53" is not supported (supported: cloudflare, desec)`},
		{"cloudflare", "", "WARDLOCK_DNS_API_TOKEN is required"},
	} {
		_, err := DNSProvider(tt.name, tt.token)
		if !errors.Is(err, platform.ErrInvalidConfig) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("DNSProvider(%q) = %v, want ErrInvalidConfig containing %q", tt.name, err, tt.want)
		}
		if err != nil && strings.Contains(err.Error(), string(token)) {
			t.Errorf("error leaks the token: %v", err)
		}
	}
}

func TestNewRequiresEverything(t *testing.T) {
	valid := Config{
		BaseDomain: "sites.example", DashboardHost: "wardlock.example", CA: "https://ca.example/dir",
		DNS: &fakeZone{}, Storage: &certmagic.FileStorage{Path: t.TempDir()}, Domains: &fakeDomains{},
		HTTPPort: 80, HTTPSPort: 443, Logger: slog.New(slog.DiscardHandler),
	}
	m, err := New(valid)
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	for name, mutate := range map[string]func(*Config){
		"base domain": func(c *Config) { c.BaseDomain = "" },
		"CA":          func(c *Config) { c.CA = "" },
		"DNS":         func(c *Config) { c.DNS = nil },
		"storage":     func(c *Config) { c.Storage = nil },
		"domains":     func(c *Config) { c.Domains = nil },
		"ports":       func(c *Config) { c.HTTPSPort = 0 },
		"logger":      func(c *Config) { c.Logger = nil },
	} {
		cfg := valid
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("New without %s succeeded", name)
		}
	}
}

func TestLoadRoots(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"}, NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "root.pem")
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRoots(good); err != nil {
		t.Fatalf("LoadRoots(good) = %v", err)
	}
	for _, path := range []string{bad, filepath.Join(dir, "missing.pem")} {
		if _, err := LoadRoots(path); !errors.Is(err, platform.ErrInvalidConfig) || !strings.Contains(err.Error(), "WARDLOCK_ACME_CA_ROOT") {
			t.Errorf("LoadRoots(%s) = %v, want ErrInvalidConfig naming the variable", filepath.Base(path), err)
		}
	}
}

func TestZapLoggerWritesToSlog(t *testing.T) {
	var buf syncBuffer
	log := zapLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	log.Named("obtain").With(zap.String("identifier", "docs.sites.example")).Info("certificate obtained", zap.Int("attempt", 2))
	log.Debug("hidden below the level")
	log.Error("renewal failed", zap.Error(errors.New("rate limited")))
	out := buf.String()
	for _, want := range []string{
		`level=INFO msg="certificate obtained" logger=obtain attempt=2 identifier=docs.sites.example`,
		`level=ERROR msg="renewal failed" error="rate limited"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("debug entry written at info level:\n%s", out)
	}
}
