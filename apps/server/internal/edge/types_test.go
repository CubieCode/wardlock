// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import "testing"

func TestContentType(t *testing.T) {
	for name, want := range map[string]string{
		"notes.TXT":      "text/plain; charset=utf-8",
		"README.md":      "text/markdown; charset=utf-8",
		"data.csv":       "text/csv; charset=utf-8",
		"a/b.json":       "application/json",
		"index.html":     "text/html; charset=utf-8",
		"logo.svg":       "image/svg+xml",
		"site.css":       "text/css; charset=utf-8",
		"blob.unknownxx": "application/octet-stream",
		"noext":          "application/octet-stream",
	} {
		if got := contentType(name); got != want {
			t.Errorf("contentType(%q) = %q, want %q", name, got, want)
		}
	}
}
