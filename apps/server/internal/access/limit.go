// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"hash/maphash"
	"math"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Rate allows N events per Per for one key.
type Rate struct {
	N   int
	Per time.Duration
}

// Limits are the visitor-facing rate limits. Every sign-in method's
// limits live here, keyed per IP (ipKey) and per site or policy. Each
// per-site limit also gives each network (netKey) a share of it while the
// site is under pressure (netRate).
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
	// EmailSend* count email-code requests (A2.4), allowed addresses or
	// not, so a limit says nothing about the allowlist: per client, per
	// address (across every site, so no inbox is flooded) and per site.
	EmailSendPerIP      Rate
	EmailSendPerAddress Rate
	EmailSendPerSite    Rate
	// EmailVerify* count code entries the same ways, on top of the
	// EmailCodeAttempts each code allows.
	EmailVerifyPerIP      Rate
	EmailVerifyPerAddress Rate
	EmailVerifyPerSite    Rate
	// OAuthStartsPerIP counts OAuth sign-ins started per client (A2.5):
	// each holds a pending flow in memory until the provider answers.
	OAuthStartsPerIP Rate
}

// DefaultLimits are the limits used when Config.Limits is zero.
func DefaultLimits() Limits {
	return Limits{
		TokenFailuresPerIP:    Rate{N: 30, Per: 15 * time.Minute},
		TokenFailuresPerSite:  Rate{N: 300, Per: 15 * time.Minute},
		PasswordPerIP:         Rate{N: 10, Per: 15 * time.Minute},
		PasswordPerSite:       Rate{N: 100, Per: 15 * time.Minute},
		EmailSendPerIP:        Rate{N: 20, Per: time.Hour},
		EmailSendPerAddress:   Rate{N: 5, Per: time.Hour},
		EmailSendPerSite:      Rate{N: 200, Per: time.Hour},
		EmailVerifyPerIP:      Rate{N: 30, Per: 15 * time.Minute},
		EmailVerifyPerAddress: Rate{N: 10, Per: 15 * time.Minute},
		EmailVerifyPerSite:    Rate{N: 300, Per: 15 * time.Minute},
		OAuthStartsPerIP:      Rate{N: 30, Per: 15 * time.Minute},
	}
}

// limiter is an in-memory fixed-window counter per key, shared by every
// access method. Counts are lost on restart, which only ever resets a limit
// early. Expired windows are swept at most once per sweepEvery.
//
// A limiter for keys a client chooses (IPs, addresses, networks) is
// bounded: its exact table never holds more than maxKeys. When it is full,
// a key it does not hold is counted in an overflow sketch instead (see
// sketch), which never undercounts, so a flood of fresh keys neither lifts
// a limit nor refuses clients that are not failing until the flood has
// saturated the sketch. A limiter for keys the server owns (sites,
// policies) is unbounded (maxKeys 0): its size follows the number of
// sites, and a full client table never disables it.
type limiter struct {
	now     func() time.Time
	maxKeys int
	// sketchWidth is the number of counters per sketch row, a power of
	// two; seed keys the sketch's hash, so a client cannot aim at the
	// counters of another.
	sketchWidth uint64
	seed        maphash.Seed

	mu        sync.Mutex
	windows   map[string]window
	nextSweep time.Time
	// sketches holds one overflow sketch per method and window length,
	// allocated when the table first overflows for that method.
	sketches map[sketchKey]*sketch
}

// sketchKey names an overflow sketch: a key's method (the part before its
// first colon, such as "password-ip") and its window length. Methods never
// share counters, so a flood of one cannot saturate another's.
type sketchKey struct {
	method string
	per    time.Duration
}

func newSketchKey(key string, per time.Duration) sketchKey {
	method, _, _ := strings.Cut(key, ":")
	return sketchKey{method: method, per: per}
}

type window struct {
	end time.Time
	n   int
}

const (
	// defaultMaxKeys bounds the table to a few MiB.
	defaultMaxKeys = 100_000
	sweepEvery     = time.Minute
	// defaultSketchWidth gives each sketch 2 x 2^17 two-byte counters
	// (512 KiB).
	defaultSketchWidth = 1 << 17
)

// newLimiter returns a limiter bounded to defaultMaxKeys, for client keys.
func newLimiter(now func() time.Time) *limiter {
	return &limiter{
		now: now, maxKeys: defaultMaxKeys, sketchWidth: defaultSketchWidth, seed: maphash.MakeSeed(),
		windows: map[string]window{}, sketches: map[sketchKey]*sketch{},
	}
}

// newServerKeyLimiter returns an unbounded limiter, for keys the server
// owns (site and policy IDs).
func newServerKeyLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, windows: map[string]window{}}
}

// full reports whether a bounded table cannot take a new key.
func (l *limiter) full() bool { return l.maxKeys > 0 && len(l.windows) >= l.maxKeys }

// allow counts one event for key under r. When the window is full it
// returns false and how long to wait. A key the full table does not hold
// is counted in the overflow sketch instead.
func (l *limiter) allow(key string, r Rate) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	w, ok := l.windows[key]
	if !ok && l.full() {
		sk := l.sketch(key, r.Per, now, true)
		if n := sk.add(l.seed, key); n > r.N {
			return false, sk.end.Sub(now)
		}
		return true, 0
	}
	if !now.Before(w.end) {
		w = window{end: now.Add(r.Per)}
		if !ok {
			// A key counted in the sketch while the table was full
			// keeps that count, so moving into the table resets nothing.
			if sk := l.sketch(key, r.Per, now, false); sk != nil {
				w.n = sk.estimate(l.seed, key)
			}
		}
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
// does not hold is over only when its sketch estimate is.
func (l *limiter) exceeded(key string, r Rate) (bool, time.Duration) {
	n, wait := l.count(key, r)
	if n < r.N {
		return false, 0
	}
	return true, wait
}

// count returns how many events key has in its current window under r,
// without counting one, and how long until the window resets.
func (l *limiter) count(key string, r Rate) (int, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	if w, ok := l.windows[key]; ok {
		if !now.Before(w.end) {
			return 0, 0
		}
		return w.n, w.end.Sub(now)
	}
	if sk := l.sketch(key, r.Per, now, false); sk != nil {
		return sk.estimate(l.seed, key), sk.end.Sub(now)
	}
	return 0, 0
}

// sketch returns the overflow sketch for key's method and windows of
// length per, starting a new window (all counters zero) when the last one
// has ended. It allocates one only when create is set.
func (l *limiter) sketch(key string, per time.Duration, now time.Time, create bool) *sketch {
	k := newSketchKey(key, per)
	sk := l.sketches[k]
	if sk == nil {
		if !create || l.sketches == nil {
			return nil
		}
		sk = newSketch(l.sketchWidth)
		l.sketches[k] = sk
	}
	if !now.Before(sk.end) {
		sk.reset(now.Add(per))
	}
	return sk
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

// sketch is a count-min sketch: the keys the full table cannot hold, all
// counted in one fixed window (until end) in sketchRows rows of saturating
// counters. A key's estimate is the least of its counters, and each counter
// holds at least the count of every key hashed to it, so the estimate is
// never below the key's true count: no limit is lifted, and other keys
// sharing its counters can only make it refuse early. Updates are
// conservative (only counters at the estimate rise), so pushing one
// unknown key to a limit N means raising most of a whole row to N, about
// N x width events in one window.
type sketch struct {
	end  time.Time
	mask uint64
	rows [sketchRows][]uint16
}

const sketchRows = 2

func newSketch(width uint64) *sketch {
	sk := &sketch{mask: width - 1}
	for i := range sk.rows {
		sk.rows[i] = make([]uint16, width)
	}
	return sk
}

func (sk *sketch) reset(end time.Time) {
	for _, row := range sk.rows {
		clear(row)
	}
	sk.end = end
}

// slots returns key's counter index in each row: two halves of one hash.
func (sk *sketch) slots(seed maphash.Seed, key string) [sketchRows]uint64 {
	h := maphash.String(seed, key)
	return [sketchRows]uint64{h & sk.mask, (h >> 32) & sk.mask}
}

func (sk *sketch) estimate(seed maphash.Seed, key string) int {
	i := sk.slots(seed, key)
	return int(min(sk.rows[0][i[0]], sk.rows[1][i[1]]))
}

// add counts one event for key and returns its new estimate.
func (sk *sketch) add(seed maphash.Seed, key string) int {
	i := sk.slots(seed, key)
	e := min(sk.rows[0][i[0]], sk.rows[1][i[1]])
	if e == math.MaxUint16 {
		return int(e)
	}
	for r, row := range sk.rows {
		row[i[r]] = max(row[i[r]], e+1)
	}
	return int(e) + 1
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

// netKey is the network a client address belongs to, for per-site network
// shares: its /24 for IPv4 and its /48 for IPv6.
func netKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	bits := 48
	if a.Is4() {
		bits = 24
	}
	p, err := a.WithZone("").Prefix(bits)
	if err != nil {
		return ip
	}
	return p.String()
}

// A site is under pressure for a method once its counter in the current
// window reaches 1/sitePressureDivisor of its limit. Only then is each
// network (netKey) held to 1/networkShareDivisor of the site's limit per
// window, counting its attempts from before the pressure began, while
// below the pressure mark many clients behind one network get only their
// own per-IP limits.
//
// The two constants set how many networks it takes to hold a method shut
// on a site. One network may spend the limit alone up to the mark, after
// which it is over its share; each further network adds its share. So it
// takes 1 + (1 - 1/sitePressureDivisor) x networkShareDivisor networks
// (six with these values) and, once fewer keep going, the method reopens
// within one window. Raising either constant raises that bound, at the
// cost of sharing earlier or more thinly among legitimate visitors.
//
// The pressure check and the counts take separate locks, so attempts in
// flight as a site crosses the mark may each still see it below the mark
// and pass a share a serial count would refuse (in tests, one or two).
// That is accepted: the per-site limit itself is never exceeded.
const (
	sitePressureDivisor = 2
	networkShareDivisor = 10
)

// underPressure reports whether n events reach the pressure mark of limit.
func underPressure(n, limit int) bool { return n*sitePressureDivisor >= limit }

// networkShare is the most one network may spend of limit under pressure.
func networkShare(limit int) int { return max(1, (limit+networkShareDivisor-1)/networkShareDivisor) }

// netRate is the rate a network's attempts on a site are held to: its
// share of site under pressure, and otherwise counted but unlimited.
func netRate(site Rate, n int) Rate {
	if underPressure(n, site.N) {
		return Rate{N: networkShare(site.N), Per: site.Per}
	}
	return Rate{N: math.MaxInt, Per: site.Per}
}
