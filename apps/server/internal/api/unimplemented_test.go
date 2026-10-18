// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store/storetest"
)

// implemented lists the operations Server declares itself. Add an operation
// here when its handler lands. Once auth middleware lands (C1.1), request
// will need to attach a credential so protected operations reach the stub
// rather than answering 401.
var implemented = map[string]bool{
	"GetHealth": true,

	// Sites and deploys (C1.2).
	"ListSites":        true,
	"CreateSite":       true,
	"GetSite":          true,
	"DeleteSite":       true,
	"ListDeploys":      true,
	"CreateDeploy":     true,
	"UploadDeployBlob": true,
	"ActivateDeploy":   true,
	"RollbackSite":     true,

	// Policies (A2.1).
	"ListPolicies": true,
	"SetPolicy":    true,
	"DeletePolicy": true,

	// Share links (A2.2).
	"ListShareLinks":  true,
	"CreateShareLink": true,
	"RevokeShareLink": true,
	"RotateShareLink": true,

	// Share-link access log (D3.3).
	"ListShareLinkAccessLog": true,

	// C1.1 owners.
	"CompleteSetup":    true,
	"RedeemSignInLink": true,
	"SendEmailCode":    true,
	"VerifyEmailCode":  true,
	"SignOut":          true,
	"GetMe":            true,
	"InviteOwner":      true,
	"ListSessions":     true,
	"RevokeSession":    true,

	// C1.6 passkeys.
	"StartPasskeySignIn":       true,
	"VerifyPasskeySignIn":      true,
	"ListPasskeys":             true,
	"StartPasskeyRegistration": true,
	"CreatePasskey":            true,
	"DeletePasskey":            true,

	// C1.7 API tokens and CLI login.
	"ListApiTokens":   true,
	"CreateApiToken":  true,
	"RevokeApiToken":  true,
	"StartCliLogin":   true,
	"PollCliLogin":    true,
	"ApproveCliLogin": true,
	"DenyCliLogin":    true,
}

// pathValues are valid values for every path parameter in the contract.
var pathValues = map[string]string{
	"site":      "docs",
	"deployId":  "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e10",
	"sessionId": "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e0f",
	"tokenId":   "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e20",
	"sha256":    strings.Repeat("ab", 32),
	"hostname":  "docs.example.com",
	"policyId":  "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e41",
	"passkeyId": "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e40",
	"linkId":    "q4v7m2xkpa",
}

var pathParam = regexp.MustCompile(`\{(\w+)\}`)

// request builds a well-formed request for op, so the only reason for a
// non-2xx answer is the operation itself.
func request(t *testing.T, op operation) *http.Request {
	t.Helper()
	path := pathParam.ReplaceAllStringFunc(op.path, func(m string) string {
		v, ok := pathValues[strings.Trim(m, "{}")]
		if !ok {
			t.Fatalf("%s: no test value for path parameter %s", op.id, m)
		}
		return v
	})
	switch {
	case op.jsonBody:
		r := httptest.NewRequest(op.method, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		return r
	case op.method == http.MethodPut:
		r := httptest.NewRequest(op.method, path, strings.NewReader("hello"))
		r.Header.Set("Content-Type", "application/octet-stream")
		return r
	default:
		return httptest.NewRequest(op.method, path, nil)
	}
}

func TestOperationTableMatchesInterface(t *testing.T) {
	iface := reflect.TypeFor[StrictServerInterface]()
	if got, want := len(operations), iface.NumMethod(); got != want {
		t.Fatalf("operations has %d entries, StrictServerInterface has %d methods", got, want)
	}
	for _, op := range operations {
		if _, ok := iface.MethodByName(op.id); !ok {
			t.Errorf("operation %s has no StrictServerInterface method", op.id)
		}
	}
	for id := range implemented {
		if _, ok := iface.MethodByName(id); !ok {
			t.Errorf("implemented lists unknown operation %s", id)
		}
	}
}

func TestUnimplementedAnswers501(t *testing.T) {
	h := newHandler(Unimplemented{})
	for _, op := range operations {
		t.Run(op.id, func(t *testing.T) {
			assertNotImplemented(t, h, op)
		})
	}
}

func TestServerRoutesEveryOperation(t *testing.T) {
	h := (&Server{Version: "test", Passkeys: bareOwnerServices(t), Tokens: newTestTokens(t)}).Handler()
	for _, op := range operations {
		t.Run(op.id, func(t *testing.T) {
			if implemented[op.id] {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, request(t, op))
				if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed || rec.Code == http.StatusNotImplemented {
					t.Fatalf("%s %s: status %d for an implemented operation", op.method, op.path, rec.Code)
				}
				return
			}
			assertNotImplemented(t, h, op)
		})
	}
}

func assertNotImplemented(t *testing.T, h http.Handler, op operation) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request(t, op))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("%s %s: status %d, want 501; body %s", op.method, op.path, rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "not_implemented" || body.Message == "" {
		t.Errorf("body = %+v, want code not_implemented with a message", body)
	}
}

func TestRollbackWithoutBodyReachesOperation(t *testing.T) {
	h := (&Server{}).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v0/sites/docs/rollback", nil))
	// 401 comes from the operation itself; a decoding failure would be 400.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; body %s", rec.Code, rec.Body)
	}
}

func TestMalformedRequestAnswers400(t *testing.T) {
	h := (&Server{}).Handler()
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"malformed JSON body", http.MethodPost, "/api/v0/sites", "{"},
		{"non-integer limit", http.MethodGet, "/api/v0/sites?limit=many", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body %s", rec.Code, rec.Body)
			}
			var body Error
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Code != "invalid_request" {
				t.Errorf("code = %q, want invalid_request", body.Code)
			}
		})
	}
}

type discardMailer struct{}

func (discardMailer) Send(context.Context, mail.Message) error { return nil }

// bareOwnerServices is passkey support over an empty database, for
// operations (such as startPasskeySignIn) that reach the service without
// any credential or body.
func bareOwnerServices(t *testing.T) *owners.Passkeys {
	t.Helper()
	db := storetest.New(t)
	svc, err := owners.New(owners.Config{
		Owners: db.Owners(), Sessions: db.Sessions(), Mailer: discardMailer{}, Logger: slog.New(slog.DiscardHandler),
		Secret: bytes.Repeat([]byte("k"), 32), DashboardURL: &url.URL{Scheme: "http", Host: "wardlock.localhost"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	pk, err := owners.NewPasskeys(svc, db.Passkeys())
	if err != nil {
		t.Fatal(err)
	}
	return pk
}
