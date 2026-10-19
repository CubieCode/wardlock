// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// newPasskeyHarness is the owner harness with passkeys wired in.
func newPasskeyHarness(t *testing.T, limits owners.Limits) *ownerHarness {
	t.Helper()
	db := storetest.New(t)
	return newOwnerHarnessWith(t, db, limits, func(s *api.Server) {
		// The harness builds the owners service before calling extend.
		pk, err := owners.NewPasskeys(s.Owners, db.Passkeys())
		if err != nil {
			t.Fatal(err)
		}
		s.Passkeys = pk
	})
}

func decode[T any](t testing.TB, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return v
}

// startRegistration begins adding a passkey for the session's owner.
func (h *ownerHarness) startRegistration(cookie string) api.PasskeyChallenge {
	h.t.Helper()
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/passkeys/registration", cookie: cookie})
	wantStatus(h.t, rec, http.StatusOK)
	c := decode[api.PasskeyChallenge](h.t, rec)
	h.secret(c.CeremonyId)
	return c
}

func (h *ownerHarness) finishRegistration(cookie, ceremonyID, name string, cred map[string]any) *httptest.ResponseRecorder {
	h.t.Helper()
	body := map[string]any{"ceremonyId": ceremonyID, "credential": cred}
	if name != "" {
		body["name"] = name
	}
	return h.do(req{method: http.MethodPost, path: "/api/v0/passkeys", cookie: cookie, body: body})
}

// register adds a passkey held by a for the session's owner.
func (h *ownerHarness) register(cookie string, a *softAuthenticator, name string) api.Passkey {
	h.t.Helper()
	c := h.startRegistration(cookie)
	rec := h.finishRegistration(cookie, c.CeremonyId, name, a.create(c.Options, dashOrig, nil))
	wantStatus(h.t, rec, http.StatusCreated)
	return decode[api.Passkey](h.t, rec)
}

func (h *ownerHarness) startSignIn() api.PasskeyChallenge {
	h.t.Helper()
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/auth/passkey"})
	wantStatus(h.t, rec, http.StatusOK)
	c := decode[api.PasskeyChallenge](h.t, rec)
	h.secret(c.CeremonyId)
	return c
}

func (h *ownerHarness) finishSignIn(ceremonyID string, cred map[string]any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(req{method: http.MethodPost, path: "/api/v0/auth/passkey/verify", body: map[string]any{"ceremonyId": ceremonyID, "credential": cred}})
}

func (h *ownerHarness) passkeys(cookie string) []api.Passkey {
	h.t.Helper()
	rec := h.do(req{method: http.MethodGet, path: "/api/v0/passkeys", cookie: cookie})
	wantStatus(h.t, rec, http.StatusOK)
	return decode[api.PasskeyList](h.t, rec).Items
}

func TestPasskeyRegistrationAndSignIn(t *testing.T) {
	h := newPasskeyHarness(t, owners.Limits{})
	cookie := h.owner("owner@example.test")
	me := decode[api.Me](t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: cookie}))

	c := h.startRegistration(cookie)
	o := decodeOptions(t, c.Options)
	if o.RP.ID != dashHost || o.User.ID != b64.EncodeToString([]byte(me.Owner.Id)) || len(o.Challenge) < 22 {
		t.Fatalf("registration options = %+v", c.Options)
	}
	sel, _ := c.Options["authenticatorSelection"].(map[string]any)
	if sel["residentKey"] != "required" || sel["userVerification"] != "preferred" {
		t.Fatalf("authenticatorSelection = %v, want a discoverable credential with UV preferred", sel)
	}
	a := newSoftAuthenticator(t)
	rec := h.finishRegistration(cookie, c.CeremonyId, "  Laptop  ", a.create(c.Options, dashOrig, nil))
	wantStatus(t, rec, http.StatusCreated)
	added := decode[api.Passkey](t, rec)
	if added.Name != "Laptop" || added.CreatedAt != h.clock.Now().UnixMilli() || added.LastUsedAt != nil {
		t.Fatalf("created passkey = %+v", added)
	}

	// A second registration excludes the passkey the owner already has.
	c = h.startRegistration(cookie)
	excluded, _ := c.Options["excludeCredentials"].([]any)
	if len(excluded) != 1 || excluded[0].(map[string]any)["id"] != b64.EncodeToString(a.credID) {
		t.Fatalf("excludeCredentials = %v, want the existing passkey", c.Options["excludeCredentials"])
	}
	// An unnamed passkey gets the default name.
	b := newSoftAuthenticator(t)
	rec = h.finishRegistration(cookie, c.CeremonyId, "", b.create(c.Options, dashOrig, nil))
	wantStatus(t, rec, http.StatusCreated)
	if got := decode[api.Passkey](t, rec).Name; got != owners.DefaultPasskeyName {
		t.Fatalf("unnamed passkey called %q", got)
	}

	h.clock.advance(time.Minute)
	s := h.startSignIn()
	so := decodeOptions(t, s.Options)
	if so.RPID != dashHost || s.Options["allowCredentials"] != nil {
		t.Fatalf("sign-in options = %v, want rpId %s and no allowCredentials", s.Options, dashHost)
	}
	rec = h.finishSignIn(s.CeremonyId, a.get(s.Options, dashOrig, nil))
	wantStatus(t, rec, http.StatusOK)
	if got := decode[api.Me](t, rec); got.Owner.Id != me.Owner.Id || got.Credential != api.MeCredentialSession {
		t.Fatalf("signed in as %+v, want %s", got, me.Owner.Id)
	}
	fresh := sessionToken(t, rec)
	if fresh == cookie {
		t.Fatal("passkey sign-in reused the old session")
	}
	wantStatus(t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: fresh}), http.StatusOK)

	list := h.passkeys(fresh)
	if len(list) != 2 || list[0].Id != added.Id || list[0].LastUsedAt == nil || *list[0].LastUsedAt != h.clock.Now().UnixMilli() || list[1].LastUsedAt != nil {
		t.Fatalf("passkeys after sign-in = %+v", list)
	}
	if !strings.Contains(h.logs.String(), `"method":"passkey"`) {
		t.Error("sign-in not logged with method passkey")
	}

	// The counter moves on: a second sign-in with the same passkey works.
	s = h.startSignIn()
	wantStatus(t, h.finishSignIn(s.CeremonyId, a.get(s.Options, dashOrig, nil)), http.StatusOK)
}

func TestPasskeySignInRejected(t *testing.T) {
	tests := []struct {
		name string
		// forge returns the assertion to send for challenge c, given the
		// owner's registered authenticator a.
		forge func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (ceremonyID string, cred map[string]any)
	}{
		{"cross-origin", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.get(c.Options, "http://evil.localhost", nil)
		}},
		{"site origin on the base domain", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.get(c.Options, "http://docs.wardlock.localhost", nil)
		}},
		{"embedded cross-origin", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.get(c.Options, dashOrig, func(cd *clientData, _ *string) { cd.CrossOrigin = true })
		}},
		{"another relying party", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.get(c.Options, dashOrig, func(_ *clientData, rp *string) { *rp = "evil.localhost" })
		}},
		{"registration client data", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.get(c.Options, dashOrig, func(cd *clientData, _ *string) { cd.Type = "webauthn.create" })
		}},
		{"another ceremony's challenge", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			other := h.startSignIn()
			return c.CeremonyId, a.get(other.Options, dashOrig, nil)
		}},
		{"replayed assertion", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			cred := a.get(c.Options, dashOrig, nil)
			wantStatus(t, h.finishSignIn(c.CeremonyId, cred), http.StatusOK)
			return c.CeremonyId, cred
		}},
		{"replayed assertion on a new ceremony", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			first := h.startSignIn()
			cred := a.get(first.Options, dashOrig, nil)
			wantStatus(t, h.finishSignIn(first.CeremonyId, cred), http.StatusOK)
			return c.CeremonyId, cred
		}},
		{"signature counter went backwards", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			first := h.startSignIn()
			wantStatus(t, h.finishSignIn(first.CeremonyId, a.get(first.Options, dashOrig, nil)), http.StatusOK)
			a.count = 0 // the next assertion carries 1 again
			return c.CeremonyId, a.get(c.Options, dashOrig, nil)
		}},
		{"bad signature", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			cred := a.get(c.Options, dashOrig, nil)
			cred["response"].(map[string]any)["signature"] = b64.EncodeToString([]byte("not a signature"))
			return c.CeremonyId, cred
		}},
		{"unknown passkey", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			other := newSoftAuthenticator(t)
			other.userHandle = a.userHandle
			return c.CeremonyId, other.get(c.Options, dashOrig, nil)
		}},
		{"another owner's user handle", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			other := decode[api.Me](t, h.do(req{method: http.MethodGet, path: "/api/v0/me", cookie: h.owner("other@example.test")}))
			a.userHandle = []byte(other.Owner.Id)
			return c.CeremonyId, a.get(c.Options, dashOrig, nil)
		}},
		{"expired ceremony", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			h.clock.advance(owners.CeremonyValidity)
			return c.CeremonyId, a.get(c.Options, dashOrig, nil)
		}},
		{"unknown ceremony", func(t *testing.T, _ *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return "no-such-ceremony", a.get(c.Options, dashOrig, nil)
		}},
		{"registration ceremony", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			reg := h.startRegistration(h.signIn("owner@example.test"))
			return reg.CeremonyId, a.get(c.Options, dashOrig, nil)
		}},
		{"removed passkey", func(t *testing.T, h *ownerHarness, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			cookie := h.signIn("owner@example.test")
			for _, p := range h.passkeys(cookie) {
				wantStatus(t, h.do(req{method: http.MethodDelete, path: "/api/v0/passkeys/" + p.Id, cookie: cookie}), http.StatusNoContent)
			}
			return c.CeremonyId, a.get(c.Options, dashOrig, nil)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newPasskeyHarness(t, owners.Limits{})
			a := newSoftAuthenticator(t)
			h.register(h.owner("owner@example.test"), a, "Laptop")
			c := h.startSignIn()
			id, cred := tt.forge(t, h, a, c)
			rec := h.finishSignIn(id, cred)
			wantError(t, rec, http.StatusBadRequest, "invalid_passkey")
			if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Fatalf("rejected sign-in set cookies %q", got)
			}
			if !strings.Contains(h.logs.String(), "passkey rejected") && tt.name != "unknown ceremony" && tt.name != "registration ceremony" && tt.name != "expired ceremony" && !strings.HasPrefix(tt.name, "replayed") {
				t.Error("rejection not logged")
			}
		})
	}
}

func TestPasskeyRegistrationRejected(t *testing.T) {
	tests := []struct {
		name   string
		forge  func(t *testing.T, h *ownerHarness, cookie string, a *softAuthenticator, c api.PasskeyChallenge) (ceremonyID string, cred map[string]any)
		status int
		code   string
	}{
		{"cross-origin", func(t *testing.T, _ *ownerHarness, _ string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.create(c.Options, "http://evil.localhost", nil)
		}, http.StatusBadRequest, "invalid_passkey"},
		{"another relying party", func(t *testing.T, _ *ownerHarness, _ string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.create(c.Options, dashOrig, func(_ *clientData, rp *string) { *rp = "evil.localhost" })
		}, http.StatusBadRequest, "invalid_passkey"},
		{"sign-in client data", func(t *testing.T, _ *ownerHarness, _ string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return c.CeremonyId, a.create(c.Options, dashOrig, func(cd *clientData, _ *string) { cd.Type = "webauthn.get" })
		}, http.StatusBadRequest, "invalid_passkey"},
		{"replayed attestation", func(t *testing.T, h *ownerHarness, cookie string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			cred := a.create(c.Options, dashOrig, nil)
			wantStatus(t, h.finishRegistration(cookie, c.CeremonyId, "", cred), http.StatusCreated)
			return c.CeremonyId, cred
		}, http.StatusBadRequest, "invalid_passkey"},
		{"credential already registered", func(t *testing.T, h *ownerHarness, cookie string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			h.register(cookie, a, "")
			return c.CeremonyId, a.create(c.Options, dashOrig, nil)
		}, http.StatusBadRequest, "invalid_passkey"},
		{"credential registered by another owner", func(t *testing.T, h *ownerHarness, _ string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			h.register(h.owner("other@example.test"), a, "")
			return c.CeremonyId, a.create(c.Options, dashOrig, nil)
		}, http.StatusBadRequest, "invalid_passkey"},
		{"another owner's ceremony", func(t *testing.T, h *ownerHarness, _ string, a *softAuthenticator, _ api.PasskeyChallenge) (string, map[string]any) {
			other := h.startRegistration(h.owner("other@example.test"))
			return other.CeremonyId, a.create(other.Options, dashOrig, nil)
		}, http.StatusBadRequest, "invalid_passkey"},
		{"sign-in ceremony", func(t *testing.T, h *ownerHarness, _ string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			return h.startSignIn().CeremonyId, a.create(c.Options, dashOrig, nil)
		}, http.StatusBadRequest, "invalid_passkey"},
		{"expired ceremony", func(t *testing.T, h *ownerHarness, _ string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			h.clock.advance(owners.CeremonyValidity)
			return c.CeremonyId, a.create(c.Options, dashOrig, nil)
		}, http.StatusBadRequest, "invalid_passkey"},
		{"malformed response", func(t *testing.T, _ *ownerHarness, _ string, a *softAuthenticator, c api.PasskeyChallenge) (string, map[string]any) {
			cred := a.create(c.Options, dashOrig, nil)
			cred["response"].(map[string]any)["attestationObject"] = "!!"
			return c.CeremonyId, cred
		}, http.StatusBadRequest, "invalid_passkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newPasskeyHarness(t, owners.Limits{})
			cookie := h.owner("owner@example.test")
			a := newSoftAuthenticator(t)
			c := h.startRegistration(cookie)
			id, cred := tt.forge(t, h, cookie, a, c)
			before := len(h.passkeys(cookie))
			wantError(t, h.finishRegistration(cookie, id, "", cred), tt.status, tt.code)
			if after := len(h.passkeys(cookie)); after != before {
				t.Fatalf("passkeys %d -> %d after a rejected registration", before, after)
			}
		})
	}
}

func TestPasskeyRegistrationRequests(t *testing.T) {
	h := newPasskeyHarness(t, owners.Limits{})
	cookie := h.owner("owner@example.test")

	t.Run("signed out", func(t *testing.T) {
		wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/passkeys/registration"}), http.StatusUnauthorized, "unauthenticated")
		wantError(t, h.do(req{method: http.MethodGet, path: "/api/v0/passkeys"}), http.StatusUnauthorized, "unauthenticated")
		wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/passkeys", body: map[string]any{"ceremonyId": "x", "credential": map[string]any{"id": "a", "rawId": "a", "type": "public-key", "response": map[string]any{}}}}), http.StatusUnauthorized, "unauthenticated")
		wantError(t, h.do(req{method: http.MethodDelete, path: "/api/v0/passkeys/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e40"}), http.StatusUnauthorized, "unauthenticated")
	})
	t.Run("cross-origin", func(t *testing.T) {
		rec := h.do(req{method: http.MethodPost, path: "/api/v0/passkeys/registration", cookie: cookie, headers: map[string]string{"Origin": "http://evil.localhost"}})
		wantError(t, rec, http.StatusForbidden, "forbidden")
		rec = h.do(req{method: http.MethodPost, path: "/api/v0/auth/passkey/verify", headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			body: map[string]any{"ceremonyId": "x", "credential": map[string]any{"id": "a", "rawId": "a", "type": "public-key", "response": map[string]any{}}}})
		wantError(t, rec, http.StatusForbidden, "forbidden")
	})
	t.Run("bad name", func(t *testing.T) {
		for _, name := range []string{strings.Repeat("n", owners.MaxPasskeyName+1), "Lap\ntop", "Lap\u202etop"} {
			c := h.startRegistration(cookie)
			a := newSoftAuthenticator(t)
			wantError(t, h.finishRegistration(cookie, c.CeremonyId, name, a.create(c.Options, dashOrig, nil)), http.StatusBadRequest, "invalid_request")
			// The ceremony survives a bad name, so the owner can fix it.
			wantStatus(t, h.finishRegistration(cookie, c.CeremonyId, "Fixed", a.create(c.Options, dashOrig, nil)), http.StatusCreated)
		}
	})
	t.Run("missing ceremony", func(t *testing.T) {
		rec := h.do(req{method: http.MethodPost, path: "/api/v0/passkeys", cookie: cookie, body: map[string]any{"credential": map[string]any{"id": "a", "rawId": "a", "type": "public-key", "response": map[string]any{}}}})
		wantError(t, rec, http.StatusBadRequest, "invalid_request")
	})
	t.Run("limit", func(t *testing.T) {
		for len(h.passkeys(cookie)) < owners.MaxPasskeys {
			h.register(cookie, newSoftAuthenticator(t), "")
			h.clock.advance(3 * time.Minute) // under the registration rate
		}
		wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/passkeys/registration", cookie: cookie}), http.StatusConflict, "passkey_limit")
	})
}

func TestPasskeyOwnerIsolation(t *testing.T) {
	h := newPasskeyHarness(t, owners.Limits{})
	alice := h.owner("alice@example.test")
	bob := h.owner("bob@example.test")
	pa := h.register(alice, newSoftAuthenticator(t), "Alice's laptop")
	pb := h.register(bob, newSoftAuthenticator(t), "Bob's phone")

	if got := h.passkeys(bob); len(got) != 1 || got[0].Id != pb.Id {
		t.Fatalf("bob lists %+v, want only his own", got)
	}
	foreign := h.do(req{method: http.MethodDelete, path: "/api/v0/passkeys/" + pa.Id, cookie: bob})
	missing := h.do(req{method: http.MethodDelete, path: "/api/v0/passkeys/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e40", cookie: bob})
	wantError(t, foreign, http.StatusNotFound, "not_found")
	wantError(t, missing, http.StatusNotFound, "not_found")
	if foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign and missing passkeys answer differently:\n%s\n%s", foreign.Body, missing.Body)
	}
	if got := h.passkeys(alice); len(got) != 1 || got[0].Id != pa.Id {
		t.Fatalf("alice lists %+v after bob's delete, want her passkey intact", got)
	}
	wantStatus(t, h.do(req{method: http.MethodDelete, path: "/api/v0/passkeys/" + pa.Id, cookie: alice}), http.StatusNoContent)
	wantError(t, h.do(req{method: http.MethodDelete, path: "/api/v0/passkeys/" + pa.Id, cookie: alice}), http.StatusNotFound, "not_found")
	if got := h.passkeys(alice); len(got) != 0 {
		t.Fatalf("alice lists %+v after removing her passkey", got)
	}
}

func TestPasskeySignInRateLimit(t *testing.T) {
	h := newPasskeyHarness(t, owners.Limits{VerifyPerIP: owners.Rate{N: 2, Per: time.Minute}})
	for range 2 {
		h.startSignIn()
	}
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/auth/passkey"})
	wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// Another client is unaffected.
	wantStatus(t, h.do(req{method: http.MethodPost, path: "/api/v0/auth/passkey", ip: "198.51.100.7"}), http.StatusOK)
	h.clock.advance(time.Minute)
	h.startSignIn()
}

func TestPasskeyRegistrationLimits(t *testing.T) {
	h := newPasskeyHarness(t, owners.Limits{})
	alice := h.owner("alice@example.test")

	// An owner may hold only a few registrations open at once...
	for range 3 {
		h.startRegistration(alice)
	}
	rec := h.do(req{method: http.MethodPost, path: "/api/v0/passkeys/registration", cookie: alice})
	wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// ...which neither starves passkey sign-in nor another owner.
	h.startSignIn()
	h.startRegistration(h.owner("bob@example.test"))

	// Spent ceremonies free their slots, but starts are also rate-limited
	// per owner: 3 above plus 7 more, then refused.
	carol := h.owner("carol@example.test")
	for range 10 {
		c := h.startRegistration(carol)
		wantError(t, h.finishRegistration(carol, c.CeremonyId, "", map[string]any{"id": "a", "rawId": "a", "type": "public-key", "response": map[string]any{}}), http.StatusBadRequest, "invalid_passkey")
	}
	wantError(t, h.do(req{method: http.MethodPost, path: "/api/v0/passkeys/registration", cookie: carol}), http.StatusTooManyRequests, "rate_limited")
	h.clock.advance(15 * time.Minute)
	h.startRegistration(carol)
}

func TestPasskeySignInOpenCeremoniesPerNetwork(t *testing.T) {
	h := newPasskeyHarness(t, owners.Limits{})
	start := func(ip string) *httptest.ResponseRecorder {
		return h.do(req{method: http.MethodPost, path: "/api/v0/auth/passkey", ip: ip})
	}
	for range 5 {
		wantStatus(t, start("2001:db8:1:1::1"), http.StatusOK)
	}
	// Another /64 in the same /48 shares the allowance; another /48 and an
	// IPv4 client do not.
	wantError(t, start("2001:db8:1:2::1"), http.StatusTooManyRequests, "rate_limited")
	wantStatus(t, start("2001:db8:2::1"), http.StatusOK)
	wantStatus(t, start("192.0.2.9"), http.StatusOK)
	// Open ceremonies expire, freeing the allowance.
	h.clock.advance(owners.CeremonyValidity)
	wantStatus(t, start("2001:db8:1:2::1"), http.StatusOK)
}

// racyPasskeys records a concurrent sign-in with the same passkey just
// before the one under test records its own.
type racyPasskeys struct{ store.Passkeys }

func (r racyPasskeys) RecordUse(ctx context.Context, ownerID, id string, previous, signCount uint32, credential []byte, at time.Time) error {
	if err := r.Passkeys.RecordUse(ctx, ownerID, id, previous, signCount+1, credential, at); err != nil {
		return err
	}
	return r.Passkeys.RecordUse(ctx, ownerID, id, previous, signCount, credential, at)
}

func TestPasskeySignInLosesCounterRace(t *testing.T) {
	db := storetest.New(t)
	h := newOwnerHarnessWith(t, db, owners.Limits{}, func(s *api.Server) {
		pk, err := owners.NewPasskeys(s.Owners, racyPasskeys{db.Passkeys()})
		if err != nil {
			t.Fatal(err)
		}
		s.Passkeys = pk
	})
	a := newSoftAuthenticator(t)
	h.register(h.owner("owner@example.test"), a, "Laptop")
	c := h.startSignIn()
	rec := h.finishSignIn(c.CeremonyId, a.get(c.Options, dashOrig, nil))
	wantError(t, rec, http.StatusBadRequest, "invalid_passkey")
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("lost race set cookies %q", got)
	}
	if !strings.Contains(h.logs.String(), "used concurrently") {
		t.Error("lost race not logged")
	}
}
