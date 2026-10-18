// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Synthetic credentials: shaped like real ones, valid nowhere.
const (
	testToken      = "wl_k7m2q6xw4a_TESTonlyTESTonlyTESTonlyTESTonlyTESTonlyTESTonl"
	testDeviceCode = "dc_TESTonlyTESTonlyTESTonlyTESTonlyTESTonlyTES"
)

type fakeKeyring struct {
	mu      sync.Mutex
	secrets map[string]string
	broken  bool // like a Linux host without a Secret Service
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{secrets: map[string]string{}} }

func (k *fakeKeyring) Get(service, user string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.broken {
		return "", errors.New("no keychain")
	}
	s, ok := k.secrets[service+"|"+user]
	if !ok {
		return "", errSecretNotFound
	}
	return s, nil
}

func (k *fakeKeyring) Set(service, user, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.broken {
		return errors.New("no keychain")
	}
	k.secrets[service+"|"+user] = secret
	return nil
}

func (k *fakeKeyring) Delete(service, user string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.broken {
		return errors.New("no keychain")
	}
	if _, ok := k.secrets[service+"|"+user]; !ok {
		return errSecretNotFound
	}
	delete(k.secrets, service+"|"+user)
	return nil
}

// fakeServer answers the CLI login operations. polls lists the poll answers
// in order (status codes; 429 is slow_down); the last one repeats.
type fakeServer struct {
	*httptest.Server
	mu        sync.Mutex
	polls     []int
	pollCount int
	signOuts  []string // Authorization headers seen by sign-out
	signOut   int      // status sign-out answers; default 204
	start     map[string]any
}

func newFakeServer(t *testing.T, polls ...int) *fakeServer {
	t.Helper()
	f := &fakeServer{polls: polls, signOut: http.StatusNoContent}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}
	mux.HandleFunc("POST /api/v0/cli-login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.start = body
		f.mu.Unlock()
		reply(w, http.StatusCreated, map[string]any{
			"deviceCode": testDeviceCode, "userCode": "BDFH-KMPR",
			"verificationUrl": f.URL + "/cli-login", "verificationUrlComplete": f.URL + "/cli-login?code=BDFH-KMPR",
			"expiresAt": 1_800_000_900_000, "interval": 5,
		})
	})
	mux.HandleFunc("POST /api/v0/cli-login/poll", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ DeviceCode string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.DeviceCode != testDeviceCode {
			t.Errorf("polled with %q", body.DeviceCode)
		}
		f.mu.Lock()
		status := f.polls[min(f.pollCount, len(f.polls)-1)]
		f.pollCount++
		f.mu.Unlock()
		switch status {
		case http.StatusOK:
			reply(w, status, map[string]any{"token": testToken, "apiToken": map[string]any{
				"id": "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e20", "publicId": "k7m2q6xw4a", "name": "wardlock dev on build-host",
				"scopes": []string{"deploy", "manage"}, "createdAt": 1_800_000_000_000,
			}})
		case http.StatusAccepted:
			reply(w, status, map[string]any{"status": "pending"})
		case http.StatusGone:
			reply(w, status, map[string]any{"code": "login_expired", "message": "gone"})
		case http.StatusTooManyRequests:
			w.Header().Set("Retry-After", "5")
			reply(w, status, map[string]any{"code": "slow_down", "message": "slow down"})
		default:
			reply(w, status, map[string]any{"code": "internal", "message": "Internal server error."})
		}
	})
	mux.HandleFunc("GET /api/v0/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			reply(w, http.StatusUnauthorized, map[string]any{"code": "unauthenticated", "message": "no"})
			return
		}
		reply(w, http.StatusOK, map[string]any{
			"owner":      map[string]any{"id": "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00", "email": "owner@example.test", "createdAt": 1},
			"credential": "apiToken", "scopes": []string{"deploy", "manage"},
		})
	})
	mux.HandleFunc("POST /api/v0/auth/sign-out", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.signOuts = append(f.signOuts, r.Header.Get("Authorization"))
		status := f.signOut
		f.mu.Unlock()
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		reply(w, status, map[string]any{"code": "unauthenticated", "message": "no"})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func credentialsPath(t *testing.T, a *app) string {
	t.Helper()
	dir, err := a.creds.dir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, credentialsFile)
}

func readCredentials(t *testing.T, a *app) storedCredentials {
	t.Helper()
	b, err := os.ReadFile(credentialsPath(t, a))
	if err != nil {
		t.Fatal(err)
	}
	var rec storedCredentials
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func assertPrivate(t *testing.T, a *app) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // Windows has no POSIX modes; the user profile is private.
	}
	fi, err := os.Stat(credentialsPath(t, a))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.json mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(credentialsPath(t, a)))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("config directory mode %v, want 0700", di.Mode().Perm())
	}
}

func assertNoToken(t *testing.T, outputs ...string) {
	t.Helper()
	for _, o := range outputs {
		if strings.Contains(o, testToken) || strings.Contains(o, testDeviceCode) || strings.Contains(o, testToken[14:]) {
			t.Fatalf("output contains a secret:\n%s", o)
		}
	}
}

func TestLoginStoresTokenInKeychain(t *testing.T) {
	srv := newFakeServer(t, http.StatusAccepted, http.StatusTooManyRequests, http.StatusAccepted, http.StatusOK)
	a, out, errOut := testApp(t)
	var opened []string
	a.interactive = true
	a.openBrowser = func(u string) error { opened = append(opened, u); return nil }
	start := a.now()
	if err := run(t.Context(), a, []string{"login", "--server", srv.URL + "/"}); err != nil {
		t.Fatalf("login: %v\n%s", err, errOut)
	}
	if len(opened) != 1 || opened[0] != srv.URL+"/cli-login?code=BDFH-KMPR" {
		t.Fatalf("opened %q", opened)
	}
	if srv.start["clientName"] != "wardlock dev on build-host" {
		t.Fatalf("clientName %v", srv.start["clientName"])
	}
	// Four polls at 5s, with one slow_down adding 5s to the later ones.
	if waited := a.now().Sub(start); waited.Seconds() != 5+5+10+10 {
		t.Fatalf("waited %v between polls", waited)
	}
	for _, want := range []string{"BDFH-KMPR", "Logged in to " + srv.URL + " as owner@example.test", "deploy, manage", "k7m2q6xw4a", "system keychain"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	assertNoToken(t, out.String(), errOut.String())
	server, tok, err := a.creds.load()
	if err != nil || server != srv.URL || tok != testToken {
		t.Fatalf("stored %q %q %v", server, tok, err)
	}
	if rec := readCredentials(t, a); rec.Token != "" || rec.Server != srv.URL {
		t.Fatalf("credentials.json %+v holds the token although the keychain works", rec)
	}
	assertPrivate(t, a)
}

func TestLoginFallsBackToPrivateFile(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, out, errOut := testApp(t)
	a.creds.keyring.(*fakeKeyring).broken = true
	if err := run(t.Context(), a, []string{"login", "--server", srv.URL}); err != nil {
		t.Fatalf("login: %v\n%s", err, errOut)
	}
	if rec := readCredentials(t, a); rec.Token != testToken || rec.Server != srv.URL {
		t.Fatalf("credentials.json %+v", rec)
	}
	assertPrivate(t, a)
	if !strings.Contains(out.String(), credentialsPath(t, a)) {
		t.Fatalf("output does not say where the token is:\n%s", out)
	}
	assertNoToken(t, out.String(), errOut.String())
	// An existing, too-open file is replaced by a private one.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(credentialsPath(t, a), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := run(t.Context(), a, []string{"login", "--server", srv.URL}); err != nil {
			t.Fatal(err)
		}
		assertPrivate(t, a)
	}
}

func TestLoginJSON(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, out, errOut := testApp(t)
	a.interactive = true
	a.openBrowser = func(string) error { t.Error("opened a browser with --json"); return nil }
	if err := run(t.Context(), a, []string{"login", "--json", "--server", srv.URL}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON value: %v\n%s", err, out)
	}
	if got["server"] != srv.URL || got["email"] != "owner@example.test" || got["publicId"] != "k7m2q6xw4a" || got["storage"] != "keychain" {
		t.Fatalf("JSON %v", got)
	}
	if !strings.Contains(errOut.String(), "BDFH-KMPR") {
		t.Fatalf("instructions missing from stderr: %s", errOut)
	}
	assertNoToken(t, out.String(), errOut.String())
}

func TestLoginUsesWardlockServer(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, _, _ := testApp(t)
	a.getenv = env(map[string]string{"WARDLOCK_SERVER": srv.URL})
	if err := run(t.Context(), a, []string{"login"}); err != nil {
		t.Fatal(err)
	}
	// Next time the saved server is the default.
	a.getenv = noEnv
	if err := run(t.Context(), a, []string{"login"}); err != nil {
		t.Fatal(err)
	}
}

func TestLoginFailures(t *testing.T) {
	tests := map[string]struct {
		polls []int
		want  string
	}{
		"denied":       {[]int{http.StatusAccepted, http.StatusGone}, "denied or has expired"},
		"server error": {[]int{http.StatusInternalServerError}, "Internal server error."},
		"expires":      {[]int{http.StatusAccepted}, "expired before it was approved"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := newFakeServer(t, tt.polls...)
			a, _, _ := testApp(t)
			err := run(t.Context(), a, []string{"login", "--server", srv.URL})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if _, err := os.Stat(credentialsPath(t, a)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("credentials stored after a failed login")
			}
		})
	}
}

func TestLoginUsage(t *testing.T) {
	for _, args := range [][]string{
		{"login"},
		{"login", "--server", "ftp://wardlock.example.test"},
		{"login", "--server", "https://wardlock.example.test/api"},
		{"login", "extra"},
	} {
		a, _, _ := testApp(t)
		if err := run(t.Context(), a, args); !errors.Is(err, errUsage) {
			t.Errorf("%q = %v, want errUsage", args, err)
		}
	}
}

func TestLoginCancelled(t *testing.T) {
	srv := newFakeServer(t, http.StatusAccepted)
	a, _, _ := testApp(t)
	ctx, cancel := contextWithCancel(t)
	cancel()
	if err := run(ctx, a, []string{"login", "--server", srv.URL}); err == nil {
		t.Fatal("cancelled login succeeded")
	}
}

func TestLogout(t *testing.T) {
	for _, revokeStatus := range []int{http.StatusNoContent, http.StatusUnauthorized} {
		srv := newFakeServer(t, http.StatusOK)
		srv.signOut = revokeStatus
		a, out, errOut := testApp(t)
		if err := run(t.Context(), a, []string{"login", "--server", srv.URL}); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := run(t.Context(), a, []string{"logout"}); err != nil {
			t.Fatalf("logout (server answers %d): %v", revokeStatus, err)
		}
		if len(srv.signOuts) != 1 || srv.signOuts[0] != "Bearer "+testToken {
			t.Fatalf("sign-out calls %d", len(srv.signOuts))
		}
		if _, _, err := a.creds.load(); !errors.Is(err, errNotLoggedIn) {
			t.Fatalf("after logout load = %v", err)
		}
		if len(a.creds.keyring.(*fakeKeyring).secrets) != 0 {
			t.Fatal("keychain entry left behind")
		}
		if !strings.Contains(out.String(), "Logged out of "+srv.URL) {
			t.Fatalf("output %q", out)
		}
		assertNoToken(t, out.String(), errOut.String())
	}
}

func TestLogoutFileFallbackAndNotLoggedIn(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, out, _ := testApp(t)
	a.creds.keyring.(*fakeKeyring).broken = true
	if err := run(t.Context(), a, []string{"login", "--server", srv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), a, []string{"logout"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credentialsPath(t, a)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("credentials.json left behind")
	}
	out.Reset()
	if err := run(t.Context(), a, []string{"logout"}); err != nil || !strings.Contains(out.String(), "Not logged in") {
		t.Fatalf("second logout = %v, %q", err, out)
	}
}

func TestLogoutForgetsEvenWhenTheServerIsDown(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, _, _ := testApp(t)
	if err := run(t.Context(), a, []string{"login", "--server", srv.URL}); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	err := run(t.Context(), a, []string{"logout"})
	if err == nil || !strings.Contains(err.Error(), "could not revoke") || strings.Contains(err.Error(), testToken) {
		t.Fatalf("logout = %v", err)
	}
	if _, _, err := a.creds.load(); !errors.Is(err, errNotLoggedIn) {
		t.Fatal("token kept after logout")
	}
}

func TestResolveCredential(t *testing.T) {
	a, _, _ := testApp(t)
	if _, err := a.resolveCredential(""); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("nothing stored = %v", err)
	}
	if _, err := a.creds.save("https://stored.example.test", testToken); err != nil {
		t.Fatal(err)
	}
	c, err := a.resolveCredential("")
	if err != nil || c != (credential{server: "https://stored.example.test", token: testToken}) {
		t.Fatalf("stored = %+v, %v", c, err)
	}
	if _, err := a.resolveCredential("https://other.example.test"); err == nil {
		t.Fatal("used the stored token for another server")
	}
	// WARDLOCK_TOKEN overrides the stored token, for the stored server or
	// the one given.
	a.getenv = env(map[string]string{"WARDLOCK_TOKEN": "wl_ci"})
	c, err = a.resolveCredential("")
	if err != nil || c != (credential{server: "https://stored.example.test", token: "wl_ci", fromEnv: true}) {
		t.Fatalf("env = %+v, %v", c, err)
	}
	a.getenv = env(map[string]string{"WARDLOCK_TOKEN": "wl_ci", "WARDLOCK_SERVER": "https://ci.example.test"})
	c, err = a.resolveCredential("")
	if err != nil || c.server != "https://ci.example.test" || c.token != "wl_ci" {
		t.Fatalf("env with server = %+v, %v", c, err)
	}
	// With nothing stored, WARDLOCK_TOKEN needs a server.
	b, _, _ := testApp(t)
	b.getenv = env(map[string]string{"WARDLOCK_TOKEN": "wl_ci"})
	if _, err := b.resolveCredential(""); !errors.Is(err, errUsage) {
		t.Fatalf("token without server = %v", err)
	}
}

func TestLoginToAnotherServerForgetsTheOldToken(t *testing.T) {
	a, _, _ := testApp(t)
	if _, err := a.creds.save("https://one.example.test", "wl_one"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.creds.save("https://two.example.test", "wl_two"); err != nil {
		t.Fatal(err)
	}
	k := a.creds.keyring.(*fakeKeyring)
	if len(k.secrets) != 1 || k.secrets[keyringService+"|https://two.example.test"] != "wl_two" {
		t.Fatalf("keychain %v", k.secrets)
	}
}

func TestConfigDir(t *testing.T) {
	got, err := configDir(env(map[string]string{"XDG_CONFIG_HOME": filepath.FromSlash("/xdg")}))
	if err != nil || got != filepath.Join(filepath.FromSlash("/xdg"), "wardlock") {
		t.Fatalf("XDG = %q, %v", got, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, _ := configDir(env(map[string]string{"XDG_CONFIG_HOME": "relative"})); got != filepath.Join(home, ".config", "wardlock") {
		t.Fatalf("default = %q", got)
	}
}

func TestWarnPlainHTTP(t *testing.T) {
	tests := map[string]bool{
		"http://wardlock.example.test":   true,
		"http://192.0.2.10:8080":         true,
		"http://[2001:db8::1]":           true,
		"https://wardlock.example.test":  false,
		"http://localhost:8080":          false,
		"http://wardlock.localhost:8080": false,
		"http://127.0.0.1:8080":          false,
		"http://127.1.2.3":               false,
		"http://[::1]:8080":              false,
		"http://WARDLOCK.LOCALHOST":      false,
	}
	for server, warn := range tests {
		var b strings.Builder
		warnPlainHTTP(&b, server)
		if got := strings.Contains(b.String(), "Warning:"); got != warn {
			t.Errorf("warnPlainHTTP(%q) warned %v, want %v", server, got, warn)
		}
	}
}

func TestLoginWarnsAboutPlainHTTP(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK)
	a, _, errOut := testApp(t)
	// The fake server is on 127.0.0.1: no warning.
	if err := run(t.Context(), a, []string{"login", "--server", srv.URL}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut.String(), "Warning:") {
		t.Fatalf("warned for a loopback server: %s", errOut)
	}
	// The same server reached by a non-loopback name over http: warned on
	// stderr, and the login still works.
	a.http = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}}
	if err := run(t.Context(), a, []string{"login", "--server", "http://wardlock.example.test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "Warning: http://wardlock.example.test uses plain http") {
		t.Fatalf("no warning on stderr: %s", errOut)
	}
}

func TestExistingConfigDirIsMadePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX modes")
	}
	a, _, _ := testApp(t)
	dir := filepath.Dir(credentialsPath(t, a))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a.creds.keyring.(*fakeKeyring).broken = true
	if _, err := a.creds.save("https://wardlock.example.test", testToken); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, a)
}
