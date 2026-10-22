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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"wardlock.nz/apiclient"
)

const (
	// uploadWorkers bounds how many files upload at once.
	uploadWorkers = 8
	// uploadAttempts is how often one file is tried when the network or
	// server fails in a way worth retrying.
	uploadAttempts = 3
	// uploadTimeout bounds one file's upload; the shared client's 30
	// seconds is too short for a large file on a slow link.
	uploadTimeout = 10 * time.Minute
	// maxMessageChars is the API's limit on a deploy message, in
	// characters (runes), not bytes.
	maxMessageChars = 500
)

// deployResult is deploy's --json output (docs/SPEC.md "CLI"): the API's
// Activation plus what this run did.
type deployResult struct {
	apiclient.Activation
	// Path is the prefix a path deploy replaced; absent for a full deploy.
	Path        string        `json:"path,omitempty"`
	SiteCreated bool          `json:"siteCreated"`
	Uploaded    uploadedStats `json:"uploaded"`
	Skipped     []string      `json:"skipped"`
}

type uploadedStats struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func runDeploy(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "deploy", "Usage: wardlock deploy [DIR] [DOMAIN] [flags]\n\n"+
		"Uploads DIR (default \"dir\" from wardlock.json, else the current\n"+
		"directory) to the site DOMAIN (default \"site\" from wardlock.json):\n"+
		"a site name such as docs, or its full hostname. A new name creates\n"+
		"the site. Only files the server does not already have are uploaded;\n"+
		"then the deploy goes live at once.\n\n"+
		"Left out: .git/, node_modules/, .DS_Store, .env*, wardlock.json,\n"+
		"other dotfiles except .well-known/, symbolic links, and whatever\n"+
		"DIR/.wardlockignore lists in .gitignore syntax (\"!\" re-includes a\n"+
		"dotfile).\n\n"+
		"With --path /mockup-x/ (default \"path\" from wardlock.json), DIR\n"+
		"replaces only that part of the site and everything else stays live.\n"+
		"An empty DIR removes it, after you confirm by typing the path;\n"+
		"without a terminal, or with --json, pass --yes instead.")
	g := addGlobalFlags(fs)
	message := fs.String("message", "", "describe this deploy in its history")
	spa := fs.Bool("spa", false, "serve /index.html for missing paths without a file extension (default \"spa\" from wardlock.json)")
	immutable := fs.Bool("immutable-assets", false, "cache hashed files under /assets/, such as index-BdK3j2Lp.js, for a year without revalidating (default \"immutableAssets\" from wardlock.json)")
	pathFlag := fs.String("path", "", "deploy DIR into this part of the site, such as /mockup-x/, keeping the rest (default \"path\" from wardlock.json; '' for the whole site)")
	yes := fs.Bool("yes", false, "with --path and an empty DIR, remove the path without asking")
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) > 2 {
		fs.Usage()
		return errUsage
	}
	wd, err := a.getwd()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(wd)
	if err != nil {
		return err
	}
	dir, domain := cfg.Dir, cfg.Site
	if len(pos) > 0 {
		dir = pos[0]
	}
	if len(pos) > 1 {
		domain = pos[1]
	}
	if dir == "" {
		dir = "."
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(wd, dir)
	}
	if domain == "" {
		return usageErrorf("which site? Pass DOMAIN, such as wardlock deploy %s docs, or set \"site\" in %s", displayDir(wd, dir), configFile)
	}
	t, err := parseTarget(domain)
	if err != nil {
		return err
	}
	// With a path, flags left unset follow the deploy the path goes into,
	// so they are sent only when given (here or in wardlock.json).
	useSPA, useImmutable := cfg.Spa, cfg.ImmutableAssets
	prefix := cfg.Path
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "spa":
			useSPA = spa
		case "immutable-assets":
			useImmutable = immutable
		case "path":
			prefix = *pathFlag
		}
	})
	if prefix != "" {
		if prefix, err = cleanPrefix(prefix); err != nil {
			return err
		}
	} else {
		useSPA, useImmutable = orFalse(useSPA), orFalse(useImmutable)
	}
	if n := utf8.RuneCountInString(*message); n > maxMessageChars {
		return usageErrorf("--message is %d characters; the limit is %d", n, maxMessageChars)
	}

	shown := displayDir(wd, dir)
	// Walk the directory a symbolic link names, rather than the link.
	path, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("deploy %s: %w", shown, err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return fmt.Errorf("deploy %s: %w", shown, err)
	}
	defer func() { _ = root.Close() }()
	c, err := a.connect(g.server, cfg.Server)
	if err != nil {
		return err
	}

	d := &deployer{a: a, c: c, root: root, prog: &progress{w: a.stderr, live: a.liveProgress, now: a.now}}
	return d.run(ctx, deployRequest{path: path, shown: shown, target: t, message: *message, spa: useSPA, immutableAssets: useImmutable,
		prefix: prefix, yes: *yes, json: g.json})
}

// cleanPrefix turns --path into the API's form, /dir/: in NFC like file
// paths, with the leading and trailing slashes added when left out. The
// server checks the rest.
func cleanPrefix(p string) (string, error) {
	p = norm.NFC.String(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if p == "/" || p == "//" {
		return "", usageErrorf("--path / is the whole site: leave --path out to deploy all of it")
	}
	return p, nil
}

// orFalse is *b, or false when b is nil, as a pointer to send.
func orFalse(b *bool) *bool {
	v := b != nil && *b
	return &v
}

type deployRequest struct {
	path, shown string
	target      target
	message     string
	// spa and immutableAssets are nil to follow the base of a path deploy.
	spa, immutableAssets *bool
	// prefix is the path deploy's prefix, /dir/, or "" for the whole site.
	prefix    string
	yes, json bool
}

type deployer struct {
	a    *app
	c    *conn
	root *os.Root
	prog *progress
	// uploaded counts bytes sent, for progress.
	uploaded atomic.Int64
}

func (d *deployer) run(ctx context.Context, r deployRequest) error {
	a, c := d.a, d.c
	start := a.now()
	files, skipped, err := scan(d.root, r.path)
	if err != nil {
		return err
	}
	if skipped == nil {
		skipped = []string{}
	}
	for _, s := range skipped {
		fmt.Fprintf(a.stderr, "Skipped %s: not a regular file (symbolic links are not followed)\n", s)
	}
	removing := len(files) == 0
	if removing && r.prefix == "" {
		return fmt.Errorf("nothing to deploy: %s has no files that are not ignored", r.shown)
	}
	if removing && !r.yes && (r.json || !a.canPrompt) {
		return usageErrorf("%s is empty, so this removes everything under %s from %s: pass --yes to confirm when there is no terminal to ask in, or with --json", r.shown, r.prefix, r.target)
	}

	var hashed atomic.Int64
	var hashedBytes atomic.Int64
	err = hashFiles(ctx, d.root, files, func(size int64) {
		n := hashed.Add(1)
		b := hashedBytes.Add(size)
		d.prog.update("Hashing files: %d of %d (%s)", n, len(files), formatBytes(b))
	})
	if err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.size
	}
	if !removing {
		d.prog.done("Hashed %s (%s).", plural(len(files), "file", "files"), formatBytes(total))
	}

	var site apiclient.Site
	created := false
	if removing {
		// Nothing to remove from a site that does not exist: never create one.
		if site, err = c.mustFindSite(ctx, r.target); err != nil {
			return err
		}
		if !r.yes {
			ok, err := a.confirm(fmt.Sprintf("%s is empty, so this removes every file under %s from %s at once.\nType the path to confirm: ", r.shown, r.prefix, site.Hostname), r.prefix)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("not confirmed: nothing was removed")
			}
		}
	} else if site, created, err = d.site(ctx, r.target); err != nil {
		return err
	}
	manifest := make([]apiclient.ManifestFile, len(files))
	byHash := make(map[string]localFile, len(files))
	for i, f := range files {
		manifest[i] = apiclient.ManifestFile{Path: f.path, Sha256: f.sha256, Size: f.size}
		byHash[f.sha256] = f
	}
	body := apiclient.CreateDeployRequest{Files: manifest, Spa: r.spa, ImmutableAssets: r.immutableAssets}
	if r.message != "" {
		body.Message = &r.message
	}
	if r.prefix != "" {
		body.Path = &r.prefix
	}
	up := &uploadedStats{}
	act, err := d.deploy(ctx, site.Name, body, byHash, up, len(files))
	if errors.Is(err, errStaleBase) {
		// Another deploy went live after the server pinned this path
		// deploy's base. Recreating it pins the new one; its files are
		// already uploaded, so nothing is sent again.
		d.prog.done("The site changed while deploying; deploying into the new version.")
		act, err = d.deploy(ctx, site.Name, body, byHash, up, len(files))
		if errors.Is(err, errStaleBase) {
			return fmt.Errorf("%s changed again while deploying %s; run the deploy again", site.Name, r.prefix)
		}
	}
	if err != nil {
		return err
	}
	if r.json {
		return writeJSON(a.stdout, deployResult{
			Activation:  act,
			Path:        r.prefix,
			SiteCreated: created,
			Uploaded:    *up,
			Skipped:     skipped,
		})
	}
	took := a.now().Sub(start).Seconds()
	if removing {
		fmt.Fprintf(a.stdout, "Removed %s from %s in %.1fs. Deploy %s is live:\n%s\n", r.prefix, act.Site.Name, took, shortID(act.Deploy.Id), act.Site.Url)
		return nil
	}
	verb := "Deployed"
	if created {
		verb = "Created " + act.Site.Name + " and deployed"
	}
	url, into := act.Site.Url, ""
	if r.prefix != "" {
		url, into = strings.TrimSuffix(url, "/")+r.prefix, " to "+r.prefix
	}
	fmt.Fprintf(a.stdout, "%s %s (%s)%s in %.1fs. Deploy %s is live:\n%s\n", verb, plural(len(files), "file", "files"), formatBytes(total),
		into, took, shortID(act.Deploy.Id), url)
	return nil
}

// errStaleBase is activateDeploy's 409 stale_base: a path deploy whose
// base is no longer live.
var errStaleBase = errors.New("stale base")

// deploy creates a deploy from body, uploads what the server is missing
// (adding it to up) and activates it. It returns errStaleBase, with nothing
// activated, when the server refuses a path deploy whose base changed.
func (d *deployer) deploy(ctx context.Context, site string, body apiclient.CreateDeployRequest, byHash map[string]localFile, up *uploadedStats, fileCount int) (apiclient.Activation, error) {
	c := d.c
	cres, err := c.api.CreateDeployWithResponse(ctx, site, body, c.auth)
	if err != nil {
		return apiclient.Activation{}, c.unreachable(ctx, err)
	}
	if cres.JSON201 == nil {
		prefix := ""
		if body.Path != nil {
			prefix = *body.Path
		}
		return apiclient.Activation{}, refusedFile(c.failed(cres.HTTPResponse, cres.Body), cres.Body, prefix, body.Files)
	}
	deploy := cres.JSON201.Deploy
	// A server older than path deploys ignores the path and would replace
	// the whole site with this upload, so never upload to or activate a
	// deploy that does not carry the prefix asked for.
	if body.Path != nil && (deploy.Path == nil || *deploy.Path != *body.Path) {
		return apiclient.Activation{}, fmt.Errorf("server %s does not support deploying into a path (it needs Wardlock 0.2.0 or later); nothing was changed", c.server)
	}

	var toUpload []localFile
	var uploadBytes int64
	for _, h := range cres.JSON201.Missing {
		f, ok := byHash[h]
		if !ok {
			return apiclient.Activation{}, fmt.Errorf("server %s asked for a file this deploy does not have", c.server)
		}
		toUpload = append(toUpload, f)
		uploadBytes += f.size
	}
	if len(toUpload) > 0 {
		var sent atomic.Int64
		err = d.uploadAll(ctx, site, deploy.Id, toUpload, func() {
			n := sent.Add(1)
			d.prog.update("Uploading: %d of %d files (%s of %s)", n, len(toUpload), formatBytes(d.uploaded.Load()), formatBytes(uploadBytes))
		})
		if err != nil {
			return apiclient.Activation{}, err
		}
		d.prog.done("Uploaded %s (%s); %d unchanged.", plural(len(toUpload), "file", "files"), formatBytes(uploadBytes), fileCount-len(toUpload))
		up.Files += len(toUpload)
		up.Bytes += uploadBytes
	} else if fileCount > 0 {
		d.prog.done("Nothing to upload: the server already has all %s.", plural(fileCount, "file", "files"))
	}

	ares, err := c.api.ActivateDeployWithResponse(ctx, site, deploy.Id, c.auth)
	if err != nil {
		return apiclient.Activation{}, c.unreachable(ctx, err)
	}
	if ares.JSON200 == nil {
		if ares.StatusCode() == http.StatusConflict && errorCode(ares.Body) == "stale_base" {
			return apiclient.Activation{}, errStaleBase
		}
		return apiclient.Activation{}, c.failed(ares.HTTPResponse, ares.Body)
	}
	return *ares.JSON200, nil
}

// refusedFile adds what to do when the server refuses the deploy because
// of one of its files: a file over a size limit (limit_exceeded naming the
// path) or of a blocked type (file_type_blocked). The server's message
// already names the file and the limit; quotas name no file and need no
// more. A path deploy's server names files with prefix, without its
// leading slash, in front; a file the site already serves outside the
// prefix is not in DIR and gets no advice.
func refusedFile(err error, body []byte, prefix string, manifest []apiclient.ManifestFile) error {
	var e apiclient.Error
	if json.Unmarshal(body, &e) != nil || e.Target == nil || (e.Code != "limit_exceeded" && e.Code != "file_type_blocked") {
		return err
	}
	for _, f := range manifest {
		if strings.TrimPrefix(prefix, "/")+f.Path == *e.Target {
			return fmt.Errorf("%w To deploy without it, list %s in %s", err, f.Path, ignoreFile)
		}
	}
	return err
}

// site finds the deploy's site, creating it when DOMAIN is a new name or
// <new name>.<base domain>.
func (d *deployer) site(ctx context.Context, t target) (apiclient.Site, bool, error) {
	c := d.c
	site, err := c.findSite(ctx, t)
	if err == nil || !errors.Is(err, errNoSite) {
		return site, false, err
	}
	name := t.name
	if name == "" {
		if name, err = c.newSiteName(ctx, t.hostname); err != nil {
			return site, false, err
		}
	}
	res, err := c.api.CreateSiteWithResponse(ctx, apiclient.CreateSiteJSONRequestBody{Name: name}, c.auth)
	if err != nil {
		return site, false, c.unreachable(ctx, err)
	}
	if res.JSON201 == nil {
		return site, false, c.failed(res.HTTPResponse, res.Body)
	}
	return *res.JSON201, true, nil
}

// uploadAll uploads files, uploadWorkers at a time, calling done after
// each.
func (d *deployer) uploadAll(ctx context.Context, site, deployID string, files []localFile, done func()) error {
	hc := *d.a.http
	hc.Timeout = uploadTimeout
	api, err := apiclient.NewClientWithResponses(d.c.server, apiclient.WithHTTPClient(&hc))
	if err != nil {
		return err
	}
	return forEach(ctx, len(files), uploadWorkers, func(ctx context.Context, i int) error {
		f := files[i]
		for attempt := 1; ; attempt++ {
			retry, wait, err := d.upload(ctx, api, site, deployID, f)
			if err == nil {
				done()
				return nil
			}
			if !retry || attempt == uploadAttempts {
				return err
			}
			if wait == 0 {
				wait = time.Duration(attempt) * time.Second
			}
			if err := d.a.sleep(ctx, wait); err != nil {
				return errors.New("cancelled")
			}
		}
	})
}

// upload sends one file. On failure it says whether a retry is worth
// trying, and after how long when the server said (otherwise wait is 0).
func (d *deployer) upload(ctx context.Context, api *apiclient.ClientWithResponses, site, deployID string, f localFile) (retry bool, wait time.Duration, err error) {
	changed := fmt.Errorf("%s changed while deploying: run wardlock deploy again", f.path)
	file, err := d.root.Open(f.file)
	if err != nil {
		return false, 0, fmt.Errorf("read %s: %w", f.path, err)
	}
	defer func() { _ = file.Close() }()
	if fi, err := file.Stat(); err != nil || fi.Size() != f.size {
		return false, 0, changed
	}
	// sent counts this attempt's bytes, which also show in the live total
	// until the answer comes; then the total gets the whole file or none.
	var sent atomic.Int64
	body := countingReader{r: io.LimitReader(file, f.size), n: &sent, total: &d.uploaded}
	setLength := func(_ context.Context, r *http.Request) error {
		r.ContentLength = f.size
		if f.size == 0 {
			r.Body = http.NoBody
		}
		return nil
	}
	res, err := api.UploadDeployBlobWithBodyWithResponse(ctx, site, deployID, f.sha256, "application/octet-stream", body, d.c.auth, setLength)
	d.uploaded.Add(-sent.Load())
	if err != nil {
		return ctx.Err() == nil, 0, fmt.Errorf("upload %s: %w", f.path, d.c.unreachable(ctx, err))
	}
	failed := fmt.Errorf("upload %s: %w", f.path, d.c.failed(res.HTTPResponse, res.Body))
	switch code := res.StatusCode(); code {
	case http.StatusNoContent:
		d.uploaded.Add(f.size)
		return false, 0, nil
	case http.StatusBadRequest:
		if errorCode(res.Body) == "hash_mismatch" {
			return false, 0, changed
		}
	case http.StatusTooManyRequests:
		return true, retryAfter(res.HTTPResponse), failed
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true, 0, failed
	}
	return false, 0, failed
}

// maxRetryAfter caps how long a Retry-After header can make an upload wait.
const maxRetryAfter = 30 * time.Second

// retryAfter reads Retry-After in seconds, capped at maxRetryAfter; 0 when
// absent or not a number.
func retryAfter(res *http.Response) time.Duration {
	secs, err := strconv.Atoi(res.Header.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return 0
	}
	return min(time.Duration(secs)*time.Second, maxRetryAfter)
}

func errorCode(body []byte) string {
	var e apiclient.Error
	_ = json.Unmarshal(body, &e)
	return e.Code
}

// newSiteName returns the name of the site to create for host when it is
// <name>.<base domain>. The API does not report the base domain, so it is
// inferred from the owner's sites, whose hostnames are all
// <name>.<base domain>.
func (c *conn) newSiteName(ctx context.Context, host string) (string, error) {
	label, rest, _ := strings.Cut(host, ".")
	hint := ""
	if siteNamePattern.MatchString(label) {
		hint = ", such as " + label
	}
	sites, err := c.sites(ctx)
	if err != nil {
		return "", err
	}
	base, ok := baseDomain(sites)
	switch {
	case len(sites) == 0:
		return "", fmt.Errorf("none of your sites on %s is served at %s, and with no sites to go by the CLI cannot tell whether it is under the server's base domain: to create a site, deploy to its name%s", c.server, host, hint)
	case !ok:
		return "", fmt.Errorf("none of your sites on %s is served at %s, and your sites are on different base domains, so the CLI cannot tell which is the server's: to create a site, deploy to its name%s", c.server, host, hint)
	case rest != base || !siteNamePattern.MatchString(label):
		return "", fmt.Errorf("none of your sites on %s is served at %s, and it is not a site address under %s: to create a site, deploy to its name%s", c.server, host, base, hint)
	}
	return label, nil
}

// baseDomain infers the base domain from sites. ok is false when there
// are none or they disagree.
func baseDomain(sites []apiclient.Site) (string, bool) {
	base := ""
	for _, s := range sites {
		b, found := strings.CutPrefix(strings.ToLower(s.Hostname), s.Name+".")
		if !found || b == "" || (base != "" && b != base) {
			return "", false
		}
		base = b
	}
	return base, base != ""
}

// displayDir shows dir relative to wd when it is inside it.
func displayDir(wd, dir string) string {
	if rel, err := filepath.Rel(wd, dir); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return rel
	}
	return dir
}

// progress writes progress lines to stderr. On a terminal update redraws
// one line, at most ten times a second; elsewhere only done lines appear,
// so logs stay short.
type progress struct {
	w    io.Writer
	live bool
	now  func() time.Time

	mu   sync.Mutex
	last time.Time
	// width is how many characters the redrawn line shows now.
	width int
}

func (p *progress) update(format string, args ...any) {
	if !p.live {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now := p.now(); now.Sub(p.last) >= 100*time.Millisecond {
		p.last = now
		p.redraw(fmt.Sprintf(format, args...))
	}
}

func (p *progress) done(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	line := fmt.Sprintf(format, args...)
	if p.live {
		p.redraw(line)
		p.width = 0
		fmt.Fprintln(p.w)
		return
	}
	fmt.Fprintln(p.w, line)
}

// redraw replaces the current line with line. It pads with spaces rather
// than using an erase sequence, which older Windows consoles print as is.
func (p *progress) redraw(line string) {
	n := utf8.RuneCountInString(line)
	fmt.Fprint(p.w, "\r"+line+strings.Repeat(" ", max(p.width-n, 0)))
	p.width = n
}
