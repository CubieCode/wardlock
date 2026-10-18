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
		"dotfile).")
	g := addGlobalFlags(fs)
	message := fs.String("message", "", "describe this deploy in its history")
	spa := fs.Bool("spa", false, "serve /index.html for missing paths without a file extension (default \"spa\" from wardlock.json)")
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
	useSPA := cfg.Spa != nil && *cfg.Spa
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "spa" {
			useSPA = *spa
		}
	})
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
	return d.run(ctx, deployRequest{path: path, shown: shown, target: t, message: *message, spa: useSPA, json: g.json})
}

type deployRequest struct {
	path, shown string
	target      target
	message     string
	spa, json   bool
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
	if len(files) == 0 {
		return fmt.Errorf("nothing to deploy: %s has no files that are not ignored", r.shown)
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
	d.prog.done("Hashed %s (%s).", plural(len(files), "file", "files"), formatBytes(total))

	site, created, err := d.site(ctx, r.target)
	if err != nil {
		return err
	}
	manifest := make([]apiclient.ManifestFile, len(files))
	byHash := make(map[string]localFile, len(files))
	for i, f := range files {
		manifest[i] = apiclient.ManifestFile{Path: f.path, Sha256: f.sha256, Size: f.size}
		byHash[f.sha256] = f
	}
	body := apiclient.CreateDeployRequest{Files: manifest, Spa: &r.spa}
	if r.message != "" {
		body.Message = &r.message
	}
	cres, err := c.api.CreateDeployWithResponse(ctx, site.Name, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if cres.JSON201 == nil {
		return c.failed(cres.HTTPResponse, cres.Body)
	}
	deploy := cres.JSON201.Deploy

	var toUpload []localFile
	var uploadBytes int64
	for _, h := range cres.JSON201.Missing {
		f, ok := byHash[h]
		if !ok {
			return fmt.Errorf("server %s asked for a file this deploy does not have", c.server)
		}
		toUpload = append(toUpload, f)
		uploadBytes += f.size
	}
	if len(toUpload) > 0 {
		var sent atomic.Int64
		err = d.uploadAll(ctx, site.Name, deploy.Id, toUpload, func() {
			n := sent.Add(1)
			d.prog.update("Uploading: %d of %d files (%s of %s)", n, len(toUpload), formatBytes(d.uploaded.Load()), formatBytes(uploadBytes))
		})
		if err != nil {
			return err
		}
		d.prog.done("Uploaded %s (%s); %d unchanged.", plural(len(toUpload), "file", "files"), formatBytes(uploadBytes), len(files)-len(toUpload))
	} else {
		d.prog.done("Nothing to upload: the server already has all %s.", plural(len(files), "file", "files"))
	}

	ares, err := c.api.ActivateDeployWithResponse(ctx, site.Name, deploy.Id, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if ares.JSON200 == nil {
		return c.failed(ares.HTTPResponse, ares.Body)
	}
	act := *ares.JSON200
	if r.json {
		return writeJSON(a.stdout, deployResult{
			Activation:  act,
			SiteCreated: created,
			Uploaded:    uploadedStats{Files: len(toUpload), Bytes: uploadBytes},
			Skipped:     skipped,
		})
	}
	verb := "Deployed"
	if created {
		verb = "Created " + act.Site.Name + " and deployed"
	}
	fmt.Fprintf(a.stdout, "%s %s (%s) in %.1fs. Deploy %s is live:\n%s\n", verb, plural(len(files), "file", "files"), formatBytes(total),
		a.now().Sub(start).Seconds(), shortID(act.Deploy.Id), act.Site.Url)
	return nil
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
	case !ok:
		return "", fmt.Errorf("none of your sites on %s is served at %s, and with no sites to go by the CLI cannot tell whether it is under the server's base domain: to create a site, deploy to its name%s", c.server, host, hint)
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
