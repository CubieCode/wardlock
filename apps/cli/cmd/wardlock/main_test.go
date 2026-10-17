// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHelpListsOnlyBuiltCommands(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), []string{"--help"}, noEnv, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, unbuilt := range []string{"deploy", "login", "share", "policy", "rollback", "teardown", "list"} {
		if strings.Contains(out.String(), "  "+unbuilt+" ") {
			t.Errorf("help lists unbuilt command %q", unbuilt)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	if err := run(t.Context(), []string{"deploy"}, noEnv, io.Discard, io.Discard); !errors.Is(err, errUsage) {
		t.Fatalf("error = %v, want errUsage", err)
	}
}

func TestVersionWithServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.0.1"}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	if err := run(t.Context(), []string{"version", "--server", srv.URL}, noEnv, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "wardlock dev") || !strings.Contains(out.String(), "wardlockd 0.0.1") {
		t.Fatalf("output = %q", out.String())
	}
}

func noEnv(string) string { return "" }
