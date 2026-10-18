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
}

// DefaultLimits are the limits used when Config.Limits is zero.
func DefaultLimits() Limits {
	return Limits{
		TokenFailuresPerIP:   Rate{N: 30, Per: 15 * time.Minute},
		TokenFailuresPerSite: Rate{N: 300, Per: 15 * time.Minute},
	}
}

// limiter is an in-memory fixed-window counter per key, shared by every
// access method. Counts are lost on restart, which only ever resets a limit
// early.
//
// Memory is bounded: expired windows are swept at most once per sweepEvery,
// and the table never holds more than maxKeys. When it is full, a key it
// does not already track is refused (fail closed) until the next sweep
// frees room, so flooding it with fresh keys can delay new clients by up to
// one window but never exhaust memory or lift a limit.
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
	l.sweep(now)
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

// exceeded reports whether key's current window under r is already full,
// or the table is full and does not hold key, without counting an event,
// and how long until that changes.
func (l *limiter) exceeded(key string, r Rate) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	w, ok := l.windows[key]
	if !ok && len(l.windows) >= l.maxKeys {
		// Full: a key the table cannot hold cannot be counted, so it is
		// refused (fail closed) until a sweep frees room.
		return true, max(l.nextSweep.Sub(now), time.Second)
	}
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
