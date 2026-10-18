// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"bytes"
	"strings"
	"testing"
)

func TestInjectStrip(t *testing.T) {
	s := stripScript
	for _, tc := range []struct {
		name, doc, want string
	}{
		{"before </head>", "<html><head><title>x</title></head><body>y</body>", "<html><head><title>x</title>" + s + "</head><body>y</body>"},
		{"upper case", "<HTML><HEAD></HEAD><BODY>", "<HTML><HEAD>" + s + "</HEAD><BODY>"},
		{"before <body without a head", "<p>a</p><Body class=x>", "<p>a</p>" + s + "<Body class=x>"},
		{"after a doctype", "\xef\xbb\xbf \n<!DOCTYPE html><p>x", "\xef\xbb\xbf \n<!DOCTYPE html>" + s + "<p>x"},
		{"at the start", "<p>x</p>", s + "<p>x</p>"},
		{"empty", "", s},
		// Unicode lower-casing changes these characters' byte lengths;
		// the script must still land exactly before </head>.
		{"Turkish title", "<head><title>İstanbul İzmir KELVİN K</title></head>", "<head><title>İstanbul İzmir KELVİN K</title>" + s + "</head>"},
		{"Latin-1 page", "<head><title>caf\xe9 cr\xe8me</title></head><body>", "<head><title>caf\xe9 cr\xe8me</title>" + s + "</head><body>"},
		{"Latin-1 before </head> at the end", "<p>" + strings.Repeat("\xe9", 20) + "</head>", "<p>" + strings.Repeat("\xe9", 20) + s + "</head>"},
		{"Latin-1 before <body", strings.Repeat("\xe9", 5) + "<body>", strings.Repeat("\xe9", 5) + s + "<body>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(injectStrip([]byte(tc.doc))); got != tc.want {
				t.Fatalf("injectStrip(%q) =\n%q\nwant\n%q", tc.doc, got, tc.want)
			}
		})
	}
}

func FuzzInjectStrip(f *testing.F) {
	f.Add([]byte("<html><head></head><body></body></html>"))
	f.Add([]byte("<p>" + strings.Repeat("\xe9", 20) + "</head>"))
	f.Add([]byte("<title>İKȺ</title></HEAD>"))
	f.Add([]byte("\xef\xbb\xbf<!doctype html>"))
	f.Fuzz(func(t *testing.T, doc []byte) {
		out := injectStrip(doc)
		at := bytes.Index(out, []byte(stripScript))
		if len(out) != len(doc)+len(stripScript) || at < 0 {
			t.Fatalf("output is not the input plus the script: %q", out)
		}
		// Removing the script gives back exactly the input.
		if back := append(append([]byte{}, out[:at]...), out[at+len(stripScript):]...); !bytes.Equal(back, doc) {
			t.Fatalf("input changed: %q -> %q", doc, out)
		}
	})
}
