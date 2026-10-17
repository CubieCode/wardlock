// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"wardlock.nz/server/internal/platform"
)

func TestSMTPConfig(t *testing.T) {
	tests := []struct {
		name string
		url  string
		from string
		want platform.SMTPConfig
	}{
		{"default is dev", "", "", platform.SMTPConfig{Mode: platform.SMTPDev}},
		{"dev", "dev://", "", platform.SMTPConfig{Mode: platform.SMTPDev}},
		{"starttls default port", "smtp://mail.example.test", "", platform.SMTPConfig{Mode: platform.SMTPStartTLS, Addr: "mail.example.test:587"}},
		{"starttls with credentials", "smtp://user%40example.test:p%40ss@mail.example.test:2587", "",
			platform.SMTPConfig{Mode: platform.SMTPStartTLS, Addr: "mail.example.test:2587", Username: "user@example.test", Password: "p@ss"}},
		{"implicit tls default port", "smtps://u:p@mail.example.test", "", platform.SMTPConfig{Mode: platform.SMTPImplicitTLS, Addr: "mail.example.test:465", Username: "u", Password: "p"}},
		{"ipv6 host", "smtps://[::1]:4650", "", platform.SMTPConfig{Mode: platform.SMTPImplicitTLS, Addr: "[::1]:4650"}},
		{"scheme is case-insensitive", "SMTPS://mail.example.test", "", platform.SMTPConfig{Mode: platform.SMTPImplicitTLS, Addr: "mail.example.test:465"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"WARDLOCK_SMTP_URL": tt.url, "WARDLOCK_SMTP_FROM": tt.from, "WARDLOCK_DASHBOARD_HOST": "admin.example.test"}
			cfg, err := platform.LoadConfig(func(k string) string { return env[k] })
			if err != nil {
				t.Fatal(err)
			}
			got := cfg.SMTP
			if got.Mode != tt.want.Mode || got.Addr != tt.want.Addr || got.Username != tt.want.Username || got.Password != tt.want.Password {
				t.Fatalf("SMTP = %#v, want %#v", got, tt.want)
			}
			if got.From.Address != "wardlock@admin.example.test" || got.From.Name != "Wardlock" {
				t.Fatalf("default From = %v", got.From)
			}
		})
	}
}

func TestSMTPFrom(t *testing.T) {
	env := map[string]string{"WARDLOCK_SMTP_FROM": "Sign-in <no-reply@example.test>"}
	cfg, err := platform.LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTP.From.Address != "no-reply@example.test" || cfg.SMTP.From.Name != "Sign-in" {
		t.Fatalf("From = %v", cfg.SMTP.From)
	}
}

func TestSMTPConfigRejects(t *testing.T) {
	tests := []struct{ name, url, from string }{
		// Plain SMTP without STARTTLS cannot be configured: there is no
		// option to turn it off, and every option is refused.
		{"starttls disabled", "smtp://u:hunter2@mail.example.test:25?starttls=false", ""},
		{"tls none", "smtp://u:hunter2@mail.example.test?tls=none", ""},
		{"insecure skip verify", "smtps://u:hunter2@mail.example.test?insecure=1", ""},
		{"empty query", "smtp://u:hunter2@mail.example.test?", ""},
		{"unknown scheme", "smtp+plain://u:hunter2@mail.example.test", ""},
		{"http scheme", "http://u:hunter2@mail.example.test", ""},
		{"no scheme", "u:hunter2@mail.example.test", ""},
		{"no host", "smtp://u:hunter2@:587", ""},
		{"path", "smtp://u:hunter2@mail.example.test/inbox", ""},
		{"bad port", "smtp://u:hunter2@mail.example.test:99999", ""},
		{"named port", "smtp://u:hunter2@mail.example.test:smtp", ""},
		{"unparseable", "smtp://u:hunter2@mail.example.test:58 7/%zz", ""},
		{"dev with host", "dev://hunter2@somewhere", ""},
		{"dev with path", "dev:///tmp/hunter2", ""},
		{"dev with options", "dev://?dir=hunter2", ""},
		{"bad from", "dev://", "not an address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"WARDLOCK_SMTP_URL": tt.url, "WARDLOCK_SMTP_FROM": tt.from}
			_, err := platform.LoadConfig(func(k string) string { return env[k] })
			if !errors.Is(err, platform.ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("error leaks credentials: %v", err)
			}
		})
	}
}

func TestSMTPConfigStringRedacts(t *testing.T) {
	env := map[string]string{"WARDLOCK_SMTP_URL": "smtps://apikey:hunter2@mail.example.test"}
	cfg, err := platform.LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{cfg.SMTP.String(), fmt.Sprint(cfg.SMTP), fmt.Sprintf("%v", cfg)} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, "apikey") {
			t.Fatalf("formatted config leaks credentials: %s", s)
		}
	}
	if got := cfg.SMTP.String(); got != "smtps://[redacted]@mail.example.test:465" {
		t.Fatalf("String = %q", got)
	}
}
