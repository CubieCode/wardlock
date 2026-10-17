// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package edge routes every request by hostname: the dashboard host, a site
// (under the base domain or at an active custom domain), or nothing. It
// serves each site's active deploy, enforces site access and owns the
// reserved /.wardlock/ path on every site. It never imports internal/api;
// the dashboard (with the API mounted) arrives as an http.Handler.
package edge

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"

	"wardlock.nz/server/internal/access"
)

// ReservedPrefix is served by Wardlock on every site. Deployed files under
// it are never served.
const ReservedPrefix = "/.wardlock/"

// Config wires the edge.
type Config struct {
	DashboardHost string
	// Dashboard serves the dashboard pages and the owner API.
	Dashboard http.Handler
	// Health answers /healthz on every host.
	Health http.Handler
	// Sites finds the site for a host and its active manifest.
	Sites SiteSource
	// Blobs opens file bodies by SHA-256.
	Blobs BlobOpener
	// Invalidator announces site changes, which drop cached manifests.
	Invalidator Invalidator
	// Access decides each request. It is required: there is no implicit
	// allow-all, so a wiring mistake cannot publish protected sites.
	Access Access
	Log    *slog.Logger
}

// Edge is the edge handler.
type Edge struct {
	cfg         Config
	blobs       BlobOpener
	access      Access
	log         *slog.Logger
	cache       *cache
	unsubscribe func()
}

// ErrNoAccess is returned by New without an Access.
var ErrNoAccess = errors.New("edge: Config.Access is required")

// New returns the edge handler, subscribed to cfg.Invalidator until Close.
func New(cfg Config) (*Edge, error) {
	if cfg.Access == nil {
		return nil, ErrNoAccess
	}
	e := &Edge{
		cfg:    cfg,
		blobs:  cfg.Blobs,
		access: cfg.Access,
		log:    cfg.Log,
		cache:  newCache(cfg.Sites),
	}
	if e.log == nil {
		e.log = slog.New(slog.DiscardHandler)
	}
	e.unsubscribe = cfg.Invalidator.Subscribe(e.cache.invalidate)
	return e, nil
}

// Close stops listening for invalidations.
func (e *Edge) Close() { e.unsubscribe() }

func (e *Edge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		e.cfg.Health.ServeHTTP(w, r)
		return
	}
	host := canonicalHost(r.Host)
	if host == e.cfg.DashboardHost {
		e.cfg.Dashboard.ServeHTTP(w, r)
		return
	}
	s, err := e.cache.lookup(r.Context(), host)
	if errors.Is(err, ErrNoSite) {
		noSite(w)
		return
	}
	if err != nil {
		e.log.ErrorContext(r.Context(), "look up site", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// A site with nothing published is indistinguishable from no site,
	// whatever the method or path.
	if s.site.ActiveDeployID == "" {
		noSite(w)
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Clean before the reserved check so /x/../.wardlock/ cannot slip past.
	clean := cleanPath(r.URL.Path)
	if reserved(strings.TrimPrefix(clean, "/")) {
		// Wardlock's own pages: policies never apply here.
		e.serveReserved(w, r, s.site, clean)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	req := access.Request{SiteID: s.site.ID, Path: clean, Cookie: sessionCookieValue(r), Browser: acceptsHTML(r)}
	d, ok := e.decide(w, r, req)
	if !ok {
		return
	}
	if d.ClearCookie {
		w.Header().Add("Set-Cookie", ClearSessionCookie().String())
	}
	if d.Outcome != access.Allow {
		e.challenge(w, r, s.site, d)
		return
	}
	protected := d.Protected

	res, ok := resolve(s, strings.TrimPrefix(clean, "/"))
	if ok && res.redirect == "" && !sameDirectory(clean, res.file.Path) {
		// 404.html, the SPA index or a clean URL's page.html answers from
		// another path, which may sit behind a stricter policy: it must
		// allow the visitor too, or they get a plain 404 rather than its
		// content.
		req.Path = "/" + res.file.Path
		fd, allowed := e.decide(w, r, req)
		if !allowed {
			return
		}
		protected = protected || fd.Protected
		if fd.Outcome != access.Allow {
			ok = false
		}
	}
	switch {
	case !ok:
		w.Header().Set("Cache-Control", cacheControl(protected))
		http.NotFound(w, r)
	case res.redirect != "":
		// An absolute path: cleaning leaves no empty first segment, so it
		// cannot become a scheme-relative //host URL.
		target := res.redirect
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Cache-Control", cacheControl(protected))
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusMovedPermanently)
	default:
		e.serveFile(w, r, s, res.file, res.status, protected)
	}
}

// reserved reports whether name, a cleaned path without its leading slash,
// is under /.wardlock/ in any letter case, as deploy validation rejects.
func reserved(name string) bool {
	first, _, _ := strings.Cut(name, "/")
	return strings.EqualFold(first, ReservedPrefix[1:len(ReservedPrefix)-1])
}

func noSite(w http.ResponseWriter) {
	http.Error(w, "No site is published at this address.", http.StatusNotFound)
}

// cleanPath resolves dot segments and repeated slashes in an absolute
// request path, keeping a trailing slash.
func cleanPath(p string) string {
	if p == "" || p[0] != '/' {
		p = "/" + p
	}
	clean := path.Clean(p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean
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

// SiteLabel returns the site name for a canonical host exactly one label
// below base, so a SiteSource can tell base-domain sites from custom domains.
// ok is false for base itself, deeper names and other hosts.
func SiteLabel(host, base string) (name string, ok bool) {
	name, ok = strings.CutSuffix(host, "."+base)
	if !ok || name == "" || strings.Contains(name, ".") {
		return "", false
	}
	return name, true
}
