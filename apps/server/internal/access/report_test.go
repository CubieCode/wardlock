// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access_test

import (
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
)

func TestStripTokens(t *testing.T) {
	const tok = "wl_abcdefghij_0123456789abcdefghijABCDEFGHIJ-_0123456"
	for _, tc := range []struct{ in, want string }{
		{"https://docs.example.test/?t=" + tok, "https://docs.example.test/?t=[removed]"},
		{"https://docs.example.test/a?x=1&t=" + tok + "#top", "https://docs.example.test/a?x=1&t=[removed]#top"},
		{"https://docs.example.test/?t=anything&y=2", "https://docs.example.test/?t=[removed]&y=2"},
		{"It came as " + tok + ", see above", "It came as [token removed], see above"},
		{"half a token wl_abcdefghij_0123", "half a token [token removed]"},
		{"Bearer " + tok, "Bearer [token removed]"},
		{"https://docs.example.test/?at=1&format=md", "https://docs.example.test/?at=1&format=md"},
		{"plain words", "plain words"},
	} {
		got := access.StripTokens(tc.in)
		if got != tc.want {
			t.Errorf("StripTokens(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, tok[14:]) {
			t.Errorf("StripTokens(%q) kept the secret", tc.in)
		}
	}
}

func FuzzStripTokens(f *testing.F) {
	f.Add("https://x.test/?t=wl_abcdefghij_secret")
	f.Fuzz(func(t *testing.T, s string) {
		out := access.StripTokens(s)
		if strings.Contains(out, "?t=wl_") || strings.Contains(out, "&t=wl_") {
			t.Fatalf("StripTokens(%q) = %q kept a token parameter", s, out)
		}
	})
}
