// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode"
)

// sameHostTarget reports whether a browser following Location: loc from
// https://docs.example.test/ stays on that host. Browsers drop tabs and
// newlines from URLs and read a backslash as a slash, so a path that
// survives those still has to be a plain path.
func sameHostTarget(loc string) bool {
	browser := strings.NewReplacer("\t", "", "\n", "", "\r", "", `\`, "/").Replace(loc)
	if !strings.HasPrefix(browser, "/") || strings.HasPrefix(browser, "//") {
		return false
	}
	base, _ := url.Parse("https://docs.example.test/")
	u, err := base.Parse(browser)
	return err == nil && u.Host == base.Host && u.Scheme == base.Scheme
}

func FuzzSafeNext(f *testing.F) {
	for _, s := range []string{
		"/", "/docs/", "/docs/a.html", "", "//evil.example", `/\evil.example`, "/\t/evil.example",
		"https://evil.example/", "/.wardlock/login", "/./.wardlock/x", "/docs/../.wardlock/", "/a?b#c", "/%2e%2e/",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := safeNext(raw)
		if !sameHostTarget(got) || strings.ContainsAny(got, `\?#`) || strings.ContainsFunc(got, unicode.IsControl) {
			t.Fatalf("safeNext(%q) = %q leaves the site", raw, got)
		}
		if reserved(strings.TrimPrefix(got, "/")) {
			t.Fatalf("safeNext(%q) = %q returns to the reserved path", raw, got)
		}
	})
}

func FuzzLocalPath(f *testing.F) {
	for _, s := range []string{"/", "/docs/?x=1", "//evil.example", `/\evil.example`, "/\t/evil.example", "/\n\\evil.example", "evil", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if got := localPath(raw); !sameHostTarget(got) {
			t.Fatalf("localPath(%q) = %q leaves the site", raw, got)
		}
	})
}

// FuzzLinkTokenFromRequest checks how the edge reads a share-link token from
// a request: the first t parameter wins over Authorization, and the URL
// the interstitial returns to keeps no t parameter in any spelling.
func FuzzLinkTokenFromRequest(f *testing.F) {
	f.Add("t=wl_aaaaaaaaaa_x", "Bearer wl_bbbbbbbbbb_y")
	f.Add("a=1&t=x&t=y", "")
	f.Add("%74=x&T=y", "bearer z")
	f.Add("t=%zz&t=ok", "Bearer   spaced  ")
	f.Add("x=1;t=2&&t", "Basic abc")
	f.Fuzz(func(t *testing.T, rawQuery, authorization string) {
		r := &http.Request{URL: &url.URL{Path: "/docs/", RawQuery: rawQuery}, Header: http.Header{}}
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		token, fromQuery := linkToken(r)
		if q, ok := firstQueryValue(rawQuery, tokenParam); ok != fromQuery || (ok && q != token) {
			t.Fatalf("linkToken = %q, %v; first t = %q, %v", token, fromQuery, q, ok)
		}
		if !fromQuery && token != "" {
			scheme, cred, _ := strings.Cut(authorization, " ")
			if !strings.EqualFold(scheme, "Bearer") || token != strings.TrimSpace(cred) {
				t.Fatalf("linkToken read %q from Authorization %q", token, authorization)
			}
		}
		back := withoutToken(r)
		if !strings.HasPrefix(back, "/docs/") {
			t.Fatalf("withoutToken = %q", back)
		}
		_, query, _ := strings.Cut(back, "?")
		if _, ok := firstQueryValue(query, tokenParam); ok {
			t.Fatalf("withoutToken(%q) = %q still has a t parameter", rawQuery, back)
		}
	})
}
