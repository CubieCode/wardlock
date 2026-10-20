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
// answers, and the abuse report form every site offers when the operator
// has set an abuse address.

// reportPath is the abuse report form on every site.
const reportPath = ReservedPrefix + "report"

// maxReportForm bounds a report form body: every field at its limit in
// the worst UTF-8 encoding, percent-encoded, with room for the names.
const maxReportForm = 3*4*(access.MaxReportDetails+access.MaxReportPage+access.MaxReportContact) + 4096

// pageCSP is the policy of Wardlock's own pages that post only to their
// own host: no scripts, inline styles, never framed.
const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// reportURL is the report form's address for a visitor on page next, or ""
// when reports are off and no link is shown.
func (e *Edge) reportURL(next string) string {
	if !e.access.ReportsEnabled() {
		return ""
	}
	return reportPath + "?" + url.Values{"page": {safeNext(next)}}.Encode()
}

// unavailableData is what the unavailable page shows.
type unavailableData struct {
	Site      string
	ReportURL string
}

// unavailablePage answers every request to a taken-down site, or a site
// whose owner is suspended: 403, never stored, never indexed, saying
// nothing about why or whose. Its deploys and files are untouched.
func (e *Edge) unavailablePage(w http.ResponseWriter, r *http.Request, clean string) {
	e.page(w, r, "unavailable.html", unavailableData{Site: canonicalHost(r.Host), ReportURL: e.reportURL(clean)}, http.StatusForbidden)
}

// reportData is what the report form shows.
type reportData struct {
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
	// Back is the page the reporter came from, on this host.
	Back string
}

// report answers /.wardlock/report: GET shows the form for the page in
// ?page=, a same-origin POST sends it. Without an abuse address it does
// not exist (404), as no link to it is shown.
func (e *Edge) report(w http.ResponseWriter, r *http.Request, site ServedSite) {
	if !e.access.ReportsEnabled() {
		http.NotFound(w, r)
		return
	}
	data := reportData{
		Site: canonicalHost(r.Host), Reasons: access.ReportReasons, MaxDetails: access.MaxReportDetails,
		Errors: map[string]string{},
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		data.Back = safeNext(r.URL.Query().Get("page"))
		data.Page = data.Site + data.Back
		e.page(w, r, "report.html", data, http.StatusOK)
	case http.MethodPost:
		// Like the login form: only a page on this host may send it.
		if !sameOrigin(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxReportForm)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		f := r.PostForm
		data.Back = safeNext(f.Get("back"))
		data.Page, data.Reason, data.Details, data.Contact = f.Get("page"), f.Get("reason"), f.Get("details"), f.Get("contact")
		err := e.access.SendReport(r.Context(), access.Report{
			SiteID: site.ID, SiteName: site.Name, Site: data.Site, IP: clientIP(r),
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
			status, data.Alone = http.StatusTooManyRequests, "Too many reports from this network. Try again in "+waitText(wait)+"."
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
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
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
