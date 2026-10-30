// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"net/http"
	"net/url"
	"strings"
)

// splat is replaced in a redirect's to by what a /* prefix matched.
const splat = ":splat"

// matchRule reports whether pattern, an exact path or a prefix ending in
// /*, matches clean, a cleaned request path, and returns what a prefix
// matched past its slash ("" for an exact match).
func matchRule(pattern, clean string) (rest string, ok bool) {
	if base, isPrefix := strings.CutSuffix(pattern, "/*"); isPrefix {
		return strings.CutPrefix(clean, base+"/")
	}
	return "", pattern == clean
}

// ownerRedirect finds the first of the deploy's redirects matching clean
// and returns its Location and status. The request's query is kept unless
// to has its own; a share-link token in it is kept only on the same site,
// never sent to another origin.
func ownerRedirect(s *served, clean, rawQuery string) (location string, status int, ok bool) {
	for _, rule := range s.site.Redirects {
		rest, ok := matchRule(rule.From, clean)
		if !ok {
			continue
		}
		return redirectLocation(rule.To, rest, rawQuery), rule.Status, true
	}
	return "", 0, false
}

// redirectLocation builds a redirect's Location from to: rest, escaped
// segment by segment, replaces :splat in its path, and rawQuery is added
// before any fragment unless to has a query of its own.
func redirectLocation(to, rest, rawQuery string) string {
	base, fragment, hasFragment := strings.Cut(to, "#")
	base, query, hasQuery := strings.Cut(base, "?")
	if strings.Contains(base, splat) {
		// Escaping keeps a backslash or ? in the remainder from turning a
		// path into another host or a query. A cleaned path never leaves a
		// remainder starting with a slash, but /:splat must not become a
		// scheme-relative //host URL whatever it is given.
		segs := strings.Split(strings.TrimLeft(rest, "/"), "/")
		for i, seg := range segs {
			segs[i] = url.PathEscape(seg)
		}
		base = strings.ReplaceAll(base, splat, strings.Join(segs, "/"))
	}
	if !hasQuery && rawQuery != "" {
		if !strings.HasPrefix(to, "/") {
			// Another origin: a share-link token must not leave the site.
			rawQuery = queryWithoutToken(rawQuery)
		}
		query, hasQuery = rawQuery, rawQuery != ""
	}
	loc := base
	if hasQuery {
		loc += "?" + query
	}
	if hasFragment {
		loc += "#" + fragment
	}
	return loc
}

// publicOnlyHeader reports whether a deploy-set header is sent only on
// responses a public policy governs.
func publicOnlyHeader(name string) bool {
	return name == "Referrer-Policy" || strings.HasPrefix(name, "Access-Control-")
}

// ownerHeaders sets the deploy's header rules matching clean on h, every
// match in order. Under a protected policy the public-only headers are
// left out, and Wardlock's own headers are set again afterwards, so they
// always win.
func ownerHeaders(h http.Header, s *served, clean string, protected bool) {
	for _, rule := range s.site.Headers {
		if _, ok := matchRule(rule.Path, clean); !ok {
			continue
		}
		for name, value := range rule.Set {
			if protected && publicOnlyHeader(name) {
				continue
			}
			h.Set(name, value)
		}
	}
	h.Set("X-Content-Type-Options", "nosniff")
	if protected {
		protectedHeaders(h)
	}
}
