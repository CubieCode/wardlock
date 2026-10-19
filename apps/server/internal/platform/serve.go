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

// Listener is a server and the listener it serves. A server with a
// TLSConfig serves TLS on the listener.
type Listener struct {
	Server   *http.Server
	Listener net.Listener
}

// Serve runs every server until ctx is cancelled or one of them fails, then
// shuts them all down gracefully.
func Serve(ctx context.Context, log *slog.Logger, servers ...Listener) error {
	errs := make(chan error, len(servers))
	for _, s := range servers {
		// Read TLSConfig before serving: ServeTLS and Serve configure HTTP/2
		// on the server concurrently.
		tls := s.Server.TLSConfig != nil
		log.Info("listening", slog.String("addr", s.Listener.Addr().String()), slog.Bool("tls", tls))
		go func() {
			if tls {
				errs <- s.Server.ServeTLS(s.Listener, "", "")
				return
			}
			errs <- s.Server.Serve(s.Listener)
		}()
	}

	var failed error
	received := 0
	select {
	case err := <-errs:
		received++
		failed = fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	for _, s := range servers {
		if err := s.Server.Shutdown(shutdownCtx); err != nil && failed == nil {
			failed = fmt.Errorf("shutdown: %w", err)
		}
	}
	for ; received < len(servers); received++ {
		if err := <-errs; !errors.Is(err, http.ErrServerClosed) && failed == nil {
			failed = fmt.Errorf("serve: %w", err)
		}
	}
	return failed
}

// Healthz reports liveness: the process answers. Readiness is Readiness.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
}
