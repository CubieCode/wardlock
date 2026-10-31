// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package mail_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/platform"
)

// Synthetic test data only.
var (
	recipient = netmail.Address{Name: "Test Visitor", Address: "visitor@example.test"}
	sender    = netmail.Address{Name: "Wardlock", Address: "wardlock@admin.example.test"}
	fixedNow  = func() time.Time { return time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC) }
)

const testCode = "481516"

func TestTemplates(t *testing.T) {
	signIn, err := mail.SignInMessage(recipient, mail.SignIn{Code: testCode, Host: "admin.example.test", ValidFor: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	visitor, err := mail.EmailCodeMessage(recipient, mail.EmailCode{Code: testCode, Site: "docs<b>.example.test", ValidFor: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []mail.Message{signIn, visitor} {
		if strings.Contains(msg.Subject, testCode) {
			t.Errorf("subject %q contains the code; the dev sink logs subjects", msg.Subject)
		}
		for name, body := range map[string]string{"text": msg.Text, "html": msg.HTML} {
			if !strings.Contains(body, testCode) || !strings.Contains(body, "10 minutes") {
				t.Errorf("%s body missing code or expiry:\n%s", name, body)
			}
		}
		if msg.To != recipient {
			t.Errorf("To = %v", msg.To)
		}
	}
	if !strings.Contains(signIn.Text, "admin.example.test") || !strings.Contains(signIn.HTML, "admin.example.test") {
		t.Error("sign-in message does not name the dashboard host")
	}
	if strings.Contains(visitor.HTML, "<b>") || !strings.Contains(visitor.HTML, "docs&lt;b&gt;.example.test") {
		t.Errorf("HTML does not escape the site name:\n%s", visitor.HTML)
	}
}

func TestInviteMessage(t *testing.T) {
	const link = "http://admin.example.test/sign-in?code=EXAMPLEonlyEXAMPLE"
	msg, err := mail.InviteMessage(recipient, mail.Invite{
		URL: link, Host: "admin.example.test", InvitedBy: "first<b>@example.test", ValidFor: 7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.Subject, "EXAMPLEonly") {
		t.Errorf("subject %q contains the link; the dev sink logs subjects", msg.Subject)
	}
	for name, body := range map[string]string{"text": msg.Text, "html": msg.HTML} {
		if !strings.Contains(body, link) || !strings.Contains(body, "7 days") || !strings.Contains(body, "admin.example.test") {
			t.Errorf("%s body missing link, expiry or host:\n%s", name, body)
		}
	}
	if strings.Contains(msg.HTML, "first<b>") || !strings.Contains(msg.HTML, "first&lt;b&gt;@example.test") {
		t.Errorf("HTML does not escape the inviter:\n%s", msg.HTML)
	}
}

func TestDevSink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mail")
	var logs bytes.Buffer
	sink := mail.NewDevSink(mail.Config{
		SMTP:   platform.SMTPConfig{Mode: platform.SMTPDev, From: sender},
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		DevDir: dir,
		Now:    fixedNow,
	})
	msg, err := mail.SignInMessage(recipient, mail.SignIn{Code: testCode, Host: "admin.example.test", ValidFor: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.eml"))
	if err != nil || len(files) != 1 {
		t.Fatalf("eml files = %v, %v", files, err)
	}
	info, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %v, want 0600", perm)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	text, html := readMessage(t, raw)
	if !strings.Contains(text, testCode) || !strings.Contains(html, testCode) {
		t.Errorf("eml does not carry the code: text %q", text)
	}

	log := logs.String()
	if strings.Contains(log, testCode) {
		t.Fatalf("dev sink logged the code: %s", log)
	}
	for _, want := range []string{"visitor@example.test", "Your Wardlock sign-in code", files[0]} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q: %s", want, log)
		}
	}
}

func TestNewByMode(t *testing.T) {
	m, err := mail.New(mail.Config{SMTP: platform.SMTPConfig{Mode: platform.SMTPDev}})
	if _, ok := m.(*mail.DevSink); err != nil || !ok {
		t.Fatalf("dev mode = %T, %v", m, err)
	}
	m, err = mail.New(mail.Config{SMTP: platform.SMTPConfig{Mode: platform.SMTPStartTLS, Addr: "mail.example.test:587"}})
	if _, ok := m.(*mail.SMTP); err != nil || !ok {
		t.Fatalf("smtp mode = %T, %v", m, err)
	}
	if _, err := mail.New(mail.Config{}); err == nil {
		t.Fatal("empty mode accepted")
	}
}

func TestRejectsHeaderInjection(t *testing.T) {
	sink := mail.NewDevSink(mail.Config{SMTP: platform.SMTPConfig{Mode: platform.SMTPDev, From: sender}, DevDir: t.TempDir()})
	for name, msg := range map[string]mail.Message{
		"subject":         {To: recipient, Subject: "hi\r\nBcc: other@example.test", Text: "x"},
		"recipient name":  {To: netmail.Address{Name: "a\nBcc: b@example.test", Address: "visitor@example.test"}, Subject: "s", Text: "x"},
		"recipient addr":  {To: netmail.Address{Address: "visitor@example.test>\r\nBcc: b@example.test"}, Subject: "s", Text: "x"},
		"empty recipient": {Subject: "s", Text: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := sink.Send(t.Context(), msg); !errors.Is(err, mail.ErrInvalidMessage) {
				t.Fatalf("error = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

// readMessage parses an encoded message and returns its decoded text and
// HTML parts.
func readMessage(t *testing.T, raw []byte) (text, html string) {
	t.Helper()
	m, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a readable message: %v", err)
	}
	if got := m.Header.Get("To"); !strings.Contains(got, "visitor@example.test") {
		t.Errorf("To = %q", got)
	}
	if got := m.Header.Get("From"); !strings.Contains(got, sender.Address) {
		t.Errorf("From = %q", got)
	}
	if m.Header.Get("Message-ID") == "" || m.Header.Get("Date") == "" {
		t.Error("missing Message-ID or Date")
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if err != nil || subject == "" {
		t.Errorf("Subject = %q, %v", subject, err)
	}
	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, %v", mediaType, err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(p))
		if err != nil {
			t.Fatal(err)
		}
		switch ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type")); ct {
		case "text/plain":
			text = string(body)
		case "text/html":
			html = string(body)
		}
	}
	if text == "" || html == "" {
		t.Fatal("message lacks a text or HTML part")
	}
	return text, html
}

// TestScreeningMessages checks the content-screening notices (A2.15): the
// operator's names the file, categories and sites and, for sexual content
// involving minors, says to preserve and report instead of review; the
// owner's names the site and paths and never why.
func TestNoticeMessage(t *testing.T) {
	msg, err := mail.NoticeMessage(netmail.Address{Address: "owner@example.test"}, mail.Notice{
		Subject: "A share link expires soon", Lead: "These links <expire>:", Items: []string{"docs: \"Launch\""}, Note: "Soon.",
		Action: "View your links", URL: "https://app.example.test/sites/docs/links", SettingsURL: "https://app.example.test/notifications",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"These links <expire>:", "    docs: \"Launch\"", "Soon.", "View your links: https://app.example.test/sites/docs/links", "https://app.example.test/notifications"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("text has no %q:\n%s", want, msg.Text)
		}
	}
	for _, want := range []string{"These links &lt;expire&gt;:", `href="https://app.example.test/sites/docs/links"`, `href="https://app.example.test/notifications"`, "<li>"} {
		if !strings.Contains(msg.HTML, want) {
			t.Errorf("html has no %q:\n%s", want, msg.HTML)
		}
	}
}

func TestScreeningMessages(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	abuse := netmail.Address{Address: "abuse@example.test"}
	base := mail.Screening{
		SHA256: sha, Status: "quarantined", Categories: []string{"sexual"}, CheckedAt: fixedNow(),
		Uses: []mail.ScreeningUse{{Site: "docs", Path: "img/a.png", Owner: "owner@example.test"}},
	}
	q, err := mail.ScreeningMessage(abuse, base)
	if err != nil {
		t.Fatal(err)
	}
	if q.Subject != "Content screening withheld a file" || strings.Contains(q.Subject, sha) {
		t.Errorf("subject %q", q.Subject)
	}
	for _, want := range []string{sha, "Categories: sexual", "docs  /img/a.png  (owner owner@example.test)", "moderation release " + sha, "moderation discard " + sha} {
		if !strings.Contains(q.Text, want) {
			t.Errorf("quarantine notice lacks %q:\n%s", want, q.Text)
		}
	}
	minors := base
	minors.Categories, minors.Minors, minors.Uses = []string{"sexual", "sexual/minors"}, true, nil
	m, err := mail.ScreeningMessage(abuse, minors)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Preserving and reporting", "illegal material", "moderation confirm " + sha, "No site serves it"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("minors notice lacks %q:\n%s", want, m.Text)
		}
	}
	if strings.Contains(m.Text, "moderation release") {
		t.Errorf("minors notice offers release:\n%s", m.Text)
	}
	flagged := base
	flagged.Status, flagged.Categories = "flagged", []string{"violence", "hate"}
	f, err := mail.FlaggedMessage(abuse, []mail.Screening{flagged})
	if err != nil {
		t.Fatal(err)
	}
	if f.Subject != "Content screening flagged a file" || !strings.Contains(f.Text, "It is still served") || !strings.Contains(f.Text, "violence, hate") ||
		strings.Contains(f.Text, "Reason:") || strings.Contains(f.Text, "moderation confirm") {
		t.Errorf("flag notice %q:\n%s", f.Subject, f.Text)
	}
	// The digest lists every file of a pass, unscreenable ones with why.
	other := mail.Screening{SHA256: strings.Repeat("cd", 32), Status: "flagged", Unscreenable: true, CheckedAt: fixedNow()}
	f, err = mail.FlaggedMessage(abuse, []mail.Screening{flagged, other})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 files", "They are still served", sha, other.SHA256, "Reason:     unscreenable", "No site serves it", "moderation release SHA256", "takedown-site"} {
		if !strings.Contains(f.Subject+f.Text, want) {
			t.Errorf("digest lacks %q:\n%s", want, f.Text)
		}
	}

	o, err := mail.WithheldMessage(recipient, mail.Withheld{Site: "docs", Host: "admin.example.test", Paths: []string{"img/a.png", "b.png"}, Contact: "abuse@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/img/a.png", "/b.png", "docs", "admin.example.test", "abuse@example.test"} {
		if !strings.Contains(o.Text, want) {
			t.Errorf("owner notice lacks %q:\n%s", want, o.Text)
		}
	}
	for _, never := range []string{"sexual", "categor", "minor", sha} {
		if strings.Contains(o.Text, never) || strings.Contains(o.Subject, never) {
			t.Errorf("owner notice mentions %q:\n%s", never, o.Text)
		}
	}
	if o.Subject != "A file on docs is withheld" {
		t.Errorf("owner subject %q", o.Subject)
	}
	noContact, err := mail.WithheldMessage(recipient, mail.Withheld{Site: "docs", Host: "admin.example.test", Paths: []string{"a.png"}})
	if err != nil || !strings.Contains(noContact.Text, "contact the operator of the server") {
		t.Errorf("owner notice without contact: %v\n%s", err, noContact.Text)
	}
}
