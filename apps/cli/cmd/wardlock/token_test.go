// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

func TestTokenCreatePrintsOnlyTheTokenOnStdout(t *testing.T) {
	srv := newFakeServer(t, http.StatusAccepted, http.StatusOK)
	a, out, errOut := testApp(t)
	if err := run(t.Context(), a, []string{"token", "create", "--name", "GitHub deploys", "--server", srv.URL + "/"}); err != nil {
		t.Fatalf("token create: %v\n%s", err, errOut)
	}
	if out.String() != testToken+"\n" {
		t.Fatalf("stdout = %q, want only the token", out)
	}
	if srv.start["clientName"] != "GitHub deploys" {
		t.Fatalf("clientName %v", srv.start["clientName"])
	}
	for _, want := range []string{"BDFH-KMPR", srv.URL + "/cli-login?code=BDFH-KMPR", "k7m2q6xw4a", "deploy, manage", "only this once"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	assertNoToken(t, errOut.String())
}

func TestTokenCreateJSON(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, out, errOut := testApp(t)
	if err := run(t.Context(), a, []string{"token", "create", "--json", "--server", srv.URL}); err != nil {
		t.Fatalf("token create: %v\n%s", err, errOut)
	}
	var got apiclient.CreatedApiToken
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON value: %v\n%s", err, out)
	}
	if got.Token != testToken || got.ApiToken.PublicId != "k7m2q6xw4a" {
		t.Fatalf("JSON %+v", got)
	}
	if !strings.Contains(errOut.String(), "BDFH-KMPR") {
		t.Fatalf("instructions missing from stderr: %s", errOut)
	}
	assertNoToken(t, errOut.String())
}

func TestTokenCreateGlobalFlagsFirst(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, out, _ := testApp(t)
	if err := run(t.Context(), a, []string{"--json", "--server", srv.URL, "token", "create"}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("stdout %q", out)
	}
}

func TestTokenCreateDefaultName(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, _, _ := testApp(t)
	if err := run(t.Context(), a, []string{"token", "create", "--name", "  ", "--server", srv.URL}); err != nil {
		t.Fatal(err)
	}
	if srv.start["clientName"] != "wardlock dev on build-host" {
		t.Fatalf("clientName %v", srv.start["clientName"])
	}
}

// The browser depends on stderr alone, so a piped stdout still gets one.
func TestTokenCreateOpensTheBrowserOnlyWhenStderrIsATerminal(t *testing.T) {
	for _, args := range [][]string{{"token", "create"}, {"token", "create", "--json"}} {
		srv := newFakeServer(t, http.StatusOK)
		a, _, _ := testApp(t)
		var opened []string
		a.liveProgress = true
		a.interactive = false
		a.openBrowser = func(u string) error { opened = append(opened, u); return nil }
		if err := run(t.Context(), a, append(args, "--server", srv.URL)); err != nil {
			t.Fatal(err)
		}
		if len(opened) != 1 || opened[0] != srv.URL+"/cli-login?code=BDFH-KMPR" {
			t.Fatalf("%q opened %q", args, opened)
		}
	}
	srv := newFakeServer(t, http.StatusOK)
	a, _, _ := testApp(t)
	a.interactive = true // testApp's openBrowser fails the test if called
	if err := run(t.Context(), a, []string{"token", "create", "--server", srv.URL}); err != nil {
		t.Fatal(err)
	}
}

func TestTokenCreateLeavesStoredCredentialsAlone(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, _, _ := testApp(t)
	if _, err := a.creds.save(srv.URL, "wl_stored"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(credentialsPath(t, a))
	if err != nil {
		t.Fatal(err)
	}
	// The saved server is the default.
	if err := run(t.Context(), a, []string{"token", "create"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(credentialsPath(t, a))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("credentials.json changed:\n%s\n%s", before, after)
	}
	server, tok, err := a.creds.load()
	if err != nil || server != srv.URL || tok != "wl_stored" {
		t.Fatalf("stored %q %q %v", server, tok, err)
	}
}

func TestTokenCreateStoresNothingWithoutALogin(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, _, _ := testApp(t)
	a.getenv = env(map[string]string{"WARDLOCK_SERVER": srv.URL})
	if err := run(t.Context(), a, []string{"token", "create"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credentialsPath(t, a)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("credentials stored by token create")
	}
	if len(a.creds.keyring.(*fakeKeyring).secrets) != 0 {
		t.Fatal("keychain entry stored by token create")
	}
}

func TestTokenCreateFailures(t *testing.T) {
	tests := map[string]struct {
		polls []int
		want  string
	}{
		"denied":       {[]int{http.StatusAccepted, http.StatusGone}, "denied or has expired: run wardlock token create again"},
		"expires":      {[]int{http.StatusAccepted}, "expired before it was approved: run wardlock token create again"},
		"server error": {[]int{http.StatusInternalServerError}, "Internal server error."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := newFakeServer(t, tt.polls...)
			a, out, errOut := testApp(t)
			err := run(t.Context(), a, []string{"token", "create", "--server", srv.URL})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if out.Len() != 0 {
				t.Fatalf("stdout %q after a failure", out)
			}
			assertNoToken(t, err.Error(), errOut.String())
		})
	}
}

func TestTokenCreateCancelled(t *testing.T) {
	srv := newFakeServer(t, http.StatusAccepted)
	a, out, _ := testApp(t)
	ctx, cancel := contextWithCancel(t)
	cancel()
	if err := run(ctx, a, []string{"token", "create", "--server", srv.URL}); err == nil {
		t.Fatal("cancelled token create succeeded")
	}
	if out.Len() != 0 {
		t.Fatalf("stdout %q", out)
	}
}

func TestTokenUsage(t *testing.T) {
	for _, args := range [][]string{
		{"token"},
		{"token", "list"},
		{"token", "revoke"},
		{"token", "create"},
		{"token", "create", "extra", "--server", "https://wardlock.example.test"},
		{"token", "create", "--server", "ftp://wardlock.example.test"},
		{"token", "create", "--server", "https://wardlock.example.test", "--name", strings.Repeat("n", 101)},
	} {
		a, _, _ := testApp(t)
		if err := run(t.Context(), a, args); !errors.Is(err, errUsage) {
			t.Errorf("%q = %v, want errUsage", args, err)
		}
	}
}

func TestTokenHelp(t *testing.T) {
	a, out, _ := testApp(t)
	if err := run(t.Context(), a, []string{"token", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "wardlock token create") {
		t.Fatalf("help %q", out)
	}
	if !strings.Contains(usage(), "token ") {
		t.Fatal("wardlock help does not list token")
	}
}
