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
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/platform"
)

func TestCommands(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr error
		want    string
	}{
		{[]string{"version"}, nil, "wardlockd dev"},
		{[]string{"help"}, nil, "serve"},
		{[]string{"--help"}, nil, "admin create --email"},
		{[]string{"admin", "--help"}, nil, "admin create --email ADDRESS"},
		{[]string{"admin"}, errUsage, ""},
		{[]string{"admin", "create"}, errUsage, ""},
		{[]string{"admin", "delete"}, errUsage, ""},
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

// TestServe boots the real server on a free port, checks health, the API,
// the dashboard and an unknown site, then shuts it down through context
// cancellation.
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
		"WARDLOCK_SECRET":      testSecret,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	logs := &syncBuffer{}
	go func() {
		done <- run(ctx, []string{"serve"}, func(k string) string { return env[k] }, io.Discard, logs)
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
	if code, body := get("nothing.wardlock.localhost", "/"); code != 404 || !strings.Contains(body, "No site") {
		t.Errorf("unknown site = %d %q", code, body)
	}
	// The sites API is wired; without a credential it answers 401.
	if code, body := get("wardlock.localhost", "/api/v0/sites"); code != 401 || !strings.Contains(body, "unauthenticated") {
		t.Errorf("/api/v0/sites = %d %q", code, body)
	}

	// A site session cookie never authenticates the dashboard API.
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/v0/me", nil)
	req.Host = "wardlock.localhost"
	req.AddCookie(&http.Cookie{Name: access.SessionCookieName, Value: "djE.c2lnbmF0dXJl"})
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 401 {
		t.Errorf("/api/v0/me with a site cookie = %v %v", res, err)
	} else {
		_ = res.Body.Close()
	}

	if !strings.Contains(logs.String(), "setup_url") || !strings.Contains(logs.String(), "/setup?code=") {
		t.Errorf("serve with no owner did not log a setup URL:\n%s", logs)
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

// testSecret is a synthetic WARDLOCK_SECRET (32 bytes, base64url).
const testSecret = "dGVzdC1vbmx5LXNlY3JldC1ub3QtZm9yLXByb2R1Y3Q"

// syncBuffer is a bytes.Buffer safe for the server goroutine to write while
// the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestPublicOrigin(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"wardlock.localhost", 21837, "http://wardlock.localhost:21837"},
		{"admin.wardlock.localhost", 80, "http://admin.wardlock.localhost"},
		{"localhost", 8080, "http://localhost:8080"},
		{"wardlock.example.com", 8080, "https://wardlock.example.com"},
		{"wardlock.example.com", 443, "https://wardlock.example.com"},
		{"notlocalhost", 8080, "https://notlocalhost"},
	}
	for _, tt := range tests {
		if got := publicOrigin(tt.host, tt.port).String(); got != tt.want {
			t.Errorf("publicOrigin(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}

func TestAdminCreateProductionURL(t *testing.T) {
	env := map[string]string{
		"WARDLOCK_DATA_DIR":       t.TempDir(),
		"WARDLOCK_HTTP_ADDR":      "127.0.0.1:8080",
		"WARDLOCK_BASE_DOMAIN":    "sites.example.test",
		"WARDLOCK_DASHBOARD_HOST": "wardlock.example.test",
		"WARDLOCK_SECRET":         testSecret,
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"admin", "create", "--email", "first@example.test"}, func(k string) string { return env[k] }, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://wardlock.example.test/sign-in?code=") {
		t.Errorf("admin create output:\n%s", out.String())
	}
}

func TestServeRequiresSecret(t *testing.T) {
	env := map[string]string{"WARDLOCK_DATA_DIR": t.TempDir()}
	err := run(t.Context(), []string{"serve"}, func(k string) string { return env[k] }, io.Discard, io.Discard)
	if !errors.Is(err, platform.ErrNoSecret) {
		t.Fatalf("serve without WARDLOCK_SECRET = %v, want ErrNoSecret", err)
	}
}

func TestAdminCreate(t *testing.T) {
	env := map[string]string{
		"WARDLOCK_DATA_DIR":       t.TempDir(),
		"WARDLOCK_HTTP_ADDR":      "127.0.0.1:8123",
		"WARDLOCK_DASHBOARD_HOST": "admin.wardlock.localhost",
		"WARDLOCK_SECRET":         testSecret,
	}
	getenv := func(k string) string { return env[k] }
	var out, errOut bytes.Buffer
	if err := run(t.Context(), []string{"admin", "create", "--email", "First@Example.test"}, getenv, &out, &errOut); err != nil {
		t.Fatalf("admin create: %v\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "first@example.test") || !strings.Contains(out.String(), "http://admin.wardlock.localhost:8123/sign-in?code=") {
		t.Errorf("admin create output:\n%s", out.String())
	}
	if strings.Contains(errOut.String(), "code=") {
		t.Errorf("sign-in URL leaked into the log:\n%s", errOut.String())
	}

	err := run(t.Context(), []string{"admin", "create", "--email", "first@example.test"}, getenv, io.Discard, io.Discard)
	if !errors.Is(err, owners.ErrOwnerExists) {
		t.Errorf("second admin create = %v, want ErrOwnerExists", err)
	}
	if err := run(t.Context(), []string{"admin", "create", "--email", "not an address"}, getenv, io.Discard, io.Discard); err == nil {
		t.Error("admin create accepted an invalid address")
	}
}
