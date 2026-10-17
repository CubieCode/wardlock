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
