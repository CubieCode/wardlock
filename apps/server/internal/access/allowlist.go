// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"fmt"
	netmail "net/mail"
	"slices"
	"strings"

	"wardlock.nz/server/internal/store"
)

// Allowlist bounds (SPEC "Email code").
const (
	// MaxAllowlistEntries bounds a policy's allowlist, all kinds together.
	MaxAllowlistEntries = 1000
	maxAddressBytes     = 254
	maxDomainBytes      = 253
	maxLabelBytes       = 63
)

// NormaliseAddress trims and ASCII-lower-cases a plain email address (no
// display name), as allowlists store and match addresses. ok is false for
// anything else.
func NormaliseAddress(raw string) (string, bool) {
	e := asciiLower(strings.TrimSpace(raw))
	if len(e) < 3 || len(e) > maxAddressBytes {
		return "", false
	}
	a, err := netmail.ParseAddress(e)
	if err != nil || a.Name != "" || a.Address != e {
		return "", false
	}
	return e, true
}

// normaliseDomain returns raw as an allowlist domain: a lower-case DNS name
// of A-labels with at least two labels, optionally after "*." (its
// subdomains). One trailing dot is dropped. ok is false for anything else,
// IDNs in Unicode form included (the owner gives the xn-- form).
func normaliseDomain(raw string) (string, bool) {
	d := asciiLower(strings.TrimSpace(raw))
	d = strings.TrimSuffix(d, ".")
	name, wildcard := strings.CutPrefix(d, "*.")
	if len(name) > maxDomainBytes {
		return "", false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, l := range labels {
		if l == "" || len(l) > maxLabelBytes || l[0] == '-' || l[len(l)-1] == '-' {
			return "", false
		}
		if strings.ContainsFunc(l, func(r rune) bool { return !ldh(r) }) {
			return "", false
		}
	}
	// A name ending in a numeric label is an IP address, not a domain.
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "", false
	}
	if wildcard {
		name = "*." + name
	}
	// The stored value, wildcard included, is a DNS name: 253 bytes at
	// most (policy_allowlist.value allows 254).
	if len(name) > maxDomainBytes {
		return "", false
	}
	return name, true
}

// checkAllowlist normalises the entries of a, which an owner is setting,
// sorted and without duplicates, or returns a TargetError naming the first
// entry it cannot read.
func checkAllowlist(a store.Allowlist) (store.Allowlist, error) {
	invalid := func(msg string) error {
		return &TargetError{Err: ErrInvalidPolicy, Target: "allowlist", Message: msg}
	}
	if a.Len() > MaxAllowlistEntries {
		return store.Allowlist{}, invalid(fmt.Sprintf("An allowlist holds at most %d entries.", MaxAllowlistEntries))
	}
	out := store.Allowlist{Emails: []string{}, Domains: []string{}, GitHubOrgs: []string{}}
	for _, raw := range a.Emails {
		e, ok := NormaliseAddress(raw)
		if !ok {
			return store.Allowlist{}, invalid(fmt.Sprintf("%q is not an email address like name@example.com.", clip(raw)))
		}
		out.Emails = append(out.Emails, e)
	}
	for _, raw := range a.Domains {
		d, ok := normaliseDomain(raw)
		if !ok {
			return store.Allowlist{}, invalid(fmt.Sprintf("%q is not a domain like example.com or *.example.com (international names in their xn-- form).", clip(raw)))
		}
		out.Domains = append(out.Domains, d)
	}
	for _, raw := range a.GitHubOrgs {
		o, ok := normaliseGitHubOrg(raw)
		if !ok {
			return store.Allowlist{}, invalid(fmt.Sprintf("%q is not a GitHub organisation name like example-org.", clip(raw)))
		}
		out.GitHubOrgs = append(out.GitHubOrgs, o)
	}
	for _, l := range []*[]string{&out.Emails, &out.Domains, &out.GitHubOrgs} {
		slices.Sort(*l)
		*l = slices.Compact(*l)
	}
	return out, nil
}

// maxGitHubOrgBytes is GitHub's limit on organisation names.
const maxGitHubOrgBytes = 39

// normaliseGitHubOrg returns raw as an allowlist GitHub organisation: its
// login, lower case (GitHub compares them without case): ASCII letters,
// digits and single hyphens, neither first nor last, at most 39 bytes.
func normaliseGitHubOrg(raw string) (string, bool) {
	o := asciiLower(strings.TrimSpace(raw))
	if o == "" || len(o) > maxGitHubOrgBytes || o[0] == '-' || o[len(o)-1] == '-' || strings.Contains(o, "--") ||
		strings.ContainsFunc(o, func(r rune) bool { return !ldh(r) }) {
		return "", false
	}
	return o, true
}

// errOAuthNeedsAllowlist answers the OAuth method with an empty allowlist
// to set or keep.
var errOAuthNeedsAllowlist = &TargetError{Err: ErrInvalidPolicy, Target: "allowlist", Message: "The OAuth method needs at least one allowed address, domain or GitHub organisation."}

// errNeedsAllowlist answers the email method with no addresses or domains
// to set or keep.
var errNeedsAllowlist = &TargetError{Err: ErrInvalidPolicy, Target: "allowlist", Message: "The email method needs at least one allowed address or domain."}

// clip shortens an entry quoted back in an error message.
func clip(s string) string {
	if r := []rune(s); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

// allows reports whether a lets in addr, a normalised address: listed
// exactly, at a listed domain (example.com: that domain only), or at a
// subdomain of a listed *.example.com (not example.com itself).
func allows(a store.Allowlist, addr string) bool {
	if slices.Contains(a.Emails, addr) {
		return true
	}
	at := strings.LastIndexByte(addr, '@')
	if at < 0 {
		return false
	}
	domain := addr[at+1:]
	for _, d := range a.Domains {
		if base, ok := strings.CutPrefix(d, "*."); ok {
			if strings.HasSuffix(domain, "."+base) {
				return true
			}
		} else if domain == d {
			return true
		}
	}
	return false
}

// ldh reports whether r may appear in a lower-case DNS label: an ASCII
// letter, digit or hyphen.
func ldh(r rune) bool { return 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '-' }

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}
