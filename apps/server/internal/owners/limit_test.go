// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store/storetest"
)

type discardMailer struct{}

func (discardMailer) Send(context.Context, mail.Message) error { return nil }

// Invented addresses must not fill the table that per-IP limits use:
// flooding email-code sends for addresses from a few IPs leaves sign-in
// from IPs the limiter has not seen yet unaffected.
func TestAddressKeysDoNotFillTheIPTable(t *testing.T) {
	db := storetest.New(t)
	now := time.UnixMilli(1_800_000_000_000).UTC()
	limits := DefaultLimits()
	limits.SendPerIP = Rate{N: 1000, Per: time.Hour}
	s, err := New(Config{
		Owners: db.Owners(), Sessions: db.Sessions(), Mailer: discardMailer{}, Logger: slog.New(slog.DiscardHandler),
		Secret: testSecret, DashboardURL: &url.URL{Scheme: "https", Host: "dash.example.test"},
		Now: func() time.Time { return now }, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.limiter.maxKeys = 10
	for i := range 50 {
		c := Client{IP: fmt.Sprintf("192.0.2.%d", i%3+1)}
		if err := s.SendEmailCode(t.Context(), c, fmt.Sprintf("invented%d@example.test", i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// A visitor from a new address still gets an answer, not a 429.
	_, _, err = s.VerifyEmailCode(t.Context(), Client{IP: "198.51.100.9"}, "owner@example.test", "123456")
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("verify from a new IP = %v, want ErrInvalidCode", err)
	}
	if _, _, err := s.RedeemSignInLink(t.Context(), Client{IP: "198.51.100.10"}, "not-a-code"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("redeem from a new IP = %v, want ErrInvalidCode", err)
	}
	// The per-address limit itself still holds.
	for i := range limits.SendPerAddress.N {
		if err := s.SendEmailCode(t.Context(), Client{IP: "198.51.100.11"}, "target@example.test"); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if err := s.SendEmailCode(t.Context(), Client{IP: "198.51.100.11"}, "target@example.test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over the address limit = %v", err)
	}
}

// Filling the per-address table with invented addresses, and the per-IP
// table with invented IPv6 /64s, leaves owner email-code sign-in open for
// addresses and IPs the tables do not hold, each still held to its limits.
func TestFullOwnerTablesKeepEmailCodesOpen(t *testing.T) {
	db := storetest.New(t)
	now := time.UnixMilli(1_800_000_000_000).UTC()
	s, err := New(Config{
		Owners: db.Owners(), Sessions: db.Sessions(), Mailer: discardMailer{}, Logger: slog.New(slog.DiscardHandler),
		Secret: testSecret, DashboardURL: &url.URL{Scheme: "https", Host: "dash.example.test"},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	limits := DefaultLimits()
	s.addressLimiter.maxKeys, s.limiter.maxKeys = 40, 20
	for i := range 40 {
		ip := fmt.Sprintf("2001:db8:ee:%x::1", i)
		if err := s.SendEmailCode(t.Context(), Client{IP: ip}, fmt.Sprintf("invented%d@example.test", i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if len(s.addressLimiter.windows) != 40 || len(s.limiter.windows) != 20 {
		t.Fatalf("tables hold %d addresses and %d IPs", len(s.addressLimiter.windows), len(s.limiter.windows))
	}
	// An address and IPs the tables do not hold: sends go through up to
	// the address's limit, then stop.
	for i := range limits.SendPerAddress.N {
		if err := s.SendEmailCode(t.Context(), Client{IP: fmt.Sprintf("192.0.2.%d", i+1)}, "owner@example.test"); err != nil {
			t.Fatalf("send %d to an unseen address: %v", i+1, err)
		}
	}
	if err := s.SendEmailCode(t.Context(), Client{IP: "192.0.2.99"}, "owner@example.test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("an unseen address past its limit = %v", err)
	}
	if _, held := s.addressLimiter.windows["send-address:owner@example.test"]; held {
		t.Fatal("the full address table took a new key")
	}
	// An unseen IP is held to its per-IP limits too.
	for i := range limits.SendPerIP.N {
		if err := s.SendEmailCode(t.Context(), Client{IP: "198.51.100.7"}, fmt.Sprintf("other%d@example.test", i)); err != nil {
			t.Fatalf("send %d from an unseen IP: %v", i+1, err)
		}
	}
	if err := s.SendEmailCode(t.Context(), Client{IP: "198.51.100.7"}, "another@example.test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("an unseen IP past its limit = %v", err)
	}
	if _, _, err := s.VerifyEmailCode(t.Context(), Client{IP: "198.51.100.8"}, "owner@example.test", "123456"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("verify from an unseen IP = %v, want ErrInvalidCode", err)
	}
	// Both reset with the window.
	now = now.Add(time.Hour)
	if err := s.SendEmailCode(t.Context(), Client{IP: "198.51.100.7"}, "owner@example.test"); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// A key counted in the sketch keeps its count when it moves into the
// table, and the sketch never undercounts colliding keys.
func TestOverflowKeepsCounts(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	l := newLimiter(func() time.Time { return now })
	l.maxKeys = 1
	l.allow("held", Rate{N: 5, Per: 30 * time.Second})
	r := Rate{N: 5, Per: time.Hour}
	for range 4 {
		l.allow("send-address:owner@example.test", r)
	}
	now = now.Add(time.Minute) // "held" is swept, making room
	if ok, _ := l.allow("send-address:owner@example.test", r); !ok {
		t.Fatal("fifth send refused")
	}
	if w := l.windows["send-address:owner@example.test"]; w.n != 5 {
		t.Fatalf("moved into the table with %d events, want 5", w.n)
	}
	if ok, _ := l.allow("send-address:owner@example.test", r); ok {
		t.Fatal("sixth send allowed: moving reset the count")
	}

	sk := newSketch(32)
	want := map[string]int{}
	for i := range 3000 {
		key := fmt.Sprintf("send-address:v%d@example.test", (i*31)%400)
		sk.add(l.seed, key)
		want[key]++
	}
	for key, n := range want {
		if got := sk.estimate(l.seed, key); got < n {
			t.Fatalf("%s estimate %d below its count %d", key, got, n)
		}
	}
}
