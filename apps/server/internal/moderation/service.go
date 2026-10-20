// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	netmail "net/mail"
	"slices"
	"sync"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store"
)

// Worker defaults.
const (
	// DefaultConcurrency is how many hashes are screened at once.
	DefaultConcurrency = 2
	// DefaultInterval is how often the worker looks for new files and
	// due retries when nothing wakes it sooner.
	DefaultInterval = 30 * time.Second
	// MinBackoff and MaxBackoff bound the wait before retrying a failed
	// attempt: MinBackoff doubled per attempt, with jitter, never more than
	// MaxBackoff, and never less than a Retry-After the provider gave.
	MinBackoff = 30 * time.Second
	MaxBackoff = 6 * time.Hour
	// batch is how many due hashes one pass takes.
	batch = 50
)

// BlobOpener opens content-addressed blobs; the deploys BlobStore
// satisfies it.
type BlobOpener interface {
	Open(ctx context.Context, sha256 string, size int64) (io.ReadSeekCloser, error)
}

// Config wires a Service.
type Config struct {
	Store store.Moderation
	Blobs BlobOpener
	// Moderator classifies content; nil leaves screening off (the operator
	// commands and the quarantine list still work).
	Moderator Moderator
	Policy    Policy
	// Quarantine receives each newly quarantined hash at once; nil skips.
	Quarantine *Quarantine
	Mailer     mail.Mailer
	// AbuseEmail receives the operator's notices; "" logs them instead.
	AbuseEmail string
	// DashboardHost names the server in owners' notices.
	DashboardHost string
	Concurrency   int
	Interval      time.Duration
	Now           func() time.Time
	// Jitter returns a random fraction in [0, 1) for back-off; tests fix it.
	Jitter func() float64
	Log    *slog.Logger
}

// Service runs screening and the operator's decisions.
type Service struct {
	c    Config
	wake chan struct{}

	mu sync.Mutex
	// pausedUntil holds every attempt back after the provider asked to
	// wait (a 429 or 503 with Retry-After).
	pausedUntil time.Time
}

// New returns a Service.
func New(c Config) *Service {
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultConcurrency
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Jitter == nil {
		c.Jitter = rand.Float64
	}
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{c: c, wake: make(chan struct{}, 1)}
}

// Enabled reports whether a Moderator is configured.
func (s *Service) Enabled() bool { return s.c.Moderator != nil }

// Wake asks the worker to look for new files now, as after an activation.
// It never blocks.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run screens until ctx ends: it queues unscreened files, then screens the
// due hashes, at most Concurrency at a time, whenever Wake is called and
// every Interval. It never touches deploys or serving. Without a Moderator
// it returns at once.
func (s *Service) Run(ctx context.Context) {
	if s.c.Moderator == nil {
		return
	}
	t := time.NewTicker(s.c.Interval)
	defer t.Stop()
	for {
		s.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

// Pass queues unscreened files and screens every hash due now, returning
// once each has a verdict or a retry time. Run calls it; tests call it
// directly.
func (s *Service) Pass(ctx context.Context) {
	if s.c.Moderator == nil {
		return
	}
	if _, err := s.c.Store.EnqueueUnscreened(ctx, s.now()); err != nil {
		s.logErr(ctx, "queue files for screening", err)
		return
	}
	seen := map[string]bool{}
	for ctx.Err() == nil {
		if wait := s.paused(); wait > 0 {
			return
		}
		due, err := s.c.Store.Due(ctx, s.now(), batch)
		if err != nil {
			s.logErr(ctx, "list files due for screening", err)
			return
		}
		var todo []store.ModerationResult
		for _, r := range due {
			if !seen[r.SHA256] {
				seen[r.SHA256] = true
				todo = append(todo, r)
			}
		}
		if len(todo) == 0 {
			return
		}
		sem := make(chan struct{}, s.c.Concurrency)
		var wg sync.WaitGroup
		for _, r := range todo {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
			wg.Go(func() {
				defer func() { <-sem }()
				s.screen(ctx, r)
			})
		}
		wg.Wait()
	}
}

func (s *Service) now() time.Time { return s.c.Now().UTC().Truncate(time.Millisecond) }

func (s *Service) paused() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pausedUntil.Sub(s.now())
}

func (s *Service) pause(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until := s.now().Add(d); until.After(s.pausedUntil) {
		s.pausedUntil = until
	}
}

// screen sends one queued hash and records what came of it.
func (s *Service) screen(ctx context.Context, r store.ModerationResult) {
	if s.paused() > 0 {
		return
	}
	src, err := s.c.Store.Source(ctx, r.SHA256)
	if errors.Is(err, store.ErrNotFound) {
		// Every owner listing it is exempt now, or no serving deploy
		// lists it: nothing may be sent.
		s.logErr(ctx, "dequeue screening", s.c.Store.Dequeue(ctx, r.SHA256))
		return
	}
	if err != nil {
		s.logErr(ctx, "find file to screen", err)
		return
	}
	in, skip, err := s.input(ctx, r, src)
	if err != nil {
		if ctx.Err() == nil {
			s.retry(ctx, r, err)
		}
		return
	}
	if skip != "" {
		s.record(ctx, r, store.ModerationSkipped, nil, skip)
		return
	}
	if len(in.Text) == 0 && len(in.Image) == 0 {
		s.record(ctx, r, store.ModerationAllowed, nil, "no text")
		return
	}
	v, err := s.c.Moderator.Moderate(ctx, in)
	switch {
	case errors.Is(err, ErrRejected):
		s.record(ctx, r, store.ModerationSkipped, nil, "rejected by the provider")
		return
	case err != nil:
		if ctx.Err() == nil {
			s.retry(ctx, r, err)
		}
		return
	}
	status := store.ModerationAllowed
	switch s.c.Policy.Decide(v.Flagged) {
	case OutcomeQuarantined:
		status = store.ModerationQuarantined
	case OutcomeFlagged:
		status = store.ModerationFlagged
	}
	s.record(ctx, r, status, v.Flagged, "")
}

// input reads what to send for r. skip is non-empty when it cannot be
// sent; err is a failure to read, retried.
func (s *Service) input(ctx context.Context, r store.ModerationResult, src store.ModerationSource) (in Input, skip string, err error) {
	if r.Kind == store.ModerationImage {
		if src.Size > MaxImageSize {
			return Input{}, "larger than 20 MB", nil
		}
		b, err := s.read(ctx, r.SHA256, src.Size)
		if err != nil {
			return Input{}, "", err
		}
		t, ok := imageType(b)
		if !ok {
			return Input{}, "not a PNG, JPEG, WEBP or still GIF", nil
		}
		return Input{Image: b, ImageType: t}, "", nil
	}
	var text []byte
	if src.TextSHA256 != "" {
		if text, err = s.read(ctx, src.TextSHA256, src.TextSize); err != nil {
			return Input{}, "", err
		}
	} else {
		// A page with no rendition (activated before A2.6, or larger than
		// the rendition limit): extract its text here, up to that limit.
		size := min(src.Size, deploys.DefaultMaxRenditionSource)
		page, err := s.read(ctx, r.SHA256, size)
		if err != nil {
			return Input{}, "", err
		}
		_, text = deploys.Render(page, nil)
	}
	return Input{Text: chunkText(string(text))}, "", nil
}

func (s *Service) read(ctx context.Context, sha256 string, size int64) ([]byte, error) {
	b, err := s.c.Blobs.Open(ctx, sha256, size)
	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}
	defer func() { _ = b.Close() }()
	out, err := io.ReadAll(io.LimitReader(b, size))
	if err != nil {
		return nil, fmt.Errorf("read blob: %w", err)
	}
	return out, nil
}

// retry counts a failed attempt and schedules the next one.
func (s *Service) retry(ctx context.Context, r store.ModerationResult, cause error) {
	attempts := r.Attempts + 1
	wait := MinBackoff << min(attempts-1, 16)
	wait = min(wait, MaxBackoff)
	wait += time.Duration(s.c.Jitter() * float64(wait) / 4)
	var re *RetryError
	if errors.As(cause, &re) && re.After > 0 {
		s.pause(re.After)
		wait = max(wait, re.After)
	}
	wait = min(wait, MaxBackoff+MaxBackoff/4)
	attrs := []slog.Attr{
		slog.String("sha256", r.SHA256), slog.Int("attempts", attempts),
		slog.Duration("retry_in", wait.Round(time.Second)), slog.String("error", cause.Error()),
	}
	if re != nil && (re.Status == 401 || re.Status == 403) {
		s.c.Log.LogAttrs(ctx, slog.LevelError, "screening provider refused the API key; check WARDLOCK_OPENAI_API_KEY", attrs...)
	} else {
		s.c.Log.LogAttrs(ctx, slog.LevelWarn, "screening attempt failed", attrs...)
	}
	s.logErr(ctx, "schedule screening retry", s.c.Store.Retry(ctx, r.SHA256, attempts, s.now().Add(wait)))
}

// record stores a verdict and sends the notices it calls for.
func (s *Service) record(ctx context.Context, r store.ModerationResult, status store.ModerationStatus, flagged []string, why string) {
	at := s.now()
	if err := s.c.Store.Record(ctx, r.SHA256, status, flagged, at); err != nil {
		// A rescan or decision in between, or the database: either way the
		// verdict is not ours to act on.
		s.logErr(ctx, "record screening verdict", err)
		return
	}
	attrs := []slog.Attr{slog.String("sha256", r.SHA256), slog.String("kind", string(r.Kind)), slog.String("status", string(status))}
	if len(flagged) > 0 {
		attrs = append(attrs, slog.Any("categories", flagged))
	}
	if why != "" {
		attrs = append(attrs, slog.String("reason", why))
	}
	s.c.Log.LogAttrs(ctx, slog.LevelInfo, "screened file", attrs...)
	switch status {
	case store.ModerationQuarantined:
		if s.c.Quarantine != nil {
			s.c.Quarantine.add(r.SHA256)
		}
		s.notify(ctx, r.SHA256, string(status), flagged, at)
	case store.ModerationFlagged:
		s.notify(ctx, r.SHA256, string(status), flagged, at)
	}
}

// notify tells the operator about a flagged or quarantined hash and, for a
// quarantine, every owner whose site served it. Owners hear which file and
// site, never the categories.
func (s *Service) notify(ctx context.Context, sha256, status string, flagged []string, at time.Time) {
	uses, err := s.c.Store.Uses(ctx, sha256)
	if err != nil {
		s.logErr(ctx, "list sites serving a screened file", err)
	}
	d := mail.Screening{SHA256: sha256, Status: status, Categories: flagged, Minors: slices.Contains(flagged, MinorsCategory), CheckedAt: at}
	for _, u := range uses {
		d.Uses = append(d.Uses, mail.ScreeningUse{Site: u.SiteName, Path: u.Path, Owner: u.OwnerEmail})
	}
	if s.c.AbuseEmail == "" {
		siteIDs := make([]string, 0, len(uses))
		for _, u := range uses {
			siteIDs = append(siteIDs, u.SiteID)
		}
		s.c.Log.LogAttrs(ctx, slog.LevelWarn, "screening needs the operator; set WARDLOCK_ABUSE_EMAIL to be emailed",
			slog.String("sha256", sha256), slog.String("status", status), slog.Any("categories", flagged), slog.Any("site_ids", siteIDs))
	} else {
		s.send(ctx, "operator", func() (mail.Message, error) {
			return mail.ScreeningMessage(netmail.Address{Address: s.c.AbuseEmail}, d)
		})
	}
	if status != string(store.ModerationQuarantined) {
		return
	}
	// One message per owner and site, listing every path there.
	type key struct{ owner, site string }
	paths := map[key][]string{}
	var order []key
	for _, u := range uses {
		k := key{u.OwnerEmail, u.SiteName}
		if _, ok := paths[k]; !ok {
			order = append(order, k)
		}
		paths[k] = append(paths[k], u.Path)
	}
	for _, k := range order {
		s.send(ctx, "owner", func() (mail.Message, error) {
			return mail.WithheldMessage(netmail.Address{Address: k.owner}, mail.Withheld{
				Site: k.site, Host: s.c.DashboardHost, Paths: paths[k], Contact: s.c.AbuseEmail,
			})
		})
	}
}

func (s *Service) send(ctx context.Context, to string, build func() (mail.Message, error)) {
	if s.c.Mailer == nil {
		return
	}
	msg, err := build()
	if err == nil {
		err = s.c.Mailer.Send(ctx, msg)
	}
	if err != nil {
		s.c.Log.LogAttrs(ctx, slog.LevelError, "screening notice failed", slog.String("to", to), slog.String("error", err.Error()))
	}
}

func (s *Service) logErr(ctx context.Context, msg string, err error) {
	if err != nil && ctx.Err() == nil {
		s.c.Log.LogAttrs(ctx, slog.LevelError, msg, slog.String("error", err.Error()))
	}
}

// Operator decisions (`wardlockd admin moderation`). Each takes a full
// lowercase SHA-256; ErrUnknown for a hash never screened, ErrWrongStatus
// when the hash's status does not allow it.

var (
	ErrUnknown     = errors.New("moderation: no screening result for that hash")
	ErrWrongStatus = errors.New("moderation: not allowed in the hash's current status")
)

func decisionErr(what string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrUnknown
	case errors.Is(err, store.ErrConflict):
		return ErrWrongStatus
	case err != nil:
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// Release serves a flagged or quarantined hash again.
func (s *Service) Release(ctx context.Context, sha256 string) error {
	err := decisionErr("release", s.c.Store.Release(ctx, sha256, s.now()))
	s.audit(ctx, "operator released screened file", sha256, err)
	return err
}

// Confirm keeps a quarantined hash withheld and its blob held indefinitely.
func (s *Service) Confirm(ctx context.Context, sha256 string) error {
	err := decisionErr("confirm", s.c.Store.Confirm(ctx, sha256, s.now()))
	s.audit(ctx, "operator confirmed quarantine", sha256, err)
	return err
}

// Discard keeps a quarantined hash withheld and lets garbage collection
// remove its blob once no deploy lists it.
func (s *Service) Discard(ctx context.Context, sha256 string) error {
	err := decisionErr("discard", s.c.Store.Discard(ctx, sha256, s.now()))
	s.audit(ctx, "operator discarded quarantined file", sha256, err)
	return err
}

// Rescan queues a hash for screening again; its verdict holds until the
// new one.
func (s *Service) Rescan(ctx context.Context, sha256 string) error {
	err := decisionErr("rescan", s.c.Store.Rescan(ctx, sha256, s.now()))
	s.audit(ctx, "operator queued rescan", sha256, err)
	return err
}

func (s *Service) audit(ctx context.Context, msg, sha256 string, err error) {
	if err == nil {
		s.c.Log.LogAttrs(ctx, slog.LevelInfo, msg, slog.String("sha256", sha256))
	}
}

// Review is a flagged or quarantined hash with the sites serving it.
type Review struct {
	store.ModerationResult
	Uses []store.ModerationUse
}

// Review lists the flagged and quarantined hashes, oldest verdict first.
func (s *Service) Review(ctx context.Context) ([]Review, error) {
	rs, err := s.c.Store.Review(ctx)
	if err != nil {
		return nil, fmt.Errorf("list screening review: %w", err)
	}
	out := make([]Review, len(rs))
	for i, r := range rs {
		uses, err := s.c.Store.Uses(ctx, r.SHA256)
		if err != nil {
			return nil, fmt.Errorf("list screening uses: %w", err)
		}
		out[i] = Review{ModerationResult: r, Uses: uses}
	}
	return out, nil
}

// SetExempt exempts the owner from screening, or ends the exemption.
func (s *Service) SetExempt(ctx context.Context, ownerID string, exempt bool) error {
	if err := s.c.Store.SetExempt(ctx, ownerID, exempt); err != nil {
		return fmt.Errorf("set screening exemption: %w", err)
	}
	s.c.Log.LogAttrs(ctx, slog.LevelInfo, "operator set screening exemption", slog.String("owner_id", ownerID), slog.Bool("exempt", exempt))
	return nil
}

// Backlog counts the hashes waiting to be screened.
func (s *Service) Backlog(ctx context.Context) (int, error) { return s.c.Store.Backlog(ctx) }
