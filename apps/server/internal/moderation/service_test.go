// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/moderation"
	"wardlock.nz/server/internal/moderation/moderationtest"
	"wardlock.nz/server/internal/moderation/testimages"
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

// mailbox records the notices a Service sends.
type mailbox struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *mailbox) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

// to returns the messages sent to addr.
func (m *mailbox) to(addr string) []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mail.Message
	for _, msg := range m.sent {
		if msg.To.Address == addr {
			out = append(out, msg)
		}
	}
	return out
}

const abuse = "abuse@example.test"

func (w *world) config(q *moderation.Quarantine) moderation.Config {
	return moderation.Config{
		Store: w.db.Moderation(), Blobs: w.blobs, Moderator: w.fake,
		Policy: moderation.Policy{Quarantine: []string{"sexual/minors", "sexual"}}, Quarantine: q,
		Now: w.now, Jitter: func() float64 { return 0 }, Log: slog.New(slog.NewTextHandler(w.logs, nil)),
	}
}

func (w *world) service(q *moderation.Quarantine) *moderation.Service {
	return moderation.New(w.config(q))
}

func (w *world) result(body string) store.ModerationResult {
	w.t.Helper()
	r, err := w.db.Moderation().Get(w.t.Context(), hashOf(body))
	if err != nil {
		w.t.Fatalf("result for %.20q: %v", body, err)
	}
	return r
}

// sentImages returns the images sent, decoded.
func (w *world) sentImages() []image.Image {
	w.t.Helper()
	var out []image.Image
	for _, in := range w.fake.Sent() {
		if len(in.Image) == 0 {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(in.Image))
		if err != nil {
			w.t.Fatalf("sent an image that does not decode (%s): %v", in.ImageType, err)
		}
		out = append(out, img)
	}
	return out
}

var png1 = testimages.PNG

func TestOutcomes(t *testing.T) {
	w := newWorld(t)
	w.fake.Flag("nsfw", "sexual")
	w.fake.Flag("minor", "sexual/minors", "sexual")
	w.fake.Flag("threat", "harassment/threatening")
	files := map[string]string{
		"ok.png": png1("plain", 0), "nsfw.png": png1("nsfw", 0), "minor.html": "<p>minor</p>", "threat.htm": "<p>threat</p>",
		"empty.html": "<p>   </p>", "doc.txt": "plain words", "site.css": "body { content: 'nsfw' }",
		"data.json": `{"note": "nsfw"}`, "table.CSV": "a,threat",
	}
	w.publish(w.owner.ID, files)
	q := moderation.NewQuarantine()
	w.service(q).Pass(t.Context())
	for body, want := range map[string]store.ModerationStatus{
		files["ok.png"]: store.ModerationAllowed, files["nsfw.png"]: store.ModerationQuarantined,
		files["minor.html"]: store.ModerationQuarantined, files["threat.htm"]: store.ModerationFlagged,
		files["empty.html"]: store.ModerationAllowed, files["doc.txt"]: store.ModerationAllowed,
		// JSON and CSV are shown as text; a stylesheet's source is not
		// screened.
		files["data.json"]: store.ModerationQuarantined, files["table.CSV"]: store.ModerationFlagged,
		files["site.css"]: store.ModerationSkipped,
	} {
		if r := w.result(body); r.Status != want {
			t.Errorf("%.20q: %+v, want %s", body, r, want)
		}
	}
	if r := w.result(files["threat.htm"]); strings.Join(r.Categories, ",") != "harassment/threatening" || r.Kind != store.ModerationText {
		t.Errorf("flagged page %+v", r)
	}
	if r := w.result(files["ok.png"]); r.Kind != store.ModerationImage {
		t.Errorf("image kind %q", r.Kind)
	}
	if !q.Has(hashOf(files["nsfw.png"])) || !q.Has(hashOf(files["minor.html"])) || q.Has(hashOf(files["threat.htm"])) || q.Len() != 3 {
		t.Fatalf("quarantine has %d", q.Len())
	}
	// Seven sent: the empty page has no text and the stylesheet nothing to
	// send.
	if n := len(w.fake.Sent()); n != 7 {
		t.Fatalf("sent %d, want 7", n)
	}
	for _, never := range []string{"nsfw", "minor", "threat", "plain"} {
		if strings.Contains(w.logs.String(), "msg=\""+never) || strings.Contains(w.logs.String(), "<p>"+never) || strings.Contains(w.logs.String(), "\x89PNG") {
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
	img := png1("same", 0)
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

// TestKindFromBytes checks what is sent depends on the bytes, never on the
// first name they were listed under, in both orders: image bytes are
// screened as an image under a page's name, page bytes as text under an
// image's name, and text bytes first listed under a name not served as
// text are screened as text once a path serves them so.
func TestKindFromBytes(t *testing.T) {
	w := newWorld(t)
	w.fake.Flag("nsfw", "sexual")
	ctx := t.Context()
	img, page, text := png1("nsfw", 0), "<p>nsfw page</p>", "nsfw words"
	img2 := png1("nsfw-2", 0)
	q := moderation.NewQuarantine()
	svc := w.service(q)

	// Image bytes as a page first, then as an image.
	w.publish(w.owner.ID, map[string]string{"a.html": img})
	// Page bytes as an image first.
	w.publish(w.owner.ID, map[string]string{"pic.png": page})
	// Text bytes as an image first, and image bytes as an image first.
	w.publish(w.owner.ID, map[string]string{"x.png": text, "y.png": img2})
	svc.Pass(ctx)
	for body, want := range map[string]store.ModerationKind{img: store.ModerationImageText, page: store.ModerationText, text: store.ModerationNone, img2: store.ModerationImage} {
		if r := w.result(body); r.Kind != want {
			t.Errorf("%.20q: kind %q, want %q", body, r.Kind, want)
		}
	}
	for _, body := range []string{img, page, img2} {
		if !q.Has(hashOf(body)) {
			t.Errorf("%.20q not quarantined", body)
		}
	}
	if r := w.result(text); r.Status != store.ModerationSkipped || q.Has(hashOf(text)) {
		t.Fatalf("text under an image's name: %+v", r)
	}
	// The other names: the text is now served as text, so it is queued
	// again and screened as text.
	w.publish(w.owner.ID, map[string]string{"b.png": img, "page.html": page, "x.txt": text, "y.html": img2})
	svc.Pass(ctx)
	if r := w.result(text); r.Status != store.ModerationQuarantined || r.Kind != store.ModerationText || !q.Has(hashOf(text)) {
		t.Fatalf("text once served as text: %+v", r)
	}
	// Each quarantined hash stays quarantined and is not sent again.
	sent := len(w.fake.Sent())
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != sent {
		t.Fatalf("sent %d more", n-sent)
	}
}

// TestSiblings checks a gzip file records what it decompresses to, so the
// edge offers it only for the file whose bytes it holds.
func TestSiblings(t *testing.T) {
	w := newWorld(t)
	gz := func(parts ...string) string {
		var buf bytes.Buffer
		for _, p := range parts {
			zw := gzip.NewWriter(&buf)
			_, _ = zw.Write([]byte(p))
			_ = zw.Close()
		}
		return buf.String()
	}
	base := "<p>benign</p>"
	good, abusive, twoMembers := gz(base), gz("<p>abusive</p>"), gz("<p>ben", "ign</p>")
	w.publish(w.owner.ID, map[string]string{
		"index.html": base, "index.html.gz": good, "other.html": "<p>other</p>", "other.html.gz": abusive,
		"two.html": base + " ", "two.html.gz": twoMembers, "notgz.html.gz": "\x1f\x8b\x08 not really",
	})
	w.service(nil).Pass(t.Context())
	for body, want := range map[string]string{good: hashOf(base), abusive: hashOf("<p>abusive</p>"), twoMembers: "", "\x1f\x8b\x08 not really": ""} {
		if r := w.result(body); r.Decoded != want || r.Status != store.ModerationSkipped {
			t.Errorf("%.12q: %+v, want decoded %q", body, r, want)
		}
	}
}

func TestExemptOwnersAreNeverSent(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	if err := w.db.Moderation().SetExempt(ctx, w.owner.ID, true); err != nil {
		t.Fatal(err)
	}
	w.publish(w.owner.ID, map[string]string{"a.png": png1("exempt", 0), "index.html": "<p>exempt</p>"})
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
	ids, err := w.db.Moderation().ScreenedSites(ctx, "", 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("ScreenedSites() = %v, %v", ids, err)
	}
	if _, err := w.db.Moderation().EnqueueSite(ctx, ids[0], w.now()); err != nil {
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

// TestEnqueue checks a changed site is queued at once and every other site
// in turn by the sweep, a sweepBatch at a time.
func TestEnqueue(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	for i := range 101 {
		w.publish(w.owner.ID, map[string]string{"index.html": fmt.Sprintf("<p>page %d</p>", i)})
	}
	svc := w.service(nil)
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 100 {
		t.Fatalf("first pass sent %d, want the first sweep's 100", n)
	}
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 101 {
		t.Fatalf("second pass sent %d, want all 101", n)
	}
}

// TestRetriesAndBackoff checks failed attempts back off (doubling, after
// any Retry-After, which also pauses every other attempt, at most
// MaxBackoff), resume from the database in a new Service as after a
// restart, and that an input the provider rejects is unscreenable rather
// than retried.
func TestRetriesAndBackoff(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	a := png1("a", 0)
	w.publish(w.owner.ID, map[string]string{"a.png": a})
	w.fake.Fail(&moderation.RetryError{Status: 503}, &moderation.RetryError{Status: 503, After: 10 * time.Minute})

	w.service(nil).Pass(ctx)
	r := w.result(a)
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
	r = w.result(a)
	if r.Attempts != 2 || !r.NextAttemptAt.Equal(w.now().Add(10*time.Minute)) {
		t.Fatalf("after a 503 with Retry-After: %+v", r)
	}
	// The pause holds back other hashes too.
	b := png1("b", 0)
	w.publish(w.owner.ID, map[string]string{"b.png": b})
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 2 || !svc.Paused() {
		t.Fatalf("sent %d during the provider's pause", n)
	}
	if n, err := svc.Backlog(ctx); err != nil || n != 2 {
		t.Fatalf("Backlog() = %d, %v; want 2", n, err)
	}
	// A restart resumes from the queue.
	w.advance(10 * time.Minute)
	w.service(nil).Pass(ctx)
	if r := w.result(a); r.Status != store.ModerationAllowed || r.Attempts != 0 {
		t.Fatalf("after the restart: %+v", r)
	}
	if r := w.result(b); r.Status != store.ModerationAllowed {
		t.Fatalf("b after the restart: %+v", r)
	}

	// Back-off doubles up to its cap.
	c := png1("c", 0)
	w.publish(w.owner.ID, map[string]string{"c.png": c})
	for i := range 3 {
		w.fake.Fail(moderation.ErrUnavailable)
		w.service(nil).Pass(ctx)
		r := w.result(c)
		if want := w.now().Add(moderation.MinBackoff << i); r.Attempts != i+1 || !r.NextAttemptAt.Equal(want) {
			t.Fatalf("attempt %d: %+v, want next at %s", i+1, r, want)
		}
		w.advance(moderation.MinBackoff << i)
	}

	// A Retry-After beyond the cap pauses for MaxBackoff at most.
	w.fake.Fail(&moderation.RetryError{Status: 503, After: 1000 * time.Hour})
	svc = w.service(nil)
	svc.Pass(ctx)
	if r := w.result(c); !r.NextAttemptAt.Equal(w.now().Add(moderation.MaxBackoff)) {
		t.Fatalf("after a huge Retry-After: %+v", r)
	}
	w.advance(moderation.MaxBackoff)
	if svc.Paused() {
		t.Fatal("paused beyond MaxBackoff")
	}

	d := png1("d", 0)
	w.publish(w.owner.ID, map[string]string{"d.png": d})
	w.fake = moderationtest.New()
	w.fake.Fail(moderation.ErrRejected, moderation.ErrRejected)
	w.service(nil).Pass(ctx)
	for _, body := range []string{c, d} {
		if r := w.result(body); r.Status != store.ModerationFlagged || r.Reason != store.ModerationUnscreenable {
			t.Fatalf("rejected %.20q: %+v", body, r)
		}
	}
	if !strings.Contains(w.logs.String(), hashOf(a)) {
		t.Fatalf("retries are logged with the hash:\n%s", w.logs.String())
	}
}

// TestProviderErrors checks a failure that is not the input's (a request
// the provider calls malformed, a refused key, a rate limit) pauses the
// whole worker with its own back-off, is logged at error and counted, and
// leaves the hash's attempts alone instead of skipping it.
func TestProviderErrors(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	a, b := png1("a", 0), png1("b", 0)
	w.publish(w.owner.ID, map[string]string{"a.png": a, "b.png": b})
	w.fake.Fail(&moderation.RetryError{Status: 400, Global: true, Code: "invalid_value"})
	c := w.config(nil)
	c.Concurrency = 1
	svc := moderation.New(c)
	svc.Pass(ctx)
	if n := len(w.fake.Sent()); n != 1 || !svc.Paused() || svc.ProviderErrors() != 1 {
		t.Fatalf("sent %d, paused %v, errors %d; want 1, true, 1", n, svc.Paused(), svc.ProviderErrors())
	}
	for _, body := range []string{a, b} {
		if r := w.result(body); r.Status != store.ModerationPending || r.Attempts != 0 {
			t.Fatalf("%+v; want pending with no attempt counted", r)
		}
	}
	if !strings.Contains(w.logs.String(), "level=ERROR") || !strings.Contains(w.logs.String(), "screening paused") {
		t.Fatalf("not alerted:\n%s", w.logs.String())
	}
	// A second failure in a row doubles the pause.
	w.advance(moderation.MinBackoff)
	w.fake.Fail(&moderation.RetryError{Status: 401, Global: true})
	svc.Pass(ctx)
	if r := w.result(a); !r.NextAttemptAt.Equal(w.now().Add(2 * moderation.MinBackoff)) {
		t.Fatalf("second pause: %+v", r)
	}
	if !strings.Contains(w.logs.String(), "WARDLOCK_OPENAI_API_KEY") {
		t.Fatalf("refused key not named:\n%s", w.logs.String())
	}
	w.advance(2 * moderation.MinBackoff)
	svc.Pass(ctx)
	for _, body := range []string{a, b} {
		if r := w.result(body); r.Status != store.ModerationAllowed {
			t.Fatalf("after the pause: %+v", r)
		}
	}
}

// TestImages checks what is sent for each kind of image: GIF frames as
// PNG (an animated GIF's first, middle and last; trailing junk and a cut
// frame tolerated; every frame of a short one), a large PNG scaled down, an icon's PNG, and that
// formats nothing can read are served but flagged unscreenable, listed for
// review and in the operator's digest.
func TestImages(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	red, green, blue := color.RGBA{0xff, 0, 0, 0xff}, color.RGBA{0, 0xff, 0, 0xff}, color.RGBA{0, 0, 0xff, 0xff}
	animated := testimages.GIF(red, red, green, red, blue)
	junk := testimages.GIF(green) + "trailing junk"
	cut := testimages.GIF(blue, red)
	cut = cut[:len(cut)-8]
	big := testimages.SizedPNG("big", 3000, 1000, moderation.MaxImageSize)
	icon := iconWith(png1("in-icon", 0))
	bmp, avif := "BM\x36\x00\x00\x00 bitmap", "\x00\x00\x00\x1cftypavif\x00\x00\x00\x00 rest"
	w.publish(w.owner.ID, map[string]string{
		"anim.gif": animated, "junk.gif": junk, "cut.gif": cut, "big.png": big, "favicon.ico": icon,
		"a.bmp": bmp, "b.avif": avif, "c.ico": iconWith("BMP-only entry"), "fake.png": "not an image at all",
	})
	box := &mailbox{}
	c := w.config(moderation.NewQuarantine())
	c.Mailer, c.AbuseEmail = box, abuse
	w.fake.Flag("in-icon", "violence")
	moderation.New(c).Pass(ctx)

	var colours []color.RGBA
	var sizes []image.Point
	for _, img := range w.sentImages() {
		r, g, b, _ := img.At(0, 0).RGBA()
		colours = append(colours, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff})
		sizes = append(sizes, img.Bounds().Size())
	}
	count := func(c color.RGBA) int {
		n := 0
		for _, got := range colours {
			if got == c {
				n++
			}
		}
		return n
	}
	// anim: all five frames (red, red, green, red, blue); junk: green;
	// cut: blue.
	if count(red) != 3 || count(green) != 2 || count(blue) != 2 {
		t.Errorf("GIF frames sent: %v", colours)
	}
	scaled := false
	for _, s := range sizes {
		if s == image.Pt(moderation.ScaledSide, moderation.ScaledSide/3) {
			scaled = true
		}
	}
	if !scaled {
		t.Errorf("big PNG not sent scaled: %v", sizes)
	}
	for _, in := range w.fake.Sent() {
		if len(in.Image) > moderation.MaxImageSize || in.ImageType == "image/gif" {
			t.Errorf("sent %s of %d bytes", in.ImageType, len(in.Image))
		}
	}
	if r := w.result(icon); r.Status != store.ModerationFlagged || strings.Join(r.Categories, ",") != "violence" {
		t.Errorf("icon: %+v", r)
	}
	for _, body := range []string{animated, junk, cut, big} {
		if r := w.result(body); r.Status != store.ModerationAllowed {
			t.Errorf("%.12q: %+v", body, r)
		}
	}
	unscreenable := []string{bmp, avif, iconWith("BMP-only entry"), "not an image at all"}
	for _, body := range unscreenable[:3] {
		if r := w.result(body); r.Status != store.ModerationFlagged || r.Reason != store.ModerationUnscreenable || r.Kind != store.ModerationImage {
			t.Errorf("%.12q: %+v, want flagged unscreenable", body, r)
		}
	}
	// Bytes that are not an image under an image's name are nothing to
	// screen.
	if r := w.result("not an image at all"); r.Status != store.ModerationSkipped {
		t.Errorf("fake.png: %+v", r)
	}
	review, err := moderation.New(c).Review(ctx)
	if err != nil || len(review) != 4 {
		t.Fatalf("Review() = %d, %v; want the icon and three unscreenable images", len(review), err)
	}
	// One digest for the pass, naming every flagged file.
	msgs := box.to(abuse)
	if len(msgs) != 1 || msgs[0].Subject != "Content screening flagged 4 files" {
		t.Fatalf("operator notices: %+v", msgs)
	}
	for _, body := range unscreenable[:3] {
		if !strings.Contains(msgs[0].Text, hashOf(body)) || !strings.Contains(msgs[0].Text, "unscreenable") {
			t.Errorf("digest lacks %.12q:\n%s", body, msgs[0].Text)
		}
	}
}

// iconWith returns an ICO holding one entry of data.
func iconWith(data string) string {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 1, 0, 1, 0})
	entry := make([]byte, 16)
	binary.LittleEndian.PutUint32(entry[8:], uint32(len(data)))
	binary.LittleEndian.PutUint32(entry[12:], 22)
	b.Write(entry)
	b.WriteString(data)
	return b.String()
}

// TestEmbedded checks a page's and an SVG's text, and the images they
// embed as data: URIs (an SVG embedded in a page included), are screened.
func TestEmbedded(t *testing.T) {
	w := newWorld(t)
	w.fake.Flag("nsfw", "sexual")
	w.fake.Flag("svg-words", "harassment")
	uri := func(b string) string { return "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(b)) }
	svgWithImage := `<svg xmlns="http://www.w3.org/2000/svg"><image href="` + uri(png1("nsfw", 0)) + `"/></svg>`
	page := `<p>Hello</p><img src="` + uri(png1("nsfw", 0)) + `">`
	nested := `<p>Hi</p><img src="data:image/svg+xml;base64,` + base64.StdEncoding.EncodeToString([]byte(svgWithImage)) + `">`
	svgText := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><text>svg-words</text></svg>`
	inline := `<p>Text</p><svg><text>svg-words</text></svg>`
	many := "<p>icons</p>" + strings.Repeat(`<img src="`+uri(png1("icon", 0))+`">`, moderation.MaxEmbedded+1)
	// Stylesheets and scripts: their images are screened, plain and
	// percent-encoded SVG data: URIs as text, but never their source.
	css := `.a { background: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg'><text>svg-words</text></svg>") } /* nsfw */`
	js := `const icon = "data:image/svg+xml,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%3E%3Ctext%3Esvg-words%3C%2Ftext%3E%3C%2Fsvg%3E"; // nsfw`
	jsImage := `img.src = '` + uri(png1("nsfw", 0)) + `';`
	cssUnquoted := `.b{background:url(data:image/svg+xml;charset=utf-8,%3Csvg%3E%3Ctext%3Esvg-words%3C/text%3E%3C/svg%3E)}`
	w.publish(w.owner.ID, map[string]string{
		"page.html": page, "nested.html": nested, "logo.svg": svgText, "drawn.svg": svgWithImage, "inline.html": inline, "many.html": many,
		"a.css": css, "b.js": js, "c.js": jsImage, "d.css": cssUnquoted,
	})
	q := moderation.NewQuarantine()
	w.service(q).Pass(t.Context())
	for body, want := range map[string]store.ModerationStatus{
		page: store.ModerationQuarantined, nested: store.ModerationQuarantined, svgWithImage: store.ModerationQuarantined,
		svgText: store.ModerationFlagged, inline: store.ModerationFlagged,
	} {
		if r := w.result(body); r.Status != want || r.Kind != store.ModerationText {
			t.Errorf("%.30q: %+v, want %s", body, r, want)
		}
	}
	if r := w.result(many); r.Status != store.ModerationFlagged || r.Reason != store.ModerationUnscreenable {
		t.Errorf("too many embedded images: %+v", r)
	}
	for body, want := range map[string]store.ModerationStatus{
		css: store.ModerationFlagged, js: store.ModerationFlagged, jsImage: store.ModerationQuarantined, cssUnquoted: store.ModerationFlagged,
	} {
		if r := w.result(body); r.Status != want || r.Kind != store.ModerationNone {
			t.Errorf("%.30q: %+v, want %s", body, r, want)
		}
	}
	for _, in := range w.fake.Sent() {
		if strings.Contains(strings.Join(in.Text, " "), "nsfw") || strings.Contains(strings.Join(in.Text, " "), "background") {
			t.Fatalf("stylesheet or script source sent as text: %q", in.Text)
		}
	}
}

// TestPageText checks a page is sent as its plain text, in chunks, and
// truncated past the limit.
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

// TestRescans checks a rescan keeps the verdict and the operator's decision
// unless the endpoint gives a new one, and that a rescan confirming a
// quarantine tells owners nothing new.
func TestRescans(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	w.fake.Flag("nsfw", "sexual")
	img := png1("nsfw", 0)
	w.publish(w.owner.ID, map[string]string{"a.png": img})
	box := &mailbox{}
	c := w.config(moderation.NewQuarantine())
	c.Mailer, c.AbuseEmail = box, abuse
	svc := moderation.New(c)
	svc.Pass(ctx)
	if n := len(box.to(w.owner.Email)); n != 1 {
		t.Fatalf("%d owner notices, want 1", n)
	}
	if err := svc.Confirm(ctx, hashOf(img)); err != nil {
		t.Fatal(err)
	}
	// Rejected on the rescan: no verdict, so the quarantine and its
	// confirmation stand.
	if err := svc.Rescan(ctx, hashOf(img)); err != nil {
		t.Fatal(err)
	}
	w.fake.Fail(moderation.ErrRejected)
	svc.Pass(ctx)
	if r := w.result(img); r.Status != store.ModerationQuarantined || r.ConfirmedAt.IsZero() || !r.NextAttemptAt.IsZero() {
		t.Fatalf("after a rejected rescan: %+v", r)
	}
	// The same verdict again: the confirmation stands, and nobody is told
	// again.
	if err := svc.Rescan(ctx, hashOf(img)); err != nil {
		t.Fatal(err)
	}
	svc.Pass(ctx)
	if r := w.result(img); r.Status != store.ModerationQuarantined || r.ConfirmedAt.IsZero() {
		t.Fatalf("after a rescan with the same verdict: %+v", r)
	}
	if n := len(box.to(w.owner.Email)); n != 1 {
		t.Fatalf("%d owner notices after the rescan, want still 1", n)
	}
	if n := len(box.to(abuse)); n != 1 {
		t.Fatalf("%d operator notices, want 1", n)
	}
	// A new verdict replaces both.
	if err := svc.Rescan(ctx, hashOf(img)); err != nil {
		t.Fatal(err)
	}
	w.fake = moderationtest.New()
	c.Moderator = w.fake
	moderation.New(c).Pass(ctx)
	if r := w.result(img); r.Status != store.ModerationAllowed || !r.ConfirmedAt.IsZero() {
		t.Fatalf("after a rescan with a new verdict: %+v", r)
	}
}

// TestRescanKeepsQuarantine checks a rescan never lowers a quarantine
// because part of the file could not be judged: a page withheld for an
// embedded image stays withheld when the image is rejected on the rescan
// and its text is clean, and is released only when every input is judged
// clean.
func TestRescanKeepsQuarantine(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	w.fake.Flag("nsfw", "sexual")
	page := `<p>Clean words</p><img src="data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte(png1("nsfw", 0))) + `">`
	w.publish(w.owner.ID, map[string]string{"index.html": page})
	q := moderation.NewQuarantine()
	svc := w.service(q)
	svc.Pass(ctx)
	if r := w.result(page); r.Status != store.ModerationQuarantined {
		t.Fatalf("first verdict: %+v", r)
	}
	if err := svc.Confirm(ctx, hashOf(page)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rescan(ctx, hashOf(page)); err != nil {
		t.Fatal(err)
	}
	// The text first (judged clean), then the image (rejected).
	w.fake.Fail(nil, moderation.ErrRejected)
	svc.Pass(ctx)
	if r := w.result(page); r.Status != store.ModerationQuarantined || r.ConfirmedAt.IsZero() || !q.Has(hashOf(page)) {
		t.Fatalf("after a rescan that could not judge the image: %+v", r)
	}
	// Every input judged clean: released.
	if err := svc.Rescan(ctx, hashOf(page)); err != nil {
		t.Fatal(err)
	}
	w.fake = moderationtest.New()
	moderation.New(w.config(q)).Pass(ctx)
	if r := w.result(page); r.Status != store.ModerationAllowed {
		t.Fatalf("after a clean rescan: %+v", r)
	}
}

// TestOwnerNotices checks owners of taken-down sites and suspended owners
// are not told of a quarantine; the operator hears of every site.
func TestOwnerNotices(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	w.fake.Flag("nsfw", "sexual")
	img := png1("nsfw", 0)
	suspended := store.Owner{ID: store.NewID(), Email: "suspended@example.test", CreatedAt: w.now()}
	if err := w.db.Owners().Create(ctx, suspended); err != nil {
		t.Fatal(err)
	}
	w.publish(w.owner.ID, map[string]string{"a.png": img})
	w.publish(w.owner.ID, map[string]string{"b.png": img})
	w.publish(suspended.ID, map[string]string{"c.png": img})
	ss, err := w.db.Sites().List(ctx, w.owner.ID, "", 10)
	if err != nil || len(ss) != 2 {
		t.Fatalf("sites: %v, %v", ss, err)
	}
	if err := w.db.Sites().TakeDown(ctx, ss[1].ID, w.now()); err != nil {
		t.Fatal(err)
	}
	box := &mailbox{}
	c := w.config(moderation.NewQuarantine())
	c.Mailer, c.AbuseEmail = box, abuse
	svc := moderation.New(c)
	svc.Pass(ctx)
	if err := w.db.Owners().Suspend(ctx, suspended.ID, w.now()); err != nil {
		t.Fatal(err)
	}
	// Quarantined by a rescan once the owner is suspended.
	if err := svc.Rescan(ctx, hashOf(img)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Release(ctx, hashOf(img)); err != nil {
		t.Fatal(err)
	}
	box.sent = nil
	svc.Pass(ctx)
	owners := box.to(w.owner.Email)
	if len(owners) != 1 || !strings.Contains(owners[0].Text, ss[0].Name) || strings.Contains(owners[0].Text, ss[1].Name) {
		t.Fatalf("owner notices: %+v", owners)
	}
	if n := len(box.to(suspended.Email)); n != 0 {
		t.Fatalf("suspended owner told %d times", n)
	}
	if ops := box.to(abuse); len(ops) != 1 || !strings.Contains(ops[0].Text, ss[1].Name) {
		t.Fatalf("operator notice: %+v", ops)
	}
}

// TestInvalidate checks the sites serving a hash are announced once it has
// its first verdict, so the edge stops treating it as pending.
func TestInvalidate(t *testing.T) {
	w := newWorld(t)
	w.publish(w.owner.ID, map[string]string{"a.png": png1("a", 0)})
	var mu sync.Mutex
	var got []string
	c := w.config(nil)
	c.Invalidate = func(id string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, id)
	}
	svc := moderation.New(c)
	svc.Pass(t.Context())
	ss, err := w.db.Sites().List(t.Context(), w.owner.ID, "", 10)
	if err != nil || len(got) != 1 || got[0] != ss[0].ID {
		t.Fatalf("invalidated %v, want %s (%v)", got, ss[0].ID, err)
	}
	svc.Pass(t.Context())
	if len(got) != 1 {
		t.Fatalf("invalidated again: %v", got)
	}
	// A gzip sibling recorded without a new verdict (a rescan) announces
	// its site again, so the edge offers it.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("<p>page</p>"))
	_ = zw.Close()
	w.publish(w.owner.ID, map[string]string{"index.html": "<p>page</p>", "index.html.gz": buf.String()})
	svc.Pass(t.Context())
	n := len(got)
	if err := svc.Rescan(t.Context(), hashOf(buf.String())); err != nil {
		t.Fatal(err)
	}
	svc.Pass(t.Context())
	if len(got) != n+1 {
		t.Fatalf("invalidated %v after the sibling's rescan, want one more than %d", got, n)
	}
}

func TestDecisions(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	w.fake.Flag("nsfw", "sexual")
	nsfw, fine := png1("nsfw", 0), png1("fine", 0)
	w.publish(w.owner.ID, map[string]string{"a.png": nsfw, "b.png": fine})
	svc := w.service(moderation.NewQuarantine())
	svc.Pass(ctx)
	a, b := hashOf(nsfw), hashOf(fine)
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
	if !strings.Contains(w.logs.String(), "operator confirmed quarantine") {
		t.Fatalf("decisions are not logged:\n%s", w.logs.String())
	}
}
