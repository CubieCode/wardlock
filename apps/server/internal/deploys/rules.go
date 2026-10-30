// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/store"
)

// Limits on a deploy's redirects and headers (SPEC "Redirects and response
// headers"): checked when the deploy is created.
const (
	MaxRedirects   = 1000
	MaxHeaderRules = 100
	// MaxRuleValue bounds each from, to, path, header name and header value.
	MaxRuleValue = 8 << 10
	// MaxRulesBytes bounds those strings summed over every rule.
	MaxRulesBytes = 64 << 10
)

// DefaultRedirectStatus is a redirect's status when the rule gives none.
const DefaultRedirectStatus = 301

// redirectStatuses are the statuses a redirect may answer.
var redirectStatuses = []int{301, 302, 307, 308}

// splat is replaced in a redirect's to by what a /* prefix matched.
const splat = ":splat"

// ownerHeaders are the response headers a deploy may set, by lowercase
// name, with the canonical spelling they are stored and sent in. Those
// marked publicOnly are not sent on responses a protected policy governs
// (the edge's call: policies may change after the deploy).
var ownerHeaders = map[string]struct {
	name       string
	publicOnly bool
}{
	"content-security-policy":             {name: "Content-Security-Policy"},
	"content-security-policy-report-only": {name: "Content-Security-Policy-Report-Only"},
	"permissions-policy":                  {name: "Permissions-Policy"},
	"x-frame-options":                     {name: "X-Frame-Options"},
	"cross-origin-opener-policy":          {name: "Cross-Origin-Opener-Policy"},
	"cross-origin-embedder-policy":        {name: "Cross-Origin-Embedder-Policy"},
	"x-content-type-options":              {name: "X-Content-Type-Options"},
	"link":                                {name: "Link"},
	"referrer-policy":                     {name: "Referrer-Policy", publicOnly: true},
	"access-control-allow-origin":         {name: "Access-Control-Allow-Origin", publicOnly: true},
	"access-control-allow-credentials":    {name: "Access-Control-Allow-Credentials", publicOnly: true},
	"access-control-allow-headers":        {name: "Access-Control-Allow-Headers", publicOnly: true},
	"access-control-allow-methods":        {name: "Access-Control-Allow-Methods", publicOnly: true},
	"access-control-expose-headers":       {name: "Access-Control-Expose-Headers", publicOnly: true},
	"access-control-max-age":              {name: "Access-Control-Max-Age", publicOnly: true},
}

// PublicOnlyHeader reports whether a canonical header name a deploy set is
// sent only on responses a public policy governs.
func PublicOnlyHeader(name string) bool {
	h, ok := ownerHeaders[strings.ToLower(name)]
	return ok && h.publicOnly
}

// wardlockHeader reports whether name, lowercase, is one only Wardlock ever
// sets: caching, cookies, the body's description, redirects, variants and
// transport security.
func wardlockHeader(name string) bool {
	switch name {
	case "cache-control", "set-cookie", "etag", "last-modified", "location", "vary", "strict-transport-security":
		return true
	}
	return strings.HasPrefix(name, "content-") && ownerHeaders[name].name == ""
}

// validateRules checks m's redirects and headers in place: a redirect
// without a status gets DefaultRedirectStatus and header names their
// canonical spelling. Errors are *TargetError wrapping ErrInvalidConfig
// with the rule (redirects[i], headers[i]) as target, or ErrLimitExceeded
// for a limit, with the list or the rule as target.
func validateRules(m *Manifest) error {
	var redirects []store.Redirect
	if m.Redirects != nil {
		redirects = *m.Redirects
	}
	var headers []store.HeaderRule
	if m.Headers != nil {
		headers = *m.Headers
	}
	if len(redirects) > MaxRedirects {
		return &TargetError{Err: ErrLimitExceeded, Target: "redirects", Message: fmt.Sprintf("The deploy has %d redirects; the limit is %d.", len(redirects), MaxRedirects)}
	}
	if len(headers) > MaxHeaderRules {
		return &TargetError{Err: ErrLimitExceeded, Target: "headers", Message: fmt.Sprintf("The deploy has %d header rules; the limit is %d.", len(headers), MaxHeaderRules)}
	}
	total := 0
	for i := range redirects {
		r := &redirects[i]
		target := fmt.Sprintf("redirects[%d]", i)
		n, err := checkRedirect(r, target)
		if err != nil {
			return err
		}
		total += n
	}
	if total > MaxRulesBytes {
		return rulesTooLarge("redirects", total)
	}
	for i := range headers {
		h := &headers[i]
		target := fmt.Sprintf("headers[%d]", i)
		n, err := checkHeaderRule(h, target)
		if err != nil {
			return err
		}
		total += n
	}
	if total > MaxRulesBytes {
		return rulesTooLarge("headers", total)
	}
	return nil
}

func rulesTooLarge(target string, total int) error {
	return &TargetError{Err: ErrLimitExceeded, Target: target, Message: fmt.Sprintf("The deploy's redirects and headers add up to %s; the limit is %s.", FormatBytes(int64(total)), FormatBytes(MaxRulesBytes))}
}

// checkRedirect validates one redirect and returns the bytes it counts
// towards MaxRulesBytes.
func checkRedirect(r *store.Redirect, target string) (int, error) {
	invalid := func(format string, a ...any) (int, error) {
		return 0, &TargetError{Err: ErrInvalidConfig, Target: target, Message: fmt.Sprintf("Redirect %s %s.", target, fmt.Sprintf(format, a...))}
	}
	for _, v := range []struct{ field, value string }{{"from", r.From}, {"to", r.To}} {
		if err := checkValue(target, "Redirect", v.field, v.value); err != nil {
			return 0, err
		}
	}
	prefix, why := checkRulePath(r.From)
	if why != "" {
		return invalid("from %q %s", r.From, why)
	}
	if r.Status == 0 {
		r.Status = DefaultRedirectStatus
	}
	if !slices.Contains(redirectStatuses, r.Status) {
		return invalid("status %d is not one of 301, 302, 307 or 308", r.Status)
	}
	if strings.ContainsFunc(r.To, unicode.IsSpace) {
		return invalid("to %q contains a space; percent-encode it", r.To)
	}
	host, rest := "", r.To
	if after, ok := strings.CutPrefix(r.To, "https://"); ok {
		u, err := url.Parse(r.To)
		if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" {
			return invalid("to %q is not a valid https:// URL with a host", r.To)
		}
		i := strings.IndexAny(after, "/?#")
		if i < 0 {
			i = len(after)
		}
		host, rest = after[:i], after[i:]
	} else {
		if !strings.HasPrefix(r.To, "/") || strings.HasPrefix(r.To, "//") || strings.HasPrefix(r.To, `/\`) {
			return invalid("to %q must be a path on this site starting with a single /, or an https:// URL", r.To)
		}
		if _, err := url.Parse(r.To); err != nil {
			return invalid("to %q is not a valid path", r.To)
		}
	}
	if strings.Contains(r.To, splat) && !prefix {
		return invalid("to uses %s, but from %q does not end in /*", splat, r.From)
	}
	if i := strings.IndexAny(rest, "?#"); strings.Contains(host, splat) || i >= 0 && strings.Contains(rest[i:], splat) {
		return invalid("to has %s outside its path, where it is not replaced", splat)
	}
	return len(r.From) + len(r.To), nil
}

// checkHeaderRule validates one header rule, canonicalising its names, and
// returns the bytes it counts towards MaxRulesBytes.
func checkHeaderRule(h *store.HeaderRule, target string) (int, error) {
	invalid := func(format string, a ...any) (int, error) {
		return 0, &TargetError{Err: ErrInvalidConfig, Target: target, Message: fmt.Sprintf("Header rule %s %s.", target, fmt.Sprintf(format, a...))}
	}
	if err := checkValue(target, "Header rule", "path", h.Path); err != nil {
		return 0, err
	}
	if _, why := checkRulePath(h.Path); why != "" {
		return invalid("path %q %s", h.Path, why)
	}
	if len(h.Set) == 0 {
		return invalid("sets no headers")
	}
	n := len(h.Path)
	set := make(map[string]string, len(h.Set))
	// Sorted, so a rule naming one header twice is refused the same way
	// every time.
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range h.Set {
			if !yield(k) {
				return
			}
		}
	}) {
		value := h.Set[name]
		for _, v := range []struct{ field, value string }{{"header name", name}, {name, value}} {
			if err := checkValue(target, "Header rule", v.field, v.value); err != nil {
				return 0, err
			}
		}
		lower := strings.ToLower(name)
		allowed, ok := ownerHeaders[lower]
		switch {
		case wardlockHeader(lower):
			return invalid("sets %s, which only Wardlock sets", name)
		case !ok:
			return invalid("sets %s, which a deploy may not set; see SPEC \"Redirects and response headers\" for the headers it may", name)
		case value == "":
			return invalid("gives %s no value", name)
		case lower == "x-content-type-options" && !strings.EqualFold(value, "nosniff"):
			return invalid("sets X-Content-Type-Options to %q; the only value is nosniff, which Wardlock always sends", value)
		}
		if _, dup := set[allowed.name]; dup {
			return invalid("sets %s twice", allowed.name)
		}
		set[allowed.name] = value
		n += len(name) + len(value)
	}
	h.Set = set
	return n, nil
}

// checkValue refuses a rule string that is not valid UTF-8, holds a
// control character or passes MaxRuleValue (ErrLimitExceeded).
func checkValue(target, kind, field, v string) error {
	if len(v) > MaxRuleValue {
		return &TargetError{Err: ErrLimitExceeded, Target: target, Message: fmt.Sprintf("%s %s: %s is %s; the limit is %s.", kind, target, field, FormatBytes(int64(len(v))), FormatBytes(MaxRuleValue))}
	}
	if !utf8.ValidString(v) || strings.ContainsFunc(v, unicode.IsControl) {
		return &TargetError{Err: ErrInvalidConfig, Target: target, Message: fmt.Sprintf("%s %s: %s must be valid UTF-8 without control characters.", kind, target, field)}
	}
	return nil
}

// checkRulePath checks a redirect's from or a header rule's path: an exact
// absolute path, or a prefix ending in /* (prefix true). Either must be a
// clean path, as the edge matches the cleaned request path, and never
// under /.wardlock/, where rules never apply.
func checkRulePath(p string) (prefix bool, why string) {
	if !strings.HasPrefix(p, "/") {
		return false, "must start with /"
	}
	exact := p
	if base, ok := strings.CutSuffix(p, "/*"); ok {
		prefix, exact = true, base+"/"
	}
	if strings.ContainsAny(exact, "*?#") {
		return false, "may hold * only as a final /*, and no ? or #"
	}
	clean := path.Clean(exact)
	if strings.HasSuffix(exact, "/") && clean != "/" {
		clean += "/"
	}
	if clean != exact {
		return false, "is not a clean path (no empty, . or .. segments)"
	}
	first, _, _ := strings.Cut(exact[1:], "/")
	if strings.EqualFold(first, ".wardlock") {
		return false, "is under /.wardlock/, which Wardlock serves itself"
	}
	return prefix, ""
}
