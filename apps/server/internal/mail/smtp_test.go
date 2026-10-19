// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package mail_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/platform"
)

const (
	testUser = "smtp-user@example.test"
	testPass = "synthetic-pass-9f3a"
)

func TestSMTPSends(t *testing.T) {
	for _, mode := range []platform.SMTPMode{platform.SMTPStartTLS, platform.SMTPImplicitTLS} {
		t.Run(string(mode), func(t *testing.T) {
			srv := startFakeSMTP(t, fakeOptions{implicitTLS: mode == platform.SMTPImplicitTLS, starttls: true})
			m := srv.mailer(mode, testUser, testPass)
			msg, err := mail.EmailCodeMessage(recipient, mail.EmailCode{Code: testCode, Site: "docs.example.test", ValidFor: 10 * time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Send(t.Context(), msg); err != nil {
				t.Fatal(err)
			}
			got := srv.received()
			if len(got) != 1 {
				t.Fatalf("server received %d messages", len(got))
			}
			r := got[0]
			if !r.tls || !r.authed || r.from != sender.Address || r.to != recipient.Address {
				t.Fatalf("delivery = %+v", r)
			}
			text, _ := readMessage(t, r.data)
			if !strings.Contains(text, testCode) {
				t.Fatalf("delivered text lacks the code: %q", text)
			}
		})
	}
}

func TestSMTPRequiresSTARTTLS(t *testing.T) {
	srv := startFakeSMTP(t, fakeOptions{starttls: false})
	m := srv.mailer(platform.SMTPStartTLS, testUser, testPass)
	err := m.Send(t.Context(), mail.Message{To: recipient, Subject: "s", Text: testCode})
	if !errors.Is(err, mail.ErrNoSTARTTLS) {
		t.Fatalf("error = %v, want ErrNoSTARTTLS", err)
	}
	for _, cmd := range srv.commands() {
		if strings.HasPrefix(cmd, "AUTH") || strings.HasPrefix(cmd, "MAIL") || strings.HasPrefix(cmd, "DATA") {
			t.Fatalf("client sent %q over a plain connection", cmd)
		}
	}
	assertNoCredentials(t, err)
}

func TestSMTPRejectsUntrustedCertificate(t *testing.T) {
	srv := startFakeSMTP(t, fakeOptions{starttls: true})
	m := mail.NewSMTP(mail.Config{SMTP: srv.config(platform.SMTPStartTLS, testUser, testPass), Now: fixedNow})
	err := m.Send(t.Context(), mail.Message{To: recipient, Subject: "s", Text: "x"})
	if err == nil {
		t.Fatal("sent to a server with an untrusted certificate")
	}
	if len(srv.received()) != 0 {
		t.Fatal("message delivered despite certificate failure")
	}
	assertNoCredentials(t, err)
}

func TestSMTPAuthErrorHidesCredentials(t *testing.T) {
	srv := startFakeSMTP(t, fakeOptions{starttls: true, rejectAuth: true})
	m := srv.mailer(platform.SMTPStartTLS, testUser, testPass)
	err := m.Send(t.Context(), mail.Message{To: recipient, Subject: "s", Text: "x"})
	if !errors.Is(err, mail.ErrAuth) {
		t.Fatalf("error = %v, want ErrAuth", err)
	}
	assertNoCredentials(t, err)
	if !strings.Contains(err.Error(), "smtp://[redacted]@127.0.0.1:") {
		t.Fatalf("error does not name the server: %v", err)
	}
}

// A refused recipient is reported by stage and reply code only: the
// server's text, which echoes the address, never reaches the error.
func TestSMTPRecipientErrorHidesAddress(t *testing.T) {
	srv := startFakeSMTP(t, fakeOptions{starttls: true, rejectRcpt: true})
	msg, err := mail.EmailCodeMessage(recipient, mail.EmailCode{Code: testCode, Site: "docs.example.test", ValidFor: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	err = srv.mailer(platform.SMTPStartTLS, testUser, testPass).Send(t.Context(), msg)
	if err == nil || !strings.HasSuffix(err.Error(), "rcpt to: server replied 550") {
		t.Fatalf("Send = %v, want the stage and code only", err)
	}
	if strings.Contains(err.Error(), recipient.Address) {
		t.Fatalf("error contains the recipient: %v", err)
	}
}

// Errors before the recipient stage are reported by stage and reply code
// too: the server's text is never kept.
func TestSMTPGreetingErrorHidesServerText(t *testing.T) {
	srv := startFakeSMTP(t, fakeOptions{greeting: "554 fake.example.test server-text-marker"})
	msg, err := mail.EmailCodeMessage(recipient, mail.EmailCode{Code: testCode, Site: "docs.example.test", ValidFor: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	err = srv.mailer(platform.SMTPStartTLS, testUser, testPass).Send(t.Context(), msg)
	if err == nil || !strings.HasSuffix(err.Error(), "greeting: server replied 554") || strings.Contains(err.Error(), "marker") {
		t.Fatalf("Send = %v, want the stage and code only", err)
	}
}

func TestSMTPHonoursContext(t *testing.T) {
	// A server that accepts and never greets.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	wg.Go(func() {
		if c, err := ln.Accept(); err == nil {
			<-t.Context().Done()
			_ = c.Close()
		}
	})
	m := mail.NewSMTP(mail.Config{SMTP: platform.SMTPConfig{Mode: platform.SMTPStartTLS, Addr: ln.Addr().String(), From: sender}})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = m.Send(ctx, mail.Message{To: recipient, Subject: "s", Text: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func assertNoCredentials(t *testing.T, err error) {
	t.Helper()
	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + testUser + "\x00" + testPass))
	for _, secret := range []string{testUser, testPass, plain} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks credentials: %v", err)
		}
	}
}

// fakeSMTP is a minimal in-process SMTP server: EHLO, STARTTLS, AUTH PLAIN,
// MAIL, RCPT, DATA and QUIT.
type fakeSMTP struct {
	t    *testing.T
	ln   net.Listener
	opts fakeOptions
	cert tls.Certificate
	pool *x509.CertPool

	mu   sync.Mutex
	cmds []string
	msgs []delivery
}

type fakeOptions struct {
	implicitTLS bool
	starttls    bool
	rejectAuth  bool
	// rejectRcpt refuses every recipient, echoing the address back.
	rejectRcpt bool
	// greeting, when set, replaces the 220 greeting line.
	greeting string
}

type delivery struct {
	from, to    string
	data        []byte
	tls, authed bool
}

func startFakeSMTP(t *testing.T, opts fakeOptions) *fakeSMTP {
	t.Helper()
	cert, pool := selfSigned(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if opts.implicitTLS {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
	s := &fakeSMTP{t: t, ln: ln, opts: opts, cert: cert, pool: pool}
	var wg sync.WaitGroup
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() { s.serve(c) })
		}
	})
	return s
}

func (s *fakeSMTP) config(mode platform.SMTPMode, user, pass string) platform.SMTPConfig {
	return platform.SMTPConfig{Mode: mode, Addr: s.ln.Addr().String(), Username: user, Password: platform.Secret(pass), From: sender}
}

func (s *fakeSMTP) mailer(mode platform.SMTPMode, user, pass string) *mail.SMTP {
	return mail.NewSMTP(mail.Config{SMTP: s.config(mode, user, pass), RootCAs: s.pool, Hello: "wardlock.example.test", Now: fixedNow})
}

func (s *fakeSMTP) received() []delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]delivery(nil), s.msgs...)
}

func (s *fakeSMTP) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	tp := textproto.NewConn(conn)
	isTLS := s.opts.implicitTLS
	var cur delivery
	reply := func(format string, args ...any) bool { return tp.PrintfLine(format, args...) == nil }
	greeting := "220 fake.example.test ESMTP"
	if s.opts.greeting != "" {
		greeting = s.opts.greeting
	}
	if !reply("%s", greeting) {
		return
	}
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		s.mu.Lock()
		s.cmds = append(s.cmds, verb)
		s.mu.Unlock()
		switch verb {
		case "EHLO", "HELO":
			if s.opts.starttls && !isTLS {
				reply("250-fake.example.test")
				reply("250-STARTTLS")
			} else {
				reply("250-fake.example.test")
			}
			reply("250 AUTH PLAIN")
		case "STARTTLS":
			if !s.opts.starttls || isTLS {
				reply("502 not available")
				continue
			}
			reply("220 go ahead")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn, isTLS = tlsConn, true
			tp = textproto.NewConn(conn)
		case "AUTH":
			want := "PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00"+testUser+"\x00"+testPass))
			if s.opts.rejectAuth || arg != want {
				// Echo the attempt back, as some servers do, to prove the
				// client does not pass server text into its errors.
				reply("535 5.7.8 bad credentials: %s", arg)
				continue
			}
			cur.authed = true
			reply("235 ok")
		case "MAIL":
			cur.from = mailbox(arg, "FROM:")
			reply("250 ok")
		case "RCPT":
			cur.to = mailbox(arg, "TO:")
			if s.opts.rejectRcpt {
				reply("550 5.1.1 <%s>: Recipient address rejected", cur.to)
				continue
			}
			reply("250 ok")
		case "DATA":
			reply("354 go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			cur.data, cur.tls = data, isTLS
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			cur = delivery{authed: cur.authed}
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("500 unknown")
		}
	}
}

// mailbox extracts the address from a MAIL or RCPT argument.
func mailbox(arg, prefix string) string {
	addr, _, _ := strings.Cut(strings.TrimPrefix(arg, prefix), " ")
	return strings.Trim(addr, "<>")
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake smtp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}
