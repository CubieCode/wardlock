// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"sync"
	"time"
)

// FakeLimiter is a fixed-window attemptLimiter on a test clock that
// records every key it counts.
type FakeLimiter struct {
	now func() time.Time

	mu      sync.Mutex
	windows map[string]fakeWindow
	keys    []string
}

type fakeWindow struct {
	end time.Time
	n   int
}

func NewFakeLimiter(now func() time.Time) *FakeLimiter {
	return &FakeLimiter{now: now, windows: map[string]fakeWindow{}}
}

func (f *FakeLimiter) allow(key string, r Rate) (bool, time.Duration) {
	now := f.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, key)
	w := f.windows[key]
	if !now.Before(w.end) {
		w = fakeWindow{end: now.Add(r.Per)}
	}
	w.n++
	f.windows[key] = w
	if w.n > r.N {
		return false, w.end.Sub(now)
	}
	return true, 0
}

// Keys returns and forgets the keys counted so far.
func (f *FakeLimiter) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.keys
	f.keys = nil
	return k
}

// SetAttemptLimiter makes s count password attempts, per IP and per site,
// with l; nil removes both limiters.
func SetAttemptLimiter(s *Service, l *FakeLimiter) {
	if l == nil {
		s.ipAttempts, s.siteAttempts = nil, nil
		return
	}
	s.ipAttempts, s.siteAttempts = l, l
}
