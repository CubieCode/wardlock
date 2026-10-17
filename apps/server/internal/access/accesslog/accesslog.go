// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package accesslog is the access-log pipeline: the edge hands it one Entry
// per site access, it scrubs the entry (IP truncation, token and query
// redaction) and a background goroutine writes buffered entries to the store
// in batches, so serving never waits on the database. It also prunes entries
// past the retention period. Like the rest of internal/access it takes plain
// values and imports no HTTP packages.
package accesslog

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"wardlock.nz/server/internal/store"
)

// Defaults for Config's zero values.
const (
	DefaultBufferSize    = 10_000
	DefaultBatchSize     = 500
	DefaultFlushInterval = time.Second
	DefaultPruneInterval = time.Hour
	DefaultPruneChunk    = 1000
	// writeTimeout bounds one batch write or prune chunk while running, so
	// a stuck database cannot hold the writer forever.
	writeTimeout = 10 * time.Second
	// dropReportInterval is the least time between warnings about drops.
	dropReportInterval = 10 * time.Second
)

// Entry is one site access as the edge saw it. Path and UserAgent may hold
// anything the client sent and IP is the full client address: Record
// scrubs all three before the entry is buffered.
type Entry struct {
	Time         time.Time
	SiteID       string
	Host         string
	Path         string
	Status       int
	Via          store.AccessVia
	LinkPublicID string // set exactly when Via is store.ViaLink
	IP           string
	UserAgent    string
}

// Repository is the part of store.AccessLog the writer uses.
type Repository interface {
	Append(ctx context.Context, entries []store.AccessEntry) error
	Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

// Config configures a Writer. Zero durations and sizes take the defaults.
type Config struct {
	Repo Repository
	Log  *slog.Logger
	Now  func() time.Time
	// Truncation is applied to every IP before it is buffered.
	Truncation Truncation
	// BufferSize is how many entries may wait for a write; further entries
	// are dropped and counted.
	BufferSize int
	// BatchSize entries trigger a write before FlushInterval elapses.
	BatchSize     int
	FlushInterval time.Duration
	// Retention is how long entries are kept; zero keeps them forever.
	Retention     time.Duration
	PruneInterval time.Duration
	// PruneChunk is how many entries one delete removes; pruning repeats
	// until none are left, so no statement holds the write lock for long.
	PruneChunk int
}

// Writer buffers entries and writes them in batches from one goroutine.
type Writer struct {
	cfg     Config
	log     *slog.Logger
	entries chan store.AccessEntry
	dropped atomic.Int64
	// lastReport is when drops were last logged, in Unix nanoseconds.
	lastReport atomic.Int64

	// mu orders Record against Close: Record holds it shared while it
	// buffers, so no entry lands after the final drain.
	mu     sync.RWMutex
	closed bool

	stop   chan struct{}
	done   chan struct{} // closed when the batch writer and pruner exit
	ctx    context.Context
	cancel context.CancelFunc
}

// Start returns a Writer whose goroutine runs until Close.
func Start(cfg Config) *Writer {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = DefaultBufferSize
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.PruneInterval <= 0 {
		cfg.PruneInterval = DefaultPruneInterval
	}
	if cfg.PruneChunk <= 0 {
		cfg.PruneChunk = DefaultPruneChunk
	}
	// The writer outlives any request, and Close bounds its last write.
	ctx, cancel := context.WithCancel(context.Background())
	w := &Writer{
		cfg:     cfg,
		log:     cfg.Log,
		entries: make(chan store.AccessEntry, cfg.BufferSize),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	var wg sync.WaitGroup
	wg.Go(w.run)
	if cfg.Retention > 0 {
		wg.Go(w.runPruner)
	}
	go func() { wg.Wait(); close(w.done) }()
	return w
}

// Record scrubs e and buffers it without blocking. When the buffer is full,
// or the writer is closed, the entry is dropped and counted.
func (w *Writer) Record(e Entry) {
	entry, ok := w.scrub(e)
	if !ok {
		w.drop(1)
		return
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.drop(1)
		return
	}
	select {
	case w.entries <- entry:
	default:
		w.drop(1)
	}
}

// drop counts n lost entries and warns, at most once per
// dropReportInterval, from whichever goroutine dropped them: the writer
// goroutine may be the one stuck.
func (w *Writer) drop(n int) {
	total := w.dropped.Add(int64(n))
	now := time.Now().UnixNano()
	last := w.lastReport.Load()
	if now-last < int64(dropReportInterval) || !w.lastReport.CompareAndSwap(last, now) {
		return
	}
	w.log.Warn("access log entries dropped", slog.Int64("dropped_total", total))
}

// Dropped is how many entries have been dropped since Start: buffer full,
// recorded after Close, unstorable (a status outside 100 to 599), or lost
// to a failed write.
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Close stops accepting entries and writes everything buffered. If ctx ends
// first, the write in progress is abandoned and the rest are dropped.
func (w *Writer) Close(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.stop)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		w.cancel()
		return nil
	case <-ctx.Done():
		w.cancel()
		// This returns promptly only because the store honours context
		// cancellation: a write that ignored it would hold Close here.
		<-w.done
		return ctx.Err()
	}
}

// run writes batches until Close.
func (w *Writer) run() {
	flush := time.NewTicker(w.cfg.FlushInterval)
	defer flush.Stop()
	batch := make([]store.AccessEntry, 0, w.cfg.BatchSize)
	for {
		select {
		case e := <-w.entries:
			if batch = append(batch, e); len(batch) >= w.cfg.BatchSize {
				batch = w.write(batch)
			}
		case <-flush.C:
			batch = w.write(batch)
		case <-w.stop:
			// Record no longer buffers, so this drains everything.
			for len(w.entries) > 0 {
				if batch = append(batch, <-w.entries); len(batch) >= w.cfg.BatchSize {
					batch = w.write(batch)
				}
			}
			w.write(batch)
			return
		}
	}
}

// write appends batch in one transaction and returns it emptied. A failed
// batch is dropped and counted: retrying would back up the buffer.
func (w *Writer) write(batch []store.AccessEntry) []store.AccessEntry {
	if len(batch) == 0 {
		return batch
	}
	ctx, cancel := context.WithTimeout(w.ctx, writeTimeout)
	defer cancel()
	if err := w.cfg.Repo.Append(ctx, batch); err != nil {
		w.drop(len(batch))
		w.log.ErrorContext(ctx, "write access log", slog.Int("entries", len(batch)), slog.Any("error", err))
	}
	return batch[:0]
}

// runPruner prunes at start and every PruneInterval until Close, beside
// the batch writer so a large backlog never delays batches.
func (w *Writer) runPruner() {
	t := time.NewTicker(w.cfg.PruneInterval)
	defer t.Stop()
	for {
		w.prune()
		select {
		case <-t.C:
		case <-w.stop:
			return
		}
	}
}

// prune deletes expired entries a chunk at a time, each chunk within
// writeTimeout, until none are left or the writer closes.
func (w *Writer) prune() {
	cutoff := w.cfg.Now().Add(-w.cfg.Retention)
	var total int64
	for {
		n, err := w.pruneChunk(cutoff)
		total += n
		if err != nil {
			w.log.Error("prune access log", slog.Int64("entries", total), slog.Any("error", err))
			return
		}
		if n < int64(w.cfg.PruneChunk) {
			break
		}
		select {
		case <-w.stop:
			w.log.Info("pruned access log until shutdown", slog.Int64("entries", total))
			return
		default:
		}
	}
	if total > 0 {
		w.log.Info("pruned access log", slog.Int64("entries", total))
	}
}

func (w *Writer) pruneChunk(cutoff time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(w.ctx, writeTimeout)
	defer cancel()
	return w.cfg.Repo.Prune(ctx, cutoff, w.cfg.PruneChunk)
}
