// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"strings"
	"testing"

	"wardlock.nz/server/internal/deploys"
)

func TestNormalisePath(t *testing.T) {
	for in, want := range map[string]string{
		"index.html":               "index.html",
		"a/b/c.txt":                "a/b/c.txt",
		`docs\install.html`:        "docs/install.html",
		".well-known/security.txt": ".well-known/security.txt",
		"..foo":                    "..foo",
		"foo..":                    "foo..",
		"x/.wardlock/y":            "x/.wardlock/y",
		"wardlock/x":               "wardlock/x",
		".wardlockish/x":           ".wardlockish/x",
		"café/ünï.html":            "café/ünï.html",
		"with space.html":          "with space.html",
		strings.Repeat("a", 1024):  strings.Repeat("a", 1024),
	} {
		got, err := deploys.NormalisePath(in)
		if err != nil || got != want {
			t.Errorf("NormalisePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "/", "/index.html", "index.html/", "a//b", "..", "../a", "a/..", "a/../b", ".", "./a", "a/./b",
		`a\..\b`, `\a`, `a\`, "a\x00b", "a\x01", "a\tb", "a\rb", "a\nb", "a\x1b", "a\x7f", "a\u0080", "a\u009f",
		"\xff", "a\xc3", "ok/\xed\xa0\x80", // invalid UTF-8, including a surrogate
		".wardlock", ".wardlock/health", ".WARDLOCK/x", ".WardLock/login", `.wardlock\x`,
		strings.Repeat("a", 1025),
	} {
		_, err := deploys.NormalisePath(in)
		var te *deploys.TargetError
		if !errors.Is(err, deploys.ErrInvalidPath) || !errors.As(err, &te) || te.Target != in || te.Message == "" {
			t.Errorf("NormalisePath(%q) error = %#v, want ErrInvalidPath naming it", in, err)
		}
	}
}
