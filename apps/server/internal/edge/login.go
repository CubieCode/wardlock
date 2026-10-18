// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/access"
)

// maxLoginForm bounds a sign-in form body: a password of
// access.MaxPasswordBytes, percent-encoded, plus the other fields.
const maxLoginForm = 4*access.MaxPasswordBytes + 4096

// loginData is what the login page shows. Each sign-in method adds its
// fields here and one <section class="method"> in templates/login.html,
// whose form posts to /.wardlock/login with a "method" field naming it;
// login dispatches on that field.
type loginData struct {
	// Site is the host the visitor asked for.
	Site string
	// Next is the cleaned path the visitor is signing in for; forms carry
	// it back and success returns there.
	Next  string
	Links bool
	// Password shows the password form (A2.3); PasswordError is the
	// message under it after a failed attempt, or above the methods when
	// the page's policy has no password form to put it under.
	Password      bool
	PasswordError string
	// Email shows the email-code form (A2.4): first the address, then,
	// once EmailSent, the code sent to EmailAddress (as the visitor typed
	// it, whether or not it may sign in). EmailError is the message under
	// the form, or above the methods when the page's policy has no email
	// form to put it under.
	Email        bool
	EmailSent    bool
	EmailAddress string
	EmailError   string
	// EmailMinutes is how long a code is valid.
	EmailMinutes int
}

func newLoginData(r *http.Request, d access.Decision, next string) loginData {
	return loginData{
		Site: canonicalHost(r.Host), Next: next,
		Links: d.Policy.LinksEnabled, Password: d.Policy.PasswordEnabled,
		Email: d.Policy.EmailEnabled, EmailMinutes: int(access.EmailCodeValidity / time.Minute),
	}
}

// AnyMethod reports whether the page offers any way in.
func (l loginData) AnyMethod() bool { return l.Links || l.Password || l.Email }

// AloneError is the message for a failed method the page has no form for,
// shown above the methods.
func (l loginData) AloneError() string {
	switch {
	case l.PasswordError != "" && !l.Password:
		return l.PasswordError
	case l.EmailError != "" && !l.Email:
		return l.EmailError
	}
	return ""
}

// loginPage renders the site's login page with status. It is Wardlock's
// page, not the site's: never cached, never framed, no scripts.
func (e *Edge) loginPage(w http.ResponseWriter, r *http.Request, data loginData, status int) {
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, "login.html", data); err != nil {
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

// login answers /.wardlock/login: GET and HEAD show the page for the path
// in ?next=, POST runs the sign-in method the form names.
func (e *Edge) login(w http.ResponseWriter, r *http.Request, site ServedSite) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		next := safeNext(r.URL.Query().Get("next"))
		d, ok := e.decide(w, r, access.Request{SiteID: site.ID, Path: next})
		if !ok {
			return
		}
		e.loginPage(w, r, newLoginData(r, d, next), http.StatusOK)
	case http.MethodPost:
		// Only a form on this host may sign visitors in, so another site
		// cannot sign a visitor into a session of its choosing.
		if !sameOrigin(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxLoginForm)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		switch r.PostForm.Get("method") {
		case "password":
			e.passwordLogin(w, r, site, safeNext(r.PostForm.Get("next")))
		case "email":
			e.emailLogin(w, r, site, safeNext(r.PostForm.Get("next")))
		default:
			http.Error(w, "Bad Request", http.StatusBadRequest)
		}
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// passwordLogin checks the form's password for next's policy. Success sets
// the session cookie and returns to next; a wrong password or a policy
// without one shows the page again with the same message, and too many
// attempts answer 429 with Retry-After. The password is never logged.
func (e *Edge) passwordLogin(w http.ResponseWriter, r *http.Request, site ServedSite, next string) {
	c, err := e.access.PasswordLogin(r.Context(), access.PasswordAttempt{
		SiteID: site.ID, Path: next, IP: clientIP(r), Password: r.PostForm.Get("password"),
	})
	if err == nil {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Add("Set-Cookie", SessionCookie(c).String())
		h.Set("Location", (&url.URL{Path: next}).EscapedPath())
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	status, msg := http.StatusUnauthorized, "That password is not right. Check it and try again."
	var limited *access.RateLimitError
	switch {
	case errors.As(err, &limited):
		wait := max(time.Second, limited.RetryAfter.Round(time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		status, msg = http.StatusTooManyRequests, fmt.Sprintf("Too many attempts. Try again in %s.", waitText(wait))
	case !errors.Is(err, access.ErrWrongPassword):
		e.log.ErrorContext(r.Context(), "password sign-in", slog.String("site_id", site.ID), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	d, ok := e.decide(w, r, access.Request{SiteID: site.ID, Path: next})
	if !ok {
		return
	}
	data := newLoginData(r, d, next)
	data.PasswordError = msg
	e.loginPage(w, r, data, status)
}

// maxAddressField bounds the address echoed back on the page.
const maxAddressField = 254

// emailLogin runs a step of the email-code form: "send" emails a code to
// the form's address for next's policy, "verify" checks the code entered.
// Sending answers the same page and status whether or not the address may
// sign in, so the allowlist cannot be probed; a wrong, expired or used-up
// code shows the code form again with one message and 401; too many
// requests answer 429 with Retry-After. Success sets the session cookie and
// returns to next. Neither the address nor the code is logged.
func (e *Edge) emailLogin(w http.ResponseWriter, r *http.Request, site ServedSite, next string) {
	addr := r.PostForm.Get("email")
	if len(addr) > maxAddressField || !utf8.ValidString(addr) {
		addr = ""
	}
	var (
		err    error
		status = http.StatusOK
		data   func(access.Decision) loginData
	)
	switch r.PostForm.Get("step") {
	case "send":
		err = e.access.SendEmailCode(r.Context(), access.EmailCodeRequest{
			SiteID: site.ID, Site: canonicalHost(r.Host), Path: next, IP: clientIP(r), Address: addr,
		})
		data = func(d access.Decision) loginData {
			l := newLoginData(r, d, next)
			l.EmailAddress, l.EmailSent = addr, err == nil
			return l
		}
	case "verify":
		var c access.Cookie
		c, err = e.access.EmailCodeLogin(r.Context(), access.EmailCodeAttempt{
			SiteID: site.ID, Path: next, IP: clientIP(r), Address: addr, Code: strings.TrimSpace(r.PostForm.Get("code")),
		})
		if err == nil {
			h := w.Header()
			h.Set("Cache-Control", "no-store")
			h.Add("Set-Cookie", SessionCookie(c).String())
			h.Set("Location", (&url.URL{Path: next}).EscapedPath())
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		data = func(d access.Decision) loginData {
			l := newLoginData(r, d, next)
			l.EmailAddress, l.EmailSent = addr, true
			return l
		}
	default:
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	msg := ""
	var limited *access.RateLimitError
	switch {
	case err == nil:
	case errors.As(err, &limited):
		wait := max(time.Second, limited.RetryAfter.Round(time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		status, msg = http.StatusTooManyRequests, fmt.Sprintf("Too many attempts. Try again in %s.", waitText(wait))
	case errors.Is(err, access.ErrInvalidAddress):
		status, msg = http.StatusBadRequest, "Enter an email address like name@example.com."
	case errors.Is(err, access.ErrInvalidCode):
		status, msg = http.StatusUnauthorized, "That code is not right, or it has expired. Check it, or send a new code."
	default:
		e.log.ErrorContext(r.Context(), "email-code sign-in", slog.String("site_id", site.ID), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	d, ok := e.decide(w, r, access.Request{SiteID: site.ID, Path: next})
	if !ok {
		return
	}
	l := data(d)
	if !l.Email && msg == "" {
		// The policy has no email form to show the sent state in.
		status, msg = http.StatusBadRequest, "Email codes are not available for this page."
	}
	l.EmailError = msg
	e.loginPage(w, r, l, status)
}

// waitText says how long to wait in whole minutes, or seconds under one.
func waitText(d time.Duration) string {
	if d < time.Minute {
		return "a moment"
	}
	m := int((d + time.Minute - 1) / time.Minute)
	if m == 1 {
		return "1 minute"
	}
	return strconv.Itoa(m) + " minutes"
}

// safeNext returns raw as a path on this site to return to after signing
// in, or "/" when it is not a plain absolute path: no scheme or host, no
// backslash or control character (browsers read /\host as //host), no
// query or fragment, and not under /.wardlock/.
func safeNext(raw string) string {
	if raw == "" || raw[0] != '/' || strings.HasPrefix(raw, "//") ||
		strings.ContainsAny(raw, `\?#`) || strings.ContainsFunc(raw, unicode.IsControl) {
		return "/"
	}
	clean := cleanPath(raw)
	if reserved(strings.TrimPrefix(clean, "/")) {
		return "/"
	}
	return clean
}
