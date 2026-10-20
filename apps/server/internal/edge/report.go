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
	"time"
	"unicode/utf8"

	"wardlock.nz/server/internal/access"
)

// Operator controls on the edge (A2.13): the page a taken-down site
// answers, and the abuse report form the dashboard host serves for every
// site when the operator has set an abuse address.

// ReportPath is the abuse report form on the dashboard host. It is served
// there, not on the site, so a reported site's own scripts (which run on
// its origin, service workers included) can neither read, script, fake nor
// flood it.
const ReportPath = "/report"

// maxReportForm bounds a report form body: every field at its limit in
// the worst UTF-8 encoding, percent-encoded, with room for the names.
const maxReportForm = 3*4*(access.MaxReportDetails+access.MaxReportPage+access.MaxReportContact) + 4096

// pageCSP is the policy of Wardlock's own pages that post only to their
// own host: no scripts, inline styles, never framed.
const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// reportURL is the report form's address for a visitor on page next of
// the site at host, or "" when reports are off and no link is shown.
func (e *Edge) reportURL(host, next string) string {
	if !e.access.ReportsEnabled() || e.cfg.DashboardOrigin == "" {
		return ""
	}
	return e.cfg.DashboardOrigin + ReportPath + "?" + url.Values{"site": {host}, "page": {safeNext(next)}}.Encode()
}

// unavailableData is what the unavailable page shows.
type unavailableData struct {
	Site      string
	ReportURL string
}

// unavailablePage answers every request to a taken-down site, or a site
// whose owner is suspended: 403, never stored, never indexed, saying
// nothing about why or whose. Its deploys and files are untouched.
// Clear-Site-Data drops what the site left in the browser before it was
// taken down, so a service worker or cached page of its own cannot go on
// answering in its place; cookies are left alone (SECURITY-MODEL "Public
// instances and abuse").
func (e *Edge) unavailablePage(w http.ResponseWriter, r *http.Request, clean string) {
	host := canonicalHost(r.Host)
	w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	e.page(w, r, "unavailable.html", unavailableData{Site: host, ReportURL: e.reportURL(host, clean)}, http.StatusForbidden)
}

// reportData is what the report form shows.
type reportData struct {
	// Site is the reported site's host.
	Site    string
	Reasons []access.ReportReason
	// The reporter's entries, shown again after a refused attempt.
	Page, Reason, Details, Contact string
	// Errors holds a message per field; Alone a message above the form.
	Errors map[string]string
	Alone  string
	// Sent replaces the form with the thank-you message.
	Sent       bool
	MaxDetails int
	// Back is the path on the site the reporter came from; BackURL is it
	// on the site's origin.
	Back, BackURL string
}

// report answers /report on the dashboard host: GET shows the form for
// the site in ?site= and its page in ?page=, a same-origin POST sends it.
// The form exists only for a host that serves a published site, taken
// down or not; any other host answers the unknown-host 404, so the form
// says no more about a name than visiting it does. Without an abuse
// address it does not exist at all, as no link to it is shown.
func (e *Edge) report(w http.ResponseWriter, r *http.Request) {
	if !e.access.ReportsEnabled() {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	var host string
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		host = r.URL.Query().Get("site")
	case http.MethodPost:
		// Like the login form: only the form on this host may send it.
		if !sameOrigin(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxReportForm)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		host = r.PostForm.Get("site")
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	site, ok := e.reportedSite(r, host)
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		noSite(w)
		return
	}
	host = canonicalHost(host)
	data := reportData{
		Site: host, Reasons: access.ReportReasons, MaxDetails: access.MaxReportDetails, Errors: map[string]string{},
	}
	if r.Method != http.MethodPost {
		data.Back = safeNext(r.URL.Query().Get("page"))
		data.BackURL = e.siteURL(host, data.Back)
		data.Page = host + data.Back
		e.page(w, r, "report.html", data, http.StatusOK)
		return
	}
	f := r.PostForm
	data.Back = safeNext(f.Get("back"))
	data.BackURL = e.siteURL(host, data.Back)
	data.Page, data.Reason, data.Details, data.Contact = f.Get("page"), f.Get("reason"), f.Get("details"), f.Get("contact")
	err := e.access.SendReport(r.Context(), access.Report{
		SiteID: site.ID, SiteName: site.Name, Site: host, IP: clientIP(r),
		Reason: data.Reason, Page: data.Page, Details: data.Details, Contact: data.Contact,
	})
	var (
		field   *access.ReportFieldError
		limited *access.RateLimitError
	)
	status := http.StatusOK
	switch {
	case err == nil:
		data.Sent = true
	case errors.As(err, &field):
		status = http.StatusBadRequest
		data.Errors[field.Field] = field.Message
	case errors.As(err, &limited):
		wait := max(time.Second, limited.RetryAfter.Round(time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		status, data.Alone = http.StatusTooManyRequests, "Too many reports have been sent. Try again in "+waitText(wait)+"."
	default:
		e.log.ErrorContext(r.Context(), "abuse report", slog.String("site_id", site.ID), slog.Any("error", err))
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	// Entries that were too long or not text are not shown again.
	for _, v := range []*string{&data.Page, &data.Details, &data.Contact} {
		if !utf8.ValidString(*v) || len(*v) > 4*access.MaxReportDetails {
			*v = ""
		}
	}
	e.page(w, r, "report.html", data, status)
}

// reportedSite finds the published site served at host, as a visitor's
// request to host would: the dashboard and auth hosts, unknown hosts and
// sites with nothing published are not found.
func (e *Edge) reportedSite(r *http.Request, host string) (ServedSite, bool) {
	host = canonicalHost(host)
	if host == "" || len(host) > 253 || host == e.cfg.DashboardHost || host == e.cfg.AuthHost {
		return ServedSite{}, false
	}
	s, err := e.cache.lookup(r.Context(), host)
	if err != nil {
		if !errors.Is(err, ErrNoSite) {
			e.log.ErrorContext(r.Context(), "look up reported site", slog.Any("error", err))
		}
		return ServedSite{}, false
	}
	if s.site.ActiveDeployID == "" {
		return ServedSite{}, false
	}
	return s.site, true
}

// siteURL is path on the site at host, for the link back to it.
func (e *Edge) siteURL(host, path string) string {
	if e.cfg.SiteOrigin == nil {
		return path
	}
	return e.cfg.SiteOrigin(host) + path
}

// page renders one of Wardlock's own pages with status: never stored,
// never framed, no scripts, not indexed, no referrer.
func (e *Edge) page(w http.ResponseWriter, r *http.Request, name string, data any, status int) {
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, name, data); err != nil {
		e.log.ErrorContext(r.Context(), "render page", slog.String("page", name), slog.Any("error", err))
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	protectedHeaders(h)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", pageCSP)
	h.Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}
