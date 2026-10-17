// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/platform"
)

// freeAddr returns a loopback address with a port nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// TestServeAutoTLS boots automatic TLS wiring against a CA that cannot be
// reached (so nothing leaves the machine and no certificate is issued) and
// checks the plain-HTTP listener: /healthz answers, everything else
// redirects to HTTPS on the default port.
func TestServeAutoTLS(t *testing.T) {
	httpAddr, httpsAddr := freeAddr(t), freeAddr(t)
	env := map[string]string{
		"WARDLOCK_TLS_MODE":       "auto",
		"WARDLOCK_HTTP_ADDR":      httpAddr,
		"WARDLOCK_HTTPS_ADDR":     httpsAddr,
		"WARDLOCK_DATA_DIR":       t.TempDir(),
		"WARDLOCK_BASE_DOMAIN":    "sites.test",
		"WARDLOCK_DASHBOARD_HOST": "wardlock.test",
		"WARDLOCK_SECRET":         testSecret,
		"WARDLOCK_ACME_CA":        "https://127.0.0.1:1/directory",
		"WARDLOCK_DNS_PROVIDER":   "desec",
		"WARDLOCK_DNS_API_TOKEN":  "test-token-not-real",
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	logs := &syncBuffer{}
	go func() {
		done <- run(ctx, []string{"serve"}, func(k string) string { return env[k] }, io.Discard, logs)
	}()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(host, path string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+httpAddr+path, nil)
		req.Host = host
		var lastErr error
		for range 50 {
			res, err := client.Do(req)
			if err == nil {
				_ = res.Body.Close()
				return res.StatusCode, res.Header.Get("Location")
			}
			lastErr = err
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("server did not answer: %v", lastErr)
		return 0, ""
	}
	if code, _ := get("docs.sites.test", "/healthz"); code != http.StatusOK {
		t.Errorf("/healthz over HTTP = %d", code)
	}
	if code, loc := get("docs.sites.test", "/guide?t=abc"); code != http.StatusMovedPermanently || loc != "https://docs.sites.test/guide?t=abc" {
		t.Errorf("HTTP = %d %q, want a redirect to HTTPS", code, loc)
	}
	conn, err := net.DialTimeout("tcp", httpsAddr, 5*time.Second)
	if err != nil {
		t.Errorf("HTTPS listener: %v", err)
	} else {
		_ = conn.Close()
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down")
	}
	if out := logs.String(); !strings.Contains(out, "*.sites.test") {
		t.Errorf("logs do not name the managed certificates:\n%s", out)
	} else if strings.Contains(out, "test-token-not-real") {
		t.Errorf("logs contain the DNS token")
	}
}

func TestServeTLSConfigErrors(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unknown DNS provider": {
			"WARDLOCK_TLS_MODE": "auto", "WARDLOCK_BASE_DOMAIN": "sites.test",
			"WARDLOCK_DNS_PROVIDER": "route53", "WARDLOCK_DNS_API_TOKEN": "test-token-not-real",
		},
		"missing CA root file": {
			"WARDLOCK_TLS_MODE": "auto", "WARDLOCK_BASE_DOMAIN": "sites.test",
			"WARDLOCK_DNS_PROVIDER": "cloudflare", "WARDLOCK_DNS_API_TOKEN": "test-token-not-real",
			"WARDLOCK_ACME_CA_ROOT": "/nonexistent/ca.pem",
		},
	} {
		t.Run(name, func(t *testing.T) {
			env["WARDLOCK_SECRET"] = testSecret
			env["WARDLOCK_DATA_DIR"] = t.TempDir()
			env["WARDLOCK_HTTP_ADDR"], env["WARDLOCK_HTTPS_ADDR"] = freeAddr(t), freeAddr(t)
			err := run(t.Context(), []string{"serve"}, func(k string) string { return env[k] }, io.Discard, io.Discard)
			if !errors.Is(err, platform.ErrInvalidConfig) {
				t.Fatalf("serve = %v, want ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), "test-token-not-real") {
				t.Fatalf("error leaks the DNS token: %v", err)
			}
		})
	}
}

// TestServeProxyWarnsWithoutTrustedProxies checks the one startup warning
// for proxy mode without WARDLOCK_TRUSTED_PROXIES, and its absence when
// proxies are configured.
func TestServeProxyWarnsWithoutTrustedProxies(t *testing.T) {
	for _, proxies := range []string{"", "127.0.0.1"} {
		t.Run("proxies="+proxies, func(t *testing.T) {
			addr := freeAddr(t)
			env := map[string]string{
				"WARDLOCK_HTTP_ADDR":       addr,
				"WARDLOCK_DATA_DIR":        t.TempDir(),
				"WARDLOCK_BASE_DOMAIN":     "sites.test",
				"WARDLOCK_SECRET":          testSecret,
				"WARDLOCK_TRUSTED_PROXIES": proxies,
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			logs := &syncBuffer{}
			go func() {
				done <- run(ctx, []string{"serve"}, func(k string) string { return env[k] }, io.Discard, logs)
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
				t.Fatalf("serve = %v, health = %v", err, lastErr)
			}
			warned := strings.Count(logs.String(), "proxy mode without WARDLOCK_TRUSTED_PROXIES")
			if want := map[string]int{"": 1, "127.0.0.1": 0}[proxies]; warned != want {
				t.Fatalf("warning logged %d times, want %d:\n%s", warned, want, logs)
			}
		})
	}
}
