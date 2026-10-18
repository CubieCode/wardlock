// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// testApp returns an app with no environment, a fake keychain, a config
// directory in t's temporary directory, no browser and an instant clock.
func testApp(t *testing.T) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	now := time.UnixMilli(1_800_000_000_000)
	a := &app{
		getenv:      noEnv,
		stdout:      &out,
		stderr:      &errOut,
		http:        http.DefaultClient,
		creds:       credentialStore{keyring: newFakeKeyring(), dir: func() (string, error) { return dir + "/wardlock", nil }},
		openBrowser: func(string) error { t.Error("opened a browser"); return nil },
		hostname:    func() (string, error) { return "build-host", nil },
		now:         func() time.Time { return now },
	}
	a.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		now = now.Add(d)
		return nil
	}
	return a, &out, &errOut
}

func noEnv(string) string { return "" }

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestHelpListsExactlyTheBuiltCommands(t *testing.T) {
	a, out, _ := testApp(t)
	if err := run(t.Context(), a, []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	_, commandList, _ := strings.Cut(out.String(), "Commands:\n")
	listed := regexp.MustCompile(`(?m)^  ([a-z]+) `).FindAllStringSubmatch(commandList, -1)
	var got []string
	for _, m := range listed {
		got = append(got, m[1])
	}
	want := []string{"login", "logout", "version", "help"}
	if !slices.Equal(got, want) {
		t.Fatalf("help lists %q, want %q\n%s", got, want, out)
	}
}

func TestNoArgumentsIsAUsageError(t *testing.T) {
	a, _, errOut := testApp(t)
	if err := run(t.Context(), a, nil); !errors.Is(err, errUsage) || !strings.Contains(errOut.String(), "login") {
		t.Fatalf("error = %v, stderr %q", err, errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	a, _, _ := testApp(t)
	if err := run(t.Context(), a, []string{"deploy"}); !errors.Is(err, errUsage) {
		t.Fatalf("error = %v, want errUsage", err)
	}
}

func TestVersionWithServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.0.1"}`))
	}))
	defer srv.Close()
	a, out, _ := testApp(t)
	if err := run(t.Context(), a, []string{"version", "--server", srv.URL}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "wardlock dev") || !strings.Contains(out.String(), "wardlockd 0.0.1") {
		t.Fatalf("output = %q", out.String())
	}
}

func contextWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(t.Context())
}
