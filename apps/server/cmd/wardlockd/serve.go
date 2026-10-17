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
	"os"
	"path/filepath"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/dashboard"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/edge"
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
	log := platform.NewLogger(logOut, cfg)

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "wardlock.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	applied, err := db.Migrate(ctx, time.Now)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Info("applied migrations", slog.Any("versions", applied))
	}

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	blobs := deploys.NewFSBlobStore(cfg.BlobDir)
	notifier := deploys.NewLocalNotifier()
	apiServer := &api.Server{
		Version: platform.Version,
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
		// Plain HTTP until TLS lands (C1.4).
		SiteURL: func(host string) string { return "http://" + hostPort(host, ln.Addr()) + "/" },
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
		slog.String("dashboard", "http://"+hostPort(cfg.DashboardHost, ln.Addr())),
		slog.String("data_dir", cfg.DataDir),
		slog.String("blob_dir", cfg.BlobDir),
	)
	return platform.Serve(ctx, log, srv, ln)
}

func hostPort(host string, addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok && tcp.Port != 80 {
		return net.JoinHostPort(host, fmt.Sprint(tcp.Port))
	}
	return host
}
