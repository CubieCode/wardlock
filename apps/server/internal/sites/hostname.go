// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
)

// ErrInvalidHostname means a hostname cannot be a custom domain. The
// HostnameError wrapping it says why.
var ErrInvalidHostname = errors.New("invalid hostname")

// HostnameError explains why a hostname was refused, in words fit for the
// owner.
type HostnameError struct{ Reason string }

func (e *HostnameError) Error() string { return "invalid hostname: " + e.Reason }
func (e *HostnameError) Unwrap() error { return ErrInvalidHostname }

func badHostname(format string, args ...any) error {
	return &HostnameError{Reason: fmt.Sprintf(format, args...)}
}

// maxHostname is the longest hostname DNS allows, without the trailing dot.
const maxHostname = 253

// hostnameProfile converts a typed hostname to its A-label form: Unicode is
// mapped (case-folded and normalised) as browsers do for lookup, every
// label must be a valid letter-digit-hyphen label after conversion, and
// the DNS length limits apply.
var hostnameProfile = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.StrictDomainName(true),
	idna.VerifyDNSLength(true),
	idna.Transitional(false),
)

// NormaliseHostname returns raw as a custom domain is stored: lowercase,
// IDN labels as A-labels (punycode), without a trailing dot or port. It
// refuses IP addresses, wildcards, single labels and anything that is not
// a DNS hostname. Instance rules (the base domain, the dashboard host) are
// Service.CheckHostname's.
func NormaliseHostname(raw string) (string, error) {
	h := strings.TrimSpace(raw)
	if strings.HasPrefix(h, "[") {
		return "", badHostname("IP addresses cannot be custom domains.")
	}
	if host, port, err := net.SplitHostPort(h); err == nil && port != "" && strings.Trim(port, "0123456789") == "" {
		h = host
	}
	h = strings.TrimSuffix(h, ".")
	switch {
	case h == "":
		return "", badHostname("Enter a hostname, such as docs.example.com.")
	case isIPAddress(h):
		return "", badHostname("IP addresses cannot be custom domains.")
	case strings.Contains(h, "*"):
		return "", badHostname("Wildcard domains are not supported; add each hostname separately.")
	}
	a, err := hostnameProfile.ToASCII(h)
	if err != nil {
		return "", badHostname("%q is not a valid hostname.", raw)
	}
	a = strings.ToLower(a)
	switch {
	case len(a) > maxHostname:
		return "", badHostname("Hostnames are at most %d characters.", maxHostname)
	case !strings.Contains(a, "."):
		return "", badHostname("Enter a full hostname, such as docs.example.com.")
	case isIPAddress(a):
		return "", badHostname("IP addresses cannot be custom domains.")
	case strings.Trim(a[strings.LastIndexByte(a, '.')+1:], "0123456789") == "":
		// An all-numeric top label is an IP address in some notation
		// (1.2.3, 0x7f.1 is caught by the profile), never a DNS name.
		return "", badHostname("IP addresses cannot be custom domains.")
	}
	return a, nil
}

func isIPAddress(h string) bool {
	_, err := netip.ParseAddr(h)
	return err == nil
}

// CheckHostname normalises raw and applies the instance's rules: the base
// domain and every name under it (sites and the auth origin) and the
// dashboard host belong to Wardlock and cannot be custom domains.
func (s *Service) CheckHostname(raw string) (string, error) {
	h, err := NormaliseHostname(raw)
	if err != nil {
		return "", err
	}
	switch {
	case h == s.c.BaseDomain || strings.HasSuffix(h, "."+s.c.BaseDomain):
		return "", badHostname("%s is under this server's base domain, %s; sites there need no custom domain.", h, s.c.BaseDomain)
	case h == s.c.DashboardHost:
		return "", badHostname("%s is this server's dashboard host.", h)
	}
	return h, nil
}
