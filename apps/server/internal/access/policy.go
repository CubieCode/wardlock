// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/store"
)

// DefaultPrefix is the path prefix of a site's default policy.
const DefaultPrefix = "/"

// maxPrefixBytes matches the policies.path_prefix CHECK.
const maxPrefixBytes = 1024

// Rule is what an owner sets for a path prefix. Password, email-code and
// OAuth settings join it with A2.3, A2.4 and A2.5.
type Rule struct {
	// PathPrefix is "/" for the site default, or a directory prefix such
	// as "/docs/".
	PathPrefix string
	Visibility store.Visibility
	// Links enables share links (A2.2) on a protected policy.
	Links bool
}

// newDefault is the policy a site has before its owner sets one: public,
// so deploys keep working and protection is opt-in (SPEC "Policies").
func newDefault(siteID string, now timeFunc) store.Policy {
	at := now()
	return store.Policy{
		ID: store.NewID(), SiteID: siteID, PathPrefix: DefaultPrefix,
		Visibility: store.Public,
		Version:    1, CreatedAt: at, UpdatedAt: at,
	}
}

// CheckPrefix returns a TargetError wrapping ErrInvalidPath unless p is "/"
// or a clean absolute directory prefix ending in "/": valid UTF-8, at most
// 1024 bytes, no empty, "." or ".." segments, no control or invisible
// characters or backslashes, and not under the reserved /.wardlock/ path.
func CheckPrefix(p string) error {
	invalid := func(why string) error {
		return &TargetError{Err: ErrInvalidPath, Target: "path", Message: fmt.Sprintf("path %s.", why)}
	}
	switch {
	case !utf8.ValidString(p):
		return invalid("is not valid UTF-8")
	case p == "" || len(p) > maxPrefixBytes:
		return invalid(fmt.Sprintf("must be 1 to %d bytes", maxPrefixBytes))
	case p[0] != '/' || p[len(p)-1] != '/':
		return invalid("must start and end with /, like /docs/")
	case strings.ContainsFunc(p, func(r rune) bool { return r == '\\' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }):
		return invalid("contains a backslash, control or invisible format character")
	case p != DefaultPrefix && (path.Clean(p) == DefaultPrefix || path.Clean(p)+"/" != p):
		return invalid("has an empty, . or .. segment")
	}
	first, _, _ := strings.Cut(p[1:], "/")
	if strings.EqualFold(first, ".wardlock") {
		return invalid("is under the reserved /.wardlock/ path")
	}
	return nil
}

// checkRule validates r against SPEC "Policies": a public policy has no
// methods and a protected one at least one.
func checkRule(r Rule) error {
	if err := CheckPrefix(r.PathPrefix); err != nil {
		return err
	}
	switch r.Visibility {
	case store.Public:
		if r.Links {
			return &TargetError{Err: ErrInvalidPolicy, Target: "methods", Message: "A public policy takes no methods."}
		}
	case store.Protected:
		if !r.Links {
			return &TargetError{Err: ErrInvalidPolicy, Target: "methods", Message: "A protected policy needs at least one method."}
		}
	default:
		return &TargetError{Err: ErrInvalidPolicy, Target: "visibility", Message: "visibility must be public or protected."}
	}
	return nil
}

// Governing returns the policy for a cleaned absolute request path: the one
// with the longest prefix matching it, falling back to the default. A
// prefix "/docs/" also governs "/docs" itself, so the directory's
// clean-URL and redirect answers sit behind the same policy as its
// contents. Policies do not stack. ok is false when nothing matches, which
// happens only without a default.
func Governing(policies []store.Policy, reqPath string) (store.Policy, bool) {
	var best store.Policy
	found := false
	for _, p := range policies {
		if !strings.HasPrefix(reqPath, p.PathPrefix) && reqPath+"/" != p.PathPrefix {
			continue
		}
		if !found || len(p.PathPrefix) > len(best.PathPrefix) {
			best, found = p, true
		}
	}
	return best, found
}

// enables reports whether p accepts sessions created by m. A public policy
// enables nothing (and needs nothing).
func enables(p store.Policy, m store.SessionMethod) bool {
	if p.Visibility != store.Protected {
		return false
	}
	switch m {
	case store.SessionLink:
		return p.LinksEnabled
	case store.SessionPassword:
		return p.PasswordEnabled
	case store.SessionEmail:
		return p.EmailEnabled
	case store.SessionOAuth:
		return p.OAuthEnabled
	}
	return false
}
