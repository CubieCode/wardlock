// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Version is the release version, set at build time with
// -ldflags "-X wardlock.nz/server/internal/platform.Version=…".
var Version = "dev"

// ShutdownTimeout bounds how long in-flight requests may finish after a
// shutdown signal.
const ShutdownTimeout = 10 * time.Second

// Serve runs srv on ln until ctx is cancelled, then shuts it down gracefully.
func Serve(ctx context.Context, log *slog.Logger, srv *http.Server, ln net.Listener) error {
	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(ln) }()
	log.Info("listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-errs:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errs; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// Healthz reports liveness. Readiness (database, certificates) is O4.2.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
}
