// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"wardlock.nz/apiclient"
)

func runVersion(ctx context.Context, a *app, args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	server := fs.String("server", a.getenv("WARDLOCK_SERVER"), "also report this server's version (default $WARDLOCK_SERVER), e.g. https://wardlock.example.com")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	fmt.Fprintln(a.stdout, "wardlock", version)
	if *server == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := apiclient.NewClientWithResponses(*server, apiclient.WithHTTPClient(a.http))
	if err != nil {
		return fmt.Errorf("server %s: %w", *server, err)
	}
	res, err := c.GetHealthWithResponse(ctx)
	if err != nil {
		return fmt.Errorf("server %s: %w", *server, err)
	}
	if res.JSON200 == nil {
		return fmt.Errorf("server %s: unexpected response %s", *server, res.Status())
	}
	fmt.Fprintln(a.stdout, "wardlockd", res.JSON200.Version, "at", *server)
	return nil
}
