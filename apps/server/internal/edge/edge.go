// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package edge routes every request by hostname: the dashboard host, a site
// under the base domain, or nothing. It enforces site access and owns the
// reserved /.wardlock/ path on every site. It never imports internal/api;
// the dashboard (with the API mounted) arrives as an http.Handler.
package edge

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"strings"
)

// ReservedPrefix is served by Wardlock on every site. Deployed files under
// it are never served.
const ReservedPrefix = "/.wardlock/"

// Config wires the edge.
type Config struct {
	BaseDomain    string
	DashboardHost string
	// Dashboard serves the dashboard pages and the owner API.
	Dashboard http.Handler
	// Health answers /healthz on every host.
	Health http.Handler
}

//go:embed hello
var helloFS embed.FS

// helloSite is the built-in placeholder site served at hello.<base domain>
// until deploys exist (C1.3 replaces it).
const helloSite = "hello"

// New returns the edge handler.
func New(cfg Config) http.Handler {
	hello, _ := fs.Sub(helloFS, "hello")
	sites := map[string]http.Handler{helloSite: http.FileServerFS(hello)}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			cfg.Health.ServeHTTP(w, r)
			return
		}
		host := canonicalHost(r.Host)
		if host == cfg.DashboardHost {
			cfg.Dashboard.ServeHTTP(w, r)
			return
		}
		site, ok := sites[siteName(host, cfg.BaseDomain)]
		if !ok {
			http.Error(w, "No site is published at this address.", http.StatusNotFound)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, ReservedPrefix) {
			serveReserved(w, r)
			return
		}
		site.ServeHTTP(w, r)
	})
}

// serveReserved answers /.wardlock/* on a site. Login pages, callbacks and
// logout join health here in A2.
func serveReserved(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == ReservedPrefix+"health" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
		return
	}
	http.NotFound(w, r)
}

// canonicalHost lowercases the Host header and strips any port and
// trailing dot.
func canonicalHost(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// siteName returns the single label in front of base, or "" when host is not
// exactly one level below base.
func siteName(host, base string) string {
	name, ok := strings.CutSuffix(host, "."+base)
	if !ok || name == "" || strings.Contains(name, ".") {
		return ""
	}
	return name
}
