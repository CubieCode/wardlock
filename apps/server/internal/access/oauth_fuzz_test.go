// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"errors"
	"testing"
	"time"
)

// FuzzOAuthCodes sends arbitrary OAuth state values to the auth origin's
// callback and arbitrary site codes and binding cookies to a site's
// callback, while one live flow and one live code wait. Nothing but the
// exact state finds the flow, and nothing but the exact code, with the
// exact binding cookie on the code's own site and host, gets past the
// code check; a code is spent by the first attempt that names it.
func FuzzOAuthCodes(f *testing.F) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	s := &Service{
		key: keyed{secret: []byte("fuzz-server-key-0123456789abcdef-012345")},
		now: func() time.Time { return now }, oauth: newOAuthState(),
	}
	const (
		state, code, binding = "c3RhdGUtc3RhdGUtc3RhdGUtc3RhdGUtc3RhdGUtc3Q", "Y29kZS1jb2RlLWNvZGUtY29kZS1jb2RlLWNvZGUtY28", "YmluZC1iaW5kLWJpbmQtYmluZC1iaW5kLWJpbmQtYmk"
		siteID, siteHost     = "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e01", "docs.wardlock.test"
	)
	// Each execution starts from a fresh table holding one flow and one
	// code: re-arming one table at a fixed time would fill its queue bound.
	arm := func() {
		s.oauth = newOAuthState()
		s.oauth.flows.put(s.key.hashOf(purposeOAuthState, state), siteID,
			oauthFlow{provider: "github", siteID: siteID, host: siteHost, path: "/", expires: now.Add(OAuthFlowLifetime)}, now.Add(OAuthFlowLifetime), now)
		s.oauth.codes.put(s.key.hashOf(purposeOAuthCode, code), siteID,
			oauthCode{siteID: siteID, host: siteHost, path: "/", binding: s.key.hashOf(purposeOAuthBinding, binding), expires: now.Add(OAuthCodeLifetime)}, now.Add(OAuthCodeLifetime), now)
	}
	arm()
	f.Add(state, "github", code, binding, siteID, siteHost)
	f.Add(state[:len(state)-1], "github", code[:len(code)-1]+"A", binding, siteID, siteHost)
	f.Add("", "", code, "", siteID, siteHost)
	f.Add(state, "google", code, binding+"A", siteID, "other.wardlock.test")
	f.Add("x", "github", code, binding, "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e02", siteHost)
	f.Fuzz(func(t *testing.T, gotState, provider, gotCode, gotBinding, gotSite, gotHost string) {
		defer arm()
		if gotState != state {
			// The flow is found only by its state; anything else ends
			// before any provider or policy is consulted.
			if _, err := s.FinishOAuth(t.Context(), OAuthCallback{Provider: provider, State: gotState, Code: "x"}); !errors.Is(err, ErrOAuthState) {
				t.Fatalf("FinishOAuth(state %q) = %v", gotState, err)
			}
		} else if provider != "github" {
			if _, err := s.FinishOAuth(t.Context(), OAuthCallback{Provider: provider, State: gotState, Code: "x"}); !errors.Is(err, ErrOAuthState) {
				t.Fatalf("FinishOAuth(provider %q) = %v", provider, err)
			}
			if _, ok := s.oauth.flows.take(s.key.hashOf(purposeOAuthState, state), now); ok {
				t.Fatal("a callback for another provider left the flow live")
			}
		}
		if gotCode == code && gotBinding == binding && gotSite == siteID && gotHost == siteHost {
			return // the one input that reaches the policy check
		}
		if _, err := s.RedeemOAuth(t.Context(), OAuthRedeem{SiteID: gotSite, Host: gotHost, Code: gotCode, Binding: gotBinding}); !errors.Is(err, ErrOAuthCode) {
			t.Fatalf("RedeemOAuth(%q, %q, %q, %q) = %v", gotCode, gotBinding, gotSite, gotHost, err)
		}
		if _, live := s.oauth.codes.take(s.key.hashOf(purposeOAuthCode, code), now); live == (gotCode == code) {
			t.Fatalf("after an attempt with %q the code is live=%v", gotCode, live)
		}
	})
}
