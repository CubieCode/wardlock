// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// wantRefusal fails unless res is status and code with target and a
// message containing every one of parts.
func wantRefusal(t *testing.T, res response, status int, code, target string, parts ...string) {
	t.Helper()
	res.want(t, status, code)
	e := res.err(t)
	if e.Target == nil || *e.Target != target {
		t.Fatalf("target = %v, want %q; body %s", e.Target, target, res.body)
	}
	for _, p := range parts {
		if !strings.Contains(e.Message, p) {
			t.Fatalf("message %q does not mention %q", e.Message, p)
		}
	}
}

func sized(path string, size int) file { return file{path, strings.Repeat("x", size)} }

func TestVideoLimit(t *testing.T) {
	limits := deploys.Limits{MaxFileSize: 100, MaxFiles: 10, MaxDeploySize: 1000, MaxVideoSize: 10}
	hs := newHarness(t, limits)
	a := hs.ownerA
	hs.createSite(a, "docs")
	for _, path := range []string{"clip.mp4", "media/Intro.MOV", "a.webm", "b.mkv", "c.avi", "d.m4v"} {
		t.Run(path, func(t *testing.T) {
			res := hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(sized("index.html", 1), sized(path, 11)))
			wantRefusal(t, res, http.StatusRequestEntityTooLarge, "limit_exceeded", path, path, "video", "11 bytes", "10 bytes")
		})
	}
	// At the video limit, and non-video files up to the general limit
	// (.ts is TypeScript far more often than video).
	hs.deploy(a, "docs", sized("clip.mp4", 10), sized("app.ts", 100), sized("photo.jpg", 100), sized("mp4", 100))
	// The general limit still applies to video when it is the lower one.
	low := newHarness(t, deploys.Limits{MaxFileSize: 5, MaxFiles: 10, MaxDeploySize: 1000, MaxVideoSize: 10})
	low.createSite(low.ownerA, "blog")
	res := low.do(low.ownerA, http.MethodPost, "/sites/blog/deploys", manifest(sized("clip.mp4", 6)))
	wantRefusal(t, res, http.StatusRequestEntityTooLarge, "limit_exceeded", "clip.mp4", "clip.mp4")
}

func TestVideoRefused(t *testing.T) {
	limits := deploys.DefaultLimits()
	limits.MaxVideoSize = 0
	hs := newHarness(t, limits)
	a := hs.ownerA
	hs.createSite(a, "docs")
	res := hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(sized("index.html", 1), file{"films/tiny.WebM", "x"}))
	wantRefusal(t, res, http.StatusRequestEntityTooLarge, "limit_exceeded", "films/tiny.WebM", "films/tiny.WebM", "does not accept video")
	hs.deploy(a, "docs", sized("index.html", 1))
}

func TestBlockedExtensions(t *testing.T) {
	limits := deploys.DefaultLimits()
	limits.BlockedExtensions = map[string]bool{".exe": true, ".msi": true}
	hs := newHarness(t, limits)
	a := hs.ownerA
	hs.createSite(a, "docs")
	for _, path := range []string{"setup.exe", "tools/Installer.MSI", "x.EXE"} {
		t.Run(path, func(t *testing.T) {
			res := hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(sized("index.html", 1), sized(path, 3)))
			wantRefusal(t, res, http.StatusUnprocessableEntity, "file_type_blocked", path, path, strings.ToLower(path[strings.LastIndexByte(path, '.'):]))
		})
	}
	hs.deploy(a, "docs", sized("index.html", 1), sized("setup.exe.txt", 1), sized("exe", 1), sized("docs/msi/readme.md", 1))
	// Without a list nothing is blocked.
	open := newHarness(t, deploys.DefaultLimits())
	open.createSite(open.ownerA, "docs")
	open.deploy(open.ownerA, "docs", sized("setup.exe", 1))
}

func TestOwnerQuotas(t *testing.T) {
	hs := newQuotaHarness(t, deploys.DefaultLimits(), store.Quotas{StorageBytes: 20, Sites: 1, DeploysPerDay: 2})
	a, b := hs.ownerA, hs.ownerB
	hs.createSite(a, "docs")
	res := hs.do(a, http.MethodPost, "/sites", map[string]string{"name": "blog"})
	wantRefusal(t, res, http.StatusRequestEntityTooLarge, "limit_exceeded", "name", "1 site,")
	// Another owner has their own allowance.
	hs.createSite(b, "blog")

	// 15 bytes fits; 6 more new bytes would pass 20, though the 15 already
	// stored are free to list again.
	hs.deploy(a, "docs", sized("a.txt", 15))
	res = hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(sized("a.txt", 15), file{"b.txt", "yyyyyy"}))
	wantRefusal(t, res, http.StatusRequestEntityTooLarge, "limit_exceeded", "files", "6 bytes", "15 bytes", "20 bytes")
	// The refused deploy did not count towards the daily quota: this is
	// the second of two.
	hs.deploy(a, "docs", sized("a.txt", 15), file{"b.txt", "yyyyy"})
	res = hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(sized("a.txt", 15)))
	wantRefusal(t, res, http.StatusRequestEntityTooLarge, "limit_exceeded", "deploys", "2 deploys", "24 hours")
	// Owner B is untouched by A's usage.
	hs.deploy(b, "blog", sized("a.txt", 20))
}
