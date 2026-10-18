// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"net/netip"
	"sync"
	"time"
)

// Rate allows N events per Per for one key.
type Rate struct {
	N   int
	Per time.Duration
}

// Limits are the sign-in rate limits. Keys are client IPs, normalised
// addresses and owner IDs; every request is counted, successful or not, so
// a limit says nothing about whether an address belongs to an owner.
type Limits struct {
	// SendPerAddress and SendPerIP limit email-code sends.
	SendPerAddress Rate
	SendPerIP      Rate
	// VerifyPerIP limits email-code verifications, on top of the five
	// attempts each code allows.
	VerifyPerIP Rate
	// RedeemPerIP limits setup and sign-in-link redemptions.
	RedeemPerIP Rate
	// InvitePerOwner limits invitations sent by one owner.
	InvitePerOwner Rate
}

// DefaultLimits are the limits used when Config.Limits is zero.
func DefaultLimits() Limits {
	return Limits{
		SendPerAddress: Rate{N: 5, Per: time.Hour},
		SendPerIP:      Rate{N: 20, Per: time.Hour},
		VerifyPerIP:    Rate{N: 30, Per: 15 * time.Minute},
		RedeemPerIP:    Rate{N: 10, Per: 15 * time.Minute},
		InvitePerOwner: Rate{N: 20, Per: time.Hour},
	}
}

// limiter is an in-memory fixed-window counter per key. Counts are lost on
// restart, which only ever resets a limit early.
//
// Memory is bounded: expired windows are swept at most once per sweepEvery,
// and the table never holds more than maxKeys. When it is full, a key it
// does not already track is refused (fail closed) until the next sweep
// frees room. Flooding the table with fresh keys can therefore delay
// sign-in for new clients by up to one window, but can never exhaust
// memory or lift a limit.
type limiter struct {
	now     func() time.Time
	maxKeys int

	mu        sync.Mutex
	windows   map[string]window
	nextSweep time.Time
}

type window struct {
	end time.Time
	n   int
}

const (
	// defaultMaxKeys bounds the table to a few MiB.
	defaultMaxKeys = 100_000
	sweepEvery     = time.Minute
)

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, maxKeys: defaultMaxKeys, windows: map[string]window{}}
}

// allow counts one event for key under r. When the window is full, or the
// table is full and key is new, it returns false and how long to wait.
func (l *limiter) allow(key string, r Rate) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.nextSweep) {
		for k, w := range l.windows {
			if !now.Before(w.end) {
				delete(l.windows, k)
			}
		}
		l.nextSweep = now.Add(sweepEvery)
	}
	w, ok := l.windows[key]
	if !ok && len(l.windows) >= l.maxKeys {
		return false, max(l.nextSweep.Sub(now), time.Second)
	}
	if !now.Before(w.end) {
		w = window{end: now.Add(r.Per)}
	}
	w.n++
	l.windows[key] = w
	if w.n > r.N {
		return false, w.end.Sub(now)
	}
	return true, 0
}

// ipKey is the rate-limit key for a client address: the address itself for
// IPv4, its /64 for IPv6, since one host usually holds a whole /64.
func ipKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.WithZone("").Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}

// exceeded reports whether key's current window under r is already full,
// without counting an event, and how long until it resets.
func (l *limiter) exceeded(key string, r Rate) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.windows[key]
	if !ok || !now.Before(w.end) || w.n < r.N {
		return false, 0
	}
	return true, w.end.Sub(now)
}
