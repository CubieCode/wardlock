// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wardlock.nz/server/internal/access"
)

// OAuth and OIDC at the edge (A2.5). The flow starts from the site's login
// form, which sets a binding cookie on the site host and sends the visitor
// to the provider. The provider returns to the auth origin's
// /callback/<provider>, which never sets a cookie: it sends the visitor on
// to the site's /.wardlock/callback with a single-use code, and the site
// host exchanges it for the session cookie.

// authPage is what the auth origin's error page shows.
type authPage struct {
	Title   string
	Message string
	// Return is the site's login page to try again from, when known.
	Return string
}

// serveAuthOrigin answers the auth origin. Only GET /callback/<provider>
// exists; everything else is a plain 404. Nothing here sets a cookie.
func (e *Edge) serveAuthOrigin(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	protectedHeaders(h)
	provider, ok := strings.CutPrefix(r.URL.Path, "/callback/")
	if !ok || provider == "" || strings.Contains(provider, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		h.Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	done, err := e.access.FinishOAuth(r.Context(), access.OAuthCallback{
		Provider: provider, State: q.Get("state"), Code: q.Get("code"), Error: q.Get("error"),
	})
	if err == nil {
		h.Set("Location", done.URL)
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	page := authPage{Title: "Sign-in did not complete", Return: done.Return}
	status := http.StatusBadRequest
	var limited *access.RateLimitError
	switch {
	case errors.Is(err, access.ErrOAuthState):
		page.Message = "This sign-in has expired or was already used. Go back to the site and sign in again."
	case errors.Is(err, access.ErrOAuthDenied):
		page.Message = "The sign-in was cancelled or refused by the provider."
	case errors.Is(err, access.ErrNoVerifiedEmail):
		status = http.StatusForbidden
		page.Message = "Your account has no verified email address. Verify one with the provider, then try again."
	case errors.Is(err, access.ErrNotAllowed):
		status = http.StatusForbidden
		page.Title = "No access"
		page.Message = "This account is not allowed to view this site. Ask the site's owner for access, or sign in with another account."
	case errors.Is(err, access.ErrOAuthDisabled):
		status = http.StatusForbidden
		page.Message = "This sign-in method is no longer available for this page."
	case errors.Is(err, access.ErrOAuthFailed):
		status = http.StatusBadGateway
		page.Message = "The provider could not confirm who you are. Try again."
	case errors.As(err, &limited):
		status = http.StatusTooManyRequests
		h.Set("Retry-After", strconv.Itoa(int(max(time.Second, limited.RetryAfter.Round(time.Second)).Seconds())))
		page.Message = "Too many sign-ins are in progress. Try again shortly."
	default:
		e.log.ErrorContext(r.Context(), "finish oauth sign-in", slog.String("provider", provider), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	e.renderPage(w, r, "auth.html", page, status)
}

// renderPage renders one of Wardlock's own pages: never cached, never
// framed, no scripts.
func (e *Edge) renderPage(w http.ResponseWriter, r *http.Request, name string, data any, status int) {
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, name, data); err != nil {
		e.log.ErrorContext(r.Context(), "render page", slog.String("page", name), slog.Any("error", err))
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

// oauthLogin starts a provider sign-in from the login form: it sets the
// binding cookie on this host and sends the visitor to the provider.
func (e *Edge) oauthLogin(w http.ResponseWriter, r *http.Request, site ServedSite, next string) {
	started, err := e.access.StartOAuth(r.Context(), access.OAuthStart{
		SiteID: site.ID, Host: canonicalHost(r.Host), Path: next,
		Provider: r.PostForm.Get("provider"), IP: clientIP(r),
	})
	if err == nil {
		h := w.Header()
		h.Add("Set-Cookie", oauthBindingCookie(started.Binding).String())
		h.Set("Location", started.URL)
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	status, msg := http.StatusBadRequest, "That sign-in method is not available here."
	var limited *access.RateLimitError
	switch {
	case errors.As(err, &limited):
		wait := max(time.Second, limited.RetryAfter.Round(time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		status, msg = http.StatusTooManyRequests, "Too many sign-in attempts. Try again in "+waitText(wait)+"."
	case errors.Is(err, access.ErrOAuthFailed):
		e.log.WarnContext(r.Context(), "start oauth sign-in", slog.String("site_id", site.ID), slog.Any("error", err))
		status, msg = http.StatusBadGateway, "The sign-in provider is not answering. Try again shortly."
	case !errors.Is(err, access.ErrUnknownProvider) && !errors.Is(err, access.ErrOAuthDisabled):
		e.log.ErrorContext(r.Context(), "start oauth sign-in", slog.String("site_id", site.ID), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	e.loginAgain(w, r, site, next, msg, status)
}

// oauthCallback exchanges a site code for the session cookie. The binding
// cookie is cleared whatever happens; a refused code shows the login page
// again.
func (e *Edge) oauthCallback(w http.ResponseWriter, r *http.Request, site ServedSite) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var binding string
	if c, err := r.Cookie(access.OAuthBindingCookieName); err == nil {
		binding = c.Value
	}
	w.Header().Add("Set-Cookie", clearOAuthBindingCookie().String())
	got, err := e.access.RedeemOAuth(r.Context(), access.OAuthRedeem{
		SiteID: site.ID, Host: canonicalHost(r.Host), Code: r.URL.Query().Get("code"), Binding: binding,
	})
	if err == nil {
		h := w.Header()
		h.Add("Set-Cookie", SessionCookie(got.Cookie).String())
		h.Set("Location", (&url.URL{Path: got.Next}).EscapedPath())
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	if !errors.Is(err, access.ErrOAuthCode) {
		e.log.ErrorContext(r.Context(), "redeem oauth code", slog.String("site_id", site.ID), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	e.loginAgain(w, r, site, "/", "That sign-in has expired or was already used. Sign in again.", http.StatusBadRequest)
}

// oauthBindingCookie is the Set-Cookie for a flow's binding cookie, with
// the session cookie's attributes: host-only, Path=/, Secure, HttpOnly,
// SameSite=Lax (the callback arrives as a top-level GET from the auth
// origin, which Lax allows).
func oauthBindingCookie(c access.Cookie) *http.Cookie {
	return &http.Cookie{
		Name: c.Name, Value: c.Value, Path: "/", Expires: c.Expires,
		MaxAge: maxAge(c.Expires),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
}

func clearOAuthBindingCookie() *http.Cookie {
	return &http.Cookie{
		Name: access.OAuthBindingCookieName, Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
}

// loginAgain shows next's login page with an OAuth notice.
func (e *Edge) loginAgain(w http.ResponseWriter, r *http.Request, site ServedSite, next, msg string, status int) {
	d, ok := e.decide(w, r, access.Request{SiteID: site.ID, Path: next})
	if !ok {
		return
	}
	data := newLoginData(r, d, next)
	data.OAuthError = msg
	e.loginPage(w, r, data, status)
}

// formAction is the login page's CSP form-action. Browsers apply it to a
// form's whole redirect chain, so a page with OAuth buttons also allows the
// origins pressing one may pass through: each provider's authorisation
// endpoint and the auth origin. Nothing else is ever allowed, so the
// password and email forms keep posting only to the page's own origin.
func formAction(providers []access.ProviderInfo) string {
	out := "'self'"
	seen := map[string]bool{}
	for _, p := range providers {
		for _, o := range p.Origins {
			if cspOrigin(o) && !seen[o] {
				seen[o] = true
				out += " " + o
			}
		}
	}
	return out
}

// cspOrigin reports whether o is a plain http(s) origin, safe to put in a
// CSP source list as is.
func cspOrigin(o string) bool {
	rest, ok := strings.CutPrefix(o, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(o, "http://")
	}
	return ok && rest != "" && !strings.ContainsFunc(rest, func(r rune) bool { return !hostChar(r) })
}

// hostChar reports whether r may appear in a host and port.
func hostChar(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune(".-:[]", r)
}
