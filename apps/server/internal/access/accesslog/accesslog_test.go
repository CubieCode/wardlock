// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package accesslog_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

// fakeRepo records batches. While gate is non-nil, Append waits on it (or
// ctx), standing in for a slow or stuck database.
type fakeRepo struct {
	mu      sync.Mutex
	batches [][]store.AccessEntry
	prunes  []time.Time
	backlog int // entries Prune pretends are expired
	gate    chan struct{}
	err     error
	written chan int // receives each batch's size, if set
}

func (r *fakeRepo) Append(ctx context.Context, entries []store.AccessEntry) error {
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	err := r.err
	if err == nil {
		r.batches = append(r.batches, append([]store.AccessEntry(nil), entries...))
	}
	r.mu.Unlock()
	if r.written != nil {
		r.written <- len(entries)
	}
	return err
}

func (r *fakeRepo) Prune(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prunes = append(r.prunes, cutoff)
	n := min(r.backlog, limit)
	r.backlog -= n
	return int64(n), nil
}

func (r *fakeRepo) entries() []store.AccessEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []store.AccessEntry
	for _, b := range r.batches {
		all = append(all, b...)
	}
	return all
}

func (r *fakeRepo) pruned() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.prunes...)
}

func entry(path string) accesslog.Entry {
	return accesslog.Entry{
		Time: time.UnixMilli(1_800_000_000_000), SiteID: "site-1", Host: "docs.wardlock.localhost", Path: path,
		Status: 200, Via: store.ViaPublic, IP: "192.0.2.77", UserAgent: "curl/8",
	}
}

func start(t *testing.T, cfg accesslog.Config) *accesslog.Writer {
	t.Helper()
	w := accesslog.Start(cfg)
	t.Cleanup(func() {
		// A test's stuck store would otherwise hold Close for writeTimeout.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = w.Close(ctx)
	})
	return w
}

func closeWriter(t *testing.T, w *accesslog.Writer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestRecordNeverBlocksOnTheStore(t *testing.T) {
	repo := &fakeRepo{gate: make(chan struct{})} // never opened: the database hangs
	w := start(t, accesslog.Config{Repo: repo, BufferSize: 10, BatchSize: 2, FlushInterval: time.Hour})
	const n = 10_000
	began := time.Now()
	for i := range n {
		w.Record(entry(fmt.Sprintf("/page-%d", i)))
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("%d Record calls against a stuck store took %v", n, took)
	}
	// At most the buffer plus the batch stuck in Append got through.
	if got := w.Dropped(); got < n-10-2 || got > n {
		t.Fatalf("Dropped() = %d, want about %d", got, n-12)
	}
}

func TestDroppedEntriesAreCountedAndLogged(t *testing.T) {
	repo := &fakeRepo{gate: make(chan struct{})}
	var logs syncBuffer
	w := start(t, accesslog.Config{
		Repo: repo, Log: slog.New(slog.NewTextHandler(&logs, nil)),
		BufferSize: 3, BatchSize: 1, FlushInterval: 10 * time.Millisecond,
	})
	// The first entry may be taken into a batch before the buffer fills,
	// so record until something has been dropped.
	for i := 0; w.Dropped() < 5; i++ {
		w.Record(entry("/"))
		if i > 1000 {
			t.Fatal("nothing was dropped")
		}
	}
	waitFor(t, func() bool { return strings.Contains(logs.String(), "access log entries dropped") })
	close(repo.gate)
	closeWriter(t, w)
	if got, kept := w.Dropped(), int64(len(repo.entries())); got == 0 || kept == 0 || kept > 4 {
		t.Fatalf("Dropped() = %d with %d kept; want drops and at most buffer+batch kept", got, kept)
	}
}

func TestBatching(t *testing.T) {
	t.Run("full batch writes before the interval", func(t *testing.T) {
		repo := &fakeRepo{written: make(chan int, 10)}
		w := start(t, accesslog.Config{Repo: repo, BatchSize: 3, FlushInterval: time.Hour})
		for range 3 {
			w.Record(entry("/"))
		}
		select {
		case n := <-repo.written:
			if n != 3 {
				t.Fatalf("batch of %d, want 3", n)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a full batch was not written")
		}
	})
	t.Run("interval writes a partial batch", func(t *testing.T) {
		repo := &fakeRepo{written: make(chan int, 10)}
		w := start(t, accesslog.Config{Repo: repo, BatchSize: 500, FlushInterval: 20 * time.Millisecond})
		w.Record(entry("/"))
		select {
		case n := <-repo.written:
			if n != 1 {
				t.Fatalf("batch of %d, want 1", n)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the flush interval did not write")
		}
	})
}

func TestCloseFlushesEverythingBuffered(t *testing.T) {
	repo := &fakeRepo{}
	w := accesslog.Start(accesslog.Config{Repo: repo, BufferSize: 2000, BatchSize: 500, FlushInterval: time.Hour})
	for i := range 1234 {
		w.Record(entry(fmt.Sprintf("/%d", i)))
	}
	closeWriter(t, w)
	if got := len(repo.entries()); got != 1234 {
		t.Fatalf("%d entries written by Close, want 1234", got)
	}
	for _, b := range repo.batches {
		if len(b) > 500 {
			t.Fatalf("batch of %d exceeds the batch size", len(b))
		}
	}
	if w.Dropped() != 0 {
		t.Fatalf("Dropped() = %d, want 0", w.Dropped())
	}

	w.Record(entry("/late"))
	if w.Dropped() != 1 || len(repo.entries()) != 1234 {
		t.Fatalf("a record after Close was not dropped and counted")
	}
	closeWriter(t, w) // Close is idempotent.
}

func TestCloseGivesUpAtItsDeadline(t *testing.T) {
	repo := &fakeRepo{gate: make(chan struct{})}
	w := accesslog.Start(accesslog.Config{Repo: repo, FlushInterval: time.Hour})
	w.Record(entry("/"))
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := w.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() = %v, want deadline exceeded", err)
	}
	if w.Dropped() != 1 {
		t.Fatalf("Dropped() = %d, want the abandoned entry counted", w.Dropped())
	}
}

func TestFailedWritesAreCounted(t *testing.T) {
	repo := &fakeRepo{err: errors.New("disk full")}
	var logs syncBuffer
	w := accesslog.Start(accesslog.Config{Repo: repo, Log: slog.New(slog.NewTextHandler(&logs, nil)), FlushInterval: time.Hour})
	for range 3 {
		w.Record(entry("/"))
	}
	closeWriter(t, w)
	if w.Dropped() != 3 {
		t.Fatalf("Dropped() = %d, want 3", w.Dropped())
	}
	if !strings.Contains(logs.String(), "write access log") || !strings.Contains(logs.String(), "disk full") {
		t.Fatalf("failed write not logged:\n%s", logs.String())
	}
}

func TestTruncation(t *testing.T) {
	tests := []struct {
		name   string
		trunc  accesslog.Truncation
		ip     string
		stored string
	}{
		{"IPv4 default /24", accesslog.DefaultTruncation, "203.0.113.77", "203.0.113.0"},
		{"IPv4 /16", accesslog.Truncation{IPv4Prefix: 16, IPv6Prefix: 48}, "203.0.113.77", "203.0.0.0"},
		{"IPv4 kept whole", accesslog.Truncation{IPv4Prefix: 32, IPv6Prefix: 128}, "203.0.113.77", "203.0.113.77"},
		{"IPv4 not stored", accesslog.Truncation{IPv4Prefix: 0, IPv6Prefix: 48}, "203.0.113.77", ""},
		{"IPv6 default /48", accesslog.DefaultTruncation, "2001:db8:abcd:12:34::1", "2001:db8:abcd::"},
		{"IPv6 /64", accesslog.Truncation{IPv4Prefix: 24, IPv6Prefix: 64}, "2001:db8:abcd:12:34::1", "2001:db8:abcd:12::"},
		{"IPv6 kept whole", accesslog.Truncation{IPv4Prefix: 32, IPv6Prefix: 128}, "2001:db8::1", "2001:db8::1"},
		{"IPv6 not stored", accesslog.Truncation{IPv4Prefix: 24, IPv6Prefix: 0}, "2001:db8::1", ""},
		{"IPv6 zone dropped", accesslog.Truncation{IPv4Prefix: 32, IPv6Prefix: 128}, "fe80::1%eth0", "fe80::1"},
		{"IPv4-mapped IPv6 is IPv4", accesslog.DefaultTruncation, "::ffff:203.0.113.77", "203.0.113.0"},
		{"not an address", accesslog.DefaultTruncation, "proxy.internal", ""},
		{"host and port", accesslog.DefaultTruncation, "203.0.113.77:443", ""},
		{"empty", accesslog.DefaultTruncation, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.trunc.Truncate(tt.ip); got != tt.stored {
				t.Fatalf("Truncate(%q) = %q, want %q", tt.ip, got, tt.stored)
			}
			// The writer applies its configured truncation to every entry.
			repo := &fakeRepo{}
			w := accesslog.Start(accesslog.Config{Repo: repo, Truncation: tt.trunc})
			e := entry("/")
			e.IP = tt.ip
			w.Record(e)
			closeWriter(t, w)
			if got := repo.entries()[0].IP; got != tt.stored {
				t.Fatalf("stored IP %q, want %q", got, tt.stored)
			}
		})
	}
}

// secret is a synthetic token secret: 43 base64url characters, with "_"
// and "-" in it.
const (
	secret = "Zm9vYmFy_c2VjcmV0-dGVzdC1vbmx5LW5vdC1yZWFsx"
	token  = "wl_abcdefgh23_" + secret
)

func TestRawTokensAreNeverStored(t *testing.T) {
	tests := []struct {
		name, path, ua, wantPath, wantUA string
	}{
		{"share link query", "/guide?t=" + token, "curl/8", "/guide", "curl/8"},
		{"any query string", "/search?q=private&page=2", "curl/8", "/search", "curl/8"},
		{"fragment", "/a#t=" + token, "curl/8", "/a", "curl/8"},
		{"token in the path", "/links/" + token + "/view", "curl/8", "/links/wl_abcdefgh23_[redacted]/view", "curl/8"},
		{"token at the end of the path", "/" + token, "curl/8", "/wl_abcdefgh23_[redacted]", "curl/8"},
		{"upper-case token", "/" + strings.ToUpper(token), "curl/8", "/wl_ABCDEFGH23_[redacted]", "curl/8"},
		{"token glued to text", "/x_" + token, "curl/8", "/x_wl_abcdefgh23_[redacted]", "curl/8"},
		{"token in the user agent", "/", "bot (" + token + ")", "/", "bot (wl_abcdefgh23_[redacted])"},
		{"empty path", "", "", "/", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			var logs syncBuffer
			w := accesslog.Start(accesslog.Config{Repo: repo, Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
			e := entry(tt.path)
			e.UserAgent = tt.ua
			w.Record(e)
			closeWriter(t, w)
			got := repo.entries()[0]
			if got.Path != tt.wantPath || got.UserAgent != tt.wantUA {
				t.Fatalf("stored path %q, user agent %q; want %q, %q", got.Path, got.UserAgent, tt.wantPath, tt.wantUA)
			}
			for _, s := range []string{fmt.Sprintf("%+v", got), logs.String()} {
				if strings.Contains(strings.ToLower(s), strings.ToLower(secret)) {
					t.Fatalf("raw token secret stored or logged: %s", s)
				}
			}
		})
	}
}

func TestScrubbedFields(t *testing.T) {
	repo := &fakeRepo{}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.FixedZone("NZDT", 13*3600))
	w := accesslog.Start(accesslog.Config{Repo: repo, Now: func() time.Time { return now }})
	long := entry("/" + strings.Repeat("é", 2000))
	long.UserAgent = strings.Repeat("u", 1000)
	long.Time = time.Time{}
	notLink := entry("/")
	notLink.LinkPublicID = "abcdefgh23" // ignored unless Via is link
	link := entry("/")
	link.Via, link.LinkPublicID = store.ViaLink, "abcdefgh23"
	w.Record(long)
	w.Record(notLink)
	w.Record(link)
	closeWriter(t, w)
	got := repo.entries()
	if len(got[0].Path) > 2048 || !strings.HasPrefix(got[0].Path, "/é") || strings.ContainsRune(got[0].Path, '�') {
		t.Errorf("long path stored as %d bytes, not cut to 2048 bytes of valid UTF-8", len(got[0].Path))
	}
	if len(got[0].UserAgent) != 512 {
		t.Errorf("user agent stored as %d bytes, want 512", len(got[0].UserAgent))
	}
	if !got[0].OccurredAt.Equal(now) || got[0].OccurredAt.Location() != time.UTC {
		t.Errorf("zero time stored as %v, want now in UTC", got[0].OccurredAt)
	}
	if got[1].LinkPublicID != "" || got[2].LinkPublicID != "abcdefgh23" {
		t.Errorf("link public IDs = %q, %q", got[1].LinkPublicID, got[2].LinkPublicID)
	}
	if got[0].ID == "" || got[0].ID == got[1].ID {
		t.Errorf("entries need distinct IDs: %q, %q", got[0].ID, got[1].ID)
	}
}

func TestRetention(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	t.Run("prunes at start and on the interval", func(t *testing.T) {
		repo := &fakeRepo{}
		start(t, accesslog.Config{
			Repo: repo, Now: func() time.Time { return now },
			Retention: 90 * 24 * time.Hour, PruneInterval: 10 * time.Millisecond,
		})
		waitFor(t, func() bool { return len(repo.pruned()) >= 3 })
		for _, cutoff := range repo.pruned() {
			if want := now.Add(-90 * 24 * time.Hour); !cutoff.Equal(want) {
				t.Fatalf("pruned before %v, want %v", cutoff, want)
			}
		}
	})
	t.Run("a backlog is pruned in chunks", func(t *testing.T) {
		repo := &fakeRepo{backlog: 25}
		start(t, accesslog.Config{
			Repo: repo, Now: func() time.Time { return now },
			Retention: time.Hour, PruneInterval: time.Hour, PruneChunk: 10,
		})
		// 10, 10, 5: the short chunk ends the run.
		waitFor(t, func() bool { return len(repo.pruned()) >= 3 })
		time.Sleep(20 * time.Millisecond) // proving no fourth chunk follows
		repo.mu.Lock()
		defer repo.mu.Unlock()
		if len(repo.prunes) != 3 || repo.backlog != 0 {
			t.Fatalf("%d prune calls leaving %d, want 3 leaving 0", len(repo.prunes), repo.backlog)
		}
	})
	t.Run("pruning does not hold up batches", func(t *testing.T) {
		repo := &blockingPrune{fakeRepo: fakeRepo{written: make(chan int, 1)}, release: make(chan struct{})}
		defer close(repo.release)
		w := start(t, accesslog.Config{Repo: repo, Retention: time.Hour, FlushInterval: time.Millisecond})
		w.Record(entry("/"))
		select {
		case <-repo.written:
		case <-time.After(5 * time.Second):
			t.Fatal("a batch waited on pruning")
		}
	})
	t.Run("zero retention keeps everything", func(t *testing.T) {
		repo := &fakeRepo{}
		w := accesslog.Start(accesslog.Config{Repo: repo, PruneInterval: time.Millisecond})
		time.Sleep(20 * time.Millisecond) // nothing to wait for: proving an absence
		closeWriter(t, w)
		if n := len(repo.pruned()); n != 0 {
			t.Fatalf("pruned %d times with no retention", n)
		}
	})
}

// blockingPrune is a store whose Prune waits for release (or ctx).
type blockingPrune struct {
	fakeRepo
	release chan struct{}
}

func (r *blockingPrune) Prune(ctx context.Context, _ time.Time, _ int) (int64, error) {
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return 0, nil
}

func TestViaIsNormalised(t *testing.T) {
	tests := []struct {
		via, link string
		wantVia   store.AccessVia
		wantLink  string
	}{
		{"public", "", store.ViaPublic, ""},
		{"link", "abcdefgh23", store.ViaLink, "abcdefgh23"},
		{"link", "", store.ViaNone, ""},
		{"cookie", "", store.ViaNone, ""},
		{"", "", store.ViaNone, ""},
		{"password", "abcdefgh23", store.ViaPassword, ""},
	}
	for _, tt := range tests {
		repo := &fakeRepo{}
		w := accesslog.Start(accesslog.Config{Repo: repo})
		e := entry("/")
		e.Via, e.LinkPublicID = store.AccessVia(tt.via), tt.link
		w.Record(e)
		closeWriter(t, w)
		got := repo.entries()[0]
		if got.Via != tt.wantVia || got.LinkPublicID != tt.wantLink {
			t.Errorf("via %q link %q stored as %q %q, want %q %q", tt.via, tt.link, got.Via, got.LinkPublicID, tt.wantVia, tt.wantLink)
		}
	}
	if accesslog.ParseVia("oauth") != store.ViaOAuth || accesslog.ParseVia("OAUTH") != store.ViaNone {
		t.Error("ParseVia accepts only the exact names")
	}
}

// TestMalformedEntriesDoNotLoseTheBatch writes good and malformed entries
// in one batch to SQLite: every storable entry survives, and the one with a
// status the table cannot hold is dropped and counted alone.
func TestMalformedEntriesDoNotLoseTheBatch(t *testing.T) {
	db := storetest.New(t)
	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Owners().Create(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(t.Context(), store.ActorFor(site.OwnerID), site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	w := accesslog.Start(accesslog.Config{Repo: db.AccessLog(), FlushInterval: time.Hour})
	for _, mutate := range []func(*accesslog.Entry){
		func(*accesslog.Entry) {},
		func(e *accesslog.Entry) { e.Via = "cookie" },
		func(e *accesslog.Entry) { e.Via, e.LinkPublicID = store.ViaLink, "" },
		func(e *accesslog.Entry) { e.Via, e.LinkPublicID = store.ViaPublic, "abcdefgh23" },
		func(e *accesslog.Entry) { e.Status = 0 },
		func(e *accesslog.Entry) { e.Status = 999 },
		func(*accesslog.Entry) {},
	} {
		e := entry("/")
		e.SiteID = site.ID
		mutate(&e)
		w.Record(e)
	}
	closeWriter(t, w)
	got, err := db.AccessLog().ListBySite(t.Context(), store.ActorFor(owner.ID), site.ID, store.AccessCursor{}, 100)
	if err != nil || len(got) != 5 {
		t.Fatalf("ListBySite() = %d entries, %v; want 5", len(got), err)
	}
	if w.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want the 2 bad statuses", w.Dropped())
	}
}

// TestWithSQLite runs the writer against the real store: entries arrive
// scrubbed, queryable by site and link, and retention prunes old ones.
func TestWithSQLite(t *testing.T) {
	db := storetest.New(t)
	ctx := t.Context()
	owner := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	site := store.Site{ID: store.NewID(), OwnerID: owner.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Owners().Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(ctx, store.ActorFor(site.OwnerID), site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	w := accesslog.Start(accesslog.Config{Repo: db.AccessLog(), Truncation: accesslog.DefaultTruncation})
	old := entry("/old")
	old.SiteID, old.Time = site.ID, now.Add(-100*24*time.Hour)
	viaLink := entry("/guide?t=" + token)
	viaLink.SiteID, viaLink.Time, viaLink.Via, viaLink.LinkPublicID = site.ID, now, store.ViaLink, "abcdefgh23"
	w.Record(old)
	w.Record(viaLink)
	closeWriter(t, w)

	all, err := db.AccessLog().ListBySite(ctx, store.ActorFor(owner.ID), site.ID, store.AccessCursor{}, 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListBySite() = %v, %v; want 2 entries", all, err)
	}
	byLink, err := db.AccessLog().ListByLink(ctx, store.ActorFor(owner.ID), site.ID, "abcdefgh23", store.AccessCursor{}, 10)
	if err != nil || len(byLink) != 1 {
		t.Fatalf("ListByLink() = %v, %v; want 1 entry", byLink, err)
	}
	if got := byLink[0]; got.Path != "/guide" || got.IP != "192.0.2.0" || strings.Contains(fmt.Sprintf("%+v", got), secret) {
		t.Fatalf("stored %+v", got)
	}

	// A writer with 90-day retention prunes the old entry when it starts.
	w = accesslog.Start(accesslog.Config{Repo: db.AccessLog(), Retention: 90 * 24 * time.Hour})
	closeWriter(t, w)
	all, err = db.AccessLog().ListBySite(ctx, store.ActorFor(owner.ID), site.ID, store.AccessCursor{}, 10)
	if err != nil || len(all) != 1 || all[0].Path != "/guide" {
		t.Fatalf("after retention ListBySite() = %v, %v; want only /guide", all, err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// syncBuffer is a bytes.Buffer safe for the writer goroutine to log into
// while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
