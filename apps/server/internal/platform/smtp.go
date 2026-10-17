// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
)

// SMTPMode says how outgoing mail leaves the server.
type SMTPMode string

const (
	// SMTPDev writes each message to a local directory and logs its
	// recipient and subject. Nothing is delivered.
	SMTPDev SMTPMode = "dev"
	// SMTPStartTLS connects in plain text and upgrades with STARTTLS before
	// sending anything else. A server that does not offer STARTTLS is refused.
	SMTPStartTLS SMTPMode = "smtp"
	// SMTPImplicitTLS connects over TLS from the first byte (smtps://).
	SMTPImplicitTLS SMTPMode = "smtps"
)

// SMTPConfig is the parsed WARDLOCK_SMTP_URL plus WARDLOCK_SMTP_FROM.
type SMTPConfig struct {
	Mode SMTPMode
	// Addr is host:port for SMTPStartTLS and SMTPImplicitTLS.
	Addr     string
	Username string
	Password Secret
	// From is the sender address for the envelope and the From header.
	From mail.Address
}

// String describes the configuration without credentials, for logs and
// errors.
func (c SMTPConfig) String() string {
	if c.Mode == SMTPDev {
		return "dev://"
	}
	userinfo := ""
	if c.Username != "" || c.Password != "" {
		userinfo = "[redacted]@"
	}
	return string(c.Mode) + "://" + userinfo + c.Addr
}

// parseSMTPConfig parses WARDLOCK_SMTP_URL and WARDLOCK_SMTP_FROM. Error
// messages never repeat the raw URL, which may carry credentials.
func parseSMTPConfig(raw, from, dashboardHost string) (SMTPConfig, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_URL is not a valid URL", ErrInvalidConfig)
	}
	cfg := SMTPConfig{Mode: SMTPMode(strings.ToLower(u.Scheme))}
	switch cfg.Mode {
	case SMTPDev:
		if u.Host != "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Opaque != "" {
			return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_URL dev:// takes no host, path or options", ErrInvalidConfig)
		}
	case SMTPStartTLS, SMTPImplicitTLS:
		host := u.Hostname()
		if host == "" {
			return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_URL needs a host", ErrInvalidConfig)
		}
		if (u.Path != "" && u.Path != "/") || u.Fragment != "" {
			return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_URL takes no path", ErrInvalidConfig)
		}
		// There is deliberately no option to skip STARTTLS or certificate
		// checks: credentials and sign-in codes never travel in plain text.
		if u.RawQuery != "" || u.ForceQuery {
			return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_URL takes no options; %s:// always requires TLS", ErrInvalidConfig, cfg.Mode)
		}
		port := u.Port()
		if port == "" {
			port = "587"
			if cfg.Mode == SMTPImplicitTLS {
				port = "465"
			}
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_URL port must be 1-65535", ErrInvalidConfig)
		}
		cfg.Addr = net.JoinHostPort(host, port)
		if u.User != nil {
			cfg.Username = u.User.Username()
			pass, _ := u.User.Password()
			cfg.Password = Secret(pass)
		}
	default:
		return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_URL must be smtp://, smtps:// or dev://", ErrInvalidConfig)
	}

	if from == "" {
		from = "Wardlock <wardlock@" + dashboardHost + ">"
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return SMTPConfig{}, fmt.Errorf("%w: WARDLOCK_SMTP_FROM is not an email address", ErrInvalidConfig)
	}
	cfg.From = *addr
	return cfg, nil
}
