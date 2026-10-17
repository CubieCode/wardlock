// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package tls is the transport layer around the edge: certmagic for
// automatic TLS (a wildcard certificate for the base domain over DNS-01,
// the dashboard host, and on-demand certificates for verified custom
// domains only), the HTTP to HTTPS redirect, and trusted-proxy handling of
// X-Forwarded-* for the behind-a-proxy mode. Local mode needs none of it.
// Importers alias it (for example wltls) to avoid confusion with
// crypto/tls.
package tls
