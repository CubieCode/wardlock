// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites_test

import (
	"testing"

	"wardlock.nz/server/internal/sites"
)

func TestGroupNameKey(t *testing.T) {
	for _, tt := range []struct{ a, b string }{
		{"Board", "board"},
		{"Kōwhai", "KŌWHAI"},
		{"Kōwhai", "Kōwhai"}, // composed and decomposed macron
		{"Ärzte", "ärzte"},
		{"Ελλάδα", "ελλάδα"},
	} {
		if ka, kb := sites.GroupNameKey(tt.a), sites.GroupNameKey(tt.b); ka != kb {
			t.Errorf("GroupNameKey(%q) = %q, GroupNameKey(%q) = %q; want equal", tt.a, ka, tt.b, kb)
		}
	}
	for _, tt := range []struct{ a, b string }{{"Board", "Boards"}, {"Kōwhai", "Kowhai"}} {
		if sites.GroupNameKey(tt.a) == sites.GroupNameKey(tt.b) {
			t.Errorf("GroupNameKey(%q) == GroupNameKey(%q); want different", tt.a, tt.b)
		}
	}
}
