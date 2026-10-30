// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package accesslog

import (
	"net/netip"
	"regexp"
	"strings"

	"wardlock.nz/server/internal/store"
)

// Stored field limits, in bytes.
const (
	maxPathBytes      = 2048
	maxUserAgentBytes = 512
)

// Truncation keeps the first IPv4 or IPv6 bits of each address and zeroes
// the rest. A prefix of 0 stores no address at all.
type Truncation struct {
	IPv4Prefix int // 0 to 32
	IPv6Prefix int // 0 to 128
}

// DefaultTruncation keeps an IPv4 /24 and an IPv6 /48: enough to tell
// networks apart, not enough to name a household.
var DefaultTruncation = Truncation{IPv4Prefix: 24, IPv6Prefix: 48}

// Truncate returns ip with only the configured prefix kept, or "" when ip
// is not an address or its prefix is 0. IPv4-mapped IPv6 addresses count
// as IPv4, and zones are dropped.
func (t Truncation) Truncate(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap().WithZone("")
	bits := t.IPv6Prefix
	if addr.Is4() {
		bits = t.IPv4Prefix
	}
	if bits <= 0 {
		return ""
	}
	p, err := addr.Prefix(min(bits, addr.BitLen()))
	if err != nil {
		return ""
	}
	return p.Addr().String()
}

// tokenPattern matches a share-link or API token (wl_<public id>_<secret>)
// anywhere, in any letter case. The secret runs to the end of the
// base64url alphabet, which includes "_".
var tokenPattern = regexp.MustCompile(`(?i)wl_([a-z2-7]{10})_[A-Za-z0-9_-]+`)

// redactTokens replaces each token's secret, keeping the public ID that
// names it.
func redactTokens(s string) string {
	if !strings.Contains(strings.ToLower(s), "wl_") {
		return s
	}
	return tokenPattern.ReplaceAllString(s, "wl_${1}_[redacted]")
}

// scrubPath drops any query or fragment (a share link's ?t= lives there),
// redacts tokens and caps the length.
func scrubPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		p = "/"
	}
	return limit(redactTokens(p), maxPathBytes)
}

// limit cuts s to at most n bytes of valid UTF-8.
func limit(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}

// ParseVia returns the access method named v, or store.ViaNone for
// anything else, so an unexpected value can never fail a batch.
func ParseVia(v string) store.AccessVia {
	switch via := store.AccessVia(v); via {
	case store.ViaPublic, store.ViaLink, store.ViaPassword, store.ViaEmail, store.ViaOAuth, store.ViaNone:
		return via
	}
	return store.ViaNone
}

// scrub turns an Entry into the row stored: no raw tokens, no query string,
// truncated IP, bounded lengths, a valid via and a fresh ID. Every row it
// returns satisfies the table's checks, so one entry cannot roll back its
// batch; ok is false only for a status the table cannot hold.
func (w *Writer) scrub(e Entry) (_ store.AccessEntry, ok bool) {
	if e.Status < 100 || e.Status > 599 {
		return store.AccessEntry{}, false
	}
	at := e.Time
	if at.IsZero() {
		at = w.cfg.Now()
	}
	via, link := ParseVia(string(e.Via)), ""
	if via == store.ViaLink {
		// A link without its public ID cannot be attributed: record the
		// access, not a guess.
		if link = limit(e.LinkPublicID, 64); link == "" {
			via = store.ViaNone
		}
	}
	return store.AccessEntry{
		ID:           store.NewID(),
		OccurredAt:   at.UTC(),
		SiteID:       e.SiteID,
		Host:         limit(e.Host, 253),
		Path:         scrubPath(e.Path),
		Status:       e.Status,
		Via:          via,
		LinkPublicID: link,
		IP:           w.cfg.Truncation.Truncate(e.IP),
		UserAgent:    limit(redactTokens(e.UserAgent), maxUserAgentBytes),
		Preview:      limit(e.Preview, 20),
	}, true
}
