// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"wardlock.nz/server/internal/access"
)

//go:embed templates/*.html
var templateFS embed.FS

var pages = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// public is the Access used without a policy service: every site is public.
type public struct{}

func (public) Decide(_ context.Context, r access.Request) (access.Decision, error) {
	return access.Decision{Outcome: access.Allow, Via: access.ViaPublic}, nil
}

func (public) EndSession(context.Context, string, string) error { return nil }

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
	}
	return d, true
}

// protectedHeaders are SPEC "Protected-site response headers", less
// Cache-Control, which each response sets.
func protectedHeaders(h http.Header) {
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Referrer-Policy", "no-referrer")
}

// challenge answers a request the policy did not allow: the login page for
// browsers, a bare 401 for everything else. Neither says whether the path
// exists.
func (e *Edge) challenge(w http.ResponseWriter, r *http.Request, site ServedSite, d access.Decision) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	if d.Outcome == access.Login {
		e.loginPage(w, r, site, d.Policy.LinksEnabled, http.StatusUnauthorized)
		return
	}
	// Share-link tokens are also accepted as Bearer credentials (A2.2).
	h.Set("WWW-Authenticate", `Bearer realm="`+site.Name+`"`)
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("This site is protected.\n"))
	}
}

// loginData is what the login page shows. A2.3, A2.4 and A2.5 add their
// forms (password, email code, OAuth providers) as fields here and blocks
// in templates/login.html.
type loginData struct {
	Site  string
	Links bool
}

// loginPage renders the site's login page with status. It is Wardlock's
// page, not the site's: never cached, never framed, no scripts.
func (e *Edge) loginPage(w http.ResponseWriter, r *http.Request, site ServedSite, links bool, status int) {
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, "login.html", loginData{Site: site.Name, Links: links}); err != nil {
		e.log.ErrorContext(r.Context(), "render login page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}

// serveReserved answers /.wardlock/* on a site. Policies never apply here,
// so health and sign-in work whatever the site's policy says. Callbacks
// and the share-link interstitial join in later A2 items.
func (e *Edge) serveReserved(w http.ResponseWriter, r *http.Request, site ServedSite, clean string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	protectedHeaders(h)
	switch clean {
	case ReservedPrefix + "health":
		h.Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	case ReservedPrefix + "login":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		d, ok := e.decide(w, r, access.Request{SiteID: site.ID, Path: "/"})
		if !ok {
			return
		}
		e.loginPage(w, r, site, d.Policy.LinksEnabled, http.StatusOK)
	case ReservedPrefix + "logout":
		e.logout(w, r, site)
	default:
		http.NotFound(w, r)
	}
}

// logout ends the visitor's site session and clears its cookie. POST only,
// so a link or image elsewhere cannot sign visitors out.
func (e *Edge) logout(w http.ResponseWriter, r *http.Request, site ServedSite) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
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
