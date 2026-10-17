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
	"testing"
)

// failingHealth answers GetHealth with an unmapped error.
type failingHealth struct{ Unimplemented }

func (failingHealth) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return nil, errors.New("disk on fire")
}

func TestInternalErrorsAreLogged(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := (&Server{Logger: log}).authenticate(newLoggedHandler(failingHealth{}, log))
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v0/health", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "cookie-value-never-logged"})
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	var line struct {
		Level, Msg, Error string
		RequestID         string `json:"request_id"`
		Method, Route     string
	}
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("log %q: %v", logs.String(), err)
	}
	if line.Level != "ERROR" || line.Msg != "internal error" || line.Method != http.MethodGet ||
		line.Route != "GET /api/v0/health" || line.Error != "disk on fire" {
		t.Errorf("log line %+v", line)
	}
	if line.RequestID == "" || line.RequestID != rec.Header().Get("X-Request-Id") {
		t.Errorf("request_id %q, X-Request-Id %q", line.RequestID, rec.Header().Get("X-Request-Id"))
	}
	if bytes.Contains(logs.Bytes(), []byte("cookie-value-never-logged")) {
		t.Error("the cookie reached the log")
	}
}
