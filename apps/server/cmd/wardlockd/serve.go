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
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/dashboard"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
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

	db, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	dashURL := publicOrigin(cfg.DashboardHost, port)
	ownerSvc, err := newOwners(cfg, log, db, dashURL)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer ownerSvc.Close()
	setupURL, err := ownerSvc.PrepareSetup(ctx)
	if err != nil {
		_ = ln.Close()
		return err
	}

	blobs := deploys.NewFSBlobStore(cfg.BlobDir)
	notifier := deploys.NewLocalNotifier()
	apiServer := &api.Server{
		Version: platform.Version,
		Owners:  ownerSvc,
		Logger:  log,
		Sites: sites.NewService(sites.Config{
			Sites:         db.Sites(),
			Notifier:      notifier,
			BaseDomain:    cfg.BaseDomain,
			DashboardHost: cfg.DashboardHost,
			Now:           time.Now,
		}),
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
		}),
		SiteURL: func(host string) string { return publicOrigin(host, port).String() + "/" },
	}

	dash := http.NewServeMux()
	dash.Handle("/api/", apiServer.Handler())
	dash.Handle("/", dashboard.Handler(cfg.DevDashboardURL))

	handler := edge.New(edge.Config{
		BaseDomain:    cfg.BaseDomain,
		DashboardHost: cfg.DashboardHost,
		Dashboard:     dash,
		Health:        platform.Healthz(),
	})
	srv := &http.Server{
		Handler:           platform.LogRequests(log, handler),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	log.Info("wardlockd started",
		slog.String("version", platform.Version),
		slog.String("dashboard", dashURL.String()),
		slog.String("data_dir", cfg.DataDir),
		slog.String("blob_dir", cfg.BlobDir),
	)
	if setupURL != "" {
		// The one place a credential is logged, as SPEC requires: it is
		// single use, expires in an hour and stops working once an owner
		// exists.
		log.Info("no owner exists yet: open the setup URL within 60 minutes to create the first owner, or run wardlockd admin create",
			slog.String("setup_url", setupURL))
	}
	return platform.Serve(ctx, log, srv, ln)
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

func newOwners(cfg platform.Config, log *slog.Logger, db *store.DB, dashURL *url.URL) (*owners.Service, error) {
	mailer, err := mail.New(mail.Config{SMTP: cfg.SMTP, Logger: log})
	if err != nil {
		return nil, err
	}
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
// are served over HTTPS on the default port (behind a proxy until C1.4 adds
// TLS), so the listen port is not part of it. Local mode (localhost or *.localhost) is reached directly
// over plain HTTP on the listen port; browsers treat it as secure, so
// __Host- cookies still work. C1.4 owns changing this rule.
func publicOrigin(host string, port int) *url.URL {
	if host != "localhost" && !strings.HasSuffix(host, ".localhost") {
		return &url.URL{Scheme: "https", Host: host}
	}
	if port != 80 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return &url.URL{Scheme: "http", Host: host}
}
