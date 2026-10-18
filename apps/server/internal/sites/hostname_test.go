// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites_test

import (
	"errors"
	"strings"
	"testing"

	"wardlock.nz/server/internal/sites"
)

func TestNormaliseHostname(t *testing.T) {
	tests := []struct {
		in, want string // want "" means refused
	}{
		{"docs.example.com", "docs.example.com"},
		{"Docs.Example.COM", "docs.example.com"},
		{"docs.example.com.", "docs.example.com"},
		{"  docs.example.com  ", "docs.example.com"},
		{"docs.example.com:443", "docs.example.com"},
		{"docs.example.com.:8443", "docs.example.com"},
		{"bücher.example", "xn--bcher-kva.example"},
		{"BÜCHER.example.", "xn--bcher-kva.example"},
		{"xn--bcher-kva.example", "xn--bcher-kva.example"},
		{"例え.テスト", "xn--r8jz45g.xn--zckzah"},
		{"a-b.example.com", "a-b.example.com"},
		{strings.Repeat("a.", 125) + "com", strings.Repeat("a.", 125) + "com"}, // 253 characters
		{"", ""},
		{".", ""},
		{"localhost", ""},
		{"example", ""},
		{"192.0.2.1", ""},
		{"192.0.2.1.", ""},
		{"192.0.2.1:80", ""},
		{"1.2.3", ""},
		{"2001:db8::1", ""},
		{"[2001:db8::1]", ""},
		{"[2001:db8::1]:443", ""},
		{"::ffff:192.0.2.1", ""},
		{"*.example.com", ""},
		{"docs.*.example.com", ""},
		{"*", ""},
		{"_dmarc.example.com", ""},
		{"docs..example.com", ""},
		{"-docs.example.com", ""},
		{"docs-.example.com", ""},
		{"do cs.example.com", ""},
		{"docs.example.com/path", ""},
		{"https://docs.example.com", ""},
		{strings.Repeat("a", 64) + ".com", ""},
		{strings.Repeat("a.", 126) + "co", ""}, // 254 characters
		{"xn--zz.example", ""},                 // not valid punycode
	}
	for _, tt := range tests {
		got, err := sites.NormaliseHostname(tt.in)
		if tt.want == "" {
			if err == nil || !errors.Is(err, sites.ErrInvalidHostname) {
				t.Errorf("NormaliseHostname(%q) = %q, %v; want ErrInvalidHostname", tt.in, got, err)
			}
			var he *sites.HostnameError
			if !errors.As(err, &he) || he.Reason == "" {
				t.Errorf("NormaliseHostname(%q): %v has no reason for the owner", tt.in, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("NormaliseHostname(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestCheckHostname(t *testing.T) {
	tests := []struct {
		dashboard, in string
		want          string // "" means refused
	}{
		{"wardlock.example.com", "docs.example.org", "docs.example.org"},
		{"wardlock.example.com", "example.com", "example.com"},
		{"wardlock.example.com", "sub.wardlock.example.com", "sub.wardlock.example.com"},
		// The base domain and everything under it: sites and the auth origin.
		{"wardlock.example.com", "sites.test", ""},
		{"wardlock.example.com", "SITES.TEST.", ""},
		{"wardlock.example.com", "docs.sites.test", ""},
		{"wardlock.example.com", "auth.sites.test", ""},
		{"wardlock.example.com", "a.docs.sites.test", ""},
		{"wardlock.example.com", "notsites.test", "notsites.test"},
		// The dashboard host, inside and outside the base domain.
		{"wardlock.example.com", "wardlock.example.com", ""},
		{"wardlock.example.com", "Wardlock.Example.com:443", ""},
		{"dash.sites.test", "dash.sites.test", ""},
	}
	for _, tt := range tests {
		s := sites.NewService(sites.Config{BaseDomain: "sites.test", DashboardHost: tt.dashboard})
		got, err := s.CheckHostname(tt.in)
		if tt.want == "" {
			if !errors.Is(err, sites.ErrInvalidHostname) {
				t.Errorf("dashboard %s: CheckHostname(%q) = %q, %v; want ErrInvalidHostname", tt.dashboard, tt.in, got, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("dashboard %s: CheckHostname(%q) = %q, %v; want %q", tt.dashboard, tt.in, got, err, tt.want)
		}
	}
}
