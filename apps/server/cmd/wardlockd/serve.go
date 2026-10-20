// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/dashboard"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/oauth"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
	wltls "wardlock.nz/server/internal/tls"
)

func version() string { return platform.Version }

// serve wires the packages together. It is the only place that sees both the
// edge and the API.
func serve(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	cfg, err := platform.LoadConfig(getenv)
	if err != nil {
		return err
	}
	if err := cfg.RequireSecret(); err != nil {
		return err
	}
	log := platform.NewLogger(logOut, cfg)
	autoTLS, err := loadAutoTLS(cfg)
	if err != nil {
		return err
	}

	db, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	blobs, blobsReady, err := openBlobs(cfg)
	if err != nil {
		return err
	}
	recorder, stopAccessLog := startAccessLog(ctx, cfg, log, db)
	defer stopAccessLog()
	stopGC := startGC(ctx, cfg, log, db, blobs)
	defer stopGC()

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = ln.Close() }()
	var tlsLn net.Listener
	if autoTLS != nil {
		if tlsLn, err = net.Listen("tcp", cfg.TLS.HTTPSAddr); err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		defer func() { _ = tlsLn.Close() }()
	}
	var opLn net.Listener
	if cfg.OperatorAddr != "" {
		if opLn, err = net.Listen("tcp", cfg.OperatorAddr); err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		defer func() { _ = opLn.Close() }()
	}
	port := ln.Addr().(*net.TCPAddr).Port
	dashURL := publicOrigin(cfg.DashboardHost, port)
	mailer, err := mail.New(mail.Config{SMTP: cfg.SMTP, Logger: log})
	if err != nil {
		_ = ln.Close()
		return err
	}
	ownerSvc, err := newOwners(cfg, log, db, dashURL, mailer)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer ownerSvc.Close()
	passkeys, err := owners.NewPasskeys(ownerSvc, db.Passkeys())
	if err != nil {
		_ = ln.Close()
		return err
	}
	setupURL, err := ownerSvc.PrepareSetup(ctx)
	if err != nil {
		_ = ln.Close()
		return err
	}

	notifier := deploys.NewLocalNotifier()
	accessSvc, err := access.New(access.Config{
		Sites:    db.Sites(),
		Policies: db.Policies(),
		Sessions: db.Sessions(),
		Links:    db.Links(),
		Notifier: notifier,

		PasswordSessions: db.PasswordSessions(),
		AccessLog:        db.AccessLog(),
		EmailCodes:       db.EmailCodes(),
		Mailer:           mailer,
		OAuth: access.OAuthConfig{
			Providers:  oauthProviders(ctx, cfg.OAuth),
			AuthOrigin: publicOrigin(cfg.AuthHost(), port).String(),
			SiteOrigin: func(host string) string { return publicOrigin(host, port).String() },
		},
		Secret: cfg.Secret,
		Now:    time.Now,
		Log:    log,
	})
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer accessSvc.Close()
	// In auto mode the certificate manager exists before the sites service,
	// which removes custom-domain certificates when domains go.
	var certs *wltls.Manager
	if autoTLS != nil {
		autoTLS.Domains = db.Domains()
		autoTLS.Logger = log
		autoTLS.HTTPPort = port
		autoTLS.HTTPSPort = tlsLn.Addr().(*net.TCPAddr).Port
		if certs, err = wltls.New(*autoTLS); err != nil {
			_ = ln.Close()
			return err
		}
		defer certs.Close()
	}
	var expiries certExpiries
	if certs != nil {
		expiries = certs
	}
	metrics := platform.NewMetrics(time.Now())
	collectServerMetrics(metrics, cfg, recorder.w, expiries)
	readiness := newReadiness(log, db, blobsReady, expiries)

	sitesCfg := sites.Config{
		Sites:         db.Sites(),
		Domains:       db.Domains(),
		Notifier:      notifier,
		Resolver:      sites.NewNetResolver(cfg.DNSResolvers),
		BaseDomain:    cfg.BaseDomain,
		DashboardHost: cfg.DashboardHost,
		Now:           time.Now,
		Log:           log,
	}
	if certs != nil {
		sitesCfg.Certs = certs
	}
	sitesSvc := sites.NewService(sitesCfg)
	stopDomainChecks := startDomainChecks(ctx, sitesSvc)
	defer stopDomainChecks()

	tokenSvc, err := owners.NewTokens(owners.TokensConfig{
		Tokens: db.APITokens(), Logger: log, Secret: cfg.Secret, DashboardURL: dashURL,
	})
	if err != nil {
		_ = ln.Close()
		return err
	}
	apiServer := &api.Server{
		Version: platform.Version,
		Owners:  ownerSvc,
		Tokens:  tokenSvc,
		Access:  accessSvc,
		// Passkey sign-in and management (C1.6).
		Passkeys: passkeys,
		Logger:   log,
		Sites:    sitesSvc,
		Deploys: deploys.NewService(deploys.Config{
			Sites:    db.Sites(),
			Deploys:  db.Deploys(),
			Blobs:    blobs,
			Notifier: notifier,
			Limits: deploys.Limits{
				MaxFileSize:   cfg.MaxFileSize,
				MaxFiles:      cfg.MaxDeployFiles,
				MaxDeploySize: cfg.MaxDeploySize,
			},
			Now: time.Now,
			// Renditions (A2.6) link to the site's base-domain address.
			Renditions: db.Renditions(),
			SiteOrigin: func(name string) string { return publicOrigin(name+"."+cfg.BaseDomain, port).String() },
		}),
		SiteURL: func(host string) string { return publicOrigin(host, port).String() + "/" },
	}

	dash := http.NewServeMux()
	dash.Handle("/api/", apiServer.Handler())
	dash.Handle("/", dashboard.Handler(cfg.DevDashboardURL))

	handler, err := edge.New(edge.Config{
		DashboardHost: cfg.DashboardHost,
		AuthHost:      cfg.AuthHost(),
		Dashboard:     dash,
		Health:        platform.Healthz(),
		Sites: siteSource{
			baseDomain: cfg.BaseDomain,
			sites:      db.Sites(),
			domains:    db.Domains(),
			deploys:    db.Deploys(),
			renditions: db.Renditions(),
		},
		Blobs:       blobs,
		Invalidator: notifier,
		Access:      accessSvc,
		Recorder:    recorder,
		TLSAsk:      tlsAsk(cfg, db, log),
		Log:         log,
	})
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer handler.Close()
	// Every request first has its client address settled: X-Forwarded-*
	// is believed only from a trusted proxy and removed otherwise.
	site := metrics.Instrument(platform.LogRequests(log, wltls.TrustedProxies(cfg.TLS.TrustedProxies).Handler(handler)))
	if cfg.TLS.Mode == platform.TLSProxy && len(cfg.TLS.TrustedProxies) == 0 {
		log.Warn("proxy mode without WARDLOCK_TRUSTED_PROXIES: every client appears as the proxy's address, so per-IP limits apply to all clients together; see docs/DEPLOYMENT.md")
	}
	if cfg.SMTP.Mode == platform.SMTPDev && cfg.TLS.Mode != platform.TLSLocal {
		log.Warn("WARDLOCK_SMTP_URL is dev:// outside local mode: sign-in codes and invitations are written to files, not delivered, and every recipient address is logged; set an smtp:// or smtps:// URL, see docs/DEPLOYMENT.md")
	}
	var servers []platform.Listener
	if autoTLS == nil {
		servers = []platform.Listener{{Server: newHTTPServer(log, site), Listener: ln}}
	} else {
		// Plain HTTP answers ACME HTTP-01 challenges and /healthz, and
		// redirects everything else to HTTPS on the default port, where
		// clients reach the server (see publicOrigin).
		plain := http.NewServeMux()
		plain.Handle("/healthz", platform.Healthz())
		plain.Handle("/", wltls.Redirect(443))
		// Nothing on it reads a body, so it gets whole-request limits.
		plainSrv := newHTTPServer(log, metrics.Instrument(platform.LogRequests(log, certs.HTTPChallenge(plain))))
		plainSrv.ReadTimeout = 30 * time.Second
		plainSrv.WriteTimeout = 30 * time.Second
		httpsSrv := newHTTPServer(log, site)
		httpsSrv.TLSConfig = certs.TLSConfig()
		servers = []platform.Listener{{Server: plainSrv, Listener: ln}, {Server: httpsSrv, Listener: tlsLn}}
		if err := certs.Manage(ctx); err != nil {
			return err
		}
		log.Info("automatic TLS", slog.Any("names", certs.Names()), slog.String("ca", cfg.TLS.ACMECA))
	}
	if opLn != nil {
		// Health, readiness and metrics for the operator only: never
		// routed through the edge, so no site or dashboard host reaches
		// them. Scrapes are neither logged nor counted. Last, so it shuts
		// down last and answers /readyz with 503 while the others drain.
		opSrv := newHTTPServer(log, platform.OperatorHandler(readiness, metrics))
		opSrv.ReadTimeout = 30 * time.Second
		opSrv.WriteTimeout = 30 * time.Second
		servers = append(servers, platform.Listener{Server: opSrv, Listener: opLn})
	}
	log.Info("wardlockd started",
		slog.String("version", platform.Version),
		slog.String("dashboard", dashURL.String()),
		slog.String("data_dir", cfg.DataDir),
		blobsAttr(cfg),
		slog.String("tls_mode", cfg.TLS.Mode),
	)
	if setupURL != "" {
		// The one place a credential is logged, as SPEC requires: it is
		// single use, expires in an hour and stops working once an owner
		// exists.
		log.Info("no owner exists yet: open the setup URL within 60 minutes to create the first owner, or run wardlockd admin create",
			slog.String("setup_url", setupURL))
	}
	return platform.Serve(ctx, log, readiness, servers...)
}

// blobsAttr logs where blobs are kept, without credentials.
func blobsAttr(cfg platform.Config) slog.Attr {
	if cfg.BlobBackend == platform.BlobS3 {
		return slog.Any("blob_bucket", cfg.S3)
	}
	return slog.String("blob_dir", cfg.BlobDir)
}

// tlsAsk answers /.wardlock/tls-ask for the TLS-terminating proxy in proxy
// mode; nil in other modes, where the edge answers it 404.
func tlsAsk(cfg platform.Config, db *store.DB, log *slog.Logger) http.Handler {
	if cfg.TLS.Mode != platform.TLSProxy {
		return nil
	}
	return wltls.Ask(wltls.OnDemand{BaseDomain: cfg.BaseDomain, DashboardHost: cfg.DashboardHost, Domains: db.Domains()}, log)
}

// domainCheckInterval is how often the verification worker looks for due
// custom-domain checks: the shortest backoff.
const domainCheckInterval = sites.MinBackoff

// startDomainChecks runs the custom-domain verification worker until the
// returned stop is called, which waits for it; call it before the
// database closes.
func startDomainChecks(ctx context.Context, svc *sites.Service) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { svc.RunChecks(ctx, domainCheckInterval) })
	return func() {
		cancel()
		wg.Wait()
	}
}

// newHTTPServer bounds slow headers and idle keep-alive connections. It sets
// no ReadTimeout or WriteTimeout: those cover the whole body, and a blob
// upload (up to WARDLOCK_MAX_FILE_SIZE) or a large file download on a slow
// link can legitimately take minutes.
func newHTTPServer(log *slog.Logger, h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// loadAutoTLS checks the automatic TLS settings that can fail before
// anything starts (the DNS provider and the CA roots) and returns the
// Manager configuration without the parts serve fills in; nil outside
// auto mode.
func loadAutoTLS(cfg platform.Config) (*wltls.Config, error) {
	if cfg.TLS.Mode != platform.TLSAuto {
		return nil, nil
	}
	dns, err := wltls.DNSProvider(cfg.TLS.DNSProvider, cfg.TLS.DNSAPIToken)
	if err != nil {
		return nil, err
	}
	auto := &wltls.Config{
		BaseDomain:    cfg.BaseDomain,
		DashboardHost: cfg.DashboardHost,
		CA:            cfg.TLS.ACMECA,
		Email:         cfg.TLS.ACMEEmail,
		DNS:           dns,
		Storage:       &certmagic.FileStorage{Path: filepath.Join(cfg.DataDir, "certs")},
	}
	if cfg.TLS.ACMECARoot != "" {
		if auto.CARoots, err = wltls.LoadRoots(cfg.TLS.ACMECARoot); err != nil {
			return nil, err
		}
	}
	return auto, nil
}

// openStore opens and migrates the database in the data directory.
func openStore(ctx context.Context, cfg platform.Config, log *slog.Logger) (*store.DB, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "wardlock.db"))
	if err != nil {
		return nil, err
	}
	applied, err := db.Migrate(ctx, time.Now)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if len(applied) > 0 {
		log.Info("applied migrations", slog.Any("versions", applied))
	}
	return db, nil
}

func newOwners(cfg platform.Config, log *slog.Logger, db *store.DB, dashURL *url.URL, mailer mail.Mailer) (*owners.Service, error) {
	return owners.New(owners.Config{
		Owners:       db.Owners(),
		Sessions:     db.Sessions(),
		Mailer:       mailer,
		Logger:       log,
		Secret:       cfg.Secret,
		DashboardURL: dashURL,
	})
}

// publicOrigin is the origin clients use for host: the dashboard in setup,
// sign-in and invitation URLs, and sites in API responses. Production hosts
// are served over HTTPS on the default port (by a TLS-terminating proxy, or
// by wardlockd itself in auto TLS mode, which needs 443 reachable), so the
// listen port is not part of it. Local mode (localhost or *.localhost) is
// reached directly over plain HTTP on the listen port; browsers treat it as
// secure, so __Host- cookies still work.
func publicOrigin(host string, port int) *url.URL {
	if host != "localhost" && !strings.HasSuffix(host, ".localhost") {
		return &url.URL{Scheme: "https", Host: host}
	}
	if port != 80 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return &url.URL{Scheme: "http", Host: host}
}

// oauthProviders builds the operator's OAuth and OIDC providers in the
// order login pages list them.
func oauthProviders(ctx context.Context, c platform.OAuthConfig) []access.Provider {
	var ps []access.Provider
	if c.GitHubClientID != "" {
		ps = append(ps, oauth.NewGitHub(oauth.GitHubConfig{ClientID: c.GitHubClientID, ClientSecret: string(c.GitHubClientSecret)}))
	}
	// OIDC discovery starts in the background now, so login pages can name
	// the authorisation endpoints in their form-action from the start.
	if c.GoogleClientID != "" {
		g := oauth.NewGoogle(c.GoogleClientID, string(c.GoogleClientSecret), nil)
		g.Warm(ctx)
		ps = append(ps, g)
	}
	if c.OIDCClientID != "" {
		o := oauth.NewOIDC(oauth.OIDCConfig{
			ID: "oidc", Name: c.OIDCName, Issuer: c.OIDCIssuer, ClientID: c.OIDCClientID, ClientSecret: string(c.OIDCClientSecret),
		})
		o.Warm(ctx)
		ps = append(ps, o)
	}
	return ps
}
