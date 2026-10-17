// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"wardlock.nz/server/internal/store"
)

// ErrNotAllowed means a hostname may not have an on-demand certificate.
var ErrNotAllowed = errors.New("on-demand certificate not allowed")

// Domains is the slice of the store's Domains repository the on-demand
// decision needs: an instance-wide lookup that finds only active hostnames.
type Domains interface {
	GetActiveByHostname(ctx context.Context, hostname string) (store.Domain, error)
}

// OnDemand decides whether a TLS handshake for an unknown name may obtain
// a certificate. Only custom domains that are active at that moment
// qualify; names under the base domain and the dashboard host are covered
// by the managed certificates and never issued on demand, so a stray
// record or a guessed site name cannot be used to mint certificates.
type OnDemand struct {
	BaseDomain    string
	DashboardHost string
	Domains       Domains
}

// Allow returns nil when name may have a certificate, an error wrapping
// ErrNotAllowed when it may not, and another error when the lookup failed
// (which also denies the certificate).
func (o OnDemand) Allow(ctx context.Context, name string) error {
	host := strings.TrimSuffix(strings.ToLower(name), ".")
	switch {
	case host == "" || len(host) > 253 || strings.ContainsAny(host, "*:/ "):
		return fmt.Errorf("%w: not a hostname", ErrNotAllowed)
	case isIP(host):
		return fmt.Errorf("%w: IP address", ErrNotAllowed)
	case host == o.DashboardHost || host == o.BaseDomain || strings.HasSuffix(host, "."+o.BaseDomain):
		return fmt.Errorf("%w: managed hostname", ErrNotAllowed)
	}
	_, err := o.Domains.GetActiveByHostname(ctx, host)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: not an active custom domain", ErrNotAllowed)
	case err != nil:
		return fmt.Errorf("on-demand decision: %w", err)
	}
	return nil
}

func isIP(host string) bool {
	_, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil
}
