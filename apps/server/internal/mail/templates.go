// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	netmail "net/mail"
	texttemplate "text/template"
	"time"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// Parsed once; templates are read-only after parsing.
var (
	textTemplates = texttemplate.Must(texttemplate.New("").
			Funcs(texttemplate.FuncMap{"minutes": minutes}).
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

func minutes(d time.Duration) int { return int(d.Round(time.Minute) / time.Minute) }
