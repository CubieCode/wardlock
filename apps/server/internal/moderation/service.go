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
	"strings"
	"sync"
	"sync/atomic"
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
	// MaxBackoff, and never less than a Retry-After the provider gave. A
	// pause of the whole worker never lasts longer than MaxBackoff.
	MinBackoff = 30 * time.Second
	MaxBackoff = 6 * time.Hour
	// batch is how many due hashes one pass takes.
	batch = 50
	// sweepBatch is how many sites each pass sweeps for files never
	// queued: activations queue their own site at once, and the sweep
	// catches the rest (an exemption ended, a site restored) in turn.
	sweepBatch = 100
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
	// Invalidate is told each site serving a hash that has its first
	// verdict, so the edge reloads what it knows about the site's files
	// (screened or not, a gzip sibling checked); nil skips.
	Invalidate func(siteID string)
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

	// providerErrors counts failures that paused the whole worker.
	providerErrors atomic.Int64

	mu sync.Mutex
	// pausedUntil holds every attempt back after the provider asked to
	// wait or failed in a way every input would (Global).
	pausedUntil time.Time
	// failures counts Global failures since the last answer, for the
	// pause's back-off.
	failures int
	// changed holds sites whose served state changed, to queue at once.
	changed map[string]bool
	// cursor is where the sweep of sites resumes.
	cursor string
	// flagged collects the pass's flagged files for one digest.
	flagged []mail.Screening

	dec *decoder
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
	return &Service{c: c, wake: make(chan struct{}, 1), changed: map[string]bool{}, dec: newDecoder()}
}

// Enabled reports whether a Moderator is configured.
func (s *Service) Enabled() bool { return s.c.Moderator != nil }

// Wake asks the worker to look for due files now. It never blocks.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Changed queues the site's files for screening on the next pass, as after
// an activation, and wakes the worker. It never blocks.
func (s *Service) Changed(siteID string) {
	s.mu.Lock()
	s.changed[siteID] = true
	s.mu.Unlock()
	s.Wake()
}

// Run screens until ctx ends: it queues unscreened files, then screens the
// due hashes, at most Concurrency at a time, whenever Wake or Changed is
// called and every Interval. It never touches deploys or serving. Without
// a Moderator it returns at once.
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

// Pass queues the files of changed sites and of the next sites in the
// sweep, screens every hash due now, returning once each has a verdict or
// a retry time, then sends the operator one digest of the files it
// flagged. Run calls it; tests call it directly.
func (s *Service) Pass(ctx context.Context) {
	if s.c.Moderator == nil {
		return
	}
	defer s.sendDigest(ctx)
	s.enqueue(ctx)
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
		sites := &siteSet{ids: map[string]bool{}}
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
				s.screen(ctx, r, sites)
			})
		}
		wg.Wait()
		if s.c.Invalidate != nil {
			for id := range sites.ids {
				s.c.Invalidate(id)
			}
		}
	}
}

// siteSet collects the sites a batch's verdicts change for the edge.
type siteSet struct {
	mu  sync.Mutex
	ids map[string]bool
}

func (ss *siteSet) add(uses []store.ModerationUse) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for _, u := range uses {
		ss.ids[u.SiteID] = true
	}
}

// enqueue queues the changed sites' files, then the next sweepBatch sites'
// in ID order, wrapping round once the sweep reaches the end.
func (s *Service) enqueue(ctx context.Context) {
	s.mu.Lock()
	changed, cursor := s.changed, s.cursor
	s.changed = map[string]bool{}
	s.mu.Unlock()
	failed := map[string]bool{}
	for id := range changed {
		if _, err := s.c.Store.EnqueueSite(ctx, id, s.now()); err != nil {
			s.logErr(ctx, "queue site files for screening", err)
			failed[id] = true
		}
	}
	ids, err := s.c.Store.ScreenedSites(ctx, cursor, sweepBatch)
	if err != nil {
		s.logErr(ctx, "list sites to screen", err)
	}
	for _, id := range ids {
		if changed[id] && !failed[id] {
			continue
		}
		if _, err := s.c.Store.EnqueueSite(ctx, id, s.now()); err != nil {
			s.logErr(ctx, "queue site files for screening", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range failed {
		s.changed[id] = true
	}
	if err == nil {
		s.cursor = ""
		if len(ids) == sweepBatch {
			s.cursor = ids[len(ids)-1]
		}
	}
}

func (s *Service) now() time.Time { return s.c.Now().UTC().Truncate(time.Millisecond) }

func (s *Service) paused() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pausedUntil.Sub(s.now())
}

// pause holds every attempt back for d, at most MaxBackoff.
func (s *Service) pause(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until := s.now().Add(min(d, MaxBackoff)); until.After(s.pausedUntil) {
		s.pausedUntil = until
	}
}

// answered resets the pause back-off after the provider answers.
func (s *Service) answered() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = 0
}

// ProviderErrors counts the failures that paused the whole worker.
func (s *Service) ProviderErrors() int64 { return s.providerErrors.Load() }

// Paused reports whether the worker is holding every attempt back.
func (s *Service) Paused() bool { return s.paused() > 0 }

// backoff is the wait after n failures: MinBackoff doubled per failure,
// with up to a quarter more of jitter, at most MaxBackoff.
func (s *Service) backoff(n int) time.Duration {
	wait := min(MinBackoff<<min(max(n-1, 0), 16), MaxBackoff)
	return min(wait+time.Duration(s.c.Jitter()*float64(wait)/4), MaxBackoff)
}

// screen sends one queued hash and records what came of it.
func (s *Service) screen(ctx context.Context, r store.ModerationResult, sites *siteSet) {
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
	p, err := s.plan(ctx, r, src)
	if err != nil {
		if ctx.Err() == nil {
			s.retry(ctx, r, err)
		}
		return
	}
	var flagged []string
	judged := false
	for _, in := range p.inputs {
		v, err := s.c.Moderator.Moderate(ctx, in)
		switch {
		case errors.Is(err, ErrRejected):
			p.why = append(p.why, "rejected by the provider")
			continue
		case err != nil:
			if ctx.Err() == nil {
				s.retry(ctx, r, err)
			}
			return
		}
		s.answered()
		judged = true
		for _, c := range v.Flagged {
			if !slices.Contains(flagged, c) {
				flagged = append(flagged, c)
			}
		}
	}
	slices.Sort(flagged)
	v := store.ModerationVerdict{Kind: p.kind, Decoded: p.decoded}
	switch {
	case judged:
		v.Categories = flagged
		switch s.c.Policy.Decide(flagged) {
		case OutcomeQuarantined:
			v.Status = store.ModerationQuarantined
		case OutcomeFlagged:
			v.Status = store.ModerationFlagged
		default:
			v.Status = store.ModerationAllowed
		}
		switch {
		case len(p.why) > 0 && r.Status == store.ModerationQuarantined && v.Status != store.ModerationQuarantined:
			// Part of it could not be judged this time, and that part may
			// be what was withheld: a rescan never lowers a quarantine
			// unless every input was judged clean of quarantine
			// categories.
			v.Status, v.Categories = "", nil
		case len(p.why) > 0 && v.Status != store.ModerationQuarantined:
			v.Status, v.Reason = store.ModerationFlagged, store.ModerationUnscreenable
		}
	case r.Status != store.ModerationPending:
		// A rescan, or text queued after the hash was screened, that the
		// endpoint could not judge: the verdict stands.
	case len(p.why) > 0:
		v.Status, v.Reason = store.ModerationFlagged, store.ModerationUnscreenable
	case p.kind == store.ModerationNone:
		v.Status = store.ModerationSkipped
	default:
		// An image or page with nothing in it to judge, such as no text.
		v.Status = store.ModerationAllowed
	}
	s.record(ctx, r, v, p.why, sites)
}

// plan is what screening a hash sends, decided from its bytes.
type plan struct {
	inputs  []Input
	kind    store.ModerationKind
	decoded string
	// why lists what could not be screened.
	why []string
}

// plan reads the hash and decides what to send: the image when its bytes
// are one, under any name; its text and embedded images when a path
// serves it as text or its bytes are HTML, XML or SVG; and for a gzip file
// what it decompresses to. err is a failure to read, retried.
func (s *Service) plan(ctx context.Context, r store.ModerationResult, src store.ModerationSource) (plan, error) {
	blob, err := s.c.Blobs.Open(ctx, r.SHA256, src.Size)
	if err != nil {
		return plan{}, fmt.Errorf("open blob: %w", err)
	}
	defer func() { _ = blob.Close() }()
	hd := make([]byte, min(src.Size, sniffSize))
	if _, err := io.ReadFull(blob, hd); err != nil {
		return plan{}, fmt.Errorf("read blob: %w", err)
	}
	c, what := sniff(hd)
	var p plan
	image := c == classImage || c == classIcon || c == classOtherImage
	text := src.TextServed || c == classMarkup
	// Any text may embed images in data: URIs, stylesheets and scripts
	// included, though their source is not screened as text.
	scan := text || c == classText
	readAll := func(limit int64) ([]byte, error) {
		if _, err := blob.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek blob: %w", err)
		}
		b, err := io.ReadAll(io.LimitReader(blob, min(src.Size, limit)))
		if err != nil {
			return nil, fmt.Errorf("read blob: %w", err)
		}
		return b, nil
	}
	switch {
	case c == classOtherImage:
		p.why = append(p.why, what)
	case image && src.Size > MaxScanSize:
		p.why = append(p.why, "an image too large to read")
	case image:
		b, err := readAll(src.Size)
		if err != nil {
			return plan{}, err
		}
		ins, err := s.dec.imageInputs(ctx, b)
		var u unscreenable
		switch {
		case errors.As(err, &u):
			p.why = append(p.why, u.what)
		case err != nil:
			return plan{}, err
		}
		p.inputs = append(p.inputs, ins...)
	case c == classGzip:
		if _, err := blob.Seek(0, io.SeekStart); err != nil {
			return plan{}, fmt.Errorf("seek blob: %w", err)
		}
		if p.decoded, err = gunzipHash(blobReader{blob}); err != nil {
			return plan{}, fmt.Errorf("read blob: %w", err)
		}
	}
	if scan {
		body, err := readAll(MaxScanSize)
		if err != nil {
			return plan{}, err
		}
		if src.Size > MaxScanSize {
			p.why = append(p.why, "a text file too large to read in full")
		}
		var pieces []string
		switch {
		case !text:
		case c == classText:
			// The most a browser shows of it: a text file is shown as it
			// is, anything else under a page's name is rendered as HTML.
			pieces = append(pieces, string(body[:min(len(body), deploys.DefaultMaxRenditionSource)]))
		default:
			pieces = append(pieces, markupText(body[:min(len(body), deploys.DefaultMaxRenditionSource)]))
		}
		var e embedded
		count := 0
		if err := s.dec.findEmbedded(ctx, body, 0, &e, &count); err != nil {
			return plan{}, err
		}
		pieces = append(pieces, e.text...)
		if chunks := chunkText(strings.Join(pieces, "\n")); len(chunks) > 0 {
			p.inputs = append(p.inputs, Input{Text: chunks})
		}
		p.inputs = append(p.inputs, e.images...)
		p.why = append(p.why, e.why...)
	}
	switch {
	case image && text:
		p.kind = store.ModerationImageText
	case image:
		p.kind = store.ModerationImage
	case text:
		p.kind = store.ModerationText
	}
	return p, nil
}

// retry counts a failed attempt and schedules the next one. A Global
// failure pauses the whole worker, with its own back-off, alerts the
// operator, and leaves the hash's attempts alone.
func (s *Service) retry(ctx context.Context, r store.ModerationResult, cause error) {
	var re *RetryError
	errors.As(cause, &re)
	attrs := []slog.Attr{slog.String("sha256", r.SHA256), slog.String("error", cause.Error())}
	if re != nil && re.Global {
		s.mu.Lock()
		s.failures++
		n := s.failures
		s.mu.Unlock()
		wait := min(max(s.backoff(n), re.After), MaxBackoff)
		s.pause(wait)
		s.providerErrors.Add(1)
		attrs = append(attrs, slog.Int("status", re.Status), slog.Duration("paused_for", wait.Round(time.Second)))
		msg := "screening provider refused the request; screening paused"
		if re.Status == 401 || re.Status == 403 {
			msg = "screening provider refused the API key; screening paused; check WARDLOCK_OPENAI_API_KEY"
		}
		if re.Status == 429 {
			s.c.Log.LogAttrs(ctx, slog.LevelWarn, "screening provider rate limit; screening paused", attrs...)
		} else {
			s.c.Log.LogAttrs(ctx, slog.LevelError, msg, attrs...)
		}
		s.logErr(ctx, "schedule screening retry", s.c.Store.Retry(ctx, r.SHA256, r.Attempts, s.now().Add(wait)))
		return
	}
	attempts := r.Attempts + 1
	wait := s.backoff(attempts)
	if re != nil && re.After > 0 {
		s.pause(re.After)
		wait = min(max(wait, re.After), MaxBackoff)
	}
	attrs = append(attrs, slog.Int("attempts", attempts), slog.Duration("retry_in", wait.Round(time.Second)))
	s.c.Log.LogAttrs(ctx, slog.LevelWarn, "screening attempt failed", attrs...)
	s.logErr(ctx, "schedule screening retry", s.c.Store.Retry(ctx, r.SHA256, attempts, s.now().Add(wait)))
}

// record stores a verdict and sends the notices it calls for.
func (s *Service) record(ctx context.Context, r store.ModerationResult, v store.ModerationVerdict, why []string, sites *siteSet) {
	at := s.now()
	if err := s.c.Store.Record(ctx, r.SHA256, v, at); err != nil {
		// A rescan or decision in between, or the database: either way the
		// verdict is not ours to act on.
		s.logErr(ctx, "record screening verdict", err)
		return
	}
	status := v.Status
	if status == "" {
		status = r.Status
	}
	attrs := []slog.Attr{slog.String("sha256", r.SHA256), slog.String("kind", string(v.Kind)), slog.String("status", string(status))}
	if len(v.Categories) > 0 {
		attrs = append(attrs, slog.Any("categories", v.Categories))
	}
	if len(why) > 0 {
		attrs = append(attrs, slog.Any("unscreenable", why))
	}
	if v.Status == "" {
		attrs = append(attrs, slog.Bool("verdict_kept", true))
	}
	s.c.Log.LogAttrs(ctx, slog.LevelInfo, "screened file", attrs...)
	// The edge reloads its sites when a hash has its first verdict or a
	// gzip file is known to hold its base's bytes (a sibling to offer);
	// notices go out once per change of status.
	reload := r.Status == store.ModerationPending || v.Decoded != ""
	changed := v.Status != "" && v.Status != r.Status
	notice := changed && (v.Status == store.ModerationQuarantined || v.Status == store.ModerationFlagged)
	if !reload && !notice {
		return
	}
	uses, err := s.c.Store.Uses(ctx, r.SHA256)
	if err != nil {
		s.logErr(ctx, "list sites serving a screened file", err)
	}
	if reload {
		sites.add(uses)
	}
	if !notice {
		return
	}
	d := mail.Screening{
		SHA256: r.SHA256, Status: string(v.Status), Categories: v.Categories, Minors: slices.Contains(v.Categories, MinorsCategory),
		Unscreenable: v.Reason == store.ModerationUnscreenable, CheckedAt: at,
	}
	for _, u := range uses {
		d.Uses = append(d.Uses, mail.ScreeningUse{Site: u.SiteName, Path: u.Path, Owner: u.OwnerEmail})
	}
	switch v.Status {
	case store.ModerationQuarantined:
		if s.c.Quarantine != nil {
			s.c.Quarantine.add(r.SHA256)
		}
		s.notifyOperator(ctx, d)
		s.notifyOwners(ctx, uses)
	case store.ModerationFlagged:
		s.mu.Lock()
		s.flagged = append(s.flagged, d)
		s.mu.Unlock()
	}
}

// notifyOperator tells the operator about a quarantined hash at once.
func (s *Service) notifyOperator(ctx context.Context, d mail.Screening) {
	if s.c.AbuseEmail == "" {
		s.logNotice(ctx, d)
		return
	}
	s.send(ctx, "operator", func() (mail.Message, error) {
		return mail.ScreeningMessage(netmail.Address{Address: s.c.AbuseEmail}, d)
	})
}

// sendDigest sends the operator one message listing the pass's flagged
// files.
func (s *Service) sendDigest(ctx context.Context) {
	s.mu.Lock()
	files := s.flagged
	s.flagged = nil
	s.mu.Unlock()
	if len(files) == 0 {
		return
	}
	if s.c.AbuseEmail == "" {
		for _, d := range files {
			s.logNotice(ctx, d)
		}
		return
	}
	s.send(ctx, "operator", func() (mail.Message, error) {
		return mail.FlaggedMessage(netmail.Address{Address: s.c.AbuseEmail}, files)
	})
}

func (s *Service) logNotice(ctx context.Context, d mail.Screening) {
	s.c.Log.LogAttrs(ctx, slog.LevelWarn, "screening needs the operator; set WARDLOCK_ABUSE_EMAIL to be emailed",
		slog.String("sha256", d.SHA256), slog.String("status", d.Status), slog.Any("categories", d.Categories),
		slog.Bool("unscreenable", d.Unscreenable), slog.Int("sites", len(d.Uses)))
}

// notifyOwners tells every owner whose site serves a newly quarantined
// hash which file and site, never the categories: one message per owner
// and site. Owners of taken-down sites and suspended owners are not told.
func (s *Service) notifyOwners(ctx context.Context, uses []store.ModerationUse) {
	type key struct{ owner, site string }
	paths := map[key][]string{}
	var order []key
	for _, u := range uses {
		if u.Unavailable {
			continue
		}
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

// Rescan queues a hash for screening again; its verdict holds unless the
// endpoint gives a new one.
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
