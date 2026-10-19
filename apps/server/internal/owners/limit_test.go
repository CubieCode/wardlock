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
