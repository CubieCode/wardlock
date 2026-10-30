// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wardlock.nz/apiclient"
)

// wardlock mcp (I6.5): a Model Context Protocol server on stdin and
// stdout, so AI tools can publish and share pages with the CLI's
// credentials. Each tool runs the command it wraps with --json in a copy
// of the app that cannot prompt, read stdin or write to stdout, and
// returns that command's JSON. Files are read and written only inside the
// root. Nothing a tool returns is logged: a share link's URL reaches the
// client once, as the CLI prints it.

const mcpUsage = "Usage: wardlock mcp [--root DIR] [--server URL]\n\n" +
	"Runs a Model Context Protocol server on standard input and output for\n" +
	"AI tools such as Claude Code, Claude Desktop and editors, with the\n" +
	"credentials of wardlock login or WARDLOCK_TOKEN. Its tools list sites\n" +
	"and deploys, deploy and pull directories, roll back, set policies and\n" +
	"create, list and revoke share links; they never delete sites, change\n" +
	"domains or manage tokens. Directories are deployed from and pulled\n" +
	"into only inside --root (default the current directory), which also\n" +
	"holds the wardlock.json deploys read. Logs go to standard error.\n" +
	"Start it from the AI tool's MCP configuration, not by hand."

// mcpInstructions tells the client what the server is for.
const mcpInstructions = "Publishes static sites to a Wardlock server and controls who can open them. " +
	"Sites are named by their name (docs) or hostname. Directories are relative to the server's root. " +
	"A share link's URL is returned only once, by create_share_link: give it to the user, as it cannot be shown again."

func runMCP(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "mcp", mcpUsage)
	g := addGlobalFlags(fs)
	rootFlag := fs.String("root", "", "deploy from and pull into directories only inside DIR (default the current directory)")
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
	wd, err := a.getwd()
	if err != nil {
		return err
	}
	root := *rootFlag
	if root == "" {
		root = wd
	} else if !filepath.IsAbs(root) {
		root = filepath.Join(wd, root)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return fmt.Errorf("--root: %w", err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return usageErrorf("--root %s is not a directory", root)
	}
	m := newMCPServer(a, root, g.server)
	// Check the credentials now, so a server that cannot work says so
	// before a client starts using it.
	if _, err := m.connect(a); err != nil {
		return err
	}
	m.log.Info("wardlock mcp started", slog.String("root", root))
	t := &mcp.IOTransport{Reader: io.NopCloser(a.stdin), Writer: nopWriteCloser{a.stdout}}
	if err := m.newServer().Run(ctx, t); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// newMCPServer serves root, logging to a's stderr.
func newMCPServer(a *app, root, server string) *mcpServer {
	return &mcpServer{a: a, root: root, server: server, log: slog.New(slog.NewTextHandler(a.stderr, nil))}
}

type mcpServer struct {
	a *app
	// root is the canonical directory tools read and write inside.
	root string
	// server is --server, passed on to every command.
	server string
	log    *slog.Logger
}

// Tool inputs. Their JSON schemas come from these types; an omitempty
// field is optional.
type (
	listSitesInput struct {
		Group string   `json:"group,omitempty" jsonschema:"list only the sites in this existing group"`
		Tags  []string `json:"tags,omitempty" jsonschema:"list only the sites with every one of these existing tags"`
	}
	siteInput struct {
		Site string `json:"site" jsonschema:"the site's name, such as docs, or its hostname"`
	}
	deployInput struct {
		Site      string `json:"site,omitempty" jsonschema:"the site's name, such as docs, or its hostname; a new name creates the site (default site from wardlock.json in the root)"`
		Directory string `json:"directory,omitempty" jsonschema:"the directory to publish, relative to the root (default dir from wardlock.json in the root, else the root)"`
		Path      string `json:"path,omitempty" jsonschema:"replace only this part of the site, such as /mockup-x/, keeping the rest live; an empty directory removes it (default path from wardlock.json)"`
		Message   string `json:"message,omitempty" jsonschema:"describe this deploy in the site's history (at most 500 characters)"`
		Preview   string `json:"preview,omitempty" jsonschema:"deploy to this preview of the site, such as pr-42, served at its own address, instead of the live site; the same name again replaces it"`
		Expires   string `json:"expires,omitempty" jsonschema:"with preview, how long the preview lasts after this deploy: 1d to 90d, or never (default 14d, or what the preview had)"`
	}
	pullInput struct {
		Site      string `json:"site" jsonschema:"the site's name, such as docs, or its hostname"`
		Directory string `json:"directory" jsonschema:"a new or empty directory to write the files into, relative to the root"`
		Deploy    string `json:"deploy,omitempty" jsonschema:"the deploy to pull: its ID or short ID from list_deploys (default the live deploy)"`
		Path      string `json:"path,omitempty" jsonschema:"pull only the files under this part of the site, such as /mockup-x/"`
	}
	rollbackInput struct {
		Site   string `json:"site" jsonschema:"the site's name, such as docs, or its hostname"`
		Deploy string `json:"deploy,omitempty" jsonschema:"the deploy to make live again: its ID or short ID from list_deploys (default the one live before the current one)"`
	}
	policyInput struct {
		Site       string   `json:"site" jsonschema:"the site's name, such as docs, or its hostname"`
		Path       string   `json:"path,omitempty" jsonschema:"the directory prefix the policy covers, such as /docs/ (default / for the whole site)"`
		Visibility string   `json:"visibility" jsonschema:"public lets anyone in; protected only visitors who satisfy one of the methods"`
		Methods    []string `json:"methods,omitempty" jsonschema:"with protected, how visitors get in: links (share links; the default), password (keeps the policy's current password: this tool never sets one), email (a code emailed to an allowed address), oauth (an allowed GitHub, Google or OIDC account)"`
		Allow      []string `json:"allow,omitempty" jsonschema:"with protected, replace the allowlist with these addresses (ana@example.com) and domains (example.com, or *.example.com for its subdomains); omitted keeps it, [] empties it"`
		GithubOrgs []string `json:"githubOrgs,omitempty" jsonschema:"with protected, replace the GitHub organisations whose members are let in; omitted keeps them, [] empties them"`
	}
	shareCreateInput struct {
		Site    string `json:"site" jsonschema:"the site's name, such as docs, or its hostname"`
		Path    string `json:"path,omitempty" jsonschema:"limit the link to this directory, such as /docs/ (default the whole site)"`
		Label   string `json:"label,omitempty" jsonschema:"a note for the owner, such as a ticket ID; never part of the URL"`
		Expires string `json:"expires,omitempty" jsonschema:"when the link stops working: never (the default), or such as 30m, 1h, 7d or 2w"`
		Uses    int    `json:"uses,omitempty" jsonschema:"how many times the link can be opened, 1 to 1000000 (default unlimited)"`
	}
	shareRevokeInput struct {
		Site   string `json:"site" jsonschema:"the site's name, such as docs, or its hostname"`
		LinkID string `json:"linkId" jsonschema:"the 10-character link ID from list_share_links"`
	}
)

// mcpTool describes one tool: its scope is what a token needs for it,
// named when the server refuses one without it.
type mcpTool struct {
	name, title, description, scope string
	// changesSite marks tools that change what visitors see: clients ask
	// before running them.
	changesSite bool
	readOnly    bool
}

func (t mcpTool) spec() *mcp.Tool {
	ann := &mcp.ToolAnnotations{Title: t.title, ReadOnlyHint: t.readOnly, DestructiveHint: ptr(t.changesSite)}
	return &mcp.Tool{Name: t.name, Title: t.title, Description: t.description, Annotations: ann}
}

func (m *mcpServer) newServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "wardlock", Title: "Wardlock", Version: version}, &mcp.ServerOptions{Instructions: mcpInstructions})
	addMCPTool(m, s, mcpTool{name: "list_sites", title: "List sites", readOnly: true,
		description: "Lists your sites with their URL, default policy (public or protected), group, tags and when they were last deployed."},
		func(ctx context.Context, a *app, in listSitesInput) error {
			args := []string{}
			if in.Group != "" {
				args = append(args, "--group", in.Group)
			}
			for _, t := range in.Tags {
				args = append(args, "--tag", t)
			}
			return m.run(ctx, a, runList, args)
		})
	addMCPTool(m, s, mcpTool{name: "get_site", title: "Get a site", readOnly: true,
		description: "Shows one site: its URL, its access policies, its custom domains and its live deploy."},
		m.getSite)
	addMCPTool(m, s, mcpTool{name: "list_deploys", title: "List deploys", readOnly: true,
		description: "Lists a site's deploys, newest first, with their status, path, file count, size and message."},
		func(ctx context.Context, a *app, in siteInput) error {
			return m.run(ctx, a, runList, []string{"--", in.Site})
		})
	addMCPTool(m, s, mcpTool{name: "deploy_directory", title: "Deploy a directory", scope: "deploy", changesSite: true,
		description: "Publishes a directory inside the root to a site, creating the site for a new name, as wardlock deploy does: " +
			"the same files are left out (.git/, node_modules/, dotfiles, symbolic links and .wardlockignore entries) and wardlock.json in the root applies. " +
			"The deploy goes live at once. With path, only that part of the site is replaced. " +
			"With preview, it goes to that preview of the site and its own address instead, and the live site is left as it is."},
		m.deployDirectory)
	addMCPTool(m, s, mcpTool{name: "pull_deploy", title: "Pull a deploy",
		description: "Downloads the files of a site's live deploy, or of an earlier one, into a new or empty directory inside the root."},
		m.pullDeploy)
	addMCPTool(m, s, mcpTool{name: "rollback", title: "Roll back a site", scope: "deploy", changesSite: true,
		description: "Makes an earlier deploy of a site live again at once, by default the one live before the current one."},
		func(ctx context.Context, a *app, in rollbackInput) error {
			args := []string{"--", in.Site}
			if in.Deploy != "" {
				args = append(args, in.Deploy)
			}
			return m.run(ctx, a, runRollback, args)
		})
	addMCPTool(m, s, mcpTool{name: "set_policy", title: "Set a policy", scope: "manage", changesSite: true,
		description: "Sets who may open a site, or a directory of it: public lets anyone in; protected needs a share link, the policy's password, an emailed code or an allowed account. " +
			"Ends every visitor session of that policy."},
		m.setPolicy)
	addMCPTool(m, s, mcpTool{name: "create_share_link", title: "Create a share link", scope: "manage",
		description: "Creates a share link and returns its URL. The URL is shown only this once: give it to the user. " +
			"Anyone holding it can open the site (or the path and below) wherever a protected policy enables share links."},
		func(ctx context.Context, a *app, in shareCreateInput) error {
			args := []string{}
			if in.Path != "" {
				args = append(args, "--path", in.Path)
			}
			if in.Label != "" {
				args = append(args, "--label", in.Label)
			}
			if in.Expires != "" {
				args = append(args, "--expires", in.Expires)
			}
			if in.Uses != 0 {
				args = append(args, "--uses", strconv.Itoa(in.Uses))
			}
			return m.run(ctx, a, shareCreate, append(args, "--", in.Site))
		})
	addMCPTool(m, s, mcpTool{name: "list_share_links", title: "List share links", readOnly: true,
		description: "Lists a site's share links, newest first, with label, ID, path, expiry, uses and last access. URLs are never listed."},
		func(ctx context.Context, a *app, in siteInput) error {
			return m.run(ctx, a, shareList, []string{"--", in.Site})
		})
	addMCPTool(m, s, mcpTool{name: "revoke_share_link", title: "Revoke a share link", scope: "manage", changesSite: true,
		description: "Stops a share link working at once, with every visitor session opened with it."},
		func(ctx context.Context, a *app, in shareRevokeInput) error {
			return m.run(ctx, a, shareRevoke, []string{"--", in.Site, in.LinkID})
		})
	return s
}

// addMCPTool registers t. run gets an app whose stdout collects the
// command's --json output, which becomes the tool's result; its error
// becomes a tool error.
func addMCPTool[In any](m *mcpServer, s *mcp.Server, t mcpTool, run func(ctx context.Context, a *app, in In) error) {
	mcp.AddTool(s, t.spec(), func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := m.a.now()
		out, err := m.call(ctx, t, func(ctx context.Context, a *app) error { return run(ctx, a, in) })
		// Only the tool's name and outcome are logged, never its
		// arguments or result.
		attrs := []slog.Attr{slog.String("tool", t.name), slog.Duration("took", m.a.now().Sub(start).Round(time.Millisecond))}
		if err != nil {
			m.log.LogAttrs(ctx, slog.LevelWarn, "tool failed", append(attrs, slog.String("error", err.Error()))...)
			return nil, nil, err
		}
		m.log.LogAttrs(ctx, slog.LevelInfo, "tool ran", attrs...)
		return nil, out, nil
	})
}

// call runs fn with a copy of the app for one tool call and returns what
// it printed. The copy never prompts, opens a browser, reads stdin or
// writes to the real stdout, and runs in the root.
func (m *mcpServer) call(ctx context.Context, t mcpTool, fn func(ctx context.Context, a *app) error) (json.RawMessage, error) {
	var out bytes.Buffer
	sub := *m.a
	sub.getwd = func() (string, error) { return m.root, nil }
	sub.stdin = strings.NewReader("")
	sub.stdout, sub.stderr = &out, io.Discard
	sub.interactive, sub.canPrompt, sub.stdinTerminal, sub.liveProgress = false, false, false, false
	sub.openBrowser = func(string) error { return errors.New("wardlock mcp never opens a browser") }
	hc := *m.a.http
	refused := &forbiddenRecorder{next: hc.Transport}
	if refused.next == nil {
		refused.next = http.DefaultTransport
	}
	hc.Transport = refused
	sub.http = &hc
	err := fn(ctx, &sub)
	if err != nil {
		if refused.seen.Load() && t.scope != "" {
			return nil, fmt.Errorf("%s needs a token with the %s scope, and the server refused this one (403): run wardlock login again and approve %s, or use a WARDLOCK_TOKEN that has it", t.name, t.scope, t.scope)
		}
		return nil, err
	}
	if out.Len() == 0 {
		return nil, fmt.Errorf("%s returned nothing", t.name)
	}
	return json.RawMessage(bytes.TrimSpace(out.Bytes())), nil
}

// forbiddenRecorder notes whether any answer was 403 forbidden: a token
// without the scope an operation needs.
type forbiddenRecorder struct {
	next http.RoundTripper
	seen atomic.Bool
}

func (f *forbiddenRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := f.next.RoundTrip(r)
	if err == nil && res.StatusCode == http.StatusForbidden {
		f.seen.Store(true)
	}
	return res, err
}

// run runs a command with --json and --server added to args.
func (m *mcpServer) run(ctx context.Context, a *app, cmd func(context.Context, *app, []string) error, args []string) error {
	global := []string{"--json"}
	if m.server != "" {
		global = append(global, "--server", m.server)
	}
	return cmd(ctx, a, append(global, args...))
}

// connect connects as the commands do: --server, else wardlock.json in
// the root, else the environment and the stored login.
func (m *mcpServer) connect(a *app) (*conn, error) {
	cfg, err := loadConfig(m.root)
	if err != nil {
		return nil, err
	}
	return a.connect(m.server, cfg.Server)
}

// inRoot resolves p, relative to the root or absolute, to a path inside
// the root with every symbolic link followed. A path that does not exist
// yet must be under an existing directory inside the root. Anything that
// leads out of the root, by .. or by a link, is refused.
func (m *mcpServer) inRoot(p string) (string, error) {
	outside := fmt.Errorf("%s is outside the root %s: wardlock mcp reads and writes only inside it", p, m.root)
	full := p
	if !filepath.IsAbs(full) {
		full = filepath.Join(m.root, full)
	}
	full = filepath.Clean(full)
	existing, rest := full, ""
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			full = filepath.Join(resolved, rest)
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s: %w", p, err)
		}
		// A link to nowhere would be followed when the directory is
		// created, wherever it points.
		if _, lerr := os.Lstat(existing); lerr == nil {
			return "", outside
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("%s: %w", p, err)
		}
		existing, rest = parent, filepath.Join(filepath.Base(existing), rest)
	}
	rel, err := filepath.Rel(m.root, full)
	if err != nil || rel != "." && !filepath.IsLocal(rel) {
		return "", outside
	}
	return full, nil
}

func (m *mcpServer) deployDirectory(ctx context.Context, a *app, in deployInput) error {
	dir := in.Directory
	if dir == "" {
		cfg, err := loadConfig(m.root)
		if err != nil {
			return err
		}
		dir = cfg.Dir
	}
	dir, err := m.inRoot(dir)
	if err != nil {
		return err
	}
	// An empty directory with a path removes it, as wardlock deploy --path
	// does: the tool's destructive annotation is the confirmation.
	args := []string{"--yes"}
	if in.Path != "" {
		args = append(args, "--path", in.Path)
	}
	if in.Message != "" {
		args = append(args, "--message", in.Message)
	}
	if in.Preview != "" {
		args = append(args, "--preview", in.Preview)
	}
	if in.Expires != "" {
		args = append(args, "--expires", in.Expires)
	}
	args = append(args, "--", dir)
	if in.Site != "" {
		args = append(args, in.Site)
	}
	return m.run(ctx, a, runDeploy, args)
}

func (m *mcpServer) pullDeploy(ctx context.Context, a *app, in pullInput) error {
	if in.Directory == "" {
		return usageErrorf("which directory? Pass a new or empty directory inside the root to pull into")
	}
	dir, err := m.inRoot(in.Directory)
	if err != nil {
		return err
	}
	// wardlock pull --force would replace files; this tool never does.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty: pull into a new or empty directory", in.Directory)
	}
	args := []string{}
	if in.Deploy != "" {
		args = append(args, "--deploy", in.Deploy)
	}
	if in.Path != "" {
		args = append(args, "--path", in.Path)
	}
	return m.run(ctx, a, runPull, append(args, "--", in.Site, dir))
}

// siteDetails is get_site's result.
type siteDetails struct {
	Site     apiclient.Site     `json:"site"`
	Policies []apiclient.Policy `json:"policies"`
	Domains  []apiclient.Domain `json:"domains"`
	// LiveDeploy is absent until the site's first deploy goes live.
	LiveDeploy *apiclient.Deploy `json:"liveDeploy,omitempty"`
}

func (m *mcpServer) getSite(ctx context.Context, a *app, in siteInput) error {
	t, err := parseTarget(in.Site)
	if err != nil {
		return err
	}
	c, err := m.connect(a)
	if err != nil {
		return err
	}
	site, err := c.mustFindSite(ctx, t)
	if err != nil {
		return err
	}
	out := siteDetails{Site: site, Policies: []apiclient.Policy{}, Domains: []apiclient.Domain{}}
	var cursor *string
	for {
		res, err := c.api.ListPoliciesWithResponse(ctx, site.Name, &apiclient.ListPoliciesParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return c.failed(res.HTTPResponse, res.Body)
		}
		out.Policies = append(out.Policies, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			break
		}
	}
	for {
		res, err := c.api.ListDomainsWithResponse(ctx, site.Name, &apiclient.ListDomainsParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return c.failed(res.HTTPResponse, res.Body)
		}
		out.Domains = append(out.Domains, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			break
		}
	}
	if site.ActiveDeployId != nil {
		deploys, err := c.deploys(ctx, site.Name)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(deploys, func(d apiclient.Deploy) bool { return d.Id == *site.ActiveDeployId }); i >= 0 {
			out.LiveDeploy = &deploys[i]
		}
	}
	return writeJSON(a.stdout, out)
}

// setPolicy is wardlock policy set without --password: the password
// method keeps the policy's current password, since a password given to a
// tool would pass through the AI client.
func (m *mcpServer) setPolicy(ctx context.Context, a *app, in policyInput) error {
	t, err := parseTarget(in.Site)
	if err != nil {
		return err
	}
	path := in.Path
	if path == "" {
		path = "/"
	}
	body := apiclient.SetPolicyRequest{Path: path}
	password := false
	switch in.Visibility {
	case "public":
		if len(in.Methods) > 0 || len(in.Allow) > 0 || len(in.GithubOrgs) > 0 {
			return usageErrorf("a public policy takes no methods, allowlist or GitHub organisations: leave them out, or use protected")
		}
		body.Visibility = apiclient.PolicyVisibilityPublic
	case "protected":
		body.Visibility = apiclient.PolicyVisibilityProtected
		if in.Methods != nil {
			methods := []apiclient.PolicyMethod{}
			for _, s := range uniq(in.Methods) {
				meth := apiclient.PolicyMethod(s)
				if !slices.Contains([]apiclient.PolicyMethod{apiclient.PolicyMethodLinks, apiclient.PolicyMethodPassword, apiclient.PolicyMethodEmail, apiclient.PolicyMethodOauth}, meth) {
					return usageErrorf("%q is not a method: use links, password, email or oauth", s)
				}
				password = password || meth == apiclient.PolicyMethodPassword
				methods = append(methods, meth)
			}
			if len(methods) > 0 {
				body.Methods = &methods
			}
		}
		if in.Allow != nil {
			allow := &apiclient.PolicyAllowlist{Emails: []string{}, Domains: []string{}}
			for _, v := range in.Allow {
				if err := addAllowed(allow, v); err != nil {
					return err
				}
			}
			body.Allowlist = allow
		}
		if in.GithubOrgs != nil {
			orgs := []string{}
			for _, v := range in.GithubOrgs {
				if err := addOrgs(&orgs, v); err != nil {
					return err
				}
			}
			if body.Allowlist == nil {
				body.Allowlist = &apiclient.PolicyAllowlist{}
			}
			body.Allowlist.GithubOrgs = &orgs
		}
	default:
		return usageErrorf("visibility must be public or protected")
	}
	c, err := m.connect(a)
	if err != nil {
		return err
	}
	site, err := c.mustFindSite(ctx, t)
	if err != nil {
		return err
	}
	if body.Allowlist != nil && in.Allow == nil {
		// The API replaces addresses and domains with the allowlist, so
		// GitHub organisations alone send the current ones back unchanged.
		cur, err := c.policyAllowlist(ctx, site.Name, path)
		if err != nil {
			return err
		}
		body.Allowlist.Emails, body.Allowlist.Domains = cur.Emails, cur.Domains
	}
	res, err := c.api.SetPolicyWithResponse(ctx, site.Name, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		err := c.failed(res.HTTPResponse, res.Body)
		if password && res.StatusCode() == http.StatusBadRequest && errorCode(res.Body) == "invalid_policy" {
			return fmt.Errorf("%w This tool keeps the policy's current password and never sets one: if it has none yet, set it in a terminal with wardlock policy set %s --path %s --protected --password", err, site.Name, path)
		}
		return err
	}
	return writeJSON(a.stdout, res.JSON200)
}
