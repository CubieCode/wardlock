// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"errors"
	"log/slog"
	"net/http"
)

// AskPath is where a TLS-terminating proxy asks whether to obtain an
// on-demand certificate (Caddy's on_demand_tls ask).
const AskPath = "/.wardlock/tls-ask"

// Ask answers AskPath?domain=<hostname> in proxy mode: 200 when the
// hostname is an active custom domain, 404 otherwise. Only a trusted proxy
// (recorded by TrustedProxies.Handler, so mount it behind that) gets an
// answer; any other peer gets the same 404 whatever the domain, so the
// endpoint tells the internet nothing about which domains exist.
func Ask(o OnDemand, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		peer, ok := PeerFrom(r.Context())
		if !ok || !peer.Trusted || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		err := o.Allow(r.Context(), r.URL.Query().Get("domain"))
		switch {
		case err == nil:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok\n"))
		case errors.Is(err, ErrNotAllowed):
			http.NotFound(w, r)
		default:
			log.LogAttrs(r.Context(), slog.LevelError, "tls-ask decision failed", slog.String("error", err.Error()))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})
}
