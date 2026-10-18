// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

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

// Limits are the visitor-facing rate limits. Every sign-in method's
// limits live here, keyed per IP (ipKey) and per site or policy.
type Limits struct {
	// TokenFailuresPerIP and TokenFailuresPerSite count rejected share-link
	// tokens (malformed, unknown public ID, wrong secret, another site's).
	// Over either, every token from that IP, or for that site, is refused
	// until the window ends, so guessing cannot continue behind the limit.
	// Sessions already issued keep working.
	TokenFailuresPerIP   Rate
	TokenFailuresPerSite Rate
	// PasswordPerIP and PasswordPerSite count password sign-in attempts
	// (A2.3), right or wrong: per client across every site, and per site
	// from all clients. Over either, even the right password is refused
	// until the window ends.
	PasswordPerIP   Rate
	PasswordPerSite Rate
}

// DefaultLimits are the limits used when Config.Limits is zero.
func DefaultLimits() Limits {
	return Limits{
		TokenFailuresPerIP:   Rate{N: 30, Per: 15 * time.Minute},
		TokenFailuresPerSite: Rate{N: 300, Per: 15 * time.Minute},
		PasswordPerIP:        Rate{N: 10, Per: 15 * time.Minute},
		PasswordPerSite:      Rate{N: 100, Per: 15 * time.Minute},
	}
}

// limiter is an in-memory fixed-window counter per key, shared by every
// access method. Counts are lost on restart, which only ever resets a limit
// early. Expired windows are swept at most once per sweepEvery.
//
// A limiter for keys a client chooses (IPs) is bounded: its table never
// holds more than maxKeys. When it is full, allow refuses to count a new
// key (so a new failure is refused, never silently unlimited), but
// exceeded never refuses a key it does not hold, so a flood of fresh
// addresses cannot lock out clients that are not failing. A limiter for
// keys the server owns (sites, policies) is unbounded (maxKeys 0): its
// size follows the number of sites, and a full IP table never disables it.
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

// newLimiter returns a limiter bounded to defaultMaxKeys, for client keys.
func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, maxKeys: defaultMaxKeys, windows: map[string]window{}}
}

// newServerKeyLimiter returns an unbounded limiter, for keys the server
// owns (site and policy IDs).
func newServerKeyLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, windows: map[string]window{}}
}

// full reports whether a bounded table cannot take a new key.
func (l *limiter) full() bool { return l.maxKeys > 0 && len(l.windows) >= l.maxKeys }

// allow counts one event for key under r. When the window is full, or the
// table is full and key is new, it returns false and how long to wait.
func (l *limiter) allow(key string, r Rate) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	w, ok := l.windows[key]
	if !ok && l.full() {
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

// exceeded reports whether key's current window under r is already full,
// without counting an event, and how long until it resets. A key the table
// does not hold is never over, even when the table is full.
func (l *limiter) exceeded(key string, r Rate) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	w, ok := l.windows[key]
	if !ok || !now.Before(w.end) || w.n < r.N {
		return false, 0
	}
	return true, w.end.Sub(now)
}

// sweep drops expired windows, at most once per sweepEvery, so a full
// table costs no scan per request.
func (l *limiter) sweep(now time.Time) {
	if now.Before(l.nextSweep) {
		return
	}
	for k, w := range l.windows {
		if !now.Before(w.end) {
			delete(l.windows, k)
		}
	}
	l.nextSweep = now.Add(sweepEvery)
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
