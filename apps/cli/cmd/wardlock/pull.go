// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"wardlock.nz/apiclient"
)

// downloadWorkers bounds how many files download at once, as uploads do.
const downloadWorkers = uploadWorkers

// pullResult is pull's --json output (docs/SPEC.md "CLI").
type pullResult struct {
	Site   apiclient.Site   `json:"site"`
	Deploy apiclient.Deploy `json:"deploy"`
	// Dir is the absolute directory the files were written to.
	Dir   string `json:"dir"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

func runPull(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "pull", "Usage: wardlock pull DOMAIN [DIR] [flags]\n\n"+
		"Downloads the files of the site DOMAIN's live deploy, or of any\n"+
		"deploy in its history with --deploy, into DIR (default ./<site>).\n"+
		"Each file is checked against its SHA-256 before it is written.\n"+
		"DIR must be empty or missing unless --force is given; then files\n"+
		"with the same names are replaced and other files are left alone.")
	g := addGlobalFlags(fs)
	deployFlag := fs.String("deploy", "", "pull this deploy: its ID or the short ID wardlock list DOMAIN shows (default the live deploy)")
	pathFlag := fs.String("path", "", "pull only the files under this part of the site, such as /mockup-x/, into DIR as wardlock deploy --path takes them")
	force := fs.Bool("force", false, "write into a DIR that is not empty, replacing files with the same names")
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		fs.Usage()
		return errUsage
	}
	t, err := parseTarget(pos[0])
	if err != nil {
		return err
	}
	want := strings.ToLower(*deployFlag)
	if want != "" && !deployIDPattern.MatchString(want) && !shortDeployPattern.MatchString(want) {
		return usageErrorf("%q is not a deploy ID: use the ID or short ID that wardlock list %s shows", *deployFlag, t)
	}
	prefix := ""
	if p := strings.Trim(norm.NFC.String(*pathFlag), "/"); p != "" {
		prefix = p + "/"
	}
	wd, err := a.getwd()
	if err != nil {
		return err
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return err
	}
	site, err := c.mustFindSite(ctx, t)
	if err != nil {
		return err
	}
	dir := filepath.Join(wd, site.Name)
	if len(pos) == 2 {
		if dir = pos[1]; !filepath.IsAbs(dir) {
			dir = filepath.Join(wd, dir)
		}
	}
	deploy, err := c.pullDeploy(ctx, site, want)
	if err != nil {
		return err
	}
	files, err := c.deployFiles(ctx, site.Name, deploy.Id)
	if err != nil {
		return err
	}
	plan, err := planPull(files, prefix, runtime.GOOS)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		if prefix != "" {
			return fmt.Errorf("deploy %s of %s has no files under /%s", shortID(deploy.Id), site.Name, prefix)
		}
		return fmt.Errorf("deploy %s of %s has no files", shortID(deploy.Id), site.Name)
	}
	shown := displayDir(wd, dir)
	if err := readyDir(dir, shown, *force); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("pull into %s: %w", shown, err)
	}
	defer func() { _ = root.Close() }()

	p := &puller{a: a, c: c, root: root, site: site.Name, deploy: deploy.Id, prog: &progress{w: a.stderr, live: a.liveProgress, now: a.now}}
	start := a.now()
	if err := p.pullAll(ctx, plan); err != nil {
		return err
	}
	written := len(plan) - len(p.withheld)
	bytes := p.written.Load()
	p.prog.done("Downloaded %s (%s).", plural(written, "file", "files"), formatBytes(bytes))
	if len(p.withheld) > 0 {
		slices.Sort(p.withheld)
		for _, path := range p.withheld {
			fmt.Fprintf(a.stderr, "Not pulled: the operator of %s has withheld %s\n", c.server, path)
		}
		return fmt.Errorf("%s withheld by the server's operator not pulled; the others are in %s", plural(len(p.withheld), "file", "files"), shown)
	}
	if g.json {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		return writeJSON(a.stdout, pullResult{Site: site, Deploy: deploy, Dir: abs, Files: written, Bytes: bytes})
	}
	fmt.Fprintf(a.stdout, "Pulled %s (%s) of deploy %s of %s into %s in %.1fs.\n", plural(written, "file", "files"), formatBytes(bytes),
		shortID(deploy.Id), site.Name, shown, a.now().Sub(start).Seconds())
	return nil
}

// pullDeploy returns the deploy to pull: want (an ID or short ID), or with
// "" the site's live deploy.
func (c *conn) pullDeploy(ctx context.Context, site apiclient.Site, want string) (apiclient.Deploy, error) {
	id := want
	if id == "" {
		if site.ActiveDeployId == nil {
			return apiclient.Deploy{}, fmt.Errorf("nothing is live on %s: pass --deploy with an ID wardlock list %s shows", site.Name, site.Name)
		}
		id = *site.ActiveDeployId
	}
	deploys, err := c.deploys(ctx, site.Name)
	if err != nil {
		return apiclient.Deploy{}, err
	}
	var found []apiclient.Deploy
	for _, d := range deploys {
		if d.Id == id || (!deployIDPattern.MatchString(id) && strings.HasSuffix(d.Id, id)) {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 0:
		return apiclient.Deploy{}, fmt.Errorf("site %s has no deploy %s: wardlock list %s shows its deploys", site.Name, id, site.Name)
	case 1:
		return found[0], nil
	}
	return apiclient.Deploy{}, fmt.Errorf("%s matches %d deploys of %s: give the full ID", id, len(found), site.Name)
}

// deployFiles lists every file of a deploy, ordered by path.
func (c *conn) deployFiles(ctx context.Context, site, deployID string) ([]apiclient.DeployFile, error) {
	var all []apiclient.DeployFile
	var cursor *string
	for {
		res, err := c.api.ListDeployFilesWithResponse(ctx, site, deployID, &apiclient.ListDeployFilesParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return nil, c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return nil, c.failed(res.HTTPResponse, res.Body)
		}
		all = append(all, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			return all, nil
		}
	}
}

// pullFile is one file to pull: the API's file and where it goes in DIR.
type pullFile struct {
	apiclient.DeployFile
	// name is the local path relative to DIR, with the OS's separators.
	name string
}

// planPull checks every path under prefix (a directory such as docs/, or
// "" for all) and returns the files with their names in DIR, relative to
// the prefix. Any path the file system of goos cannot hold, or that would
// leave DIR, stops the pull before anything is written, naming the path.
func planPull(files []apiclient.DeployFile, prefix, goos string) ([]pullFile, error) {
	var plan []pullFile
	foldCase := goos == "windows" || goos == "darwin"
	seen := map[string]string{} // folded relative path to the path that took it
	isFile := map[string]bool{}
	for _, f := range files {
		rel, ok := strings.CutPrefix(f.Path, prefix)
		if !ok {
			continue
		}
		if err := checkLocalPath(rel, goos); err != nil {
			return nil, fmt.Errorf("cannot pull %s: %w", f.Path, err)
		}
		key := rel
		if foldCase {
			// Case-insensitive and, on macOS, normalisation-insensitive.
			key = strings.ToLower(norm.NFC.String(rel))
		}
		if other, ok := seen[key]; ok {
			return nil, fmt.Errorf("cannot pull %s: it would be the same file as %s on this computer", f.Path, prefix+other)
		}
		seen[key] = rel
		isFile[key] = true
		plan = append(plan, pullFile{DeployFile: f, name: filepath.FromSlash(rel)})
	}
	// A path that is a file cannot also be a directory.
	for key, rel := range seen {
		for dir := key; ; {
			i := strings.LastIndexByte(dir, '/')
			if i < 0 {
				break
			}
			dir = dir[:i]
			if isFile[dir] {
				return nil, fmt.Errorf("cannot pull %s: %s is a file in this deploy, so it cannot also be a directory", prefix+rel, prefix+seen[dir])
			}
		}
	}
	return plan, nil
}

// windowsReserved are device names Windows refuses as a file name, with or
// without an extension.
var windowsReserved = func() map[string]bool {
	m := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "conin$": true, "conout$": true}
	for _, d := range "0123456789¹²³" {
		m["com"+string(d)] = true
		m["lpt"+string(d)] = true
	}
	return m
}()

// checkLocalPath reports why rel, a slash-separated path from the server,
// cannot be written under DIR on goos, or nil. The server already refuses
// most of these; the CLI checks again rather than trusting it.
func checkLocalPath(rel, goos string) error {
	switch {
	case rel == "":
		return errors.New("the path is empty")
	case !utf8.ValidString(rel):
		return errors.New("the path is not valid UTF-8")
	case strings.HasPrefix(rel, "/"):
		return errors.New("the path is absolute")
	case strings.Contains(rel, `\`):
		return errors.New(`the path contains \`)
	}
	for seg := range strings.SplitSeq(rel, "/") {
		switch {
		case seg == "":
			return errors.New("the path has an empty segment")
		case seg == "." || seg == "..":
			return fmt.Errorf("the path has a %q segment", seg)
		case len(seg) > 255:
			return fmt.Errorf("the name %.20s… is longer than file systems allow", seg)
		}
		for _, r := range seg {
			if unicode.IsControl(r) {
				return errors.New("the path contains a control character")
			}
		}
		if goos != "windows" {
			continue
		}
		if i := strings.IndexAny(seg, `<>:"|?*`); i >= 0 {
			return fmt.Errorf("a name with %q cannot be held on Windows", seg[i:i+1])
		}
		if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return errors.New("a name ending with a dot or space cannot be held on Windows")
		}
		base, _, _ := strings.Cut(seg, ".")
		if windowsReserved[strings.ToLower(strings.TrimRight(base, " "))] {
			return fmt.Errorf("%s is a device name on Windows", seg)
		}
	}
	if goos == runtime.GOOS && !filepath.IsLocal(filepath.FromSlash(rel)) {
		return errors.New("the path would leave the directory")
	}
	return nil
}

// readyDir creates dir, or with an existing one checks it is a directory
// and empty unless force.
func readyDir(dir, shown string, force bool) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("pull into %s: %w", shown, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("pull into %s: %w", shown, err)
	case len(entries) > 0 && !force:
		return fmt.Errorf("%s is not empty: pull into a new or empty directory, or pass --force to replace files with the same names", shown)
	}
	return nil
}

// errWithheld is a download's 451 file_withheld.
var errWithheld = errors.New("withheld")

type puller struct {
	a            *app
	c            *conn
	root         *os.Root
	site, deploy string
	prog         *progress
	// written counts bytes written; withheld lists the paths refused
	// with 451.
	written  atomic.Int64
	mu       sync.Mutex
	withheld []string
}

// pullAll downloads plan, downloadWorkers at a time, retrying as deploy
// retries uploads.
func (p *puller) pullAll(ctx context.Context, plan []pullFile) error {
	hc := *p.a.http
	hc.Timeout = uploadTimeout
	api, err := apiclient.NewClient(p.c.server, apiclient.WithHTTPClient(&hc))
	if err != nil {
		return err
	}
	var total int64
	for _, f := range plan {
		total += f.Size
	}
	var done atomic.Int64
	return forEach(ctx, len(plan), downloadWorkers, func(ctx context.Context, i int) error {
		f := plan[i]
		for attempt := 1; ; attempt++ {
			retry, wait, err := p.pull(ctx, api, f)
			if errors.Is(err, errWithheld) {
				p.mu.Lock()
				p.withheld = append(p.withheld, f.Path)
				p.mu.Unlock()
				return nil
			}
			if err == nil {
				n := done.Add(1)
				p.prog.update("Downloading: %d of %d files (%s of %s)", n, len(plan), formatBytes(p.written.Load()), formatBytes(total))
				return nil
			}
			if !retry || attempt == uploadAttempts {
				return err
			}
			if wait == 0 {
				wait = time.Duration(attempt) * time.Second
			}
			if err := p.a.sleep(ctx, wait); err != nil {
				return errors.New("cancelled")
			}
		}
	})
}

// pull downloads one file into place. On failure it says whether a retry
// is worth trying, and after how long when the server said (otherwise
// wait is 0).
func (p *puller) pull(ctx context.Context, api *apiclient.Client, f pullFile) (retry bool, wait time.Duration, err error) {
	res, err := api.DownloadDeployBlob(ctx, p.site, p.deploy, f.Sha256, nil, p.c.auth)
	if err != nil {
		return ctx.Err() == nil, 0, fmt.Errorf("download %s: %w", f.Path, p.c.unreachable(ctx, err))
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		failed := fmt.Errorf("download %s: %w", f.Path, p.c.failed(res, body))
		switch res.StatusCode {
		case http.StatusUnavailableForLegalReasons:
			if errorCode(body) == "file_withheld" {
				return false, 0, errWithheld
			}
		case http.StatusTooManyRequests:
			return true, retryAfter(res), failed
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true, 0, failed
		}
		return false, 0, failed
	}
	if err := p.write(f, res.Body); err != nil {
		var ne netError
		if errors.As(err, &ne) {
			return ctx.Err() == nil, 0, fmt.Errorf("download %s: %w", f.Path, p.c.unreachable(ctx, ne.err))
		}
		return false, 0, err
	}
	return false, 0, nil
}

// netError is a failure reading the response body, worth a retry.
type netError struct{ err error }

func (e netError) Error() string { return e.err.Error() }

// write streams body into a temporary file beside f's name, checks its
// size and SHA-256, then renames it into place, so f's name only ever
// holds a whole, verified file.
func (p *puller) write(f pullFile, body io.Reader) error {
	dir := filepath.Dir(f.name)
	if err := p.root.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("write %s: %w", f.Path, err)
	}
	tmp := filepath.Join(dir, ".wardlock-pull-"+rand.Text())
	out, err := p.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", f.Path, err)
	}
	renamed := false
	defer func() {
		_ = out.Close()
		if !renamed {
			_ = p.root.Remove(tmp)
		}
	}()
	sum := sha256.New()
	var counted atomic.Int64
	n, err := io.Copy(io.MultiWriter(out, sum), countingReader{r: io.LimitReader(body, f.Size+1), n: &counted, total: &p.written})
	if err != nil {
		p.written.Add(-counted.Load())
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		return netError{err}
	}
	if n != f.Size || hex.EncodeToString(sum.Sum(nil)) != f.Sha256 {
		p.written.Add(-n)
		return fmt.Errorf("%s did not match its SHA-256 from %s (got %s), so it was not written: run the pull again", f.Path, p.c.server, formatBytes(n))
	}
	if err := out.Close(); err != nil {
		p.written.Add(-n)
		return fmt.Errorf("write %s: %w", f.Path, err)
	}
	if err := p.root.Rename(tmp, f.name); err != nil {
		p.written.Add(-n)
		return fmt.Errorf("write %s: %w", f.Path, err)
	}
	renamed = true
	return nil
}
