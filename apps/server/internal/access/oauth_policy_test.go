// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access_test

import (
	"errors"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

func TestOAuthPolicyNeedsProviders(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	h.site(o, "harbour")
	list := &store.Allowlist{Emails: []string{"visitor@example.test"}}
	_, err := h.svc.Set(t.Context(), o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Protected, OAuth: true, Allowlist: list})
	if !errors.Is(err, access.ErrInvalidPolicy) {
		t.Fatalf("OAuth without providers: %v", err)
	}
	_, err = h.svc.Set(t.Context(), o, "harbour", access.Rule{PathPrefix: "/", Visibility: store.Public, OAuth: true})
	if !errors.Is(err, access.ErrInvalidPolicy) {
		t.Fatalf("public policy with OAuth: %v", err)
	}
}
