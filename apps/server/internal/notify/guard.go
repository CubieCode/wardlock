// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"syscall"
)

// errRefusedAddress means a webhook's host resolved to an address Wardlock
// does not connect to.
var errRefusedAddress = errors.New("webhook address refused")

var (
	// The rest of what IPv4 calls "this network", and broadcast: netip
	// names only 0.0.0.0 and has no method for 255.255.255.255.
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	broadcast   = netip.MustParsePrefix("255.255.255.255/32")
	// sharedAddresses (carrier-grade NAT) are private in all but name.
	sharedAddresses = netip.MustParsePrefix("100.64.0.0/10")
	// nat64 carries an IPv4 address in its last four bytes, which a gateway
	// connects to on the sender's behalf.
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
)

// addressPolicy decides which addresses webhooks may connect to. allow lists
// the private ranges the operator opened (WARDLOCK_WEBHOOK_ALLOW_PRIVATE).
type addressPolicy struct{ allow []netip.Prefix }

// permitted reports whether a webhook may connect to a.
func (p addressPolicy) permitted(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	if nat64.Contains(a) {
		b := a.As16()
		a = netip.AddrFrom4([4]byte(b[12:]))
	}
	switch {
	case !a.IsValid(), a.IsLoopback(), a.IsLinkLocalUnicast(), a.IsMulticast(), a.IsUnspecified(),
		thisNetwork.Contains(a), broadcast.Contains(a):
		return false
	case a.IsPrivate(), sharedAddresses.Contains(a):
		return slices.ContainsFunc(p.allow, func(r netip.Prefix) bool { return r.Contains(a) })
	}
	return true
}

// webhookClient is the only way a webhook leaves the server. The check runs
// in the dialler's control hook, after DNS and on each address actually
// about to be connected to, so an answer that changes between lookups has
// nothing to slip past. It ignores proxy settings, which would connect
// somewhere else, follows no redirects, and keeps no connection: every
// attempt dials, and so is checked, afresh.
func webhookClient(permitted func(netip.Addr) bool, resolver *net.Resolver) *http.Client {
	d := &net.Dialer{
		Timeout:  webhookTimeout,
		Resolver: resolver,
		ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: %w", errRefusedAddress, err)
			}
			if !permitted(ap.Addr()) {
				return errRefusedAddress
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: webhookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext:            d.DialContext,
			DisableKeepAlives:      true,
			DisableCompression:     true,
			TLSHandshakeTimeout:    webhookTimeout,
			ResponseHeaderTimeout:  webhookTimeout,
			MaxResponseHeaderBytes: maxWebhookResponse,
		},
	}
}
