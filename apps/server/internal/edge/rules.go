// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"crypto/sha256"
	"encoding/base64"
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

// stripCSPSource is the CSP hash source that allows stripScript.
var stripCSPSource = func() string {
	sum := sha256.Sum256([]byte(stripJS))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// allowStripScript adds stripCSPSource to each policy in a deploy's
// Content-Security-Policy value (policies are comma-separated), so the
// share-link strip script runs on the page the token opened. Only the
// directive governing inline script elements changes (script-src-elem,
// else script-src, else default-src), and only when the policy has one:
// one that already allows inline script (a bare 'unsafe-inline') is kept,
// since a hash would switch that off, and a lone 'none' becomes the hash.
// Every other directive is left byte for byte.
func allowStripScript(csp string) string {
	policies := strings.Split(csp, ",")
	for i, p := range policies {
		policies[i] = allowStripInPolicy(p)
	}
	return strings.Join(policies, ",")
}

func allowStripInPolicy(policy string) string {
	dirs := strings.Split(policy, ";")
	at := -1
	for _, want := range []string{"script-src-elem", "script-src", "default-src"} {
		for i, d := range dirs {
			// Browsers ignore a repeated directive, so the first one counts.
			if f := strings.Fields(d); len(f) > 0 && strings.EqualFold(f[0], want) {
				at = i
				break
			}
		}
		if at >= 0 {
			break
		}
	}
	if at < 0 {
		return policy
	}
	sources := strings.Fields(dirs[at])[1:]
	inline, keyed := false, false
	for _, src := range sources {
		lower := strings.ToLower(src)
		switch {
		case lower == "'unsafe-inline'":
			inline = true
		case lower == "'strict-dynamic'", strings.HasPrefix(lower, "'nonce-"), strings.HasPrefix(lower, "'sha256-"),
			strings.HasPrefix(lower, "'sha384-"), strings.HasPrefix(lower, "'sha512-"):
			keyed = true
		}
	}
	if inline && !keyed {
		return policy
	}
	d := dirs[at]
	trimmed := strings.TrimRight(d, " \t")
	if len(sources) == 1 && strings.EqualFold(sources[0], "'none'") {
		name := strings.Fields(d)[0]
		trimmed = d[:strings.Index(d, name)+len(name)]
	}
	dirs[at] = trimmed + " " + stripCSPSource + d[len(strings.TrimRight(d, " \t")):]
	return strings.Join(dirs, ";")
}
