// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites_test

import (
	"errors"
	"strings"
	"testing"

	"wardlock.nz/server/internal/sites"
)

func TestCheckName(t *testing.T) {
	tests := []struct {
		dashboard string
		name      string
		want      error
	}{
		{"wardlock.test", "docs", nil},
		{"wardlock.test", "a", nil},
		{"wardlock.test", strings.Repeat("a", 63), nil},
		{"wardlock.test", "my-docs-2", nil},
		{"wardlock.test", "xn", nil},
		{"wardlock.test", "", sites.ErrInvalidName},
		{"wardlock.test", strings.Repeat("a", 64), sites.ErrInvalidName},
		{"wardlock.test", "-docs", sites.ErrInvalidName},
		{"wardlock.test", "docs-", sites.ErrInvalidName},
		{"wardlock.test", "Docs", sites.ErrInvalidName},
		{"wardlock.test", "do_cs", sites.ErrInvalidName},
		{"wardlock.test", "do.cs", sites.ErrInvalidName},
		{"wardlock.test", "dócs", sites.ErrInvalidName},
		{"wardlock.test", "auth", sites.ErrReservedName},
		{"wardlock.test", "www", sites.ErrReservedName},
		{"wardlock.test", "api", sites.ErrReservedName},
		{"wardlock.test", "admin", sites.ErrReservedName},
		{"wardlock.test", "app", sites.ErrReservedName},
		{"wardlock.test", "dashboard", sites.ErrReservedName},
		{"wardlock.test", "mail", sites.ErrReservedName},
		{"wardlock.test", "wardlock", sites.ErrReservedName},
		{"wardlock.test", "xn--bcher-kva", sites.ErrReservedName},
		// The dashboard host's label under the base domain.
		{"console.wardlock.test", "console", sites.ErrReservedName},
		{"a.console.wardlock.test", "console", sites.ErrReservedName},
		{"a.console.wardlock.test", "a", nil},
		{"console.wardlock.test", "consoles", nil},
		{"console.example.test", "console", nil}, // not under the base domain
		{"notwardlock.test", "notwardlock", nil},
	}
	for _, tt := range tests {
		s := sites.NewService(sites.Config{BaseDomain: "wardlock.test", DashboardHost: tt.dashboard})
		if err := s.CheckName(tt.name); !errors.Is(err, tt.want) {
			t.Errorf("dashboard %s: CheckName(%q) = %v, want %v", tt.dashboard, tt.name, err, tt.want)
		}
	}
}
