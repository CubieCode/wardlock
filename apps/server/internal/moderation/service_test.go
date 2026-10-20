// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/moderation"
	"wardlock.nz/server/internal/moderation/moderationtest"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// world is a store with one owner, a blob store and a clock the test
// moves.
type world struct {
	t     *testing.T
	db    *store.DB
	blobs *deploys.FSBlobStore
	owner store.Owner
	fake  *moderationtest.Fake
	logs  *bytes.Buffer

	mu    sync.Mutex
	clock time.Time
}

func newWorld(t *testing.T) *world {
	w := &world{
		t: t, db: storetest.New(t), blobs: deploys.NewFSBlobStore(t.TempDir()), fake: moderationtest.New(),
		logs: &bytes.Buffer{}, clock: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
	w.owner = store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: w.clock}
	if err := w.db.Owners().Create(t.Context(), w.owner); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clock
}

func (w *world) advance(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clock = w.clock.Add(d)
}

func hashOf(b string) string {
	sum := sha256.Sum256([]byte(b))
	return hex.EncodeToString(sum[:])
}

// publish activates a deploy of files on a new site of ownerID.
func (w *world) publish(ownerID string, files map[string]string) {
	t, ctx := w.t, w.t.Context()
	t.Helper()
	svc := deploys.NewService(deploys.Config{
		Sites: w.db.Sites(), Deploys: w.db.Deploys(), Blobs: w.blobs, Notifier: deploys.NewLocalNotifier(),
		Limits: deploys.DefaultLimits(), Now: w.now, Renditions: w.db.Renditions(),
	})
	name := "site-" + strings.ToLower(store.NewID()[20:])
	if err := w.db.Sites().Create(ctx, store.Site{ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: w.now()}, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	var m deploys.Manifest
	for p, body := range files {
		m.Files = append(m.Files, store.DeployFile{Path: p, SHA256: hashOf(body), Size: int64(len(body))})
	}
	d, _, err := svc.Create(ctx, ownerID, name, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if err := svc.Upload(ctx, ownerID, name, d.ID, f.SHA256, strings.NewReader(files[f.Path])); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.Activate(ctx, ownerID, name, d.ID); err != nil {
		t.Fatal(err)
	}
}

func (w *world) service(q *moderation.Quarantine) *moderation.Service {
	return moderation.New(moderation.Config{
		Store: w.db.Moderation(), Blobs: w.blobs, Moderator: w.fake,
		Policy: moderation.Policy{Quarantine: []string{"sexual/minors", "sexual"}}, Quarantine: q,
		Now: w.now, Jitter: func() float64 { return 0 }, Log: slog.New(slog.NewTextHandler(w.logs, nil)),
	})
}

func (w *world) result(body string) store.ModerationResult {
	w.t.Helper()
	r, err := w.db.Moderation().Get(w.t.Context(), hashOf(body))
	if err != nil {
		w.t.Fatalf("result for %q: %v", body, err)
	}
	return r
}

const pngHead = "\x89PNG\r\n\x1a\n"

func TestOutcomes(t *testing.T) {
	w := newWorld(t)
	w.fake.Flag("nsfw", "sexual")
	w.fake.Flag("minor", "sexual/minors", "sexual")
	w.fake.Flag("threat", "harassment/threatening")
	files := map[string]string{
		"ok.png": pngHead + "plain", "nsfw.png": pngHead + "nsfw", "minor.html": "<p>minor</p>", "threat.htm": "<p>threat</p>",
		"empty.html": "<p>   </p>", "doc.txt": "never sent nsfw",
	}
	w.publish(w.owner.ID, files)
	q := moderation.NewQuarantine()
	w.service(q).Pass(t.Context())
	for body, want := range map[string]store.ModerationStatus{
		files["ok.png"]: store.ModerationAllowed, files["nsfw.png"]: store.ModerationQuarantined,
		files["minor.html"]: store.ModerationQuarantined, files["threat.htm"]: store.ModerationFlagged,
		files["empty.html"]: store.ModerationAllowed,
	} {
		if r := w.result(body); r.Status != want {
			t.Errorf("%q: %+v, want %s", body, r, want)
		}
	}
	if r := w.result(files["threat.htm"]); strings.Join(r.Categories, ",") != "harassment/threatening" {
		t.Errorf("flagged categories %v", r.Categories)
	}
	if !q.Has(hashOf(files["nsfw.png"])) || !q.Has(hashOf(files["minor.html"])) || q.Has(hashOf(files["threat.htm"])) || q.Len() != 2 {
		t.Fatalf("quarantine has %d", q.Len())
	}
	// Four sent: the empty page has no text to send and the .txt is not
	// screened at all.
	if n := len(w.fake.Sent()); n != 4 {
		t.Fatalf("sent %d, want 4", n)
	}
	for _, never := range []string{"nsfw", "minor", "threat", "plain"} {
		if strings.Contains(w.logs.String(), "msg=\""+never) || strings.Contains(w.logs.String(), "<p>"+never) || strings.Contains(w.logs.String(), pngHead) {
			t.Fatalf("logs carry content:\n%s", w.logs.String())
		}
	}
}

func TestPolicyDecide(t *testing.T) {
	for _, c := range []struct {
		p       moderation.Policy
		flagged []string
		want    moderation.Outcome
	}{
		{moderation.Policy{Quarantine: []string{"sexual"}}, nil, moderation.OutcomeAllowed},
		{moderation.Policy{Quarantine: []string{"sexual"}}, []string{"violence"}, moderation.OutcomeFlagged},
		{moderation.Policy{Quarantine: []string{"sexual"}}, []string{"violence", "sexual"}, moderation.OutcomeQuarantined},
		{moderation.Policy{Quarantine: []string{"sexual"}, Flag: []string{"hate"}}, []string{"violence"}, moderation.OutcomeAllowed},
		{moderation.Policy{Quarantine: []string{"sexual"}, Flag: []string{"hate"}}, []string{"hate"}, moderation.OutcomeFlagged},
		{moderation.Policy{Quarantine: []string{}, Flag: []string{}}, []string{"sexual"}, moderation.OutcomeAllowed},
	} {
		if got := c.p.Decide(c.flagged); got != c.want {
			t.Errorf("%+v.Decide(%v) = %s, want %s", c.p, c.flagged, got, c.want)
		}
	}
}

// TestOncePerHash checks each unique file is sent once ever, however many
// paths, sites, owners and passes list it.
func TestOncePerHash(t *testing.T) {
	w := newWorld(t)
	img := pngHead + "same"
	w.publish(w.owner.ID, map[string]string{"a.png": img, "b/c.png": img, "index.html": "<p>Hi</p>"})
	other := store.Owner{ID: store.NewID(), Email: "other@example.test", CreatedAt: w.now()}
	if err := w.db.Owners().Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	svc := w.service(nil)
	svc.Pass(t.Context())
	w.publish(other.ID, map[string]string{"logo.png": img, "index.html": "<p>Hi</p>"})
	svc.Pass(t.Context())
	svc.Pass(t.Context())
	if n := len(w.fake.Sent()); n != 2 {
		t.Fatalf("sent %d, want 2", n)
	}
}

func TestExemptOwnersAreNeverSent(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	if err := w.db.Moderation().SetExempt(ctx, w.owner.ID, true); err != nil {
		t.Fatal(err)
	}
	w.publish(w.owner.ID, map[string]string{"a.png": pngHead + "exempt", "index.html": "<p>exempt</p>"})
	svc := w.service(nil)
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 0 {
		t.Fatalf("sent %d for an exempt owner", n)
	}
	// Queued while screened, then exempted before its turn: dropped, not
	// sent, and queued again only when screening applies once more.
	if err := w.db.Moderation().SetExempt(ctx, w.owner.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Moderation().EnqueueUnscreened(ctx, w.now()); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Moderation().SetExempt(ctx, w.owner.ID, true); err != nil {
		t.Fatal(err)
	}
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 0 {
		t.Fatalf("sent %d after the owner was exempted", n)
	}
	if n, err := svc.Backlog(ctx); err != nil || n != 0 {
		t.Fatalf("Backlog() = %d, %v", n, err)
	}
}

// TestRetriesAndBackoff checks failed attempts back off (doubling, after
// any Retry-After, which also pauses every other attempt), resume from the
// database in a new Service as after a restart, and that a rejected input
// is skipped rather than retried.
func TestRetriesAndBackoff(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	w.publish(w.owner.ID, map[string]string{"a.png": pngHead + "a"})
	a := hashOf(pngHead + "a")
	w.fake.Fail(&moderation.RetryError{Status: 503}, &moderation.RetryError{Status: 429, After: 10 * time.Minute})

	w.service(nil).Pass(ctx)
	r := w.result(pngHead + "a")
	if r.Status != store.ModerationPending || r.Attempts != 1 || !r.NextAttemptAt.Equal(w.now().Add(moderation.MinBackoff)) {
		t.Fatalf("after a 503: %+v", r)
	}
	// Not due yet: nothing is sent.
	svc := w.service(nil)
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 1 {
		t.Fatalf("sent %d before the retry was due", n)
	}
	w.advance(moderation.MinBackoff)
	svc.Pass(ctx)
	r = w.result(pngHead + "a")
	if r.Attempts != 2 || !r.NextAttemptAt.Equal(w.now().Add(10*time.Minute)) {
		t.Fatalf("after a 429 with Retry-After: %+v", r)
	}
	// The pause holds back other hashes too.
	w.publish(w.owner.ID, map[string]string{"b.png": pngHead + "b"})
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 2 {
		t.Fatalf("sent %d during the provider's pause", n)
	}
	if n, err := svc.Backlog(ctx); err != nil || n != 2 {
		t.Fatalf("Backlog() = %d, %v; want 2", n, err)
	}
	// A restart resumes from the queue.
	w.advance(10 * time.Minute)
	w.service(nil).Pass(ctx)
	if r := w.result(pngHead + "a"); r.Status != store.ModerationAllowed || r.Attempts != 0 {
		t.Fatalf("after the restart: %+v", r)
	}
	if r := w.result(pngHead + "b"); r.Status != store.ModerationAllowed {
		t.Fatalf("b after the restart: %+v", r)
	}

	// Back-off doubles up to its cap.
	w.publish(w.owner.ID, map[string]string{"c.png": pngHead + "c"})
	for i := range 3 {
		w.fake.Fail(moderation.ErrUnavailable)
		w.service(nil).Pass(ctx)
		r := w.result(pngHead + "c")
		if want := w.now().Add(moderation.MinBackoff << i); r.Attempts != i+1 || !r.NextAttemptAt.Equal(want) {
			t.Fatalf("attempt %d: %+v, want next at %s", i+1, r, want)
		}
		w.advance(moderation.MinBackoff << i)
	}

	w.publish(w.owner.ID, map[string]string{"d.png": pngHead + "d"})
	w.fake = moderationtest.New()
	w.fake.Fail(moderation.ErrRejected, moderation.ErrRejected)
	w.service(nil).Pass(ctx)
	for _, body := range []string{pngHead + "c", pngHead + "d"} {
		if r := w.result(body); r.Status != store.ModerationSkipped {
			t.Fatalf("rejected %q: %+v", body, r)
		}
	}
	if !strings.Contains(w.logs.String(), a) {
		t.Fatalf("retries are logged with the hash:\n%s", w.logs.String())
	}
}

func TestSkippedImages(t *testing.T) {
	w := newWorld(t)
	animated := "GIF89a\x01\x00\x01\x00\x00\x00\x00" +
		"\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x01\x00\x00" +
		"\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x01\x00\x00" + "\x3b"
	still := "GIF89a\x01\x00\x01\x00\x00\x00\x00" + "\x21\xf9\x04\x00\x00\x00\x00\x00" +
		"\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x01\x00\x00" + "\x3b"
	w.publish(w.owner.ID, map[string]string{
		"anim.gif": animated, "still.gif": still, "fake.png": "not an image at all", "big.jpg": "\xff\xd8\xff" + strings.Repeat("x", moderation.MaxImageSize),
	})
	w.service(nil).Pass(t.Context())
	for body, want := range map[string]store.ModerationStatus{
		animated: store.ModerationSkipped, still: store.ModerationAllowed, "not an image at all": store.ModerationSkipped,
		"\xff\xd8\xff" + strings.Repeat("x", moderation.MaxImageSize): store.ModerationSkipped,
	} {
		if r := w.result(body); r.Status != want {
			t.Errorf("%.12q: %s, want %s", body, r.Status, want)
		}
	}
	sent := w.fake.Sent()
	if len(sent) != 1 || sent[0].ImageType != "image/gif" {
		t.Fatalf("sent %+v, want only the still GIF", sent)
	}
}

// TestPageText checks a page is sent as its plain-text rendition, in
// chunks, and truncated past the limit.
func TestPageText(t *testing.T) {
	w := newWorld(t)
	word := "lorem "
	long := "<p>" + strings.Repeat(word, (moderation.MaxTextChunks*moderation.TextChunk)/len(word)+5000) + "</p>"
	w.publish(w.owner.ID, map[string]string{"index.html": `<h1>Title</h1><script>secret()</script><p>Body text</p>`, "long.html": long})
	w.service(nil).Pass(t.Context())
	var short, chunked []string
	for _, in := range w.fake.Sent() {
		if len(in.Text) == 1 {
			short = in.Text
		} else {
			chunked = in.Text
		}
	}
	if len(short) != 1 || short[0] != "Title\n\nBody text" {
		t.Fatalf("page text = %q", short)
	}
	if len(chunked) != moderation.MaxTextChunks {
		t.Fatalf("%d chunks, want %d", len(chunked), moderation.MaxTextChunks)
	}
	for _, c := range chunked {
		if len([]rune(c)) > moderation.TextChunk || strings.HasSuffix(c, " ") || strings.HasPrefix(c, " ") || strings.Contains(c, "lor ") {
			t.Fatalf("chunk of %d runes: %.40q…", len([]rune(c)), c)
		}
	}
}

func TestDecisions(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	w.fake.Flag("nsfw", "sexual")
	w.publish(w.owner.ID, map[string]string{"a.png": pngHead + "nsfw", "b.png": pngHead + "fine"})
	svc := w.service(moderation.NewQuarantine())
	svc.Pass(ctx)
	a, b := hashOf(pngHead+"nsfw"), hashOf(pngHead+"fine")
	rs, err := svc.Review(ctx)
	if err != nil || len(rs) != 1 || rs[0].SHA256 != a || len(rs[0].Uses) != 1 || rs[0].Uses[0].Path != "a.png" {
		t.Fatalf("Review() = %+v, %v", rs, err)
	}
	if err := svc.Confirm(ctx, b); !errors.Is(err, moderation.ErrWrongStatus) {
		t.Fatalf("Confirm(allowed) = %v", err)
	}
	if err := svc.Release(ctx, strings.Repeat("0", 64)); !errors.Is(err, moderation.ErrUnknown) {
		t.Fatalf("Release(unknown) = %v", err)
	}
	if err := svc.Confirm(ctx, a); err != nil {
		t.Fatal(err)
	}
	// A rescan screens again; the new verdict replaces the confirmation.
	if err := svc.Rescan(ctx, a); err != nil {
		t.Fatal(err)
	}
	w.fake = moderationtest.New()
	svc = w.service(nil)
	svc.Pass(ctx)
	if r := w.result(pngHead + "nsfw"); r.Status != store.ModerationAllowed || !r.ConfirmedAt.IsZero() {
		t.Fatalf("after the rescan: %+v", r)
	}
	if !strings.Contains(w.logs.String(), "operator confirmed quarantine") {
		t.Fatalf("decisions are not logged:\n%s", w.logs.String())
	}
}
