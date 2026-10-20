// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	netmail "net/mail"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// Parsed once; templates are read-only after parsing.
var (
	textTemplates = texttemplate.Must(texttemplate.New("").
			Funcs(texttemplate.FuncMap{"minutes": minutes, "days": days, "quote": quote}).
			ParseFS(templateFS, "templates/*.txt.tmpl"))
	htmlTemplates = htmltemplate.Must(htmltemplate.New("").ParseFS(templateFS, "templates/*.html.tmpl"))
)

// SignIn is the data for an owner's dashboard sign-in code (C1.1).
type SignIn struct {
	Code string
	// Host is the dashboard host the code signs in to.
	Host     string
	ValidFor time.Duration
}

// EmailCode is the data for a visitor's code to open a protected site
// (A2.4).
type EmailCode struct {
	Code string
	// Site is the host name of the site the code opens.
	Site     string
	ValidFor time.Duration
}

// Invite is the data for an invitation to become an owner (C1.1). URL is a
// one-time sign-in link.
type Invite struct {
	URL string
	// Host is the dashboard host the link signs in to.
	Host string
	// InvitedBy is the inviting owner's address.
	InvitedBy string
	ValidFor  time.Duration
}

// AbuseReport is a visitor's report about a site, for the operator
// (A2.13). Every field but SiteID, SiteName and ReceivedAt is the
// reporter's words, with share-link tokens already removed.
type AbuseReport struct {
	// Site is the host the report was made on; SiteName the site's name.
	Site, SiteID, SiteName string
	Reason                 string
	Page, Details, Contact string
	ReceivedAt             time.Time
}

// Screening is a content-screening verdict for the operator (A2.15): a
// file withheld ("quarantined") or flagged for review.
type Screening struct {
	SHA256     string
	Status     string
	Categories []string
	// Minors means the endpoint flagged sexual content involving minors:
	// the message says to preserve and report rather than review.
	Minors    bool
	Uses      []ScreeningUse
	CheckedAt time.Time
}

// ScreeningUse is a site serving a screened file: the site's name, the
// file's path and the owner's address.
type ScreeningUse struct {
	Site, Path, Owner string
}

// Withheld tells an owner which of their files content screening withheld
// (A2.15). It never says why: the categories stay with the operator.
type Withheld struct {
	// Site is the site's name; Host the dashboard host.
	Site, Host string
	Paths      []string
	// Contact is the operator's abuse address; "" when there is none.
	Contact string
}

// layout is the HTML body shared by every code message.
type layout struct {
	Title, Lead, Code, Where, Ignore string
	Minutes                          int
}

// SignInMessage builds the sign-in code email. The subject never contains
// the code, because the dev sink logs subjects.
func SignInMessage(to netmail.Address, d SignIn) (Message, error) {
	return render(to, "Your Wardlock sign-in code", "signin.txt.tmpl", d, layout{
		Title:   "Your Wardlock sign-in code",
		Lead:    "Your Wardlock sign-in code for " + d.Host + " is:",
		Code:    d.Code,
		Minutes: minutes(d.ValidFor),
		Where:   "Enter it on the sign-in page that asked for it.",
		Ignore:  "If you did not try to sign in, you can ignore this email. Nobody can sign in without the code.",
	})
}

// EmailCodeMessage builds the visitor email-code message.
func EmailCodeMessage(to netmail.Address, d EmailCode) (Message, error) {
	return render(to, "Your code for "+d.Site, "emailcode.txt.tmpl", d, layout{
		Title:   "Your code for " + d.Site,
		Lead:    "Your code to open " + d.Site + " is:",
		Code:    d.Code,
		Minutes: minutes(d.ValidFor),
		Where:   "Enter it on the page that asked for it.",
		Ignore:  "If you did not ask for this code, you can ignore this email.",
	})
}

// linkLayout is the HTML body of a message carrying a one-time link.
type linkLayout struct {
	Title, Lead, Action, URL, Expiry, Ignore string
}

// InviteMessage builds the invitation email. The subject never contains
// the link, because the dev sink logs subjects.
func InviteMessage(to netmail.Address, d Invite) (Message, error) {
	var text, body bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&text, "invite.txt.tmpl", d); err != nil {
		return Message{}, fmt.Errorf("render invite.txt.tmpl: %w", err)
	}
	if err := htmlTemplates.ExecuteTemplate(&body, "link", linkLayout{
		Title:  "You're invited to Wardlock",
		Lead:   d.InvitedBy + " invited you to manage sites on the Wardlock server at " + d.Host + ".",
		Action: "Sign in to Wardlock",
		URL:    d.URL,
		Expiry: fmt.Sprintf("The link expires in %d days and works once. After that, sign in at %s with a code sent to this address.", days(d.ValidFor), d.Host),
		Ignore: "If you did not expect this invitation, you can ignore this email.",
	}); err != nil {
		return Message{}, fmt.Errorf("render html: %w", err)
	}
	return Message{To: to, Subject: "You're invited to Wardlock on " + d.Host, Text: text.String(), HTML: body.String()}, nil
}

// AbuseReportMessage builds the plain-text report sent to the operator's
// abuse address. Reporters' words never reach the subject.
func AbuseReportMessage(to netmail.Address, d AbuseReport) (Message, error) {
	var text bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&text, "report.txt.tmpl", d); err != nil {
		return Message{}, fmt.Errorf("render report.txt.tmpl: %w", err)
	}
	return Message{To: to, Subject: "Abuse report: " + d.Site, Text: text.String()}, nil
}

// ScreeningMessage builds the plain-text notice to the operator's abuse
// address. The subject names neither the file nor the categories.
func ScreeningMessage(to netmail.Address, d Screening) (Message, error) {
	var text bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&text, "screening.txt.tmpl", d); err != nil {
		return Message{}, fmt.Errorf("render screening.txt.tmpl: %w", err)
	}
	subject := "Content screening flagged a file"
	if d.Status == "quarantined" {
		subject = "Content screening withheld a file"
	}
	return Message{To: to, Subject: subject, Text: text.String()}, nil
}

// WithheldMessage builds the plain-text notice to an owner.
func WithheldMessage(to netmail.Address, d Withheld) (Message, error) {
	var text bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&text, "withheld.txt.tmpl", d); err != nil {
		return Message{}, fmt.Errorf("render withheld.txt.tmpl: %w", err)
	}
	return Message{To: to, Subject: "A file on " + d.Site + " is withheld", Text: text.String()}, nil
}

func render(to netmail.Address, subject, textName string, data any, html layout) (Message, error) {
	var text, body bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&text, textName, data); err != nil {
		return Message{}, fmt.Errorf("render %s: %w", textName, err)
	}
	if err := htmlTemplates.ExecuteTemplate(&body, "layout", html); err != nil {
		return Message{}, fmt.Errorf("render html: %w", err)
	}
	return Message{To: to, Subject: subject, Text: text.String(), HTML: body.String()}, nil
}

// quote prefixes every line of s with "> ", so text a reporter wrote can
// never pass for a line of Wardlock's own.
func quote(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	return "> " + strings.ReplaceAll(s, "\n", "\n> ")
}

func minutes(d time.Duration) int { return int(d.Round(time.Minute) / time.Minute) }

func days(d time.Duration) int { return int(d.Round(24*time.Hour) / (24 * time.Hour)) }
