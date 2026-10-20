// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/mail"
)

// Abuse reports (A2.13): a visitor's report about a site, emailed to the
// operator's abuse address. The edge renders the form; this file checks,
// limits, cleans and sends it.

// Report reasons, in the order the form lists them, with the words the
// operator's email uses.
var ReportReasons = []ReportReason{
	{"phishing", "Phishing or a scam"},
	{"malware", "Malware or a harmful download"},
	{"illegal", "Illegal content"},
	{"child-abuse", "Sexual content involving a child"},
	{"copyright", "Copyright or trademark infringement"},
	{"other", "Something else"},
}

// ReportReason is one choice on the report form.
type ReportReason struct{ Value, Label string }

// Field limits, in characters.
const (
	MaxReportDetails = 2000
	MaxReportPage    = 2000
	MaxReportContact = 254
)

// ErrReportsOff means the operator has set no abuse address.
var ErrReportsOff = errors.New("abuse reports are not enabled")

// ReportFieldError is a report rejected because of one field; Message is
// safe to show the reporter.
type ReportFieldError struct{ Field, Message string }

func (e *ReportFieldError) Error() string { return "invalid report: " + e.Field }

// Report is a visitor's abuse report, as plain values.
type Report struct {
	SiteID, SiteName string
	// Site is the host the visitor reported from.
	Site string
	// IP is the client address, for the rate limits only: it is neither
	// sent nor logged.
	IP                             string
	Reason, Page, Details, Contact string
}

// ReportsEnabled reports whether visitors may report sites.
func (s *Service) ReportsEnabled() bool { return s.c.AbuseEmail != "" }

// SendReport checks a report, counts it against the per-IP, per-network
// and instance-wide limits and emails it to the abuse address in the
// background. Only a valid report is counted, so nobody can spend the
// limits with junk. Share-link tokens are removed from every field the
// reporter wrote. It returns ErrReportsOff, a *ReportFieldError or a
// *RateLimitError.
func (s *Service) SendReport(ctx context.Context, r Report) error {
	if !s.ReportsEnabled() {
		return ErrReportsOff
	}
	reason := ""
	for _, rr := range ReportReasons {
		if rr.Value == r.Reason {
			reason = rr.Label
		}
	}
	if reason == "" {
		return &ReportFieldError{"reason", "Choose what is wrong with the site."}
	}
	details := strings.TrimSpace(strings.ReplaceAll(r.Details, "\r\n", "\n"))
	if !utf8.ValidString(details) || utf8.RuneCountInString(details) > MaxReportDetails {
		return &ReportFieldError{"details", fmt.Sprintf("Keep the details to %d characters or fewer.", MaxReportDetails)}
	}
	page := strings.TrimSpace(r.Page)
	if !utf8.ValidString(page) || utf8.RuneCountInString(page) > MaxReportPage || strings.ContainsFunc(page, unicode.IsControl) {
		return &ReportFieldError{"page", "Enter the page's address on one line."}
	}
	contact := ""
	if c := strings.TrimSpace(r.Contact); c != "" {
		addr, ok := NormaliseAddress(c)
		if !ok || len(addr) > MaxReportContact {
			return &ReportFieldError{"contact", "Enter an email address like name@example.com, or leave it empty."}
		}
		contact = addr
	}
	if err := allowAttempt(s.ipAttempts, "report-ip:"+ipKey(r.IP), s.limits.ReportsPerIP); err != nil {
		return err
	}
	if err := allowAttempt(s.netAttempts, "report-net:"+netKey(r.IP), s.limits.ReportsPerNetwork); err != nil {
		return err
	}
	if err := allowAttempt(s.siteAttempts, "report-total", s.limits.ReportsTotal); err != nil {
		return err
	}
	msg, err := mail.AbuseReportMessage(netmail.Address{Address: s.c.AbuseEmail}, mail.AbuseReport{
		Site: r.Site, SiteID: r.SiteID, SiteName: r.SiteName, Reason: reason,
		Page: StripTokens(page), Details: StripTokens(details), Contact: contact, ReceivedAt: s.now(),
	})
	if err != nil {
		return err
	}
	if s.c.Mailer == nil {
		return errors.New("abuse report: no mailer configured")
	}
	siteID, kind := r.SiteID, r.Reason
	ctx = context.WithoutCancel(ctx)
	s.bg.Go(func() {
		ctx, cancel := context.WithTimeout(ctx, emailSendTimeout)
		defer cancel()
		if err := s.c.Mailer.Send(ctx, msg); err != nil {
			s.log.LogAttrs(ctx, slog.LevelError, "abuse report failed", slog.String("site_id", siteID), slog.String("error", err.Error()))
			return
		}
		s.log.LogAttrs(ctx, slog.LevelInfo, "abuse report sent", slog.String("site_id", siteID), slog.String("reason", kind))
	})
	return nil
}

var (
	// tokenShape matches a share-link or API token anywhere in text:
	// wl_<public id>_<secret>, however much of the secret was pasted.
	tokenShape = regexp.MustCompile(`wl_[A-Za-z0-9]{1,32}_[A-Za-z0-9_-]+`)
	// tokenParam matches a t= query parameter's value in a pasted URL.
	tokenParam = regexp.MustCompile(`([?&;]t=)[^&#\s]*`)
)

// StripTokens removes share-link tokens a reporter pasted, in a URL's t=
// parameter or on their own, so a report never hands the operator (or
// their mail provider) a working link.
func StripTokens(s string) string {
	s = tokenParam.ReplaceAllString(s, "${1}[removed]")
	return tokenShape.ReplaceAllString(s, "[token removed]")
}
