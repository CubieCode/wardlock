// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

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
// and the exact table never holds more than maxKeys. When it is full, a
// key it does not hold is counted in an overflow sketch instead (see
// sketch), which never undercounts. Flooding the table with fresh keys
// therefore neither exhausts memory nor lifts a limit, and refuses clients
// it does not hold only once the flood has saturated the sketch.
type limiter struct {
	now     func() time.Time
	maxKeys int
	// sketchWidth is the number of counters per sketch row, a power of
	// two; seed keys the sketch's hash, so a client cannot aim at the
	// counters of another.
	sketchWidth int
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

func newLimiter(now func() time.Time) *limiter {
	return &limiter{
		now: now, maxKeys: defaultMaxKeys, sketchWidth: defaultSketchWidth, seed: maphash.MakeSeed(),
		windows: map[string]window{}, sketches: map[sketchKey]*sketch{},
	}
}

// allow counts one event for key under r. When the window is full it
// returns false and how long to wait. A key the full table does not hold
// is counted in the overflow sketch instead.
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

// sketch returns the overflow sketch for key's method and windows of
// length per, starting a new window (all counters zero) when the last one
// has ended. It allocates one only when create is set.
func (l *limiter) sketch(key string, per time.Duration, now time.Time, create bool) *sketch {
	k := newSketchKey(key, per)
	sk := l.sketches[k]
	if sk == nil {
		if !create {
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

func newSketch(width int) *sketch {
	sk := &sketch{mask: uint64(width - 1)}
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

// exceeded reports whether key's current window under r is already full,
// without counting an event, and how long until it resets. A key the table
// does not hold is over only when its sketch estimate is.
func (l *limiter) exceeded(key string, r Rate) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if w, ok := l.windows[key]; ok {
		if !now.Before(w.end) || w.n < r.N {
			return false, 0
		}
		return true, w.end.Sub(now)
	}
	if sk := l.sketch(key, r.Per, now, false); sk != nil && sk.estimate(l.seed, key) >= r.N {
		return true, sk.end.Sub(now)
	}
	return false, 0
}
