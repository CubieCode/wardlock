// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import "net/http"

// Primary hostname (C1.18): a site with a primary answers a request on any
// other of its hostnames with 308 to the same path and query there. It
// comes before the access decision: hostnames are public through DNS, and
// site sessions are host-only, so signing in on the wrong host would be
// wasted. /.wardlock/ is never redirected, so sign-in, health and tls-ask
// keep working on every hostname.

// redirectsToPrimary reports whether a request for site at host goes to
// the site's primary hostname instead. Every exemption by host belongs
// here: a preview host (C1.19) is never redirected, since it serves
// another deploy than the primary does.
func redirectsToPrimary(site ServedSite, host string) bool {
	return site.PrimaryHost != "" && host != site.PrimaryHost && site.Preview == ""
}

// toPrimary answers 308 to the request's path and query, as received, on
// site's primary hostname. A share-link token in the query goes with it:
// both hostnames serve the same site, and the primary checks it again
// (SECURITY-MODEL "Primary hostname"). The answer carries nothing but the
// hostname, and is never stored, so a later change of primary takes effect
// at once.
func (e *Edge) toPrimary(w http.ResponseWriter, r *http.Request, site ServedSite) {
	target := r.URL.EscapedPath()
	if target == "" || target[0] != '/' {
		target = "/" // OPTIONS * and the like
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	h := w.Header()
	h.Set("Location", e.siteOrigin(site.PrimaryHost)+target)
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusPermanentRedirect)
}

// siteOrigin is the public origin of a site host, without a trailing slash.
func (e *Edge) siteOrigin(host string) string {
	if e.cfg.SiteOrigin == nil {
		return "https://" + host
	}
	return e.cfg.SiteOrigin(host)
}
