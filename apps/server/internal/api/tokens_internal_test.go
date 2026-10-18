// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store/storetest"
)

func newTestTokens(t *testing.T) *owners.Tokens {
	t.Helper()
	svc, err := owners.NewTokens(owners.TokensConfig{
		Tokens: storetest.New(t).APITokens(), Logger: slog.New(slog.DiscardHandler),
		Secret: bytes.Repeat([]byte("k"), 32), DashboardURL: &url.URL{Scheme: "https", Host: "dash.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// TestTokenScopesMatchContract checks publicOperations, sessionOnly and
// tokenScopes against the security requirements
// packages/api-spec/openapi.yaml gives every operation, and that every
// operation is in exactly one of them.
func TestTokenScopesMatchContract(t *testing.T) {
	seen := map[string]bool{}
	for _, op := range operations {
		var want []string
		if slices.Equal(op.security, []string{"sessionCookie"}) != sessionOnly[op.id] {
			t.Errorf("%s: sessionOnly %v, contract security %q", op.id, sessionOnly[op.id], op.security)
		}
		if (len(op.security) == 0) != publicOperations[op.id] {
			t.Errorf("%s: publicOperations %v, contract security %q", op.id, publicOperations[op.id], op.security)
		}
		tables := 0
		for _, in := range []bool{publicOperations[op.id], sessionOnly[op.id], tokenScopes[op.id] != nil} {
			if in {
				tables++
			}
		}
		if tables != 1 {
			t.Errorf("%s is in %d token tables, want exactly 1", op.id, tables)
		}
		for _, sec := range op.security {
			name, scopes, _ := strings.Cut(sec, ":")
			if name != "apiToken" {
				continue
			}
			if strings.Contains(scopes, ":") {
				t.Fatalf("%s: a requirement needing several scopes (%q) is not supported", op.id, sec)
			}
			want = append(want, scopes)
		}
		got := slices.Clone(tokenScopes[op.id])
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s: tokenScopes %q, contract %q", op.id, got, want)
		}
		seen[op.id] = true
	}
	for id := range tokenScopes {
		if !seen[id] {
			t.Errorf("tokenScopes lists unknown operation %s", id)
		}
	}
	for id := range sessionOnly {
		if !seen[id] {
			t.Errorf("sessionOnly lists unknown operation %s", id)
		}
	}
	for id := range publicOperations {
		if !seen[id] {
			t.Errorf("publicOperations lists unknown operation %s", id)
		}
	}
}

// Every session-only operation, passkey management included, refuses a
// token with both scopes: a leaked token must never register a passkey,
// mint tokens or manage owners and sessions.
func TestTokensRefusedOnSessionOnlyOperations(t *testing.T) {
	for _, id := range []string{"ListPasskeys", "StartPasskeyRegistration", "CreatePasskey", "DeletePasskey"} {
		if !sessionOnly[id] {
			t.Errorf("%s is not session-only", id)
		}
	}
	stub := newHandler(Unimplemented{})
	both := owners.Scopes{Deploy: true, Manage: true}
	n := 0
	for _, op := range operations {
		if !sessionOnly[op.id] {
			continue
		}
		n++
		r := request(t, op)
		ri := requestInfo{tokenID: "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e20", scopes: both}
		r = r.WithContext(context.WithValue(WithOwner(r.Context(), "owner"), requestInfoKey{}, ri))
		rec := httptest.NewRecorder()
		stub.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with a token: status %d, want 403", op.id, rec.Code)
		}
	}
	if n < 12 {
		t.Fatalf("only %d session-only operations in the contract", n)
	}
}

// An operation in no table (one added to the contract before this file)
// refuses tokens at runtime, whatever their scopes; sessions pass.
func TestUnknownOperationRefusesTokens(t *testing.T) {
	reached := false
	next := func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		reached = true
		return nil, nil
	}
	h := requireTokenScopes(next, "SomeFutureOperation")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), requestInfoKey{}, requestInfo{tokenID: "t", scopes: owners.Scopes{Deploy: true, Manage: true}})
	if _, err := h(ctx, httptest.NewRecorder(), r, nil); !errors.Is(err, errTokenScope) || reached {
		t.Fatalf("token on an unknown operation: err %v, reached %v", err, reached)
	}
	ctx = context.WithValue(r.Context(), requestInfoKey{}, requestInfo{sessionID: "s"})
	if _, err := h(ctx, httptest.NewRecorder(), r, nil); err != nil || !reached {
		t.Fatalf("session on an unknown operation: err %v, reached %v", err, reached)
	}
}

// TestScopeEnforcementEveryOperation sends every operation with a deploy,
// a manage and a two-scope token to the stub server: an operation the
// token may use, or one that takes no credentials, reaches the stub (501);
// any other answers 403 before it.
func TestScopeEnforcementEveryOperation(t *testing.T) {
	stub := newHandler(Unimplemented{})
	tokens := map[string]owners.Scopes{
		"deploy": {Deploy: true},
		"manage": {Manage: true},
		"both":   {Deploy: true, Manage: true},
	}
	for _, op := range operations {
		for name, scopes := range tokens {
			t.Run(op.id+"/"+name, func(t *testing.T) {
				r := request(t, op)
				ri := requestInfo{tokenID: "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e20", scopes: scopes}
				r = r.WithContext(context.WithValue(WithOwner(r.Context(), "owner"), requestInfoKey{}, ri))
				rec := httptest.NewRecorder()
				stub.ServeHTTP(rec, r)
				allowed := len(op.security) == 0 || slices.ContainsFunc(op.security, func(sec string) bool {
					req, ok := strings.CutPrefix(sec, "apiToken")
					return ok && (req == "" || (req == ":deploy" && scopes.Deploy) || (req == ":manage" && scopes.Manage))
				})
				want := http.StatusForbidden
				if allowed {
					want = http.StatusNotImplemented
				}
				if rec.Code != want {
					t.Fatalf("status %d, want %d; body %s", rec.Code, want, rec.Body)
				}
				if !allowed {
					var e Error
					if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Code != "forbidden" {
						t.Fatalf("body %s, want code forbidden", rec.Body)
					}
				}
			})
		}
	}
	// Session and unauthenticated requests are not the middleware's
	// business.
	for _, op := range operations {
		rec := httptest.NewRecorder()
		stub.ServeHTTP(rec, request(t, op))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s without a token: status %d", op.id, rec.Code)
		}
	}
}

func TestBearerToken(t *testing.T) {
	tests := map[string]string{
		"Bearer wl_x":  "wl_x",
		"bearer wl_x":  "wl_x",
		"BEARER  wl_x": "wl_x",
		"Basic wl_x":   "",
		"Bearer":       "",
		"":             "",
	}
	for header, want := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		got, ok := bearerToken(r)
		if got != want || ok != (want != "") {
			t.Errorf("bearerToken(%q) = %q, %v; want %q", header, got, ok, want)
		}
	}
}
