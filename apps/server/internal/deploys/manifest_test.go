// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"slices"
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
		"a\u200bb", "a\u200d", "evil\u202Elmth.js", "\ufeffindex.html", "a\u2066b", "a\u00adb", // Cf format characters
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

func TestIsVideo(t *testing.T) {
	for _, p := range []string{"a.mp4", "b/c.MP4", "x.webm", "x.mov", "x.m4v", "x.mkv", "x.avi", "x.wmv", "x.ogv", "x.3gp", "dir.d/x.Mpeg"} {
		if !deploys.IsVideo(p) {
			t.Errorf("IsVideo(%q) = false", p)
		}
	}
	// .ts is TypeScript far more often than MPEG transport stream; audio,
	// images and names merely containing a video extension are not video.
	for _, p := range []string{"app.ts", "song.mp3", "song.m4a", "x.ogg", "a.gif", "mp4", "mp4/index.html", "x.mp4.txt", ".mp4x"} {
		if deploys.IsVideo(p) {
			t.Errorf("IsVideo(%q) = true", p)
		}
	}
	if exts := deploys.VideoExtensions(); !slices.IsSorted(exts) || !slices.Contains(exts, ".mp4") || slices.Contains(exts, ".ts") {
		t.Errorf("VideoExtensions() = %v", exts)
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{
		0:          "0 bytes",
		1:          "1 byte",
		1023:       "1023 bytes",
		1024:       "1 KiB",
		10 << 20:   "10 MiB",
		10<<20 + 1: "10485761 bytes (10.0 MiB)",
		1536:       "1536 bytes (1.5 KiB)",
		5 << 30:    "5 GiB",
		3 << 40:    "3 TiB",
		2048 << 40: "2048 TiB",
		15_728_640: "15 MiB",
		15_000_000: "15000000 bytes (14.3 MiB)",
	} {
		if got := deploys.FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
