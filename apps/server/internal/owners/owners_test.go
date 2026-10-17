// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNormaliseEmail(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"owner@example.test", "owner@example.test", true},
		{"  Owner@Example.TEST ", "owner@example.test", true},
		{"Owner <owner@example.test>", "", false},
		{"owner.example.test", "", false},
		{"a@", "", false},
		{"", "", false},
		{strings.Repeat("a", 250) + "@x.nz", "", false},
		{"owner@example.test\r\nBcc: x@example.test", "", false},
	}
	for _, tt := range tests {
		got, err := NormaliseEmail(tt.in)
		if tt.ok != (err == nil) || got != tt.want {
			t.Errorf("NormaliseEmail(%q) = %q, %v", tt.in, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("NormaliseEmail(%q) error %v, want ErrInvalidEmail", tt.in, err)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	l := newLimiter(func() time.Time { return now })
	r := Rate{N: 2, Per: time.Minute}
	for i := range 2 {
		if ok, _ := l.allow("k", r); !ok {
			t.Fatalf("event %d refused", i+1)
		}
	}
	now = now.Add(20 * time.Second)
	ok, wait := l.allow("k", r)
	if ok || wait != 40*time.Second {
		t.Fatalf("third event = %v, wait %v; want refused, 40s", ok, wait)
	}
	if ok, _ := l.allow("other", r); !ok {
		t.Fatal("keys are not independent")
	}
	now = now.Add(40 * time.Second)
	if ok, _ := l.allow("k", r); !ok {
		t.Fatal("window did not reset")
	}
}

func TestLimiterIsBounded(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	l := newLimiter(func() time.Time { return now })
	l.maxKeys = 1000
	r := Rate{N: 5, Per: 10 * time.Minute}
	refused := 0
	for i := range 5000 {
		if ok, wait := l.allow(fmt.Sprint("ip-", i), r); !ok {
			refused++
			if wait < time.Second {
				t.Fatalf("wait %v for a full table", wait)
			}
		}
		if len(l.windows) > l.maxKeys {
			t.Fatalf("%d keys, cap %d", len(l.windows), l.maxKeys)
		}
	}
	if refused != 4000 {
		t.Fatalf("refused %d new keys, want 4000", refused)
	}
	// Known keys are still counted while the table is full.
	if ok, _ := l.allow("ip-0", r); !ok {
		t.Fatal("a tracked key was refused")
	}
	// Expired windows are swept once the interval passes, making room.
	now = now.Add(r.Per)
	if ok, _ := l.allow("fresh", r); !ok {
		t.Fatal("no room after the windows expired")
	}
	if len(l.windows) != 1 {
		t.Fatalf("%d windows after sweep, want 1", len(l.windows))
	}
}

func TestLimiterSweepsAtMostOncePerInterval(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	l := newLimiter(func() time.Time { return now })
	r := Rate{N: 1, Per: time.Second}
	l.allow("a", r)
	now = now.Add(2 * time.Second) // "a" expired, but the sweep is not due
	l.allow("b", r)
	if _, ok := l.windows["a"]; !ok {
		t.Fatal("swept before the interval")
	}
	now = now.Add(sweepEvery)
	l.allow("c", r)
	if _, ok := l.windows["a"]; ok {
		t.Fatal("not swept after the interval")
	}
}

func TestIPKey(t *testing.T) {
	tests := map[string]string{
		"192.0.2.1":            "192.0.2.1",
		"::ffff:192.0.2.1":     "192.0.2.1",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":      "2001:db8:1:3::/64",
		"fe80::1%eth0":         "fe80::/64",
		"not an address":       "not an address",
	}
	for in, want := range tests {
		if got := ipKey(in); got != want {
			t.Errorf("ipKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	seen := time.UnixMilli(1_800_000_000_123).UTC()
	got, id, err := decodeCursor(encodeCursor(seen, "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e0f"))
	if err != nil || !got.Equal(seen) || id != "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e0f" {
		t.Fatalf("round trip = %v, %q, %v", got, id, err)
	}
	for _, bad := range []string{"bogus!", "bm9kb3Q", "eC55"} {
		if _, _, err := decodeCursor(bad); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor(%q) = %v, want ErrInvalidCursor", bad, err)
		}
	}
}

func TestTokens(t *testing.T) {
	a, b := randomToken(), randomToken()
	if a == b || !wellFormedToken(a) || len(a) != 43 {
		t.Fatalf("tokens %q %q", a, b)
	}
	for _, bad := range []string{"", a[:42], a + "A", strings.Repeat("!", 43)} {
		if wellFormedToken(bad) {
			t.Errorf("wellFormedToken(%q) = true", bad)
		}
	}
	for range 100 {
		if c := sixDigits(); !isSixDigits(c) {
			t.Fatalf("sixDigits() = %q", c)
		}
	}
}

func FuzzDecodeCursor(f *testing.F) {
	f.Add(encodeCursor(time.UnixMilli(1), "id"))
	f.Add("")
	f.Fuzz(func(t *testing.T, c string) {
		if _, id, err := decodeCursor(c); err == nil && id == "" {
			t.Fatal("accepted a cursor without an ID")
		}
	})
}

func FuzzWellFormedToken(f *testing.F) {
	f.Add(randomToken())
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		if wellFormedToken(s) && len(s) != 43 {
			t.Fatalf("accepted %q", s)
		}
	})
}

func TestTruncateUserAgent(t *testing.T) {
	long := strings.Repeat("é", 250)
	if got := truncateUserAgent(long); len([]rune(got)) != 200 {
		t.Errorf("kept %d characters, want 200", len([]rune(got)))
	}
	if got := truncateUserAgent("ok\xffua"); got != "okua" {
		t.Errorf("invalid UTF-8 kept: %q", got)
	}
}
