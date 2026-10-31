// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"net"
	"net/http"
	"net/netip"
	"time"
)

const (
	WebhookRetryFor     = webhookRetryFor
	WebhookDisableAfter = webhookDisableAfter
	MaxWebhookResponse  = maxWebhookResponse
	KeepDeliveries      = keepDeliveries
	FailRefused         = failRefused
)

// ErrRefusedAddress is the dialler's refusal.
var ErrRefusedAddress = errRefusedAddress

// AllowLoopback lets s reach httptest servers, which listen on loopback,
// through the same client as ever, and shortens its timeout when given one.
func (s *Service) AllowLoopback(timeout time.Duration) {
	permitted := s.permitted
	s.permitted = func(a netip.Addr) bool { return a.IsLoopback() || permitted(a) }
	s.client = webhookClient(s.permitted, nil)
	if timeout > 0 {
		s.client.Timeout = timeout
	}
}

// Permitted is the address policy with allow opened.
func Permitted(a netip.Addr, allow ...netip.Prefix) bool {
	return addressPolicy{allow: allow}.permitted(a)
}

// GuardedClient is the webhook client over a chosen policy and resolver.
func GuardedClient(permitted func(netip.Addr) bool, r *net.Resolver) *http.Client {
	return webhookClient(permitted, r)
}

// SigningSecret is the secret s signs webhook id's deliveries with.
func (s *Service) SigningSecret(id, salt string) string { return s.signingSecret(id, salt) }
