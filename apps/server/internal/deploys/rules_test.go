// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// Redirects and response headers (C1.17).

func redirects(rs ...store.Redirect) *[]store.Redirect { return &rs }

func headerRules(hs ...store.HeaderRule) *[]store.HeaderRule { return &hs }

func set(name, value string) store.HeaderRule {
	return store.HeaderRule{Path: "/*", Set: map[string]string{name: value}}
}

func TestRulesAccepted(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	m := w.manifest(map[string]string{"index.html": "<h1>Home</h1>"})
	m.Redirects = redirects(
		store.Redirect{From: "/old", To: "/new"},
		store.Redirect{From: "/blog/*", To: "/news/:splat?from=blog#top", Status: 302},
		store.Redirect{From: "/*", To: "https://elsewhere.example.test/:splat", Status: 307},
		store.Redirect{From: "/café", To: "/caf%C3%A9-new", Status: 308},
		store.Redirect{From: "/.well-known/x", To: "/", Status: 301},
		store.Redirect{From: "/x/.wardlock/y", To: "/", Status: 301},
	)
	m.Headers = headerRules(
		store.HeaderRule{Path: "/*", Set: map[string]string{"x-frame-options": "DENY", "CONTENT-SECURITY-POLICY": "default-src 'self'"}},
		store.HeaderRule{Path: "/api/data.json", Set: map[string]string{"access-control-allow-origin": "*", "X-Content-Type-Options": "NoSniff"}},
	)
	d, _, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "docs", m)
	if err != nil {
		t.Fatal(err)
	}
	wantRedirects := []store.Redirect{
		{From: "/old", To: "/new", Status: 301},
		{From: "/blog/*", To: "/news/:splat?from=blog#top", Status: 302},
		{From: "/*", To: "https://elsewhere.example.test/:splat", Status: 307},
		{From: "/café", To: "/caf%C3%A9-new", Status: 308},
		{From: "/.well-known/x", To: "/", Status: 301},
		{From: "/x/.wardlock/y", To: "/", Status: 301},
	}
	wantHeaders := []store.HeaderRule{
		{Path: "/*", Set: map[string]string{"X-Frame-Options": "DENY", "Content-Security-Policy": "default-src 'self'"}},
		{Path: "/api/data.json", Set: map[string]string{"Access-Control-Allow-Origin": "*", "X-Content-Type-Options": "NoSniff"}},
	}
	got, err := w.db.Deploys().Get(t.Context(), store.ActorFor(w.owner.ID), d.SiteID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Redirects, wantRedirects) || !reflect.DeepEqual(got.Headers, wantHeaders) {
		t.Fatalf("stored rules\n %+v\n %+v\nwant\n %+v\n %+v", got.Redirects, got.Headers, wantRedirects, wantHeaders)
	}
	for _, name := range []string{"Referrer-Policy", "Access-Control-Max-Age"} {
		if !deploys.PublicOnlyHeader(name) {
			t.Errorf("PublicOnlyHeader(%s) = false", name)
		}
	}
	if deploys.PublicOnlyHeader("X-Frame-Options") {
		t.Error("PublicOnlyHeader(X-Frame-Options) = true")
	}
}

func TestRulesRefused(t *testing.T) {
	long := strings.Repeat("a", deploys.MaxRuleValue)
	many := func(n int) *[]store.Redirect {
		rs := make([]store.Redirect, n)
		for i := range rs {
			rs[i] = store.Redirect{From: fmt.Sprintf("/r%d", i), To: "/"}
		}
		return &rs
	}
	manyHeaders := func(n int) *[]store.HeaderRule {
		hs := make([]store.HeaderRule, n)
		for i := range hs {
			hs[i] = set("X-Frame-Options", "DENY")
		}
		return &hs
	}
	// big fills about n KiB of redirects with 8 KiB tos.
	big := func(n int) *[]store.Redirect {
		var rs []store.Redirect
		for i := 0; i < n/8; i++ {
			rs = append(rs, store.Redirect{From: fmt.Sprintf("/b%d", i), To: "/" + long[:deploys.MaxRuleValue-8]})
		}
		return &rs
	}
	invalid := func(name string, m deploys.Manifest, target string) struct {
		name   string
		m      deploys.Manifest
		err    error
		target string
	} {
		return struct {
			name   string
			m      deploys.Manifest
			err    error
			target string
		}{name, m, deploys.ErrInvalidConfig, target}
	}
	tests := []struct {
		name   string
		m      deploys.Manifest
		err    error
		target string
	}{
		invalid("from without a slash", deploys.Manifest{Redirects: redirects(store.Redirect{From: "old", To: "/"})}, "redirects[0]"),
		invalid("empty from", deploys.Manifest{Redirects: redirects(store.Redirect{From: "", To: "/"})}, "redirects[0]"),
		invalid("from with an empty segment", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/ok", To: "/"}, store.Redirect{From: "/a//b", To: "/"})}, "redirects[1]"),
		invalid("from with dot segments", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a/../b", To: "/"})}, "redirects[0]"),
		invalid("from with a star inside", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a/*/b", To: "/"})}, "redirects[0]"),
		invalid("from with a star not after a slash", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a*", To: "/"})}, "redirects[0]"),
		invalid("from with a query", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a?x=1", To: "/"})}, "redirects[0]"),
		invalid("from under /.wardlock/", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/.wardlock/login", To: "/"})}, "redirects[0]"),
		invalid("from under /.WARDLOCK/*", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/.WARDLOCK/*", To: "/"})}, "redirects[0]"),
		invalid("empty to", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: ""})}, "redirects[0]"),
		invalid("relative to", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "b"})}, "redirects[0]"),
		invalid("scheme-relative to", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "//elsewhere.example.test/"})}, "redirects[0]"),
		invalid("backslash to", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: `/\elsewhere.example.test/`})}, "redirects[0]"),
		invalid("http to", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "http://elsewhere.example.test/"})}, "redirects[0]"),
		invalid("javascript to", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "javascript:alert(1)"})}, "redirects[0]"),
		invalid("https without a host", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "https:///x"})}, "redirects[0]"),
		invalid("https with a user", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "https://user@elsewhere.example.test/"})}, "redirects[0]"),
		invalid("to with a space", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "/a b"})}, "redirects[0]"),
		invalid(":splat without a prefix", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "/b/:splat"})}, "redirects[0]"),
		invalid(":splat in the query", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a/*", To: "/b?x=:splat"})}, "redirects[0]"),
		invalid(":splat in the host", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a/*", To: "https://:splat.example.test/"})}, "redirects[0]"),
		invalid("status 200", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "/", Status: 200})}, "redirects[0]"),
		invalid("status 303", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "/", Status: 303})}, "redirects[0]"),
		invalid("status 304", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "/", Status: 304})}, "redirects[0]"),
		invalid("newline in to", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "/b\r\nSet-Cookie: x=y"})}, "redirects[0]"),
		invalid("NUL in from", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a\x00", To: "/"})}, "redirects[0]"),
		invalid("invalid UTF-8", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/\xff", To: "/"})}, "redirects[0]"),

		invalid("header path without a slash", deploys.Manifest{Headers: headerRules(store.HeaderRule{Path: "*", Set: map[string]string{"X-Frame-Options": "DENY"}})}, "headers[0]"),
		invalid("header path under /.wardlock/", deploys.Manifest{Headers: headerRules(store.HeaderRule{Path: "/.Wardlock/*", Set: map[string]string{"X-Frame-Options": "DENY"}})}, "headers[0]"),
		invalid("header path with dot segments", deploys.Manifest{Headers: headerRules(set("X-Frame-Options", "DENY"), store.HeaderRule{Path: "/a/./b", Set: map[string]string{"X-Frame-Options": "DENY"}})}, "headers[1]"),
		invalid("no headers", deploys.Manifest{Headers: headerRules(store.HeaderRule{Path: "/*"})}, "headers[0]"),
		invalid("empty value", deploys.Manifest{Headers: headerRules(set("X-Frame-Options", ""))}, "headers[0]"),
		invalid("newline in a value", deploys.Manifest{Headers: headerRules(set("X-Frame-Options", "DENY\r\nSet-Cookie: x=y"))}, "headers[0]"),
		invalid("tab in a value", deploys.Manifest{Headers: headerRules(set("Content-Security-Policy", "default-src\t'self'"))}, "headers[0]"),
		invalid("control character in a name", deploys.Manifest{Headers: headerRules(set("X-Frame-Options\n", "DENY"))}, "headers[0]"),
		invalid("X-Content-Type-Options other than nosniff", deploys.Manifest{Headers: headerRules(set("X-Content-Type-Options", "off"))}, "headers[0]"),
		invalid("one name twice", deploys.Manifest{Headers: headerRules(store.HeaderRule{Path: "/*", Set: map[string]string{"x-frame-options": "DENY", "X-Frame-Options": "SAMEORIGIN"}})}, "headers[0]"),
		invalid("an unknown header", deploys.Manifest{Headers: headerRules(set("X-Powered-By", "Wardlock"))}, "headers[0]"),
		invalid("X-Robots-Tag", deploys.Manifest{Headers: headerRules(set("X-Robots-Tag", "all"))}, "headers[0]"),
		invalid("Cross-Origin-Resource-Policy", deploys.Manifest{Headers: headerRules(set("Cross-Origin-Resource-Policy", "cross-origin"))}, "headers[0]"),
		invalid("a CORS request header", deploys.Manifest{Headers: headerRules(set("Access-Control-Request-Method", "GET"))}, "headers[0]"),

		{"1,001 redirects", deploys.Manifest{Redirects: many(deploys.MaxRedirects + 1)}, deploys.ErrLimitExceeded, "redirects"},
		{"101 header rules", deploys.Manifest{Headers: manyHeaders(deploys.MaxHeaderRules + 1)}, deploys.ErrLimitExceeded, "headers"},
		{"a from over 8 KiB", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/" + long, To: "/"})}, deploys.ErrLimitExceeded, "redirects[0]"},
		{"a to over 8 KiB", deploys.Manifest{Redirects: redirects(store.Redirect{From: "/a", To: "/"}, store.Redirect{From: "/b", To: "/" + long})}, deploys.ErrLimitExceeded, "redirects[1]"},
		{"a header path over 8 KiB", deploys.Manifest{Headers: headerRules(store.HeaderRule{Path: "/" + long, Set: map[string]string{"X-Frame-Options": "DENY"}})}, deploys.ErrLimitExceeded, "headers[0]"},
		{"a header value over 8 KiB", deploys.Manifest{Headers: headerRules(set("Content-Security-Policy", long+"a"))}, deploys.ErrLimitExceeded, "headers[0]"},
		{"redirects over 64 KiB", deploys.Manifest{Redirects: big(72)}, deploys.ErrLimitExceeded, "redirects"},
		{"redirects and headers over 64 KiB", deploys.Manifest{Redirects: big(56), Headers: headerRules(
			set("Content-Security-Policy", long[:deploys.MaxRuleValue-100]), set("Permissions-Policy", long[:deploys.MaxRuleValue-100]),
		)}, deploys.ErrLimitExceeded, "headers"},
	}
	for _, name := range []string{
		"Cache-Control", "Set-Cookie", "Content-Type", "Content-Length", "Content-Encoding", "Content-Disposition",
		"ETag", "Last-Modified", "Location", "Vary", "Strict-Transport-Security", "set-cookie",
	} {
		tests = append(tests, invalid("never owner-set "+name, deploys.Manifest{Headers: headerRules(set(name, "x"))}, "headers[0]"))
	}
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	for _, tc := range tests {
		m := w.manifest(map[string]string{"index.html": "<h1>Home</h1>"})
		m.Redirects, m.Headers = tc.m.Redirects, tc.m.Headers
		_, _, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "docs", m)
		var te *deploys.TargetError
		if !errors.Is(err, tc.err) || !errors.As(err, &te) || te.Target != tc.target || te.Message == "" {
			t.Errorf("%s: error %#v, want %v with target %s", tc.name, err, tc.err, tc.target)
		}
	}

	// At each limit, the deploy is accepted.
	for name, m := range map[string]deploys.Manifest{
		"1,000 redirects":   {Redirects: many(deploys.MaxRedirects)},
		"100 header rules":  {Headers: manyHeaders(deploys.MaxHeaderRules)},
		"an 8 KiB to":       {Redirects: redirects(store.Redirect{From: "/a", To: "/" + long[1:]})},
		"an 8 KiB value":    {Headers: headerRules(set("Content-Security-Policy", long))},
		"64 KiB, redirects": {Redirects: big(64)},
	} {
		base := w.manifest(map[string]string{"index.html": "<h1>Home</h1>"})
		base.Redirects, base.Headers = m.Redirects, m.Headers
		if _, _, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "docs", base); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPathDeployRules(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	m := w.manifest(basePages)
	baseRedirects := []store.Redirect{{From: "/old", To: "/new", Status: 301}}
	baseHeaders := []store.HeaderRule{{Path: "/*", Set: map[string]string{"X-Frame-Options": "DENY"}}}
	m.Redirects, m.Headers = &baseRedirects, &baseHeaders
	d, _, err := w.create(w.owner.ID, "docs", m)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Activate(t.Context(), store.ActorFor(w.owner.ID), "docs", d.ID); err != nil {
		t.Fatal(err)
	}
	own := []store.Redirect{{From: "/mockup-x/old", To: "/mockup-x/", Status: 302}}
	for name, tc := range map[string]struct {
		redirects *[]store.Redirect
		headers   *[]store.HeaderRule
		want      []store.Redirect
		wantH     []store.HeaderRule
	}{
		"left out, the base's":        {want: baseRedirects, wantH: baseHeaders},
		"its own redirects":           {redirects: &own, want: own, wantH: baseHeaders},
		"empty lists clear them both": {redirects: &[]store.Redirect{}, headers: &[]store.HeaderRule{}},
	} {
		pm := w.manifest(map[string]string{"index.html": "<h1>New mockup</h1>"})
		pm.Path, pm.Redirects, pm.Headers = "/mockup-x/", tc.redirects, tc.headers
		got, _, err := w.create(w.owner.ID, "docs", pm)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Redirects, tc.want) || !reflect.DeepEqual(got.Headers, tc.wantH) {
			t.Errorf("%s: rules %+v %+v, want %+v %+v", name, got.Redirects, got.Headers, tc.want, tc.wantH)
		}
	}
	// A full deploy that leaves them out has none.
	full, _, err := w.create(w.owner.ID, "docs", w.manifest(basePages))
	if err != nil {
		t.Fatal(err)
	}
	if full.Redirects != nil || full.Headers != nil {
		t.Fatalf("full deploy rules %+v %+v, want none", full.Redirects, full.Headers)
	}
}
