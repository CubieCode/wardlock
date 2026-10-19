// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"time"

	"wardlock.nz/server/internal/platform"
)

// sendTimeout bounds one delivery when the context has no deadline.
const sendTimeout = 30 * time.Second

// SMTP delivers through an SMTP server, over STARTTLS (smtp://) or implicit
// TLS (smtps://). It never falls back to plain text and always verifies the
// server certificate.
type SMTP struct {
	cfg     platform.SMTPConfig
	rootCAs *x509.CertPool
	hello   string
	now     func() time.Time
}

// NewSMTP returns an SMTP mailer for cfg.SMTP.
func NewSMTP(cfg Config) *SMTP {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &SMTP{cfg: cfg.SMTP, rootCAs: cfg.RootCAs, hello: cfg.Hello, now: now}
}

// Send delivers msg. Errors name the server without credentials and never
// include the message body.
func (m *SMTP) Send(ctx context.Context, msg Message) error {
	data, err := encode(m.cfg.From, msg, m.now())
	if err != nil {
		return err
	}
	if err := m.send(ctx, msg.To.Address, data); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return fmt.Errorf("send mail via %s: %w", m.cfg, err)
	}
	return nil
}

func (m *SMTP) send(ctx context.Context, to string, data []byte) error {
	host, _, err := net.SplitHostPort(m.cfg.Addr)
	if err != nil {
		return fmt.Errorf("server address: %w", err)
	}
	tlsConfig := &tls.Config{ServerName: host, RootCAs: m.rootCAs, MinVersion: tls.VersionTLS12}

	dialer := &net.Dialer{}
	var conn net.Conn
	if m.cfg.Mode == platform.SMTPImplicitTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", m.cfg.Addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", m.cfg.Addr)
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(sendTimeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return fmt.Errorf("connect: %w", err)
	}
	// Unblock any pending read or write as soon as ctx is cancelled.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return stageError("greeting", err)
	}
	defer func() { _ = c.Close() }()
	if m.hello != "" {
		if err := c.Hello(m.hello); err != nil {
			return stageError("hello", err)
		}
	}

	if m.cfg.Mode == platform.SMTPStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrNoSTARTTLS
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return stageError("starttls", err)
		}
	}

	if m.cfg.Username != "" || m.cfg.Password != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return fmt.Errorf("%w: server offers no AUTH", ErrAuth)
		}
		auth := smtp.PlainAuth("", m.cfg.Username, string(m.cfg.Password), host)
		if err := c.Auth(auth); err != nil {
			// Report only the reply code: the server's text is not ours to
			// trust with credentials.
			var tpErr *textproto.Error
			if errors.As(err, &tpErr) {
				return fmt.Errorf("%w: server replied %d", ErrAuth, tpErr.Code)
			}
			return ErrAuth
		}
	}

	if err := c.Mail(m.cfg.From.Address); err != nil {
		return stageError("mail from", err)
	}
	if err := c.Rcpt(to); err != nil {
		return stageError("rcpt to", err)
	}
	w, err := c.Data()
	if err != nil {
		return stageError("data", err)
	}
	if _, err := w.Write(data); err != nil {
		return stageError("data", err)
	}
	if err := w.Close(); err != nil {
		return stageError("data", err)
	}
	if err := c.Quit(); err != nil {
		return stageError("quit", err)
	}
	return nil
}

// stageError names the SMTP stage that failed. A server reply is reduced
// to its code: servers often echo the recipient (a visitor's address) in
// their text, and errors end up in logs.
func stageError(stage string, err error) error {
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		return fmt.Errorf("%s: server replied %d", stage, tpErr.Code)
	}
	return fmt.Errorf("%s: %w", stage, err)
}
