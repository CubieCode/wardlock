// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package dashboard serves the dashboard single-page app. The production
// build is copied into ui/dist by scripts/embed-dashboard.sh and embedded;
// without it the server serves ui/fallback.html. In development the pages
// are proxied to the Vite dev server instead.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

//go:embed all:ui
var embedded embed.FS

// securityHeaders apply to every built dashboard response. The dev proxy
// omits the CSP because Vite's dev client injects inline scripts.
var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'self'; img-src 'self' data:; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "same-origin",
	"X-Frame-Options":        "DENY",
}

// Handler serves the embedded dashboard, or proxies to devURL when non-nil.
func Handler(devURL *url.URL) http.Handler {
	if devURL != nil {
		return &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(devURL)
			r.Out.Host = r.In.Host
		}}
	}
	dist, err := fs.Sub(embedded, "ui/dist")
	if err != nil {
		panic(err) // Unreachable: fs.Sub only fails on an invalid path.
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return withHeaders(serveFile(embedded, "ui/fallback.html"))
	}
	files := http.FileServerFS(dist)
	index := serveFile(dist, "index.html")
	return withHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "index.html" {
			index.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(dist, name); err == nil {
			if strings.HasPrefix(name, "assets/") {
				// Vite content-hashes everything under assets/.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		index.ServeHTTP(w, r) // Client-side route.
	}))
}

func serveFile(fsys fs.FS, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, name)
	})
}

func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range securityHeaders {
			w.Header().Set(k, v)
		}
		next.ServeHTTP(w, r)
	})
}
