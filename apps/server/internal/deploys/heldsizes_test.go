// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// Listed sizes checked against the blobs the owner already holds (C1.16).

const heldBody = "<h1>Held</h1>"

// heldWorld is a pathWorld whose owner holds heldBody, uploaded through a
// live deploy of site "docs".
func heldWorld(t *testing.T) *pathWorld {
	t.Helper()
	w := newPathWorld(t)
	w.site(w.owner.ID, "docs")
	w.live("docs", "", map[string]string{"index.html": heldBody})
	return w
}

// listing is a manifest of one file at p whose hash is body's but whose
// size is given.
func listing(p, body string, size int64) deploys.Manifest {
	return deploys.Manifest{Files: []store.DeployFile{{Path: p, SHA256: hashOf(body), Size: size}}}
}

// wantSizeRefused checks err is the refusal for target listed at listed
// bytes against a held blob of stored bytes.
func wantSizeRefused(t *testing.T, err error, target string, listed, stored int) deploys.TargetError {
	t.Helper()
	var te *deploys.TargetError
	if !errors.Is(err, deploys.ErrInvalidManifest) || !errors.As(err, &te) {
		t.Fatalf("Create error = %v, want ErrInvalidManifest", err)
	}
	if te.Target != target {
		t.Errorf("target = %q, want %q", te.Target, target)
	}
	if !strings.Contains(te.Message, target) || !strings.Contains(te.Message, fmt.Sprintf("size %d", listed)) || !strings.Contains(te.Message, fmt.Sprintf("size %d", stored)) {
		t.Errorf("message %q does not name the path and both sizes", te.Message)
	}
	return *te
}

func TestCreateChecksHeldSizes(t *testing.T) {
	n := len(heldBody)
	for _, tc := range []struct {
		name string
		size int
	}{
		{"smaller", n - 1},
		{"larger", n + 1},
		{"empty", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := heldWorld(t)
			_, _, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "docs", listing("copy.html", heldBody, int64(tc.size)))
			wantSizeRefused(t, err, "copy.html", tc.size, n)
			// Nothing was recorded: the site's only deploy is the live one.
			_, list, err := w.svc.List(t.Context(), store.ActorFor(w.owner.ID), "docs", store.DeployCursor{}, 10)
			if err != nil || len(list) != 1 {
				t.Errorf("deploys after refusal = %d, %v; want 1", len(list), err)
			}
		})
	}
	t.Run("correct size", func(t *testing.T) {
		w := heldWorld(t)
		_, missing, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "docs", listing("copy.html", heldBody, int64(n)))
		if err != nil || len(missing) != 0 {
			t.Fatalf("Create = missing %v, %v; want nothing to upload", missing, err)
		}
	})
	t.Run("another site of the same owner", func(t *testing.T) {
		w := heldWorld(t)
		w.site(w.owner.ID, "blog")
		_, _, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "blog", listing("index.html", heldBody, int64(n+1)))
		wantSizeRefused(t, err, "index.html", n+1, n)
	})
	t.Run("held only by another owner", func(t *testing.T) {
		w := heldWorld(t)
		other := w.newOwner("other@example.test")
		w.site(other.ID, "theirs")
		// Not compared: the other owner has to upload it, and the upload
		// is checked against the listed size, as before C1.16.
		_, missing, err := w.svc.Create(t.Context(), store.ActorFor(other.ID), "theirs", listing("index.html", heldBody, int64(n+1)))
		if err != nil || !slices.Equal(missing, []string{hashOf(heldBody)}) {
			t.Fatalf("Create = missing %v, %v; want the hash to upload", missing, err)
		}
	})
}

func TestCreateHeldSizesPathDeploy(t *testing.T) {
	n := len(heldBody)
	// notesWorld also has site "notes", live without heldBody, so only the
	// held-size check (not the manifest's own one-size-per-hash rule) can
	// catch a path deploy on it listing heldBody at the wrong size.
	notesWorld := func(t *testing.T) *pathWorld {
		t.Helper()
		w := heldWorld(t)
		w.site(w.owner.ID, "notes")
		w.live("notes", "", map[string]string{"index.html": "<h1>Notes</h1>"})
		return w
	}
	t.Run("uploaded part is checked", func(t *testing.T) {
		w := notesWorld(t)
		m := listing("copy.html", heldBody, int64(n-1))
		m.Path = "/mockup/"
		_, _, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "notes", m)
		wantSizeRefused(t, err, "mockup/copy.html", n-1, n)
	})
	t.Run("uploaded part at the right size", func(t *testing.T) {
		w := notesWorld(t)
		m := listing("copy.html", heldBody, int64(n))
		m.Path = "/mockup/"
		_, missing, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "notes", m)
		if err != nil || len(missing) != 0 {
			t.Fatalf("Create = missing %v, %v; want nothing to upload", missing, err)
		}
	})
}

func TestCreateHeldSizesOwnerIsolation(t *testing.T) {
	// The refusal is the same whether or not another owner also holds the
	// hash, so it reveals nothing about other owners' blobs.
	w := heldWorld(t)
	n := len(heldBody)
	refuse := func() deploys.TargetError {
		t.Helper()
		_, _, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "docs", listing("copy.html", heldBody, int64(n+2)))
		return wantSizeRefused(t, err, "copy.html", n+2, n)
	}
	alone := refuse()

	other := w.newOwner("other@example.test")
	w.site(other.ID, "theirs")
	d, _, err := w.create(other.ID, "theirs", listing("index.html", heldBody, int64(n)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Activate(t.Context(), store.ActorFor(other.ID), "theirs", d.ID); err != nil {
		t.Fatal(err)
	}
	shared := refuse()
	if alone != shared {
		t.Errorf("refusal with another holder = %+v, alone = %+v", shared, alone)
	}
}

// lateBody is uploaded only after the deploys listing it are created.
const lateBody = "<h1>Late</h1>"

func TestActivateChecksHeldSizes(t *testing.T) {
	n := len(lateBody)
	// pending creates a pending deploy of docs listing lateBody at p with
	// the given size, under prefix when set; lateBody is not held yet, so
	// Create asks for it rather than comparing.
	pending := func(w *pathWorld, prefix, p string, size int) store.Deploy {
		t.Helper()
		m := listing(p, lateBody, int64(size))
		m.Path = prefix
		d, missing, err := w.svc.Create(t.Context(), store.ActorFor(w.owner.ID), "docs", m)
		if err != nil || !slices.Equal(missing, []string{hashOf(lateBody)}) {
			t.Fatalf("Create = missing %v, %v; want lateBody to upload", missing, err)
		}
		return d
	}
	// uploadElsewhere uploads lateBody at its true size through another
	// deploy of docs.
	uploadElsewhere := func(w *pathWorld) {
		t.Helper()
		b := pending(w, "", "b.html", n)
		if err := w.svc.Upload(t.Context(), store.ActorFor(w.owner.ID), "docs", b.ID, hashOf(lateBody), strings.NewReader(lateBody)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, prefix, target string
	}{
		{"full deploy", "", "a.html"},
		{"path deploy", "/mockup/", "mockup/a.html"},
	} {
		t.Run(tc.name+" listed wrong, uploaded through another", func(t *testing.T) {
			w := heldWorld(t)
			a := pending(w, tc.prefix, "a.html", n+4)
			uploadElsewhere(w)
			// A needs nothing uploaded now, but its listed size is wrong.
			_, _, err := w.svc.Activate(t.Context(), store.ActorFor(w.owner.ID), "docs", a.ID)
			wantSizeRefused(t, err, tc.target, n+4, n)
			// Uploading the true body to A does not help: it is checked
			// against A's listed size.
			err = w.svc.Upload(t.Context(), store.ActorFor(w.owner.ID), "docs", a.ID, hashOf(lateBody), strings.NewReader(lateBody))
			if !errors.Is(err, deploys.ErrHashMismatch) {
				t.Errorf("Upload to A = %v, want ErrHashMismatch", err)
			}
			// A can never activate; the site still serves its first deploy.
			_, _, err = w.svc.Activate(t.Context(), store.ActorFor(w.owner.ID), "docs", a.ID)
			wantSizeRefused(t, err, tc.target, n+4, n)
			if _, served := w.served("docs"); len(served) != 1 || served["index.html"] != heldBody {
				t.Errorf("served = %v, want the first deploy", served)
			}
		})
	}
	t.Run("listed right, uploaded through another", func(t *testing.T) {
		w := heldWorld(t)
		a := pending(w, "", "a.html", n)
		uploadElsewhere(w)
		if _, _, err := w.svc.Activate(t.Context(), store.ActorFor(w.owner.ID), "docs", a.ID); err != nil {
			t.Fatalf("Activate = %v, want activated", err)
		}
	})
}
