// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"embed"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"wardlock.nz/server/internal/access"
)

//go:embed templates/*.html
var templateFS embed.FS

var pages = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// decide asks Access about r and sets the protected-site headers when the
// governing policy is protected. ok is false when it has already answered
// 500.
func (e *Edge) decide(w http.ResponseWriter, r *http.Request, req access.Request) (access.Decision, bool) {
	d, err := e.access.Decide(r.Context(), req)
	if err != nil {
		e.log.ErrorContext(r.Context(), "decide access", slog.String("site_id", req.SiteID), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return access.Decision{}, false
	}
	if d.Protected {
		protectedHeaders(w.Header())
		// The answer depends on the session cookie, so no cache may hand
		// one visitor's response to another.
		addVary(w.Header(), "Cookie")
	}
	return d, true
}

// protectedHeaders are SPEC "Protected-site response headers", less
// Cache-Control, which each response sets.
func protectedHeaders(h http.Header) {
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Referrer-Policy", "no-referrer")
}

// addVary adds field to Vary unless it is there already.
func addVary(h http.Header, field string) {
	for _, v := range h.Values("Vary") {
		for f := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), field) {
				return
			}
		}
	}
	h.Add("Vary", field)
}

// challenge answers a request the policy did not allow: the login page for
// browsers, a bare 401 for everything else. Neither says whether the path
// exists.
func (e *Edge) challenge(w http.ResponseWriter, r *http.Request, site ServedSite, d access.Decision) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	// Browsers get the page, other clients the bare 401.
	addVary(h, "Accept")
	if d.Outcome == access.Login {
		e.loginPage(w, r, newLoginData(r, d, cleanPath(r.URL.Path)), http.StatusUnauthorized)
		return
	}
	// Share-link tokens are also accepted as Bearer credentials.
	h.Set("WWW-Authenticate", `Bearer realm="`+site.Name+`"`)
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("This site is protected.\n"))
	}
}

// serveReserved answers /.wardlock/* on a site. Policies never apply here,
// so health, sign-in and the share-link interstitial's button work
// whatever the site's policy says. Callbacks join in later A2 items.
func (e *Edge) serveReserved(w http.ResponseWriter, r *http.Request, site ServedSite, clean string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	protectedHeaders(h)
	switch clean {
	case ReservedPrefix + "health":
		h.Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	case ReservedPrefix + "login":
		e.login(w, r, site)
	case ReservedPrefix + "callback":
		e.oauthCallback(w, r, site)
	case ReservedPrefix + "logout":
		e.logout(w, r, site)
	case ReservedPrefix + "view":
		e.view(w, r, site)
	default:
		http.NotFound(w, r)
	}
}

// logout ends the visitor's site session and clears its cookie. POST only,
// and only from a page on the same host, so a link, image or form on
// another site (a sibling site included) cannot sign visitors out.
func (e *Edge) logout(w http.ResponseWriter, r *http.Request, site ServedSite) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if v := sessionCookieValue(r); v != "" {
		if err := e.access.EndSession(r.Context(), site.ID, v); err != nil {
			e.log.ErrorContext(r.Context(), "end site session", slog.String("site_id", site.ID), slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Add("Set-Cookie", ClearSessionCookie().String())
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}

// SessionCookie is the Set-Cookie for a site session: host-only (no
// Domain), Path=/, Secure, HttpOnly, SameSite=Lax, as the __Host- prefix
// requires. Local *.localhost mode keeps Secure: browsers treat it as a
// secure context.
func SessionCookie(c access.Cookie) *http.Cookie {
	return &http.Cookie{
		Name: c.Name, Value: c.Value, Path: "/", Expires: c.Expires,
		MaxAge: max(1, int(time.Until(c.Expires).Seconds())),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
}

// ClearSessionCookie deletes the site session cookie.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name: access.SessionCookieName, Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
}

// sameOrigin reports whether an unsafe request comes from a page on its own
// host. Sec-Fetch-Site, when sent, must be same-origin, and an Origin, when
// it names one, must be the request's host. "null" counts as no Origin:
// Wardlock's pages send Referrer-Policy: no-referrer, under which browsers
// post forms with Origin: null even to their own origin, and rely on
// Sec-Fetch-Site there. A request with neither header is refused. The
// scheme is not compared, since behind a proxy the edge may see plain HTTP.
func sameOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && u.Host != "" && canonicalHost(u.Host) == canonicalHost(r.Host) && samePort(u, r.Host)
	}
	return site == "same-origin"
}

// samePort reports whether origin's port matches the request host's. A
// Host without a port (as a proxy forwards it) matches only the scheme's
// default port.
func samePort(origin *url.URL, reqHost string) bool {
	op := origin.Port()
	if op == "" {
		op = map[string]string{"http": "80", "https": "443"}[origin.Scheme]
	}
	_, port, err := net.SplitHostPort(reqHost)
	if err != nil {
		return op == "80" || op == "443"
	}
	return op == port
}

// sessionCookieValue returns the site session cookie's value. Only the
// __Host- name counts, so a cookie tossed from a sibling domain (which
// cannot carry the prefix) is never read.
func sessionCookieValue(r *http.Request) string {
	c, err := r.Cookie(access.SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// acceptsHTML reports whether the client asks for HTML, as browsers
// navigating do; it decides between the login page and a bare 401.
func acceptsHTML(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// sameDirectory reports whether file (a manifest path) answers reqPath (a
// cleaned request path) as itself or as its directory's index.html, so the
// policy that allowed reqPath also covers file. Policy prefixes end in "/",
// so a directory and its index share one.
func sameDirectory(reqPath, file string) bool {
	f := "/" + file
	return f == reqPath || (strings.HasSuffix(reqPath, "/") && f == reqPath+"index.html")
}
