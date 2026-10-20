// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// TestAdminQuota sets, shows and clears one owner's quota overrides with
// `wardlockd admin quota` and checks they are what the store enforces.
func TestAdminQuota(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"WARDLOCK_DATA_DIR": dir, "WARDLOCK_SECRET": testSecret,
		"WARDLOCK_MAX_OWNER_STORAGE": "1073741824", "WARDLOCK_MAX_OWNER_SITES": "5",
	}
	getenv := func(k string) string { return env[k] }
	var logs bytes.Buffer
	admin := func(args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		err := run(t.Context(), append([]string{"admin"}, args...), getenv, &out, &logs)
		return out.String(), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := admin(args...)
		if err != nil {
			t.Fatalf("admin %v: %v\n%s", args, err, logs.String())
		}
		return out
	}
	// lines returns the table row for each quota, with runs of spaces
	// squeezed to one.
	row := func(out, quota string) string {
		t.Helper()
		for line := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(line, quota+"  ") {
				return strings.Join(strings.Fields(line), " ")
			}
		}
		t.Fatalf("no %s row in:\n%s", quota, out)
		return ""
	}
	must("create", "--email", "owner@example.test")
	must("create", "--email", "other@example.test")
	db, err := store.Open(t.Context(), filepath.Join(dir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	o, err := db.Owners().GetByEmail(t.Context(), "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	instance := store.Quotas{StorageBytes: 1 << 30, Sites: 5}
	site := store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Sites().Create(t.Context(), site, instance); err != nil {
		t.Fatal(err)
	}
	d := store.Deploy{ID: store.NewID(), SiteID: site.ID, FileCount: 1, TotalSize: 1536, CreatedAt: time.Now()}
	files := []store.DeployFile{{Path: "index.html", SHA256: strings.Repeat("a", 64), Size: 1536}}
	if err := db.Deploys().Create(t.Context(), o.ID, d, files, instance); err != nil {
		t.Fatal(err)
	}

	out := must("quota", "Owner@Example.test")
	for quota, want := range map[string]string{
		"storage":         "storage 1536 bytes (1.5 KiB) 1 GiB instance",
		"sites":           "sites 1 5 instance",
		"deploys per day": "deploys per day 1 unlimited instance",
	} {
		if got := row(out, quota); got != want {
			t.Errorf("quota row %q, want %q", got, want)
		}
	}
	if !strings.Contains(out, "owner@example.test") || !strings.Contains(out, o.ID) {
		t.Fatalf("quota output does not name the owner:\n%s", out)
	}

	out = must("quota", "owner@example.test", "--storage", "5GiB", "--sites", "1", "--deploys-per-day", "0")
	if !strings.Contains(out, "Updated the quotas of owner@example.test") {
		t.Fatalf("quota set output:\n%s", out)
	}
	for quota, want := range map[string]string{
		"storage":         "storage 1536 bytes (1.5 KiB) 5 GiB this owner",
		"sites":           "sites 1 1 this owner",
		"deploys per day": "deploys per day 1 unlimited this owner",
	} {
		if got := row(out, quota); got != want {
			t.Errorf("quota row %q, want %q", got, want)
		}
	}
	// The store enforces the override; the other owner keeps the instance's.
	err = db.Sites().Create(t.Context(), store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "blog", CreatedAt: time.Now()}, instance)
	if !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("second site under a 1-site override: %v", err)
	}
	// Flags not given keep their override; default clears one.
	out = must("quota", "owner@example.test", "--sites", "default")
	if got := row(out, "sites"); got != "sites 1 5 instance" {
		t.Errorf("sites after default: %q", got)
	}
	if got := row(out, "storage"); got != "storage 1536 bytes (1.5 KiB) 5 GiB this owner" {
		t.Errorf("storage kept: %q", got)
	}
	if got := row(must("quota", "other@example.test"), "storage"); got != "storage 0 bytes 1 GiB instance" {
		t.Errorf("other owner's storage: %q", got)
	}

	for _, bad := range [][]string{
		{"quota", "nobody@example.test"}, {"quota", "not an address"},
	} {
		if _, err := admin(bad...); err == nil || errors.Is(err, errUsage) {
			t.Errorf("admin %v = %v, want an error", bad, err)
		}
	}
	for _, bad := range [][]string{
		{"quota"}, {"quota", "--sites", "1"}, {"quota", "owner@example.test", "extra"},
		{"quota", "owner@example.test", "--sites", "-1"}, {"quota", "owner@example.test", "--sites", "lots"},
		{"quota", "owner@example.test", "--storage", "5GB"}, {"quota", "owner@example.test", "--storage", "9999999TiB"},
		{"quota", "owner@example.test", "--bandwidth", "1"},
	} {
		if _, err := admin(bad...); !errors.Is(err, errUsage) {
			t.Errorf("admin %v = %v, want usage", bad, err)
		}
	}

	// Each change is logged, by owner ID only.
	l := logs.String()
	if strings.Count(l, "operator set owner quotas") != 2 || !strings.Contains(l, o.ID) {
		t.Errorf("log of quota changes:\n%s", l)
	}
	for line := range strings.SplitSeq(l, "\n") {
		if strings.Contains(line, "operator ") && strings.Contains(line, "@example.test") {
			t.Errorf("operator action logged an address: %s", line)
		}
	}
}

func TestParseSize(t *testing.T) {
	for raw, want := range map[string]int64{
		"0": 0, "1024": 1024, "5GiB": 5 << 30, "512 MiB": 512 << 20, "1KiB": 1024, "2TiB": 2 << 40,
	} {
		if got, err := parseSize(raw); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "-1", "5GB", "1.5GiB", "GiB", "8388608TiB", "5 gib"} {
		if _, err := parseSize(raw); err == nil {
			t.Errorf("parseSize(%q) succeeded", raw)
		}
	}
}
