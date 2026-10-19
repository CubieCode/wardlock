// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServeWarnsDevMailOutsideLocalMode checks the startup warning for the
// dev:// mail sink, which delivers nothing and logs each recipient, on any
// instance not in local mode.
func TestServeWarnsDevMailOutsideLocalMode(t *testing.T) {
	tests := []struct {
		name, base, smtp string
		warn             bool
	}{
		{"local mode with dev mail", "sites.localhost", "", false},
		{"proxy mode with dev mail", "sites.test", "", true},
		{"proxy mode with explicit dev mail", "sites.test", "dev://", true},
		{"proxy mode with SMTP", "sites.test", "smtp://mail.example.test:587", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := serveLogs(t, map[string]string{
				"WARDLOCK_BASE_DOMAIN": tt.base,
				"WARDLOCK_SMTP_URL":    tt.smtp,
			})
			warned := strings.Count(logs, "WARDLOCK_SMTP_URL is dev:// outside local mode")
			if want := map[bool]int{true: 1, false: 0}[tt.warn]; warned != want {
				t.Fatalf("warning logged %d times, want %d:\n%s", warned, want, logs)
			}
		})
	}
}

// serveLogs runs `wardlockd serve` with env over the defaults until it
// answers /healthz, stops it and returns its log.
func serveLogs(t *testing.T, env map[string]string) string {
	t.Helper()
	addr := freeAddr(t)
	vars := map[string]string{
		"WARDLOCK_HTTP_ADDR": addr,
		"WARDLOCK_DATA_DIR":  t.TempDir(),
		"WARDLOCK_SECRET":    testSecret,
	}
	for k, v := range env {
		vars[k] = v
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	logs := &syncBuffer{}
	go func() {
		done <- run(ctx, []string{"serve"}, func(k string) string { return vars[k] }, io.Discard, logs)
	}()
	var lastErr error
	for range 50 {
		res, err := http.Get("http://" + addr + "/healthz")
		if lastErr = err; err == nil {
			_ = res.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil || lastErr != nil {
		t.Fatalf("serve = %v, health = %v\n%s", err, lastErr, logs)
	}
	return logs.String()
}
