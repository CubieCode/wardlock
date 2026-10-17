// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
	"github.com/libdns/desec"

	"wardlock.nz/server/internal/platform"
)

// Config is automatic TLS (WARDLOCK_TLS_MODE=auto).
type Config struct {
	BaseDomain    string
	DashboardHost string
	// CA is the ACME directory URL; Email is the account contact, optional.
	CA    string
	Email string
	// CARoots, when set, replaces the system roots for talking to the CA
	// (a private CA, or Pebble in tests).
	CARoots *x509.CertPool
	// DNS solves DNS-01 challenges for the base domain's names.
	DNS certmagic.DNSProvider
	// Storage keeps certificates, keys, ACME accounts and challenge
	// state; v1 passes certmagic file storage under the data directory.
	Storage certmagic.Storage
	// Domains backs the on-demand decision.
	Domains Domains
	// HTTPPort and HTTPSPort are the ports this process listens on. ACME
	// HTTP-01 and TLS-ALPN-01 validation must reach them (as 80 and 443
	// from the internet), so certmagic never binds its own solver
	// listeners.
	HTTPPort, HTTPSPort int
	Logger              *slog.Logger

	// dnsResolvers replaces the system resolvers for DNS-01 zone lookups
	// and propagation checks (tests).
	dnsResolvers []string
}

// Manager obtains and renews certificates and answers TLS handshakes.
//
// Three certmagic configurations share one certificate cache: dnsCfg
// manages the wildcard *.<base domain> (which covers every site and the
// auth origin) and any other name in the base domain's zone through
// DNS-01; dashCfg manages a dashboard host outside that zone through
// HTTP-01 or TLS-ALPN-01; onDemandCfg answers every handshake and obtains
// certificates for active custom domains the same way. The dashboard
// cannot share onDemandCfg because certmagic only obtains a config's
// managed names on demand once it has on-demand issuance.
type Manager struct {
	cache       *certmagic.Cache
	dnsCfg      *certmagic.Config
	dashCfg     *certmagic.Config
	onDemandCfg *certmagic.Config
	httpIssuer  *certmagic.ACMEIssuer
	baseDomain  string
	dnsNames    []string
	httpNames   []string
}

// New builds a Manager. It obtains nothing until Manage.
func New(cfg Config) (*Manager, error) {
	switch {
	case cfg.BaseDomain == "" || cfg.DashboardHost == "":
		return nil, errors.New("tls: base domain and dashboard host are required")
	case cfg.CA == "":
		return nil, errors.New("tls: ACME directory URL is required")
	case cfg.DNS == nil:
		return nil, errors.New("tls: DNS provider is required")
	case cfg.Storage == nil:
		return nil, errors.New("tls: storage is required")
	case cfg.Domains == nil:
		return nil, errors.New("tls: Domains is required")
	case cfg.HTTPPort <= 0 || cfg.HTTPSPort <= 0:
		return nil, errors.New("tls: listen ports are required")
	case cfg.Logger == nil:
		return nil, errors.New("tls: logger is required")
	}
	zl := zapLogger(cfg.Logger)
	m := &Manager{baseDomain: cfg.BaseDomain}
	m.dnsNames, m.httpNames = managedNames(cfg.BaseDomain, cfg.DashboardHost)
	m.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(c certmagic.Certificate) (*certmagic.Config, error) {
			switch {
			case m.inBaseZone(c.Names):
				return m.dnsCfg, nil
			case slices.Equal(c.Names, m.httpNames):
				return m.dashCfg, nil
			}
			return m.onDemandCfg, nil
		},
		Logger: zl,
	})
	// issuer returns an ACME issuer for c using HTTP-01 and TLS-ALPN-01,
	// or DNS-01 alone when dns is set.
	issuer := func(c *certmagic.Config, dns bool) *certmagic.ACMEIssuer {
		tmpl := certmagic.ACMEIssuer{
			CA:             cfg.CA,
			Email:          cfg.Email,
			Agreed:         true,
			TrustedRoots:   cfg.CARoots,
			AltHTTPPort:    cfg.HTTPPort,
			AltTLSALPNPort: cfg.HTTPSPort,
			Logger:         zl,
		}
		if dns {
			tmpl.DisableHTTPChallenge = true
			tmpl.DisableTLSALPNChallenge = true
			tmpl.DNS01Solver = &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{
				DNSProvider: cfg.DNS,
				Resolvers:   cfg.dnsResolvers,
				Logger:      zl,
			}}
		}
		iss := certmagic.NewACMEIssuer(c, tmpl)
		c.Issuers = []certmagic.Issuer{iss}
		return iss
	}

	onDemand := OnDemand{BaseDomain: cfg.BaseDomain, DashboardHost: cfg.DashboardHost, Domains: cfg.Domains}
	m.onDemandCfg = certmagic.New(m.cache, certmagic.Config{
		Storage: cfg.Storage,
		Logger:  zl,
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: func(ctx context.Context, name string) error {
				err := onDemand.Allow(ctx, name)
				if err != nil && !errors.Is(err, ErrNotAllowed) {
					cfg.Logger.LogAttrs(ctx, slog.LevelError, "on-demand TLS decision failed", slog.String("error", err.Error()))
				}
				return err
			},
		},
	})
	m.httpIssuer = issuer(m.onDemandCfg, false)
	m.dashCfg = certmagic.New(m.cache, certmagic.Config{Storage: cfg.Storage, Logger: zl})
	issuer(m.dashCfg, false)
	m.dnsCfg = certmagic.New(m.cache, certmagic.Config{Storage: cfg.Storage, Logger: zl})
	issuer(m.dnsCfg, true)
	return m, nil
}

// Manage loads the managed certificates from storage and obtains or renews
// them in the background, retrying with backoff; failures are logged. Call
// it once the listeners are bound, since HTTP-01 and TLS-ALPN-01 need them.
func (m *Manager) Manage(ctx context.Context) error {
	if err := m.dnsCfg.ManageAsync(ctx, m.dnsNames); err != nil {
		return fmt.Errorf("manage %v: %w", m.dnsNames, err)
	}
	if len(m.httpNames) > 0 {
		if err := m.dashCfg.ManageAsync(ctx, m.httpNames); err != nil {
			return fmt.Errorf("manage %v: %w", m.httpNames, err)
		}
	}
	return nil
}

// Names are the hostnames the Manager keeps certificates for, besides
// on-demand custom domains.
func (m *Manager) Names() []string {
	return append(append([]string(nil), m.dnsNames...), m.httpNames...)
}

// TLSConfig is the HTTPS listener's configuration: managed and on-demand
// certificates, TLS-ALPN-01 answers, HTTP/2 and HTTP/1.1.
func (m *Manager) TLSConfig() *tls.Config {
	tc := m.onDemandCfg.TLSConfig()
	tc.MinVersion = tls.VersionTLS12
	tc.NextProtos = append([]string{"h2", "http/1.1"}, tc.NextProtos...)
	return tc
}

// HTTPChallenge answers ACME HTTP-01 challenges and passes every other
// request to next.
func (m *Manager) HTTPChallenge(next http.Handler) http.Handler {
	return m.httpIssuer.HTTPChallengeHandler(next)
}

// Close stops certificate maintenance.
func (m *Manager) Close() { m.cache.Stop() }

func (m *Manager) inBaseZone(names []string) bool {
	for _, n := range names {
		if n == m.baseDomain || strings.HasSuffix(n, "."+m.baseDomain) {
			return true
		}
	}
	return false
}

// managedNames splits the names to keep certificates for. The wildcard
// covers every site and the auth origin, one label under the base domain;
// a dashboard host in the base domain's zone that the wildcard does not
// cover (the base domain itself, or deeper) also uses DNS-01, and one
// elsewhere uses HTTP-01 or TLS-ALPN-01 so the DNS token needs no access
// to its zone.
func managedNames(base, dashboard string) (dnsNames, httpNames []string) {
	dnsNames = []string{"*." + base}
	switch {
	case dashboard == base:
		dnsNames = append(dnsNames, dashboard)
	case strings.HasSuffix(dashboard, "."+base):
		if strings.Contains(strings.TrimSuffix(dashboard, "."+base), ".") {
			dnsNames = append(dnsNames, dashboard)
		}
	default:
		httpNames = []string{dashboard}
	}
	return dnsNames, httpNames
}

// dnsProviders lists the WARDLOCK_DNS_PROVIDER values DNSProvider
// accepts. Adding a libdns provider takes its name here and a case there.
const dnsProviders = "cloudflare, desec"

// DNSProvider returns the libdns provider called name, authenticated with
// token. The token never appears in errors or logs.
func DNSProvider(name string, token platform.Secret) (certmagic.DNSProvider, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: WARDLOCK_DNS_API_TOKEN is required for WARDLOCK_DNS_PROVIDER=%s", platform.ErrInvalidConfig, name)
	}
	switch name {
	case "cloudflare":
		return &cloudflare.Provider{APIToken: string(token)}, nil
	case "desec":
		return &desec.Provider{Token: string(token)}, nil
	default:
		return nil, fmt.Errorf("%w: WARDLOCK_DNS_PROVIDER %q is not supported (supported: %s)", platform.ErrInvalidConfig, name, dnsProviders)
	}
}

// LoadRoots reads a PEM bundle of CA certificates (WARDLOCK_ACME_CA_ROOT).
func LoadRoots(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("%w: WARDLOCK_ACME_CA_ROOT: %w", platform.ErrInvalidConfig, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w: WARDLOCK_ACME_CA_ROOT %q holds no PEM certificates", platform.ErrInvalidConfig, path)
	}
	return pool, nil
}
