// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// Content screening (A2.15): the queue, verdicts, operator decisions,
// exemptions and the garbage-collection hold on quarantined blobs.
func moderationContract(t *testing.T, open Opener) {
	file := func(path string, c byte) store.DeployFile {
		return store.DeployFile{Path: path, SHA256: sha(c), Size: 5}
	}
	// publish activates a deploy of files on a new site of ownerID.
	publish := func(f *fixture, ownerID string, files ...store.DeployFile) (store.Site, store.Deploy) {
		f.t.Helper()
		s := f.site(ownerID)
		d := f.deployFiles(ownerID, s.ID, files...)
		f.uploadAll(ownerID, s.ID, d.ID)
		d, err := f.r.Deploys.Activate(f.t.Context(), ownerID, s.ID, d.ID, now)
		f.must(err)
		return s, d
	}
	enqueue := func(f *fixture) int {
		f.t.Helper()
		n, err := f.r.Moderation.EnqueueUnscreened(f.t.Context(), now)
		f.must(err)
		return n
	}
	due := func(f *fixture, at time.Time) map[string]store.ModerationKind {
		f.t.Helper()
		rs, err := f.r.Moderation.Due(f.t.Context(), at, 100)
		f.must(err)
		out := map[string]store.ModerationKind{}
		for _, r := range rs {
			out[r.SHA256] = r.Kind
		}
		return out
	}
	get := func(f *fixture, h string) store.ModerationResult {
		f.t.Helper()
		r, err := f.r.Moderation.Get(f.t.Context(), h)
		f.must(err)
		return r
	}

	t.Run("EnqueueOncePerHash", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		// a twice (letter case does not matter), a stylesheet that is never
		// sent, a page, and a page also listed as an image (screened as text).
		publish(f, o.ID, file("a.png", 'a'), file("img/COPY.PNG", 'a'), file("site.css", 'b'), file("index.html", 'c'),
			file("about.htm", 'd'), file("d.webp", 'd'))
		// A pending deploy is not screened until it is activated.
		s := f.site(o.ID)
		f.deployFiles(o.ID, s.ID, file("e.jpg", 'e'))
		if n := enqueue(f); n != 3 {
			t.Fatalf("queued %d, want 3", n)
		}
		// The same hash on another owner's site is not queued again.
		publish(f, f.owner().ID, file("other.png", 'a'))
		if n := enqueue(f); n != 0 {
			t.Fatalf("queued again %d, want 0", n)
		}
		want := map[string]store.ModerationKind{sha('a'): store.ModerationImage, sha('c'): store.ModerationText, sha('d'): store.ModerationText}
		if got := due(f, now); !maps.Equal(got, want) {
			t.Fatalf("due = %v, want %v", got, want)
		}
		if got := due(f, now.Add(-time.Millisecond)); len(got) != 0 {
			t.Fatalf("due before queued = %v", got)
		}
		r := get(f, sha('a'))
		if r.Status != store.ModerationPending || !r.NextAttemptAt.Equal(now) || !r.QueuedAt.Equal(now) {
			t.Fatalf("queued row = %+v", r)
		}
		if n, err := f.r.Moderation.Backlog(ctx); err != nil || n != 3 {
			t.Fatalf("Backlog() = %d, %v; want 3", n, err)
		}
		_, err := f.r.Moderation.Get(ctx, sha('b'))
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("ExemptSuspendedAndTakenDownAreNeverQueued", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		exempt, suspended, takenDown := f.owner(), f.owner(), f.owner()
		f.must(f.r.Moderation.SetExempt(ctx, exempt.ID, true))
		publish(f, exempt.ID, file("a.png", 'a'))
		publish(f, suspended.ID, file("b.png", 'b'))
		f.must(f.r.Owners.Suspend(ctx, suspended.ID, now))
		s, _ := publish(f, takenDown.ID, file("c.png", 'c'))
		f.must(f.r.Sites.TakeDown(ctx, s.ID, now))
		if n := enqueue(f); n != 0 {
			t.Fatalf("queued %d, want 0", n)
		}
		if ex, err := f.r.Moderation.Exempt(ctx, exempt.ID); err != nil || !ex {
			t.Fatalf("Exempt() = %v, %v", ex, err)
		}
		if ex, err := f.r.Moderation.Exempt(ctx, suspended.ID); err != nil || ex {
			t.Fatalf("new owner Exempt() = %v, %v; want the default, false", ex, err)
		}
		f.must(f.r.Moderation.SetExempt(ctx, exempt.ID, false))
		if n := enqueue(f); n != 1 {
			t.Fatalf("queued after the exemption ends %d, want 1", n)
		}
		wantErr(t, f.r.Moderation.SetExempt(ctx, store.NewID(), true), store.ErrNotFound)
		_, err := f.r.Moderation.Exempt(ctx, store.NewID())
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("Source", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		exempt, o := f.owner(), f.owner()
		f.must(f.r.Moderation.SetExempt(ctx, exempt.ID, true))
		publish(f, exempt.ID, file("shared.png", 'a'), file("own.png", 'b'))
		s, d := publish(f, o.ID, file("pic.png", 'a'), file("index.html", 'c'))
		f.must(f.r.Renditions.Put(ctx, o.ID, s.ID, d.ID, []store.Rendition{
			{Path: "index.html", Format: "md", SHA256: sha('4'), Size: 3},
			{Path: "index.html", Format: "txt", SHA256: sha('5'), Size: 2},
		}, now))

		src, err := f.r.Moderation.Source(ctx, sha('a'))
		if err != nil || src.Path != "pic.png" || src.Size != 5 || src.TextSHA256 != "" {
			t.Fatalf("Source(shared) = %+v, %v; want the non-exempt owner's file", src, err)
		}
		src, err = f.r.Moderation.Source(ctx, sha('c'))
		if err != nil || src.TextSHA256 != sha('5') || src.TextSize != 2 {
			t.Fatalf("Source(page) = %+v, %v; want its plain-text rendition", src, err)
		}
		_, err = f.r.Moderation.Source(ctx, sha('b'))
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("VerdictsRetriesAndDequeue", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		publish(f, o.ID, file("a.png", 'a'), file("b.png", 'b'), file("c.png", 'c'))
		enqueue(f)

		later := now.Add(time.Minute)
		f.must(f.r.Moderation.Retry(ctx, sha('a'), 1, later))
		if got := due(f, now); len(got) != 2 {
			t.Fatalf("due while a backs off = %v", got)
		}
		if r := get(f, sha('a')); r.Attempts != 1 || !r.NextAttemptAt.Equal(later) {
			t.Fatalf("after Retry = %+v", r)
		}
		f.must(f.r.Moderation.Record(ctx, sha('a'), store.ModerationQuarantined, []string{"sexual", "violence"}, later))
		r := get(f, sha('a'))
		if r.Status != store.ModerationQuarantined || !slices.Equal(r.Categories, []string{"sexual", "violence"}) ||
			!r.CheckedAt.Equal(later) || !r.NextAttemptAt.IsZero() || r.Attempts != 0 {
			t.Fatalf("after Record = %+v", r)
		}
		// Not queued any more.
		wantErr(t, f.r.Moderation.Record(ctx, sha('a'), store.ModerationAllowed, nil, later), store.ErrNotFound)
		wantErr(t, f.r.Moderation.Retry(ctx, sha('a'), 2, later), store.ErrNotFound)
		wantErr(t, f.r.Moderation.Record(ctx, sha('b'), store.ModerationPending, nil, later), store.ErrConstraint)
		f.must(f.r.Moderation.Record(ctx, sha('b'), store.ModerationFlagged, []string{"harassment"}, now))

		// Dequeue forgets a hash never screened and keeps a verdict.
		f.must(f.r.Moderation.Dequeue(ctx, sha('c')))
		_, err := f.r.Moderation.Get(ctx, sha('c'))
		wantErr(t, err, store.ErrNotFound)
		f.must(f.r.Moderation.Rescan(ctx, sha('b'), later))
		f.must(f.r.Moderation.Dequeue(ctx, sha('b')))
		if r := get(f, sha('b')); r.Status != store.ModerationFlagged || !r.NextAttemptAt.IsZero() {
			t.Fatalf("dequeued rescan = %+v; want the verdict kept", r)
		}
		if n, err := f.r.Moderation.Backlog(ctx); err != nil || n != 0 {
			t.Fatalf("Backlog() = %d, %v; want 0", n, err)
		}
	})

	t.Run("ReviewQuarantinedAndUses", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		a, b := f.owner(), f.owner()
		sa, _ := publish(f, a.ID, file("x.png", '2'), file("ok.png", '3'))
		sb, _ := publish(f, b.ID, file("img/x.png", '2'), file("f.png", 'f'))
		enqueue(f)
		f.must(f.r.Moderation.Record(ctx, sha('2'), store.ModerationQuarantined, []string{"sexual"}, now.Add(time.Second)))
		f.must(f.r.Moderation.Record(ctx, sha('f'), store.ModerationFlagged, []string{"violence"}, now))
		f.must(f.r.Moderation.Record(ctx, sha('3'), store.ModerationAllowed, nil, now))

		q, err := f.r.Moderation.Quarantined(ctx)
		if err != nil || !slices.Equal(q, []string{sha('2')}) {
			t.Fatalf("Quarantined() = %v, %v", q, err)
		}
		rs, err := f.r.Moderation.Review(ctx)
		if err != nil || len(rs) != 2 || rs[0].SHA256 != sha('f') || rs[1].SHA256 != sha('2') {
			t.Fatalf("Review() = %+v, %v; want f then x", rs, err)
		}
		uses, err := f.r.Moderation.Uses(ctx, sha('2'))
		f.must(err)
		got := map[string]string{}
		for _, u := range uses {
			got[u.SiteID] = u.Path + " " + u.OwnerEmail
		}
		want := map[string]string{sa.ID: "x.png " + a.Email, sb.ID: "img/x.png " + b.Email}
		if !maps.Equal(got, want) {
			t.Fatalf("Uses() = %v, want %v", got, want)
		}
	})

	t.Run("Decisions", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		publish(f, o.ID, file("q.png", '1'), file("f.png", 'f'), file("a.png", 'a'))
		enqueue(f)
		f.must(f.r.Moderation.Record(ctx, sha('1'), store.ModerationQuarantined, []string{"sexual"}, now))
		f.must(f.r.Moderation.Record(ctx, sha('f'), store.ModerationFlagged, []string{"violence"}, now))
		f.must(f.r.Moderation.Record(ctx, sha('a'), store.ModerationAllowed, nil, now))
		at := now.Add(time.Hour)

		f.must(f.r.Moderation.Confirm(ctx, sha('1'), at))
		if r := get(f, sha('1')); !r.ConfirmedAt.Equal(at) || !r.DiscardedAt.IsZero() {
			t.Fatalf("after Confirm = %+v", r)
		}
		f.must(f.r.Moderation.Discard(ctx, sha('1'), at))
		if r := get(f, sha('1')); r.Status != store.ModerationQuarantined || !r.DiscardedAt.Equal(at) || !r.ConfirmedAt.IsZero() {
			t.Fatalf("after Discard = %+v", r)
		}
		wantErr(t, f.r.Moderation.Confirm(ctx, sha('f'), at), store.ErrConflict)
		wantErr(t, f.r.Moderation.Discard(ctx, sha('a'), at), store.ErrConflict)
		wantErr(t, f.r.Moderation.Release(ctx, sha('a'), at), store.ErrConflict)
		wantErr(t, f.r.Moderation.Release(ctx, sha('7'), at), store.ErrNotFound)
		wantErr(t, f.r.Moderation.Confirm(ctx, sha('7'), at), store.ErrNotFound)

		f.must(f.r.Moderation.Release(ctx, sha('1'), at))
		f.must(f.r.Moderation.Release(ctx, sha('f'), at))
		for _, h := range []string{sha('1'), sha('f')} {
			if r := get(f, h); r.Status != store.ModerationAllowed || !r.ReleasedAt.Equal(at) || !r.DiscardedAt.IsZero() {
				t.Fatalf("after Release = %+v", r)
			}
		}

		// A rescan keeps the verdict until the new one, which clears the
		// operator's decision.
		wantErr(t, f.r.Moderation.Rescan(ctx, sha('7'), at), store.ErrNotFound)
		f.must(f.r.Moderation.Rescan(ctx, sha('1'), at))
		if r := get(f, sha('1')); r.Status != store.ModerationAllowed || !r.NextAttemptAt.Equal(at) {
			t.Fatalf("after Rescan = %+v", r)
		}
		if got := due(f, at); len(got) != 1 {
			t.Fatalf("due after Rescan = %v", got)
		}
		f.must(f.r.Moderation.Record(ctx, sha('1'), store.ModerationQuarantined, []string{"sexual"}, at))
		if r := get(f, sha('1')); r.Status != store.ModerationQuarantined || !r.ReleasedAt.IsZero() {
			t.Fatalf("after the rescan's verdict = %+v", r)
		}
	})

	t.Run("GCHoldsQuarantinedBlobs", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		s, old := publish(f, o.ID, file("q.png", '1'), file("a.png", 'a'))
		enqueue(f)
		f.must(f.r.Moderation.Record(ctx, sha('1'), store.ModerationQuarantined, []string{"sexual"}, now))
		f.must(f.r.Moderation.Record(ctx, sha('a'), store.ModerationAllowed, nil, now))
		// A new deploy replaces the old one, which is then pruned: neither
		// hash is listed by any deploy now.
		d := f.deployFiles(o.ID, s.ID, file("index.html", '6'))
		f.uploadAll(o.ID, s.ID, d.ID)
		_, err := f.r.Deploys.Activate(ctx, o.ID, s.ID, d.ID, now)
		f.must(err)
		deleted, err := f.r.GC.DeletePrunableDeploy(ctx, old.ID, now)
		if err != nil || !deleted {
			t.Fatalf("DeletePrunableDeploy() = %v, %v", deleted, err)
		}
		garbage := func() []string {
			t.Helper()
			bs, err := f.r.GC.UnreferencedBlobs(ctx, now.Add(time.Hour), "", 100)
			f.must(err)
			var hs []string
			for _, b := range bs {
				hs = append(hs, b.SHA256)
			}
			return hs
		}
		if got := garbage(); !slices.Equal(got, []string{sha('a')}) {
			t.Fatalf("garbage while quarantined = %v; want only a", got)
		}
		f.must(f.r.Moderation.Confirm(ctx, sha('1'), now))
		if got := garbage(); slices.Contains(got, sha('1')) {
			t.Fatalf("garbage after Confirm = %v; want q held", got)
		}
		_, deleted, err = f.r.GC.DeleteBlob(ctx, sha('1'), now.Add(time.Hour), func(context.Context) error { return nil })
		if err != nil || deleted {
			t.Fatalf("DeleteBlob(held) = %v, %v; want kept", deleted, err)
		}
		f.must(f.r.Moderation.Discard(ctx, sha('1'), now))
		if got := garbage(); !slices.Equal(got, []string{sha('1'), sha('a')}) {
			t.Fatalf("garbage after Discard = %v; want a and q", got)
		}
	})
}
