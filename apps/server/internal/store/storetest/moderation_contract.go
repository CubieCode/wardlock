// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"fmt"
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
	// enqueue queues every serving site's files, as a full sweep does.
	enqueue := func(f *fixture) int {
		f.t.Helper()
		ids, err := f.r.Moderation.ScreenedSites(f.t.Context(), "", 1000)
		f.must(err)
		total := 0
		for _, id := range ids {
			n, err := f.r.Moderation.EnqueueSite(f.t.Context(), id, now)
			f.must(err)
			total += n
		}
		return total
	}
	record := func(f *fixture, h string, status store.ModerationStatus, at time.Time, cats ...string) {
		f.t.Helper()
		f.must(f.r.Moderation.Record(f.t.Context(), h, store.ModerationVerdict{Status: status, Categories: cats, Kind: store.ModerationImage}, at))
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
		// Every file is queued, whatever its name, once per hash.
		s1, _ := publish(f, o.ID, file("a.png", 'a'), file("img/COPY.PNG", 'a'), file("site.css", 'b'), file("index.html", 'c'))
		// A pending deploy is not screened until it is activated.
		s2 := f.site(o.ID)
		f.deployFiles(o.ID, s2.ID, file("e.jpg", 'e'))
		n, err := f.r.Moderation.EnqueueSite(ctx, s1.ID, now)
		if err != nil || n != 3 {
			t.Fatalf("EnqueueSite() = %d, %v; want 3", n, err)
		}
		if n, err := f.r.Moderation.EnqueueSite(ctx, s2.ID, now); err != nil || n != 0 {
			t.Fatalf("EnqueueSite(no active deploy) = %d, %v; want 0", n, err)
		}
		// The same hash on another owner's site is not queued again.
		publish(f, f.owner().ID, file("other.png", 'a'))
		if n := enqueue(f); n != 0 {
			t.Fatalf("queued again %d, want 0", n)
		}
		want := map[string]store.ModerationKind{sha('a'): store.ModerationNone, sha('b'): store.ModerationNone, sha('c'): store.ModerationNone}
		if got := due(f, now); !maps.Equal(got, want) {
			t.Fatalf("due = %v, want %v", got, want)
		}
		if got := due(f, now.Add(-time.Millisecond)); len(got) != 0 {
			t.Fatalf("due before queued = %v", got)
		}
		r := get(f, sha('a'))
		if r.Status != store.ModerationPending || !r.NextAttemptAt.Equal(now) || !r.QueuedAt.Equal(now) || r.Kind != store.ModerationNone {
			t.Fatalf("queued row = %+v", r)
		}
		if n, err := f.r.Moderation.Backlog(ctx); err != nil || n != 3 {
			t.Fatalf("Backlog() = %d, %v; want 3", n, err)
		}
		_, err = f.r.Moderation.Get(ctx, sha('e'))
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("RequeueForText", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		publish(f, o.ID, file("pic.png", 'a'), file("q.png", 'e'), file("page.html", 'c'))
		enqueue(f)
		record(f, sha('a'), store.ModerationAllowed, now)
		record(f, sha('e'), store.ModerationQuarantined, now, "sexual")
		f.must(f.r.Moderation.Record(ctx, sha('c'), store.ModerationVerdict{Status: store.ModerationAllowed, Kind: store.ModerationText}, now))
		// The image's bytes and a quarantined hash are now also listed as
		// pages, and the page also as an image.
		s, _ := publish(f, o.ID, file("a.HTML", 'a'), file("q.txt", 'e'), file("c.png", 'c'))
		later := now.Add(time.Hour)
		if n, err := f.r.Moderation.EnqueueSite(ctx, s.ID, later); err != nil || n != 1 {
			t.Fatalf("EnqueueSite() = %d, %v; want the image queued again for its text", n, err)
		}
		if got := due(f, later); !maps.Equal(got, map[string]store.ModerationKind{sha('a'): store.ModerationImage}) {
			t.Fatalf("due = %v", got)
		}
		if r := get(f, sha('a')); r.Status != store.ModerationAllowed {
			t.Fatalf("queued again = %+v; want the verdict kept", r)
		}
		// Once text is covered it is not queued again.
		f.must(f.r.Moderation.Record(ctx, sha('a'), store.ModerationVerdict{Kind: store.ModerationImageText}, later))
		if n, err := f.r.Moderation.EnqueueSite(ctx, s.ID, later); err != nil || n != 0 {
			t.Fatalf("EnqueueSite() again = %d, %v; want 0", n, err)
		}
		if src, err := f.r.Moderation.Source(ctx, sha('a')); err != nil || !src.TextServed {
			t.Fatalf("Source() = %+v, %v; want TextServed", src, err)
		}
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
		if ids, err := f.r.Moderation.ScreenedSites(ctx, "", 10); err != nil || len(ids) != 0 {
			t.Fatalf("ScreenedSites() = %v, %v; want none", ids, err)
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
		publish(f, o.ID, file("pic.png", 'a'), file("index.html", 'c'))
		// Only the exempt owner serves a as a page: that does not count.
		publish(f, exempt.ID, file("shared.svg", 'a'))

		src, err := f.r.Moderation.Source(ctx, sha('a'))
		if err != nil || src.Size != 5 || src.TextServed {
			t.Fatalf("Source(shared) = %+v, %v; want the non-exempt owner's file, not served as text", src, err)
		}
		src, err = f.r.Moderation.Source(ctx, sha('c'))
		if err != nil || !src.TextServed {
			t.Fatalf("Source(page) = %+v, %v; want TextServed", src, err)
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
		gz := sha('9')
		f.must(f.r.Moderation.Record(ctx, sha('a'), store.ModerationVerdict{
			Status: store.ModerationQuarantined, Categories: []string{"sexual", "violence"}, Kind: store.ModerationImageText, Decoded: gz,
		}, later))
		r := get(f, sha('a'))
		if r.Status != store.ModerationQuarantined || !slices.Equal(r.Categories, []string{"sexual", "violence"}) || r.Kind != store.ModerationImageText ||
			r.Decoded != gz || r.Reason != "" || !r.CheckedAt.Equal(later) || !r.NextAttemptAt.IsZero() || r.Attempts != 0 {
			t.Fatalf("after Record = %+v", r)
		}
		// Not queued any more.
		wantErr(t, f.r.Moderation.Record(ctx, sha('a'), store.ModerationVerdict{Status: store.ModerationAllowed}, later), store.ErrNotFound)
		wantErr(t, f.r.Moderation.Retry(ctx, sha('a'), 2, later), store.ErrNotFound)
		wantErr(t, f.r.Moderation.Record(ctx, sha('b'), store.ModerationVerdict{Status: store.ModerationPending}, later), store.ErrConstraint)
		// Keeping a verdict needs one.
		wantErr(t, f.r.Moderation.Record(ctx, sha('b'), store.ModerationVerdict{Kind: store.ModerationImage}, later), store.ErrNotFound)
		f.must(f.r.Moderation.Record(ctx, sha('b'), store.ModerationVerdict{
			Status: store.ModerationFlagged, Reason: store.ModerationUnscreenable, Kind: store.ModerationImage,
		}, now))
		if r := get(f, sha('b')); r.Status != store.ModerationFlagged || r.Reason != store.ModerationUnscreenable || len(r.Categories) != 0 {
			t.Fatalf("unscreenable = %+v", r)
		}

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
		record(f, sha('2'), store.ModerationQuarantined, now.Add(time.Second), "sexual")
		record(f, sha('f'), store.ModerationFlagged, now, "violence")
		record(f, sha('3'), store.ModerationAllowed, now)
		f.must(f.r.Sites.TakeDown(ctx, sb.ID, now))

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
			got[u.SiteID] = fmt.Sprint(u.Path, " ", u.OwnerEmail, " ", u.Unavailable)
		}
		want := map[string]string{sa.ID: "x.png " + a.Email + " false", sb.ID: "img/x.png " + b.Email + " true"}
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
		record(f, sha('1'), store.ModerationQuarantined, now, "sexual")
		record(f, sha('f'), store.ModerationFlagged, now, "violence")
		record(f, sha('a'), store.ModerationAllowed, now)
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
		// operator's decision when the status changes and keeps it when it
		// stays, and a rescan that ends without a verdict keeps both.
		wantErr(t, f.r.Moderation.Rescan(ctx, sha('7'), at), store.ErrNotFound)
		f.must(f.r.Moderation.Rescan(ctx, sha('1'), at))
		if r := get(f, sha('1')); r.Status != store.ModerationAllowed || !r.NextAttemptAt.Equal(at) {
			t.Fatalf("after Rescan = %+v", r)
		}
		if got := due(f, at); len(got) != 1 {
			t.Fatalf("due after Rescan = %v", got)
		}
		record(f, sha('1'), store.ModerationQuarantined, at, "sexual")
		if r := get(f, sha('1')); r.Status != store.ModerationQuarantined || !r.ReleasedAt.IsZero() {
			t.Fatalf("after the rescan's verdict = %+v", r)
		}
		f.must(f.r.Moderation.Confirm(ctx, sha('1'), at))
		f.must(f.r.Moderation.Rescan(ctx, sha('1'), at))
		f.must(f.r.Moderation.Record(ctx, sha('1'), store.ModerationVerdict{Kind: store.ModerationImageText}, at))
		if r := get(f, sha('1')); r.Status != store.ModerationQuarantined || !r.ConfirmedAt.Equal(at) || r.Kind != store.ModerationImageText ||
			!slices.Equal(r.Categories, []string{"sexual"}) || !r.NextAttemptAt.IsZero() {
			t.Fatalf("after a rescan with no verdict = %+v; want the quarantine and its confirmation kept", r)
		}
		f.must(f.r.Moderation.Rescan(ctx, sha('1'), at))
		record(f, sha('1'), store.ModerationQuarantined, at, "sexual")
		if r := get(f, sha('1')); !r.ConfirmedAt.Equal(at) {
			t.Fatalf("after a rescan with the same verdict = %+v; want the confirmation kept", r)
		}
	})

	t.Run("DeployScreening", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		_, d := publish(f, o.ID, file("a.png", 'a'), file("b.png", 'b'), file("b.png.gz", 'd'))
		enqueue(f)
		record(f, sha('a'), store.ModerationAllowed, now)
		f.must(f.r.Moderation.Record(ctx, sha('d'), store.ModerationVerdict{Status: store.ModerationSkipped, Decoded: sha('b')}, now))
		// A hash never queued (as on a site of an exempt owner) is pending.
		_, other := publish(f, o.ID, file("c.png", 'c'))
		states, err := f.r.Moderation.DeployScreening(ctx, d.ID)
		f.must(err)
		got := map[string]string{}
		for _, s := range states {
			got[s.SHA256] = string(s.Status) + " " + s.Decoded
		}
		want := map[string]string{sha('a'): "allowed ", sha('b'): "pending ", sha('d'): "skipped " + sha('b')}
		if !maps.Equal(got, want) {
			t.Fatalf("DeployScreening() = %v, want %v", got, want)
		}
		states, err = f.r.Moderation.DeployScreening(ctx, other.ID)
		if err != nil || len(states) != 1 || states[0].Status != store.ModerationPending {
			t.Fatalf("DeployScreening(unqueued) = %+v, %v", states, err)
		}
	})

	t.Run("GCHoldsQuarantinedBlobs", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		o := f.owner()
		s, old := publish(f, o.ID, file("q.png", '1'), file("a.png", 'a'))
		enqueue(f)
		record(f, sha('1'), store.ModerationQuarantined, now, "sexual")
		record(f, sha('a'), store.ModerationAllowed, now)
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
