// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
)

// ErrNoRecord means a name has no record of the type asked for, or does
// not exist.
var ErrNoRecord = errors.New("no such DNS record")

// Resolver answers the DNS lookups custom-domain verification needs. Both
// methods return an error wrapping ErrNoRecord when there is nothing to
// find, and any other error when the lookup itself failed or timed out.
type Resolver interface {
	// CanonicalName follows host's CNAME chain and returns the name at its
	// end, lowercase without a trailing dot: host itself when it has no
	// CNAME but has address records.
	CanonicalName(ctx context.Context, host string) (string, error)
	// LookupTXT returns the TXT strings at name.
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// NetResolver is the Resolver over the Go DNS client. With servers it asks
// them in turn (WARDLOCK_DNS_RESOLVERS); without, the system's resolvers.
// The Go client keeps no cache of its own.
type NetResolver struct{ r *net.Resolver }

var _ Resolver = NetResolver{}

// NewNetResolver returns a NetResolver asking servers (host:port each), or
// the system resolvers when servers is empty.
func NewNetResolver(servers []string) NetResolver {
	r := &net.Resolver{PreferGo: true}
	if len(servers) > 0 {
		var next atomic.Uint32
		var d net.Dialer
		r.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			// Each new connection (one per query attempt) goes to the next
			// server, so a dead one is skipped on retry.
			server := servers[int(next.Add(1)-1)%len(servers)]
			return d.DialContext(ctx, network, server)
		}
	}
	return NetResolver{r: r}
}

// CanonicalName implements Resolver.
func (n NetResolver) CanonicalName(ctx context.Context, host string) (string, error) {
	name, err := n.r.LookupCNAME(ctx, host)
	if err != nil {
		return "", lookupErr("CNAME", host, err)
	}
	return canonical(name), nil
}

// LookupTXT implements Resolver.
func (n NetResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	txt, err := n.r.LookupTXT(ctx, name)
	if err != nil {
		return nil, lookupErr("TXT", name, err)
	}
	return txt, nil
}

func lookupErr(kind, name string, err error) error {
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound {
		return fmt.Errorf("%s %s: %w", kind, name, ErrNoRecord)
	}
	return fmt.Errorf("%s %s: %w", kind, name, err)
}

// canonical lowercases a DNS name and drops its trailing dot.
func canonical(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
}
