// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"wardlock.nz/server/internal/store/storetest"
)

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	r := Rate{N: 2, Per: time.Minute}
	for i := range 2 {
		if ok, _ := l.allow("a", r); !ok {
			t.Fatalf("event %d refused", i+1)
		}
	}
	if over, wait := l.exceeded("a", r); !over || wait != time.Minute {
		t.Fatalf("exceeded = %v %v after N events", over, wait)
	}
	if ok, _ := l.allow("a", r); ok {
		t.Fatal("event N+1 allowed")
	}
	now = now.Add(time.Minute)
	if over, _ := l.exceeded("a", r); over {
		t.Fatal("limit did not reset")
	}
}

// TestLimiterFullFailsClosed: a key the full table cannot hold is limited,
// both when counted and when checked, until a sweep frees room.
func TestLimiterFullFailsClosed(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	l.maxKeys = 2
	r := Rate{N: 5, Per: time.Minute}
	l.allow("a", r)
	l.allow("b", r)
	if ok, _ := l.allow("c", r); ok {
		t.Fatal("a new key was counted in a full table")
	}
	if over, wait := l.exceeded("c", r); !over || wait < time.Second {
		t.Fatalf("exceeded(c) = %v %v in a full table, want limited", over, wait)
	}
	if over, _ := l.exceeded("a", r); over {
		t.Fatal("a tracked key under its limit was refused")
	}
	now = now.Add(2 * time.Minute) // windows over, sweep due
	if over, _ := l.exceeded("c", r); over {
		t.Fatal("still full after the sweep")
	}
	if ok, _ := l.allow("c", r); !ok {
		t.Fatal("c refused after the sweep")
	}
}

type quiet struct{}

func (quiet) Notify(context.Context, string)              {}
func (quiet) Subscribe(func(string)) (unsubscribe func()) { return func() {} }

// TestTokenGuessesWithAFullLimiter: guesses never slip through a full
// limiter; they are limited.
func TestTokenGuessesWithAFullLimiter(t *testing.T) {
	db := storetest.New(t)
	s, err := New(Config{
		Sites: db.Sites(), Policies: db.Policies(), Sessions: db.Sessions(), Links: db.Links(), Notifier: quiet{},
		Secret: []byte("limit-test-server-key-0123456789abcdef"), Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.limiter.maxKeys = 1
	s.limiter.allow("someone-else", Rate{N: 1, Per: time.Hour})
	r := Request{SiteID: "site", Token: "wl_junk", ClientIP: "192.0.2.1"}
	if over, _ := s.overTokenLimit(r); !over {
		t.Fatal("a full limiter let an untracked IP through")
	}
	_, err = s.checkToken(t.Context(), r)
	if le, ok := errors.AsType[*limitedError](err); !ok || le.RetryAfter <= 0 {
		t.Fatalf("checkToken with a full limiter: %v", err)
	}
}
