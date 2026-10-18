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

const policyUsage = "Usage: wardlock policy set DOMAIN [--path P] (--public | --protected [--links] [--password] [--email] [--allow LIST])\n\n" +
	"Sets the site default policy, or with --path the policy for a directory\n" +
	"prefix such as /docs/. The longest matching prefix decides each request.\n" +
	"--public lets anyone in; --protected needs one of the methods given:\n" +
	"--links for share links (the default when none is given), --password\n" +
	"for a password visitors enter on the site's login page, --email for a\n" +
	"code emailed to an address the allowlist lets in. The password is\n" +
	"asked for without echo on a terminal, and otherwise read from the first\n" +
	"line of standard input; it is never a flag. --allow replaces the\n" +
	"policy's allowlist with comma-separated addresses (ana@example.com) and\n" +
	"domains (example.com for that domain only, *.example.com for its\n" +
	"subdomains); it may be repeated, and --allow '' empties it. Without\n" +
	"--allow the current allowlist is kept. A change ends every visitor\n" +
	"session of that policy. Needs a token with the manage scope."

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
	switch {
	case *public == *protected:
		return usageErrorf("pass exactly one of --public or --protected")
	case *public && len(methods) > 0:
		return usageErrorf("a public policy takes no methods: drop --links, --password and --email, or use --protected")
	case *public && allow != nil && len(allow.Emails)+len(allow.Domains) > 0:
		return usageErrorf("a public policy has no allowlist: drop --allow, or use --protected")
	case *public:
		body.Visibility = apiclient.PolicyVisibilityPublic
	default:
		body.Visibility = apiclient.PolicyVisibilityProtected
		if len(methods) > 0 {
			body.Methods = &methods
		}
		body.Allowlist = allow
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
// numeric label.
func allowedDomain(d string) bool {
	name, _ := strings.CutPrefix(strings.TrimSuffix(d, "."), "*.")
	labels := strings.Split(name, ".")
	if len(name) > 253 || len(labels) < 2 || strings.Trim(labels[len(labels)-1], "0123456789") == "" {
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
