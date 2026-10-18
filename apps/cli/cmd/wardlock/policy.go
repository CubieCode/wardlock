// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"unicode/utf8"

	"wardlock.nz/apiclient"
)

const policyUsage = "Usage: wardlock policy set DOMAIN [--path P] (--public | --protected [--links] [--password] [--email] [--oauth] [--allow LIST] [--github-org LIST])\n\n" +
	"Sets the site default policy, or with --path the policy for a directory\n" +
	"prefix such as /docs/. The longest matching prefix decides each request.\n" +
	"--public lets anyone in; --protected needs one of the methods given:\n" +
	"--links for share links (the default when none is given), --password\n" +
	"for a password visitors enter on the site's login page, --email for a\n" +
	"code emailed to an address the allowlist lets in, --oauth for signing\n" +
	"in with a GitHub, Google or OIDC account the allowlist lets in (the\n" +
	"server's operator configures the providers). The password is\n" +
	"asked for without echo on a terminal, and otherwise read from the first\n" +
	"line of standard input; it is never a flag. --allow replaces the\n" +
	"policy's allowlist with comma-separated addresses (ana@example.com) and\n" +
	"domains (example.com for that domain only, *.example.com for its\n" +
	"subdomains); it may be repeated, and --allow '' empties it. Without\n" +
	"--allow the current allowlist is kept. --github-org replaces the GitHub\n" +
	"organisations (members signing in with GitHub are let in), likewise\n" +
	"comma-separated and repeatable; without it they are kept. A change\n" +
	"ends every visitor session of that policy. Needs a token with the\n" +
	"manage scope."

func runPolicy(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 && args[0] == "set" {
		return runPolicySet(ctx, a, args[1:])
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprintln(a.stdout, policyUsage)
		return nil
	}
	fmt.Fprintln(a.stderr, policyUsage)
	return errUsage
}

func runPolicySet(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "policy set", policyUsage)
	g := addGlobalFlags(fs)
	path := fs.String("path", "/", "the directory prefix the policy covers, such as /docs/ (default the whole site)")
	public := fs.Bool("public", false, "let anyone in")
	protected := fs.Bool("protected", false, "let in only visitors who satisfy one of the methods given")
	links := fs.Bool("links", false, "with --protected: share links let visitors in")
	password := fs.Bool("password", false, "with --protected: a password lets visitors in (asked for, or read from stdin)")
	email := fs.Bool("email", false, "with --protected: a code emailed to an allowed address lets visitors in")
	oauth := fs.Bool("oauth", false, "with --protected: signing in with an allowed GitHub, Google or OIDC account lets visitors in")
	var orgs *[]string
	fs.Func("github-org", "replace the allowed GitHub organisations with these comma-separated names (repeatable; '' empties them)", func(v string) error {
		if orgs == nil {
			orgs = &[]string{}
		}
		return addOrgs(orgs, v)
	})
	var allow *apiclient.PolicyAllowlist
	fs.Func("allow", "replace the allowlist with these comma-separated addresses and domains (repeatable; '' empties it)", func(v string) error {
		if allow == nil {
			allow = &apiclient.PolicyAllowlist{Emails: []string{}, Domains: []string{}}
		}
		return addAllowed(allow, v)
	})
	// A value given to --password is most likely the password itself:
	// refuse it before the flag package repeats it in an error.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "--password=") || strings.HasPrefix(arg, "-password=") {
			return usageErrorf("--password takes no value: the password is asked for, or read from stdin, never given as a flag")
		}
	}
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return errUsage
	}
	t, err := parseTarget(pos[0])
	if err != nil && *password {
		// The argument may be a password typed after --password: never
		// repeat it.
		return usageErrorf("DOMAIN is not a site name or hostname (the password is never an argument: it is asked for, or read from stdin)")
	}
	if err != nil {
		return err
	}
	// Errors name the site as typed, unless it may be a password.
	shown := t.String()
	if *password {
		shown = "the site given"
	}
	body := apiclient.SetPolicyRequest{Path: *path}
	var methods []apiclient.PolicyMethod
	if *links {
		methods = append(methods, apiclient.PolicyMethodLinks)
	}
	if *password {
		methods = append(methods, apiclient.PolicyMethodPassword)
	}
	if *email {
		methods = append(methods, apiclient.PolicyMethodEmail)
	}
	if *oauth {
		methods = append(methods, apiclient.PolicyMethodOauth)
	}
	switch {
	case *public == *protected:
		return usageErrorf("pass exactly one of --public or --protected")
	case *public && len(methods) > 0:
		return usageErrorf("a public policy takes no methods: drop --links, --password, --email and --oauth, or use --protected")
	case *public && (allow != nil && len(allow.Emails)+len(allow.Domains) > 0 || orgs != nil && len(*orgs) > 0):
		return usageErrorf("a public policy has no allowlist: drop --allow and --github-org, or use --protected")
	case *public:
		body.Visibility = apiclient.PolicyVisibilityPublic
	default:
		body.Visibility = apiclient.PolicyVisibilityProtected
		if len(methods) > 0 {
			body.Methods = &methods
		}
		body.Allowlist = allow
		if allow != nil {
			// Without --github-org, githubOrgs is omitted and kept.
			allow.GithubOrgs = orgs
		}
	}

	if *password {
		pw, err := a.newPassword()
		if err != nil {
			return err
		}
		body.Password = &pw
	}

	cfgServer, err := a.configServer()
	if err != nil {
		return err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return err
	}
	name := t.name
	if name == "" {
		site, err := c.findSite(ctx, t)
		if errors.Is(err, errNoSite) {
			return fmt.Errorf("no site %s on %s: wardlock list shows yours", shown, c.server)
		}
		if err != nil {
			return err
		}
		name = site.Name
	}
	if body.Allowlist == nil && orgs != nil && body.Visibility == apiclient.PolicyVisibilityProtected {
		// The API replaces addresses and domains with the allowlist, so
		// --github-org alone sends the current ones back unchanged.
		cur, err := c.policyAllowlist(ctx, name, *path)
		if err != nil {
			return err
		}
		body.Allowlist = &apiclient.PolicyAllowlist{Emails: cur.Emails, Domains: cur.Domains, GithubOrgs: orgs}
	}
	res, err := c.api.SetPolicyWithResponse(ctx, name, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON404 != nil {
		return fmt.Errorf("no site %s on %s: wardlock list shows yours", shown, c.server)
	}
	if res.JSON200 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	p := *res.JSON200
	if g.json {
		return writeJSON(a.stdout, p)
	}
	fmt.Fprintf(a.stdout, "%s %s is now %s.\n", name, p.Path, describePolicy(p))
	if allowed := append(append([]string{}, p.Allowlist.Emails...), p.Allowlist.Domains...); len(allowed) > 0 {
		fmt.Fprintf(a.stdout, "Allowed: %s\n", strings.Join(allowed, ", "))
	}
	if o := p.Allowlist.GithubOrgs; o != nil && len(*o) > 0 {
		fmt.Fprintf(a.stdout, "GitHub organisations: %s\n", strings.Join(*o, ", "))
	}
	return nil
}

// policyAllowlist returns the allowlist of site name's policy for path,
// empty when it has none yet.
func (c *conn) policyAllowlist(ctx context.Context, name, path string) (apiclient.PolicyAllowlist, error) {
	params := &apiclient.ListPoliciesParams{}
	for {
		res, err := c.api.ListPoliciesWithResponse(ctx, name, params, c.auth)
		if err != nil {
			return apiclient.PolicyAllowlist{}, c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return apiclient.PolicyAllowlist{}, c.failed(res.HTTPResponse, res.Body)
		}
		for _, p := range res.JSON200.Items {
			if p.Path == path {
				return p.Allowlist, nil
			}
		}
		if res.JSON200.NextCursor == nil {
			return apiclient.PolicyAllowlist{Emails: []string{}, Domains: []string{}}, nil
		}
		params.Cursor = res.JSON200.NextCursor
	}
}

// addOrgs adds the comma-separated GitHub organisation names of v to l,
// checked as the server would: letters, digits and single hyphens, not at
// either end, at most 39 characters.
func addOrgs(l *[]string, v string) error {
	for o := range strings.SplitSeq(v, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if len(o) > 39 || o[0] == '-' || o[len(o)-1] == '-' || strings.Contains(o, "--") ||
			strings.ContainsFunc(o, func(r rune) bool { return !ldh(r) }) {
			return fmt.Errorf("%q is not a GitHub organisation name like example-org", o)
		}
		*l = append(*l, o)
	}
	return nil
}

// addAllowed adds the comma-separated entries of v to l: those with an @
// are addresses, the rest domains. Each is checked as the server would
// (docs/SPEC.md "Email code"), so a typo fails before any request; the
// server normalises them.
func addAllowed(l *apiclient.PolicyAllowlist, v string) error {
	for entry := range strings.SplitSeq(v, ",") {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "":
		case strings.Contains(entry, "@"):
			if a, err := mail.ParseAddress(entry); err != nil || a.Name != "" || a.Address != entry || len(entry) > 254 {
				return fmt.Errorf("%q is not an email address like ana@example.com", entry)
			}
			l.Emails = append(l.Emails, entry)
		default:
			if !allowedDomain(entry) {
				return fmt.Errorf("%q is not an address or a domain like example.com or *.example.com (international names in their xn-- form)", entry)
			}
			l.Domains = append(l.Domains, entry)
		}
	}
	return nil
}

// ldh reports whether r may appear in a DNS label: an ASCII letter, digit
// or hyphen.
func ldh(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-'
}

// allowedDomain reports whether d is a DNS name of at least two ASCII
// labels, optionally after "*." and with one trailing dot, not ending in a
// numeric label, and at most 253 bytes with the wildcard.
func allowedDomain(d string) bool {
	d = strings.TrimSuffix(d, ".")
	name, _ := strings.CutPrefix(d, "*.")
	labels := strings.Split(name, ".")
	if len(d) > 253 || len(labels) < 2 || strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		if strings.ContainsFunc(l, func(r rune) bool { return !ldh(r) }) {
			return false
		}
	}
	return true
}

// describePolicy says who a policy lets in, as in "protected by share
// links".
func describePolicy(p apiclient.Policy) string {
	if p.Visibility == apiclient.PolicyVisibilityPublic {
		return "public"
	}
	var ms []string
	for _, m := range p.Methods {
		switch m {
		case apiclient.PolicyMethodLinks:
			ms = append(ms, "share links")
		case apiclient.PolicyMethodPassword:
			ms = append(ms, "a password")
		case apiclient.PolicyMethodEmail:
			ms = append(ms, "email codes")
		case apiclient.PolicyMethodOauth:
			ms = append(ms, "OAuth sign-in")
		default:
			ms = append(ms, string(m))
		}
	}
	return "protected by " + strings.Join(ms, " or ")
}

// Bounds the server enforces on a policy password (docs/SPEC.md
// "Password"), checked here too so a typo fails before any request.
const (
	minPasswordChars = 8
	maxPasswordBytes = 1024
)

// newPassword reads the policy password: twice without echo, prompting on
// stderr, whenever stdin is a terminal (with --json too, which only shapes
// stdout); else the first line of stdin. It never comes from a flag, so it
// stays out of shell history and process listings.
func (a *app) newPassword() (string, error) {
	var pw string
	if a.stdinTerminal {
		fmt.Fprint(a.stderr, "New password: ")
		first, err := a.readPassword()
		fmt.Fprintln(a.stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		fmt.Fprint(a.stderr, "Again: ")
		again, err := a.readPassword()
		fmt.Fprintln(a.stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		if first != again {
			return "", errors.New("the passwords do not match: nothing was changed")
		}
		pw = first
	} else {
		line, err := readLine(a.stdin)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		pw = strings.TrimSuffix(line, "\r")
	}
	switch {
	case pw == "":
		return "", usageErrorf("--password needs a password: type it when asked, or pipe it on stdin")
	case !utf8.ValidString(pw):
		return "", usageErrorf("the password is not valid UTF-8")
	case utf8.RuneCountInString(pw) < minPasswordChars:
		return "", usageErrorf("the password needs at least %d characters", minPasswordChars)
	case len(pw) > maxPasswordBytes:
		return "", usageErrorf("the password can be at most %d bytes", maxPasswordBytes)
	}
	return pw, nil
}
