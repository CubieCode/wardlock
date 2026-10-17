// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasskeyName(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"Laptop", "Laptop", true},
		{"  Chrome on macOS  ", "Chrome on macOS", true},
		{"", DefaultPasskeyName, true},
		{"   ", DefaultPasskeyName, true},
		{"Téléphone de Aroha", "Téléphone de Aroha", true},
		{strings.Repeat("ā", MaxPasskeyName), strings.Repeat("ā", MaxPasskeyName), true},
		{strings.Repeat("a", MaxPasskeyName+1), "", false},
		{"Lap\ttop", "", false},
		{"Lap\x00top", "", false},
		{"Lap\u202etop", "", false}, // bidi override
		{"Lap\u200btop", "", false}, // zero-width space
		{"Lap\xfftop", "", false},
	}
	for _, tt := range tests {
		got, err := passkeyName(tt.in)
		if tt.ok != (err == nil) || got != tt.want {
			t.Errorf("passkeyName(%q) = %q, %v", tt.in, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalidPasskeyName) {
			t.Errorf("passkeyName(%q) error %v, want ErrInvalidPasskeyName", tt.in, err)
		}
	}
}

func TestCeremonies(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	cs := newCeremonies(func() time.Time { return now })

	reg, err := cs.put(ceremony{kind: ceremonyRegister, ownerID: "o1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cs.take(reg, ceremonySignIn); ok {
		t.Fatal("a registration ceremony was taken as a sign-in")
	}
	c, ok := cs.take(reg, ceremonyRegister)
	if !ok || c.ownerID != "o1" {
		t.Fatalf("take = %+v, %v; want the registration", c, ok)
	}
	if _, ok := cs.take(reg, ceremonyRegister); ok {
		t.Fatal("a ceremony was taken twice")
	}

	old, _ := cs.put(ceremony{kind: ceremonySignIn})
	now = now.Add(CeremonyValidity)
	if _, ok := cs.take(old, ceremonySignIn); ok {
		t.Fatal("an expired ceremony was taken")
	}
	if len(cs.m) != 0 {
		t.Fatalf("%d ceremonies left after take", len(cs.m))
	}
}

func TestCeremoniesAreBounded(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	cs := newCeremonies(func() time.Time { return now })
	cs.max = 3
	for range 3 {
		if _, err := cs.put(ceremony{kind: ceremonySignIn}); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	_, err := cs.put(ceremony{kind: ceremonySignIn})
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != CeremonyValidity-3*time.Minute {
		t.Fatalf("put on a full table = %v, want a rate limit until the oldest expires", err)
	}
	// Once the oldest expires, its slot is reused.
	now = now.Add(CeremonyValidity - 3*time.Minute)
	if _, err := cs.put(ceremony{kind: ceremonySignIn}); err != nil {
		t.Fatalf("put after expiry: %v", err)
	}
	if len(cs.m) != 3 {
		t.Fatalf("%d ceremonies held, cap 3", len(cs.m))
	}
}
