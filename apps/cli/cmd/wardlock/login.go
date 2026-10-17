// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"wardlock.nz/apiclient"
)

// slowDownStep is how much longer the CLI waits between polls each time the
// server answers slow_down (as RFC 8628 does).
const slowDownStep = 5 * time.Second

func runLogin(ctx context.Context, a *app, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(a.stderr, "Usage: wardlock login [--server URL] [--json]\n\n"+
			"Opens the server's dashboard in your browser, where you approve this\n"+
			"computer, then stores the new API token in the system keychain (or a\n"+
			"0600 file where there is none).\n\n")
		fs.PrintDefaults()
	}
	serverFlag := fs.String("server", "", "the Wardlock server, e.g. https://wardlock.example.com (default $WARDLOCK_SERVER, then the last server logged in to)")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}
	server := *serverFlag
	if server == "" {
		server = a.getenv("WARDLOCK_SERVER")
	}
	if server == "" {
		if rec, err := a.creds.read(); err == nil {
			server = rec.Server
		}
	}
	if server == "" {
		fmt.Fprintln(a.stderr, "wardlock login: which server? Pass --server URL or set WARDLOCK_SERVER.")
		return errUsage
	}
	server, err := checkServer(server)
	if err != nil {
		fmt.Fprintln(a.stderr, "wardlock login:", err)
		return errUsage
	}
	// Instructions go to stderr with --json, so stdout stays one JSON value.
	info := a.stdout
	if *asJSON {
		info = a.stderr
	}

	c, err := apiclient.NewClientWithResponses(server, apiclient.WithHTTPClient(a.http))
	if err != nil {
		return fmt.Errorf("server %s: %w", server, err)
	}
	host, _ := a.hostname()
	clientName := "wardlock " + version
	if host != "" {
		clientName += " on " + host
	}
	start, err := c.StartCliLoginWithResponse(ctx, apiclient.StartCliLoginJSONRequestBody{ClientName: &clientName})
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", server, err)
	}
	if start.JSON201 == nil {
		return apiError(server, start.HTTPResponse, start.Body)
	}
	l := start.JSON201
	open := l.VerificationUrlComplete
	if !isWebURL(open) {
		return fmt.Errorf("server %s sent an unusable login page address", server)
	}
	fmt.Fprintf(info, "To log in, open this page and check it shows the code %s:\n\n  %s\n\n", l.UserCode, open)
	if a.interactive && !*asJSON {
		if err := a.openBrowser(open); err == nil {
			fmt.Fprintln(info, "Opened it in your browser.")
		}
	}
	fmt.Fprintln(info, "Waiting for approval (Ctrl+C to cancel)...")

	created, err := a.pollLogin(ctx, c, server, l)
	if err != nil {
		return err
	}
	where, err := a.creds.save(server, created.Token)
	if err != nil {
		return fmt.Errorf("logged in, but could not store the token: %w", err)
	}

	email := ""
	me, err := c.GetMeWithResponse(ctx, bearer(created.Token))
	if err == nil && me.JSON200 != nil {
		email = string(me.JSON200.Owner.Email)
	}
	if *asJSON {
		return writeJSON(a.stdout, map[string]any{
			"server":   server,
			"email":    email,
			"tokenId":  created.ApiToken.Id,
			"publicId": created.ApiToken.PublicId,
			"name":     created.ApiToken.Name,
			"scopes":   created.ApiToken.Scopes,
			"storage":  storageKind(where),
		})
	}
	who := ""
	if email != "" {
		who = " as " + email
	}
	scopes := make([]string, len(created.ApiToken.Scopes))
	for i, s := range created.ApiToken.Scopes {
		scopes[i] = string(s)
	}
	fmt.Fprintf(a.stdout, "Logged in to %s%s with scopes %s (token %s).\n", server, who, strings.Join(scopes, ", "), created.ApiToken.PublicId)
	if where == keychainStorage {
		fmt.Fprintln(a.stdout, "The token is stored in the system keychain.")
	} else {
		fmt.Fprintf(a.stdout, "No system keychain is available, so the token is stored in %s (readable only by you).\n", where)
	}
	if a.getenv("WARDLOCK_TOKEN") != "" {
		fmt.Fprintln(a.stdout, "Note: WARDLOCK_TOKEN is set, and commands use it instead of this login until you unset it.")
	}
	return nil
}

// pollLogin waits until the login is approved, then returns the token.
func (a *app) pollLogin(ctx context.Context, c *apiclient.ClientWithResponses, server string, l *apiclient.CliLogin) (*apiclient.CreatedApiToken, error) {
	interval := time.Duration(max(l.Interval, 1)) * time.Second
	expires := time.UnixMilli(l.ExpiresAt)
	for {
		if err := a.sleep(ctx, interval); err != nil {
			return nil, errors.New("login cancelled")
		}
		if !a.now().Before(expires) {
			return nil, errors.New("the login expired before it was approved: run wardlock login again")
		}
		res, err := c.PollCliLoginWithResponse(ctx, apiclient.PollCliLoginJSONRequestBody{DeviceCode: l.DeviceCode})
		if err != nil {
			if ctx.Err() != nil {
				return nil, errors.New("login cancelled")
			}
			return nil, fmt.Errorf("could not reach %s: %w", server, err)
		}
		switch {
		case res.JSON200 != nil:
			return res.JSON200, nil
		case res.JSON202 != nil:
		case res.JSON410 != nil:
			return nil, errors.New("the login was denied or has expired: run wardlock login again")
		case res.JSON429 != nil && res.JSON429.Code == "slow_down":
			interval += slowDownStep
		case res.JSON429 != nil:
			wait, _ := strconv.Atoi(res.HTTPResponse.Header.Get("Retry-After"))
			if err := a.sleep(ctx, time.Duration(max(wait, 1))*time.Second); err != nil {
				return nil, errors.New("login cancelled")
			}
		default:
			return nil, apiError(server, res.HTTPResponse, res.Body)
		}
	}
}

func runLogout(ctx context.Context, a *app, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(a.stderr, "Usage: wardlock logout [--json]\n\n"+
			"Revokes the token stored by wardlock login on its server and deletes it\n"+
			"from this computer. WARDLOCK_TOKEN is not affected.\n\n")
		fs.PrintDefaults()
	}
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}
	rec, err := a.creds.read()
	if errors.Is(err, errNotLoggedIn) {
		if *asJSON {
			return writeJSON(a.stdout, map[string]any{"loggedOut": false})
		}
		fmt.Fprintln(a.stdout, "Not logged in.")
		return nil
	}
	if err != nil {
		return err
	}
	server := rec.Server
	_, token, loadErr := a.creds.load()

	// Revoke first; forget locally whatever happens, so a dead server
	// cannot keep a token on this computer.
	var revokeErr error
	if loadErr != nil {
		revokeErr = loadErr
	} else {
		revokeErr = revoke(ctx, a, server, token)
	}
	if err := a.creds.forget(rec); err != nil {
		return err
	}
	if revokeErr != nil {
		return fmt.Errorf("removed the token from this computer, but could not revoke it on %s (%w): revoke it in the dashboard", server, revokeErr)
	}
	if *asJSON {
		return writeJSON(a.stdout, map[string]any{"loggedOut": true, "server": server})
	}
	fmt.Fprintf(a.stdout, "Logged out of %s. The token is revoked and removed from this computer.\n", server)
	return nil
}

// revoke revokes token on server; a token the server no longer accepts is
// already as good as revoked.
func revoke(ctx context.Context, a *app, server, token string) error {
	c, err := apiclient.NewClientWithResponses(server, apiclient.WithHTTPClient(a.http))
	if err != nil {
		return err
	}
	res, err := c.SignOutWithResponse(ctx, bearer(token))
	if err != nil {
		return err
	}
	switch res.StatusCode() {
	case http.StatusNoContent, http.StatusUnauthorized:
		return nil
	}
	return apiError(server, res.HTTPResponse, res.Body)
}

func bearer(token string) apiclient.RequestEditorFn {
	return func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// checkServer accepts an http or https URL with no path beyond "/", and
// returns it without the trailing slash.
func checkServer(raw string) (string, error) {
	s := normaliseServer(raw)
	u, err := url.Parse(s)
	if err != nil || !isWebURL(s) || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%q is not a server address like https://wardlock.example.com", raw)
	}
	return s, nil
}

func normaliseServer(raw string) string { return strings.TrimRight(strings.TrimSpace(raw), "/") }

func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// apiError describes an unexpected answer using the API's error body.
func apiError(server string, res *http.Response, body []byte) error {
	var e apiclient.Error
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return fmt.Errorf("server %s: %s", server, e.Message)
	}
	status := "no response"
	if res != nil {
		status = res.Status
	}
	return fmt.Errorf("server %s: unexpected response %s", server, status)
}

func storageKind(s storage) string {
	if s == keychainStorage {
		return "keychain"
	}
	return "file"
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// openBrowser opens an http or https address in the default browser
// without waiting for it. The address is re-encoded from its parsed form,
// so it cannot be mistaken for an option by the opener.
func openBrowser(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !isWebURL(raw) {
		return errors.New("not a web address")
	}
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open"}
	case "windows":
		args = []string{"rundll32", "url.dll,FileProtocolHandler"}
	default:
		args = []string{"xdg-open"}
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		return err
	}
	cmd := &exec.Cmd{Path: path, Args: append(args, u.String())}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
