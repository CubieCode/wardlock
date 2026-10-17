// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package mail sends Wardlock's outgoing email: owner sign-in codes and
// visitor email codes. It owns the Mailer interface, an SMTP client that
// always uses TLS, the dev:// sink that writes .eml files instead of
// delivering, and the message templates.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"time"

	"wardlock.nz/server/internal/platform"
)

// Mailer sends one message. Implementations are safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Message is one email. HTML may be empty for a plain-text message.
type Message struct {
	To      netmail.Address
	Subject string
	Text    string
	HTML    string
}

var (
	// ErrInvalidMessage means the message cannot be encoded safely, for
	// example a header containing a line break.
	ErrInvalidMessage = errors.New("invalid mail message")
	// ErrNoSTARTTLS means an smtp:// server did not offer STARTTLS. Wardlock
	// never sends over a plain connection.
	ErrNoSTARTTLS = errors.New("smtp server does not offer STARTTLS")
	// ErrAuth means the SMTP server rejected the configured credentials or
	// offers no way to use them.
	ErrAuth = errors.New("smtp authentication failed")
)

// DefaultDevDir is where the dev:// sink writes messages.
const DefaultDevDir = ".local/mail"

// Config holds a Mailer's dependencies.
type Config struct {
	SMTP   platform.SMTPConfig
	Logger *slog.Logger
	// DevDir overrides DefaultDevDir for the dev:// sink.
	DevDir string
	// RootCAs verifies the SMTP server's certificate; nil means the system
	// roots.
	RootCAs *x509.CertPool
	// Hello is the name sent in EHLO; empty means "localhost".
	Hello string
	// Now defaults to time.Now.
	Now func() time.Time
}

// New returns the Mailer for cfg.SMTP.Mode.
func New(cfg Config) (Mailer, error) {
	switch cfg.SMTP.Mode {
	case platform.SMTPDev:
		return NewDevSink(cfg), nil
	case platform.SMTPStartTLS, platform.SMTPImplicitTLS:
		return NewSMTP(cfg), nil
	default:
		return nil, fmt.Errorf("mail: unknown SMTP mode %q", cfg.SMTP.Mode)
	}
}

// encode renders msg as an RFC 5322 message with CRLF line endings: a
// multipart/alternative body when HTML is set, plain text otherwise.
func encode(from netmail.Address, msg Message, now time.Time) ([]byte, error) {
	if strings.ContainsAny(msg.Subject, "\r\n") || strings.ContainsAny(msg.To.Name, "\r\n") {
		return nil, fmt.Errorf("%w: header contains a line break", ErrInvalidMessage)
	}
	if _, err := netmail.ParseAddress(msg.To.Address); err != nil || strings.ContainsAny(msg.To.Address, "\r\n<>") {
		return nil, fmt.Errorf("%w: recipient is not an email address", ErrInvalidMessage)
	}

	var buf bytes.Buffer
	header := func(k, v string) { buf.WriteString(k + ": " + v + "\r\n") }
	header("Date", now.Format(time.RFC1123Z))
	header("From", from.String())
	header("To", msg.To.String())
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Message-ID", "<"+rand.Text()+"@"+domainOf(from.Address)+">")
	header("MIME-Version", "1.0")

	if msg.HTML == "" {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQP(&buf, msg.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	header("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	buf.WriteString("\r\n")
	for _, part := range []struct{ typ, content string }{
		{"text/plain; charset=utf-8", msg.Text},
		{"text/html; charset=utf-8", msg.HTML},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.typ},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, fmt.Errorf("encode mail: %w", err)
		}
		if err := writeQP(w, part.content); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("encode mail: %w", err)
	}
	buf.Write(body.Bytes())
	return buf.Bytes(), nil
}

func writeQP(w io.Writer, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(s)); err != nil {
		return fmt.Errorf("encode mail: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("encode mail: %w", err)
	}
	return nil
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[i+1:]
	}
	return "wardlock.invalid"
}
