// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"unicode/utf8"

	"wardlock.nz/apiclient"
)

// wardlock token (I6.6): mint an API token for something else, such as a CI
// secret, through the CLI login flow. Tokens never manage tokens, so listing
// and revoking stay in the dashboard.

const tokenUsage = "Usage: wardlock token create [--name NAME] [--server URL] [--json]\n\n" +
	"  create    Create an API token and print it once\n\n" +
	"List and revoke tokens on the dashboard's API tokens page.\n"

// maxTokenName is the API's limit on a token's name.
const maxTokenName = 100

func runToken(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, tokenUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "create":
		return tokenCreate(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, tokenUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown token command %q\n\n%s", sub, tokenUsage)
	return errUsage
}

// tokenCreate prints the new token on stdout and everything else on stderr,
// so stdout can be piped into a secret store. The token is never stored and
// the CLI's own login is left as it was.
func tokenCreate(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "token create", "Usage: wardlock token create [--name NAME] [--server URL] [--json]\n\n"+
		"Creates an API token and prints it once on standard output, for a CI\n"+
		"secret or another tool, for example:\n\n"+
		"  wardlock token create --name \"GitHub deploys\" | gh secret set WARDLOCK_TOKEN\n\n"+
		"You choose its scopes on the page this opens; choose only what it needs.\n"+
		"The token never expires and is not stored here, and your own login does\n"+
		"not change. Revoke it on the dashboard's API tokens page.\n")
	name := fs.String("name", "", "the token's name (default: wardlock <version> on <host>)")
	serverFlag := fs.String("server", "", "the Wardlock server (default $WARDLOCK_SERVER, then the server logged in to)")
	asJSON := fs.Bool("json", false, "print the new token as JSON")
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		fs.Usage()
		return errUsage
	}
	clientName := strings.TrimSpace(*name)
	if utf8.RuneCountInString(clientName) > maxTokenName {
		return usageErrorf("--name must be at most %d characters", maxTokenName)
	}
	if clientName == "" {
		clientName = defaultClientName(a)
	}
	server, err := loginServer(a, "token create", *serverFlag)
	if err != nil {
		return err
	}
	warnPlainHTTP(a.stderr, server)

	c, err := apiclient.NewClientWithResponses(server, apiclient.WithHTTPClient(a.http))
	if err != nil {
		return fmt.Errorf("server %s: %w", server, err)
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
	fmt.Fprintf(a.stderr, "To create the token, open this page and check it shows the code %s:\n\n  %s\n\n", l.UserCode, open)
	// Stderr, not stdout as for login: stdout is usually a pipe here.
	if a.liveProgress {
		if err := a.openBrowser(open); err == nil {
			fmt.Fprintln(a.stderr, "Opened it in your browser.")
		}
	}
	fmt.Fprintln(a.stderr, "Waiting for approval (Ctrl+C to cancel)...")

	created, err := a.pollLogin(ctx, c, server, l, "wardlock token create")
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, created)
	}
	scopes := make([]string, len(created.ApiToken.Scopes))
	for i, s := range created.ApiToken.Scopes {
		scopes[i] = string(s)
	}
	fmt.Fprintf(a.stderr, "Created token %q (%s) with scopes %s. It is shown only this once.\n", created.ApiToken.Name, created.ApiToken.PublicId, strings.Join(scopes, ", "))
	_, err = fmt.Fprintln(a.stdout, created.Token)
	return err
}
