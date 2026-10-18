// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wardlock.nz/apiclient"
)

// newFlagSet returns a flag set whose --help prints synopsis, then the
// flags.
func newFlagSet(a *app, name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(a.stderr, synopsis+"\n")
		fs.PrintDefaults()
	}
	return fs
}

// globalFlags are the flags every site command takes (docs/SPEC.md "CLI").
type globalFlags struct {
	server string
	json   bool
}

func addGlobalFlags(fs *flag.FlagSet) *globalFlags {
	g := &globalFlags{}
	fs.StringVar(&g.server, "server", "", "the Wardlock server (default from wardlock.json, then $WARDLOCK_SERVER, then the server logged in to)")
	fs.BoolVar(&g.json, "json", false, "print the result as JSON and never prompt")
	return g
}

// parseArgs parses flags placed anywhere among args, so "deploy dist
// --message M" works, and returns the other arguments. "--" ends the flags.
// For --help it returns flag.ErrHelp; for a bad flag errUsage, after the
// flag package has explained it.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, errUsage
		}
		rest := fs.Args()
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(pos, rest...), nil
		}
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// conn is an authenticated connection to the server.
type conn struct {
	server string
	api    *apiclient.ClientWithResponses
	auth   apiclient.RequestEditorFn
}

// connect picks the server and token (resolveCredential) and returns a
// client. serverFlag is --server; configServer comes from wardlock.json.
func (a *app) connect(serverFlag, configServer string) (*conn, error) {
	s := serverFlag
	if s == "" {
		s = configServer
	}
	if s != "" {
		checked, err := checkServer(s)
		if err != nil {
			return nil, usageErrorf("%v", err)
		}
		s = checked
	}
	cred, err := a.resolveCredential(s)
	if err != nil {
		return nil, err
	}
	server, err := checkServer(cred.server)
	if err != nil {
		return nil, usageErrorf("%v", err)
	}
	warnPlainHTTP(a.stderr, server)
	c, err := apiclient.NewClientWithResponses(server, apiclient.WithHTTPClient(a.http))
	if err != nil {
		return nil, fmt.Errorf("server %s: %w", server, err)
	}
	return &conn{server: server, api: c, auth: bearer(cred.token)}, nil
}

// unreachable describes a request that got no HTTP answer.
func (c *conn) unreachable(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return errors.New("cancelled")
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("could not reach %s: %w", c.server, err)
}

// failed describes an unexpected answer, adding what to do about the
// common ones.
func (c *conn) failed(res *http.Response, body []byte) error {
	err := apiError(c.server, res, body)
	if res == nil {
		return err
	}
	switch res.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: run wardlock login again, or check WARDLOCK_TOKEN", err)
	case http.StatusForbidden:
		return fmt.Errorf("%w: the token may lack the scope this needs (deploy, or manage to tear down); log in again and approve it", err)
	}
	return err
}

var siteNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// target is a DOMAIN argument: a site name (docs) or a hostname
// (docs.<base domain> or a custom domain).
type target struct {
	name, hostname string
}

func (t target) String() string {
	if t.name != "" {
		return t.name
	}
	return t.hostname
}

func parseTarget(raw string) (target, error) {
	s := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if !strings.Contains(s, ".") {
		if !siteNamePattern.MatchString(s) {
			return target{}, usageErrorf("%q is not a site name: use 1-63 letters, digits and hyphens, not starting or ending with a hyphen", raw)
		}
		return target{name: s}, nil
	}
	if strings.ContainsAny(s, "/:@ ") {
		return target{}, usageErrorf("%q is not a site name or hostname, such as docs or docs.example.com", raw)
	}
	return target{hostname: s}, nil
}

var errNoSite = errors.New("no such site")

// findSite returns the owner's site named or served at t, or errNoSite.
// Another owner's site is not found either.
func (c *conn) findSite(ctx context.Context, t target) (apiclient.Site, error) {
	if t.name != "" {
		res, err := c.api.GetSiteWithResponse(ctx, t.name, c.auth)
		switch {
		case err != nil:
			return apiclient.Site{}, c.unreachable(ctx, err)
		case res.JSON200 != nil:
			return *res.JSON200, nil
		case res.StatusCode() == http.StatusNotFound:
			return apiclient.Site{}, errNoSite
		}
		return apiclient.Site{}, c.failed(res.HTTPResponse, res.Body)
	}
	host := t.hostname
	res, err := c.api.ListSitesWithResponse(ctx, &apiclient.ListSitesParams{Hostname: &host}, c.auth)
	switch {
	case err != nil:
		return apiclient.Site{}, c.unreachable(ctx, err)
	case res.JSON200 == nil:
		return apiclient.Site{}, c.failed(res.HTTPResponse, res.Body)
	case len(res.JSON200.Items) == 0:
		return apiclient.Site{}, errNoSite
	}
	return res.JSON200.Items[0], nil
}

// mustFindSite is findSite with errNoSite explained.
func (c *conn) mustFindSite(ctx context.Context, t target) (apiclient.Site, error) {
	site, err := c.findSite(ctx, t)
	if errors.Is(err, errNoSite) {
		return site, fmt.Errorf("no site %s on %s: wardlock list shows yours", t, c.server)
	}
	return site, err
}

// forEach calls fn for 0…n-1 on up to workers goroutines. After the first
// error no new calls start and that error is returned.
func forEach(ctx context.Context, n, workers int, fn func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		next  atomic.Int64
		once  sync.Once
		first error
	)
	for range min(workers, n) {
		wg.Go(func() {
			for {
				i := int(next.Add(1) - 1)
				if i >= n || ctx.Err() != nil {
					return
				}
				if err := fn(ctx, i); err != nil {
					once.Do(func() { first = err })
					cancel()
					return
				}
			}
		})
	}
	wg.Wait()
	return first
}

// formatBytes formats n with binary units, as the deploy limits are given.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n)/unit, 0
	for v >= unit && i < 3 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, []string{"KiB", "MiB", "GiB", "TiB"}[i])
}

func formatTime(ms int64) string {
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}

// shortID is the last eight characters of an ID, as the dashboard shows.
func shortID(id string) string {
	return id[max(len(id)-8, 0):]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// confirm asks on stderr for answer to be typed and reports whether it was.
func (a *app) confirm(question, answer string) (bool, error) {
	fmt.Fprint(a.stderr, question)
	line, err := readLine(a.stdin)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.TrimSpace(line) == answer, nil
}

// readLine reads up to a newline one byte at a time, so nothing after it
// is consumed from r.
func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			return b.String(), err
		}
	}
}
