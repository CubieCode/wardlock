// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// Path deploys (C1.13): a deploy that replaces one prefix of a site.

func TestNormalisePrefix(t *testing.T) {
	for in, want := range map[string]string{
		"/mockup-x/":                          "/mockup-x/",
		"/a/b/":                               "/a/b/",
		`/a\b/`:                               "/a/b/",
		"/x/":                                 "/x/",
		"/café/":                              "/café/",
		"/.well-known/":                       "/.well-known/",
		"/x/.wardlock/":                       "/x/.wardlock/",
		"/with space/":                        "/with space/",
		"/" + strings.Repeat("a", 1022) + "/": "/" + strings.Repeat("a", 1022) + "/",
	} {
		got, err := deploys.NormalisePrefix(in)
		if err != nil || got != want {
			t.Errorf("NormalisePrefix(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "/", "//", `\`, "mockup-x", "mockup-x/", "/mockup-x", "/a//b/", "/./", "/../", "/a/../b/", "/a/./",
		"/.wardlock/", "/.WardLock/x/", "/a\x00/", "/a​/", "/\xff/", "/" + strings.Repeat("a", 1025) + "/",
	} {
		_, err := deploys.NormalisePrefix(in)
		var te *deploys.TargetError
		if !errors.Is(err, deploys.ErrInvalidPath) || !errors.As(err, &te) || te.Target != "path" || te.Message == "" {
			t.Errorf("NormalisePrefix(%q) error = %#v, want ErrInvalidPath with target path", in, err)
		}
	}
}

type pathWorld struct {
	t      *testing.T
	db     *store.DB
	svc    *deploys.Service
	owner  store.Owner
	bodies map[string]string // hash to body, for uploads
	clock  time.Time
}

func newPathWorld(t *testing.T, opts ...func(*deploys.Config)) *pathWorld {
	t.Helper()
	w := &pathWorld{t: t, db: storetest.New(t), bodies: map[string]string{}, clock: gcEpoch}
	c := deploys.Config{
		Sites: w.db.Sites(), Deploys: w.db.Deploys(), Blobs: deploys.NewFSBlobStore(t.TempDir()),
		Notifier: deploys.NewLocalNotifier(), Limits: deploys.DefaultLimits(),
		Now: func() time.Time { w.clock = w.clock.Add(time.Second); return w.clock },
	}
	for _, o := range opts {
		o(&c)
	}
	w.svc = deploys.NewService(c)
	w.owner = w.newOwner("owner@example.test")
	return w
}

func (w *pathWorld) newOwner(email string) store.Owner {
	w.t.Helper()
	o := store.Owner{ID: store.NewID(), Email: email, CreatedAt: gcEpoch}
	if err := w.db.Owners().Create(w.t.Context(), o); err != nil {
		w.t.Fatal(err)
	}
	return o
}

func (w *pathWorld) site(ownerID, name string) {
	w.t.Helper()
	if err := w.db.Sites().Create(w.t.Context(), store.Site{ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: gcEpoch}, store.Quotas{}); err != nil {
		w.t.Fatal(err)
	}
}

// manifest lists files (path to body), remembering each body for upload.
func (w *pathWorld) manifest(files map[string]string) deploys.Manifest {
	m := deploys.Manifest{Files: []store.DeployFile{}}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		h := hashOf(files[p])
		w.bodies[h] = files[p]
		m.Files = append(m.Files, store.DeployFile{Path: p, SHA256: h, Size: int64(len(files[p]))})
	}
	return m
}

// create creates a deploy and uploads what it is missing, returning the
// deploy and the hashes that were missing.
func (w *pathWorld) create(ownerID, site string, m deploys.Manifest) (store.Deploy, []string, error) {
	w.t.Helper()
	d, missing, err := w.svc.Create(w.t.Context(), ownerID, site, m)
	if err != nil {
		return d, nil, err
	}
	for _, h := range missing {
		if err := w.svc.Upload(w.t.Context(), ownerID, site, d.ID, h, strings.NewReader(w.bodies[h])); err != nil {
			w.t.Fatal(err)
		}
	}
	return d, missing, nil
}

// live creates and activates a deploy of files, under path when given.
func (w *pathWorld) live(site, path string, files map[string]string) store.Deploy {
	w.t.Helper()
	m := w.manifest(files)
	m.Path = path
	d, _, err := w.create(w.owner.ID, site, m)
	if err != nil {
		w.t.Fatalf("create %s %q: %v", site, path, err)
	}
	if _, d, err = w.svc.Activate(w.t.Context(), w.owner.ID, site, d.ID); err != nil {
		w.t.Fatalf("activate %s %q: %v", site, path, err)
	}
	return d
}

// files returns a deploy's manifest by path.
func (w *pathWorld) files(deployID string) map[string]store.DeployFile {
	w.t.Helper()
	list, err := w.db.Deploys().Files(w.t.Context(), deployID)
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]store.DeployFile{}
	for _, f := range list {
		out[f.Path] = f
	}
	return out
}

// served returns the site's active deploy ID and its files as path to body.
func (w *pathWorld) served(site string) (string, map[string]string) {
	w.t.Helper()
	s, err := w.db.Sites().GetByOwnerName(w.t.Context(), w.owner.ID, site)
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]string{}
	for p, f := range w.files(s.ActiveDeployID) {
		out[p] = w.bodies[f.SHA256]
	}
	return s.ActiveDeployID, out
}

var basePages = map[string]string{
	"index.html":          "<h1>Home</h1>",
	"about.html":          "<h1>About</h1>",
	"mockup-x/old.html":   "<h1>Old mockup</h1>",
	"mockup-x/img/a.png":  "png-a",
	"mockup-xy/page.html": "<h1>A neighbour, not under the prefix</h1>",
	"mockup-x":            "a file named like the prefix, outside it",
}

func TestPathDeployReplacesOnlyItsPrefix(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	base := w.live("docs", "", basePages)
	baseFiles := w.files(base.ID)

	m := w.manifest(map[string]string{"index.html": "<h1>New mockup</h1>", "img/a.png": "png-a", "img/b.png": "png-b"})
	m.Path = "/mockup-x/"
	d, missing, err := w.create(w.owner.ID, "docs", m)
	if err != nil {
		t.Fatal(err)
	}
	// Only the new bodies are asked for: the base's are already held.
	if want := []string{hashOf("<h1>New mockup</h1>"), hashOf("png-b")}; !sameSet(missing, want) {
		t.Errorf("missing = %v, want %v", missing, want)
	}
	if d.PathPrefix != "/mockup-x/" || d.BaseDeployID != base.ID {
		t.Errorf("deploy prefix %q base %q, want /mockup-x/ on %s", d.PathPrefix, d.BaseDeployID, base.ID)
	}
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "docs", d.ID); err != nil {
		t.Fatal(err)
	}
	got := w.files(d.ID)
	for p, f := range baseFiles {
		if strings.HasPrefix(p, "mockup-x/") {
			continue
		}
		// Outside the prefix: the same path, hash and size as the base.
		if got[p] != f {
			t.Errorf("%s = %+v, want the base's %+v", p, got[p], f)
		}
	}
	var under []string
	for p := range got {
		if strings.HasPrefix(p, "mockup-x/") {
			under = append(under, p)
		}
	}
	if want := []string{"mockup-x/img/a.png", "mockup-x/img/b.png", "mockup-x/index.html"}; !sameSet(under, want) {
		t.Errorf("under the prefix: %v, want exactly the upload %v", under, want)
	}
	if want := len(baseFiles) - 2 + 3; len(got) != want || d.FileCount != want {
		t.Errorf("deploy has %d files (FileCount %d), want %d", len(got), d.FileCount, want)
	}
	var total int64
	for _, f := range got {
		total += f.Size
	}
	if d.TotalSize != total {
		t.Errorf("TotalSize = %d, want %d, the merged deploy's", d.TotalSize, total)
	}
	stored, err := w.db.Deploys().Get(t.Context(), w.owner.ID, d.SiteID, d.ID)
	if err != nil || stored.PathPrefix != "/mockup-x/" {
		t.Errorf("stored deploy prefix %q, %v", stored.PathPrefix, err)
	}
}

func TestEmptyPathDeployRemovesThePrefix(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	w.live("docs", "", basePages)
	d := w.live("docs", "/mockup-x/", nil)
	_, served := w.served("docs")
	want := maps.Clone(basePages)
	delete(want, "mockup-x/old.html")
	delete(want, "mockup-x/img/a.png")
	if !maps.Equal(served, want) {
		t.Fatalf("served %v, want %v", slices.Sorted(maps.Keys(served)), slices.Sorted(maps.Keys(want)))
	}
	if d.FileCount != len(want) {
		t.Errorf("FileCount = %d, want %d", d.FileCount, len(want))
	}
	// Removing the only files leaves an empty deploy, which is allowed for
	// a path deploy and only for one.
	w.site(w.owner.ID, "mock")
	w.live("mock", "", map[string]string{"p/index.html": "p"})
	if d := w.live("mock", "/p/", nil); d.FileCount != 0 {
		t.Errorf("FileCount = %d, want 0", d.FileCount)
	}
	if _, _, err := w.svc.Create(t.Context(), w.owner.ID, "mock", deploys.Manifest{}); !errors.Is(err, deploys.ErrInvalidManifest) {
		t.Errorf("empty full deploy: %v, want ErrInvalidManifest", err)
	}
}

func TestPathDeployToASiteWithNothingLive(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "fresh")
	d := w.live("fresh", "/mockup-x/", map[string]string{"index.html": "hi"})
	if d.BaseDeployID != "" {
		t.Errorf("base %q, want none", d.BaseDeployID)
	}
	if _, served := w.served("fresh"); !maps.Equal(served, map[string]string{"mockup-x/index.html": "hi"}) {
		t.Fatalf("served %v", served)
	}
}

func TestPathDeployFlagsFollowTheBase(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "app")
	m := w.manifest(map[string]string{"index.html": "app"})
	m.SPA, m.ImmutableAssets = new(true), new(true)
	d, _, err := w.create(w.owner.ID, "app", m)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "app", d.ID); err != nil {
		t.Fatal(err)
	}
	inherited := w.live("app", "/x/", map[string]string{"a.html": "a"})
	if !inherited.SPA || !inherited.ImmutableAssets {
		t.Errorf("SPA %v ImmutableAssets %v, want both from the base", inherited.SPA, inherited.ImmutableAssets)
	}
	m = w.manifest(map[string]string{"b.html": "b"})
	m.Path, m.SPA = "/y/", new(false)
	given, _, err := w.create(w.owner.ID, "app", m)
	if err != nil {
		t.Fatal(err)
	}
	if given.SPA || !given.ImmutableAssets {
		t.Errorf("SPA %v ImmutableAssets %v, want the given false and the base's true", given.SPA, given.ImmutableAssets)
	}
}

func TestStaleBaseIsRefusedAndRecreatingSucceeds(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	w.live("docs", "", map[string]string{"index.html": "home"})
	mx := w.manifest(map[string]string{"index.html": "x"})
	mx.Path = "/mockup-x/"
	my := w.manifest(map[string]string{"index.html": "y"})
	my.Path = "/mockup-y/"
	dx, _, err := w.create(w.owner.ID, "docs", mx)
	if err != nil {
		t.Fatal(err)
	}
	dy, _, err := w.create(w.owner.ID, "docs", my)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "docs", dx.ID); err != nil {
		t.Fatal(err)
	}
	// Activating y now would drop x's files.
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "docs", dy.ID); !errors.Is(err, deploys.ErrStaleBase) {
		t.Fatalf("activate on a stale base: %v, want ErrStaleBase", err)
	}
	// What the CLI does: create it again, which uploads nothing, and activate.
	retry, missing, err := w.create(w.owner.ID, "docs", my)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 || retry.BaseDeployID != dx.ID {
		t.Fatalf("retry missing %v base %q, want nothing on %s", missing, retry.BaseDeployID, dx.ID)
	}
	if _, _, err := w.svc.Activate(t.Context(), w.owner.ID, "docs", retry.ID); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"index.html": "home", "mockup-x/index.html": "x", "mockup-y/index.html": "y"}
	if _, served := w.served("docs"); !maps.Equal(served, want) {
		t.Fatalf("served %v, want %v", served, want)
	}
}

func TestPathDeployLimitsCountTheMergedDeploy(t *testing.T) {
	limits := deploys.DefaultLimits()
	limits.MaxFiles = 4
	limits.MaxDeploySize = 100
	limits.MaxVideoSize = 10
	w := newPathWorld(t, func(c *deploys.Config) { c.Limits = limits })
	w.site(w.owner.ID, "docs")
	w.live("docs", "", map[string]string{"a.html": "a", "b.html": "b", "c.html": strings.Repeat("c", 60)})

	for _, tc := range []struct {
		name  string
		files map[string]string
		err   error
	}{
		{"one file too many", map[string]string{"1.html": "1", "2.html": "2"}, deploys.ErrLimitExceeded},
		{"over the deploy size", map[string]string{"big.html": strings.Repeat("x", 41)}, deploys.ErrLimitExceeded},
		{"video over its limit", map[string]string{"clip.mp4": strings.Repeat("v", 11)}, deploys.ErrLimitExceeded},
		{"fits", map[string]string{"1.html": "1"}, nil},
	} {
		m := w.manifest(tc.files)
		m.Path = "/p/"
		if _, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m); !errors.Is(err, tc.err) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.err)
		}
	}

	// A type blocked after the base went live refuses a path deploy that
	// keeps it, as it would a full deploy listing it.
	limits.BlockedExtensions = map[string]bool{".exe": true}
	w2 := newPathWorld(t, func(c *deploys.Config) { c.Limits = deploys.DefaultLimits() })
	w2.site(w2.owner.ID, "tools")
	w2.live("tools", "", map[string]string{"setup.exe": "mz"})
	strict := deploys.NewService(deploys.Config{
		Sites: w2.db.Sites(), Deploys: w2.db.Deploys(), Blobs: deploys.NewFSBlobStore(t.TempDir()),
		Notifier: deploys.NewLocalNotifier(), Limits: limits, Now: time.Now,
	})
	m := w2.manifest(map[string]string{"index.html": "x"})
	m.Path = "/x/"
	var te *deploys.TargetError
	if _, _, err := strict.Create(t.Context(), w2.owner.ID, "tools", m); !errors.Is(err, deploys.ErrBlockedType) || !errors.As(err, &te) || te.Target != "setup.exe" {
		t.Errorf("blocked file kept from the base: %v, want ErrBlockedType naming setup.exe", err)
	}
}

func TestPathDeployQuotas(t *testing.T) {
	w := newPathWorld(t, func(c *deploys.Config) { c.Quotas = store.Quotas{StorageBytes: 20, DeploysPerDay: 3} })
	w.site(w.owner.ID, "docs")
	w.live("docs", "", map[string]string{"index.html": strings.Repeat("h", 15)})

	// The base's files are already stored: reusing one under the prefix
	// costs nothing, while a new file counts.
	m := w.manifest(map[string]string{"copy.html": strings.Repeat("h", 15)})
	m.Path = "/p/"
	if _, _, err := w.create(w.owner.ID, "docs", m); err != nil {
		t.Fatalf("path deploy of stored content: %v", err)
	}
	m = w.manifest(map[string]string{"new.html": strings.Repeat("n", 6)})
	m.Path = "/p/"
	var te *deploys.TargetError
	if _, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m); !errors.Is(err, deploys.ErrLimitExceeded) || !errors.As(err, &te) || te.Target != "files" {
		t.Fatalf("over storage: %v, want the storage quota", err)
	}
	// Each path deploy counts towards the daily quota like any deploy.
	m = w.manifest(map[string]string{"small.html": "s"})
	m.Path = "/q/"
	if _, _, err := w.create(w.owner.ID, "docs", m); err != nil {
		t.Fatal(err)
	}
	m.Path = "/r/"
	if _, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m); !errors.Is(err, deploys.ErrLimitExceeded) || !errors.As(err, &te) || te.Target != "deploys" {
		t.Fatalf("fourth deploy in a day: %v, want the daily quota", err)
	}
}

func TestRollbackAcrossPathAndFullDeploys(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	full := w.live("docs", "", map[string]string{"index.html": "v1"})
	path := w.live("docs", "/m/", map[string]string{"index.html": "mock"})
	w.live("docs", "", map[string]string{"index.html": "v2"})

	for _, step := range []struct {
		to   store.Deploy
		want map[string]string
	}{
		{path, map[string]string{"index.html": "v1", "m/index.html": "mock"}},
		{full, map[string]string{"index.html": "v1"}},
		{path, map[string]string{"index.html": "v1", "m/index.html": "mock"}},
	} {
		if _, _, err := w.svc.Rollback(t.Context(), w.owner.ID, "docs", step.to.ID); err != nil {
			t.Fatal(err)
		}
		if id, served := w.served("docs"); id != step.to.ID || !maps.Equal(served, step.want) {
			t.Fatalf("after rollback to %s: %s serves %v, want %v", step.to.ID, id, served, step.want)
		}
	}
	// A path deploy built on the rolled-back site keeps what is live now.
	w.live("docs", "/n/", map[string]string{"index.html": "n"})
	if _, served := w.served("docs"); !maps.Equal(served, map[string]string{"index.html": "v1", "m/index.html": "mock", "n/index.html": "n"}) {
		t.Fatalf("served %v", served)
	}
}

func TestPathDeployOwnerIsolation(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "alpha")
	base := w.live("alpha", "", map[string]string{"secret.html": "alpha's"})
	other := w.newOwner("other@example.test")
	w.site(other.ID, "beta")

	m := w.manifest(map[string]string{"index.html": "x"})
	m.Path = "/x/"
	// Another owner's site is as missing as one that does not exist.
	for _, site := range []string{"alpha", "nowhere"} {
		if _, _, err := w.svc.Create(t.Context(), other.ID, site, m); !errors.Is(err, deploys.ErrNotFound) {
			t.Errorf("create on %s: %v, want ErrNotFound", site, err)
		}
	}
	// A path deploy on alpha cannot be activated by the other owner, even
	// through their own site.
	d, _, err := w.create(w.owner.ID, "alpha", m)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"alpha", "beta"} {
		if _, _, err := w.svc.Activate(t.Context(), other.ID, site, d.ID); !errors.Is(err, deploys.ErrNotFound) {
			t.Errorf("activate on %s: %v, want ErrNotFound", site, err)
		}
	}
	// Their own path deploy never takes alpha's files as its base.
	m.Path = "/y/"
	theirs, _, err := w.create(other.ID, "beta", m)
	if err != nil {
		t.Fatal(err)
	}
	if theirs.BaseDeployID != "" {
		t.Errorf("base %q, want none (beta has nothing live, not %s)", theirs.BaseDeployID, base.ID)
	}
	if got := w.files(theirs.ID); len(got) != 1 || got["y/index.html"].Path == "" {
		t.Errorf("beta's deploy lists %v", got)
	}
}

func TestPathDeployRejectsBadPaths(t *testing.T) {
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	for _, tc := range []struct {
		name, path, file, target string
	}{
		{"root", "/", "a.html", "path"},
		{"no leading slash", "x/", "a.html", "path"},
		{"no trailing slash", "/x", "a.html", "path"},
		{"reserved", "/.wardlock/", "a.html", "path"},
		{"dot segment", "/x/../", "a.html", "path"},
		{"bad file under the prefix", "/x/", "../a.html", "../a.html"},
		{"leading slash file", "/x/", "/a.html", "/a.html"},
	} {
		m := w.manifest(map[string]string{tc.file: "a"})
		m.Path = tc.path
		_, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m)
		var te *deploys.TargetError
		if !errors.Is(err, deploys.ErrInvalidPath) || !errors.As(err, &te) || te.Target != tc.target {
			t.Errorf("%s: %v, want ErrInvalidPath with target %q", tc.name, err, tc.target)
		}
	}
	// The merged path is held to the length limit.
	m := w.manifest(map[string]string{strings.Repeat("a", 1020): "a"})
	m.Path = "/long/"
	if _, _, err := w.svc.Create(t.Context(), w.owner.ID, "docs", m); !errors.Is(err, deploys.ErrInvalidPath) {
		t.Errorf("merged path over 1024 bytes: %v, want ErrInvalidPath", err)
	}
}

func sameSet(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}
