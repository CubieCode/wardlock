// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCommands(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr error
		want    string
	}{
		{[]string{"version"}, nil, "wardlockd dev"},
		{[]string{"help"}, nil, "serve"},
		{nil, errUsage, ""},
		{[]string{"nope"}, errUsage, ""},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		err := run(t.Context(), tt.args, func(string) string { return "" }, &out, io.Discard)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("run(%q) error = %v, want %v", tt.args, err, tt.wantErr)
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Errorf("run(%q) output %q does not contain %q", tt.args, out.String(), tt.want)
		}
	}
}

// TestServe boots the real server on a free port and checks the three
// F0.1 endpoints, then shuts it down through context cancellation.
func TestServe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	env := map[string]string{
		"WARDLOCK_HTTP_ADDR":   addr,
		"WARDLOCK_DATA_DIR":    t.TempDir(),
		"WARDLOCK_BASE_DOMAIN": "wardlock.localhost",
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve"}, func(k string) string { return env[k] }, io.Discard, io.Discard)
	}()

	get := func(host, path string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
		req.Host = host
		var lastErr error
		for range 50 {
			res, err := http.DefaultClient.Do(req)
			if err == nil {
				defer func() { _ = res.Body.Close() }()
				body, _ := io.ReadAll(res.Body)
				return res.StatusCode, string(body)
			}
			lastErr = err
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("server did not answer: %v", lastErr)
		return 0, ""
	}

	if code, body := get("wardlock.localhost", "/healthz"); code != 200 || !strings.Contains(body, "ok") {
		t.Errorf("/healthz = %d %q", code, body)
	}
	code, body := get("wardlock.localhost", "/api/v0/health")
	var health struct{ Status, Version string }
	if err := json.Unmarshal([]byte(body), &health); code != 200 || err != nil || health.Status != "ok" {
		t.Errorf("/api/v0/health = %d %q", code, body)
	}
	if code, body := get("wardlock.localhost", "/"); code != 200 || !strings.Contains(body, "<html") {
		t.Errorf("dashboard = %d %q", code, body)
	}
	if code, body := get("hello.wardlock.localhost", "/"); code != 200 || !strings.Contains(body, "Hello from Wardlock") {
		t.Errorf("hello site = %d %q", code, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal(fmt.Errorf("serve did not shut down"))
	}
}
