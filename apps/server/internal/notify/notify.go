// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"net/netip"
	"slices"
	"sync"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store"
)

// The events an owner can turn off, each a row in the settings.
const (
	DomainVerified  = "domain_verified"
	DomainFailing   = "domain_failing" // failing for 24 hours, and then lapsed
	LinkUsedUp      = "link_used_up"
	LinksExpiring   = "links_expiring"
	TokenExpiring   = "token_expiring" //nolint:gosec // an event's name, not a credential
	PreviewExpiring = "preview_expiring"
)

// Events that cannot be turned off and pass the hourly cap.
const (
	fileWithheld = "file_withheld"
	summary      = "summary"
)

// Switchable lists every event an owner can turn off.
var Switchable = []string{DomainVerified, DomainFailing, LinkUsedUp, LinksExpiring, TokenExpiring, PreviewExpiring}

// Timing (SPEC "Notifications").
const (
	// DeliverInterval is how often the worker sends what is due; a withheld
	// file wakes it sooner.
	DeliverInterval = time.Minute
	// DetectInterval is how often it looks for new events.
	DetectInterval = 5 * time.Minute
	// PruneInterval is how often it deletes old rows.
	PruneInterval = 24 * time.Hour

	// DefaultCap is the emails an owner gets an hour unless configured
	// otherwise. Withheld-file notices and summaries always go, and count.
	DefaultCap = 20
	capWindow  = time.Hour

	minBackoff = time.Minute
	maxBackoff = time.Hour
	// giveUpAfter is how long a message is retried, long enough to outlast
	// a mail provider's outage over a weekend.
	giveUpAfter = 72 * time.Hour

	// retention keeps finished rows, whose event keys stop a repeat, for
	// longer than any scan below looks back.
	retention = 30 * 24 * time.Hour

	batch = 100
)

// ErrUnknownEvent means a setting names an event owners cannot switch.
var ErrUnknownEvent = errors.New("unknown notification event")

// Config holds a Service's dependencies.
type Config struct {
	Store  store.Notifications
	Mailer mail.Mailer
	// DashboardURL is the dashboard's origin, for links in emails.
	DashboardURL string
	// Cap is the emails an owner gets an hour; 0 means no cap.
	Cap int64
	// Now defaults to time.Now.
	Now func() time.Time
	Log *slog.Logger

	// Webhooks turns on webhooks (I6.3); nil leaves them out. Sites finds
	// the sites they list.
	Webhooks store.Webhooks
	Sites    store.Sites
	// SigningKey is the server key, from which signing secrets are derived.
	SigningKey []byte
	// AllowHTTP accepts http:// webhook URLs too (local mode).
	AllowHTTP bool
	// AllowPrivate lists the private ranges webhooks may connect to
	// (WARDLOCK_WEBHOOK_ALLOW_PRIVATE).
	AllowPrivate []netip.Prefix
}

// Service keeps owners' settings and runs the outbox.
type Service struct {
	c           Config
	wake        chan struct{}
	webhookWake chan struct{}
	permitted   func(netip.Addr) bool
	client      *http.Client
}

// New returns a Service.
func New(c Config) *Service {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
	policy := addressPolicy{allow: c.AllowPrivate}
	return &Service{
		c: c, wake: make(chan struct{}, 1), webhookWake: make(chan struct{}, 1),
		permitted: policy.permitted, client: webhookClient(policy.permitted, nil),
	}
}

func (s *Service) now() time.Time { return s.c.Now().UTC().Truncate(time.Millisecond) }

// Settings returns whether each switchable event is on for ownerID.
func (s *Service) Settings(ctx context.Context, ownerID string) (map[string]bool, error) {
	off, err := s.c.Store.OptOuts(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("read notification settings: %w", err)
	}
	on := make(map[string]bool, len(Switchable))
	for _, e := range Switchable {
		on[e] = !slices.Contains(off, e)
	}
	return on, nil
}

// SetSettings turns the events in on on or off for ownerID and leaves the
// rest as they are.
func (s *Service) SetSettings(ctx context.Context, ownerID string, on map[string]bool) error {
	off := make(map[string]bool, len(on))
	for e, enabled := range on {
		if !slices.Contains(Switchable, e) {
			return fmt.Errorf("%w: %s", ErrUnknownEvent, e)
		}
		off[e] = !enabled
	}
	if err := s.c.Store.SetOptOuts(ctx, ownerID, off); err != nil {
		return fmt.Errorf("store notification settings: %w", err)
	}
	return nil
}

// Withheld queues the owner's notice that content screening withheld files
// on a site, once per key. It cannot be turned off.
func (s *Service) Withheld(ctx context.Context, key, ownerID string, d mail.Withheld) error {
	e, err := s.entry(key, ownerID, fileWithheld, emailPayload{Withheld: &d})
	if err != nil {
		return err
	}
	if _, err := s.c.Store.Enqueue(ctx, e); err != nil {
		return fmt.Errorf("queue withheld notice: %w", err)
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// emailPayload is an email row's payload: a Notice, whose URL is a path on
// the dashboard, or a withheld-file notice.
type emailPayload struct {
	Notice   *mail.Notice   `json:"notice,omitempty"`
	Withheld *mail.Withheld `json:"withheld,omitempty"`
}

func (s *Service) entry(key, ownerID, event string, p emailPayload) (store.OutboxEntry, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return store.OutboxEntry{}, fmt.Errorf("encode notification: %w", err)
	}
	return store.OutboxEntry{
		ID: store.NewID(), Channel: store.NotificationEmail, EventKey: key, OwnerID: ownerID,
		Event: event, Payload: string(body), CreatedAt: s.now(),
	}, nil
}

// Run delivers until ctx ends, looking for new events and pruning on their
// own schedules. Call it from one process only.
func (s *Service) Run(ctx context.Context) {
	s.Recover(ctx)
	if s.c.Webhooks != nil {
		var wg sync.WaitGroup
		wg.Go(func() { s.runWebhooks(ctx) })
		defer wg.Wait()
	}
	var detected, pruned time.Time
	t := time.NewTicker(DeliverInterval)
	defer t.Stop()
	for {
		now := s.now()
		if now.Sub(detected) >= DetectInterval {
			detected = now
			s.Detect(ctx)
		}
		if now.Sub(pruned) >= PruneInterval {
			pruned = now
			s.prune(ctx)
		}
		s.Deliver(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

// Recover settles what a previous process left mid-attempt, which may or
// may not have gone out. An email is sent at most once, so it is not tried
// again. A webhook delivery is sent at least once, so it goes back in the
// queue: receivers deduplicate on the event's ID.
func (s *Service) Recover(ctx context.Context) {
	n, err := s.c.Store.FailInterrupted(ctx, store.NotificationEmail, s.now())
	if err != nil {
		s.logErr(ctx, "end interrupted notifications", err)
	} else if n > 0 {
		s.c.Log.LogAttrs(ctx, slog.LevelWarn, "notifications interrupted while sending were not retried", slog.Int64("count", n))
	}
	if s.c.Webhooks == nil {
		return
	}
	n, err = s.c.Store.RequeueInterrupted(ctx, store.NotificationWebhook, s.now())
	if err != nil {
		s.logErr(ctx, "requeue interrupted webhook deliveries", err)
	} else if n > 0 {
		s.c.Log.LogAttrs(ctx, slog.LevelWarn, "webhook deliveries interrupted while sending were requeued", slog.Int64("count", n))
	}
}

func (s *Service) prune(ctx context.Context) {
	if _, err := s.c.Store.Prune(ctx, s.now().Add(-retention)); err != nil {
		s.logErr(ctx, "prune notifications", err)
	}
}

// Deliver sends what is due, then summarises what the cap held back.
func (s *Service) Deliver(ctx context.Context) {
	rows, err := s.c.Store.Due(ctx, store.NotificationEmail, s.now(), batch)
	if err != nil {
		s.logErr(ctx, "list due notifications", err)
		return
	}
	for _, e := range rows {
		if ctx.Err() != nil {
			return
		}
		if !s.deliver(ctx, e) {
			// Mail is failing: the rest wait for a later pass, rather than
			// each spending the mail timeout.
			break
		}
	}
	s.summarise(ctx)
}

// deliver handles one due row and reports whether mail can still be tried
// this pass.
func (s *Service) deliver(ctx context.Context, e store.OutboxEntry) bool {
	settle := func(status store.NotificationStatus) {
		// A send cut short by shutdown still records how it ended.
		s.logErr(ctx, "settle notification", s.c.Store.Settle(context.WithoutCancel(ctx), e.ID, status, s.now()))
	}
	to, err := s.c.Store.Recipient(ctx, e.OwnerID)
	if err != nil {
		s.logErr(ctx, "read notification recipient", err)
		return true
	}
	if to.Suspended {
		settle(store.NotificationDropped)
		return true
	}
	if !mandatory(e.Event) {
		off, err := s.c.Store.OptOuts(ctx, e.OwnerID)
		if err != nil {
			s.logErr(ctx, "read notification settings", err)
			return true
		}
		if slices.Contains(off, e.Event) {
			settle(store.NotificationDropped)
			return true
		}
		capped, err := s.capped(ctx, e.OwnerID)
		if err != nil {
			s.logErr(ctx, "count sent notifications", err)
			return true
		}
		if capped {
			settle(store.NotificationHeld)
			return true
		}
	}
	msg, err := s.message(netmail.Address{Address: to.Email}, e)
	if err != nil {
		s.fail(ctx, e, err)
		settle(store.NotificationFailed)
		return true
	}
	if err := s.c.Store.Claim(ctx, e.ID); err != nil {
		s.logErr(ctx, "claim notification", err)
		return true
	}
	err = s.c.Mailer.Send(ctx, msg)
	switch {
	case err == nil:
		settle(store.NotificationSent)
		return true
	case errors.Is(err, mail.ErrInvalidMessage), s.now().Sub(e.CreatedAt) >= giveUpAfter:
		s.fail(ctx, e, err)
		settle(store.NotificationFailed)
		return true
	}
	s.c.Log.LogAttrs(ctx, slog.LevelWarn, "notification will be retried",
		slog.String("id", e.ID), slog.String("event", e.Event), slog.Int64("attempts", e.Attempts+1), slog.String("error", err.Error()))
	next := s.now().Add(backoff(e.Attempts + 1))
	s.logErr(ctx, "reschedule notification", s.c.Store.Reschedule(context.WithoutCancel(ctx), e.ID, next))
	return false
}

func (s *Service) fail(ctx context.Context, e store.OutboxEntry, err error) {
	s.c.Log.LogAttrs(ctx, slog.LevelError, "notification failed",
		slog.String("id", e.ID), slog.String("event", e.Event), slog.String("error", err.Error()))
}

func mandatory(event string) bool {
	return event == fileWithheld || event == summary || event == webhookDisabled
}

// backoff is the wait after the attempts-th failure: 1 minute doubling to
// an hour.
func backoff(attempts int64) time.Duration {
	wait := minBackoff
	for i := int64(1); i < attempts && wait < maxBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxBackoff)
}

// capped reports whether ownerID has had their emails for the hour.
func (s *Service) capped(ctx context.Context, ownerID string) (bool, error) {
	if s.c.Cap <= 0 {
		return false, nil
	}
	n, err := s.c.Store.CountSent(ctx, ownerID, store.NotificationEmail, s.now().Add(-capWindow))
	if err != nil {
		return false, err
	}
	return n >= s.c.Cap, nil
}

func (s *Service) message(to netmail.Address, e store.OutboxEntry) (mail.Message, error) {
	var p emailPayload
	if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
		return mail.Message{}, fmt.Errorf("decode notification: %w", err)
	}
	switch {
	case p.Withheld != nil:
		return mail.WithheldMessage(to, *p.Withheld)
	case p.Notice != nil:
		n := *p.Notice
		n.URL = s.c.DashboardURL + n.URL
		n.SettingsURL = s.c.DashboardURL + settingsPath
		return mail.NoticeMessage(to, n)
	}
	return mail.Message{}, errors.New("notification has no content")
}

// summarise rolls each owner's held rows into one email, as soon as the cap
// has room for it. Anything turned off since, or a suspended owner's, is
// dropped instead.
func (s *Service) summarise(ctx context.Context) {
	ids, err := s.c.Store.HeldOwners(ctx, store.NotificationEmail)
	if err != nil {
		s.logErr(ctx, "list owners with held notifications", err)
		return
	}
	for _, ownerID := range ids {
		if err := s.summariseOwner(ctx, ownerID); err != nil {
			s.logErr(ctx, "summarise notifications", err)
		}
	}
}

func (s *Service) summariseOwner(ctx context.Context, ownerID string) error {
	to, err := s.c.Store.Recipient(ctx, ownerID)
	if err != nil {
		return err
	}
	if !to.Suspended {
		capped, err := s.capped(ctx, ownerID)
		if err != nil || capped {
			return err
		}
	}
	off, err := s.c.Store.OptOuts(ctx, ownerID)
	if err != nil {
		return err
	}
	held, err := s.c.Store.Held(ctx, ownerID, store.NotificationEmail)
	if err != nil {
		return err
	}
	var ids, subjects []string
	for _, e := range held {
		var p emailPayload
		if to.Suspended || slices.Contains(off, e.Event) || json.Unmarshal([]byte(e.Payload), &p) != nil || p.Notice == nil {
			if err := s.c.Store.Settle(ctx, e.ID, store.NotificationDropped, s.now()); err != nil {
				return err
			}
			continue
		}
		ids = append(ids, e.ID)
		subjects = append(subjects, p.Notice.Subject)
	}
	if len(ids) == 0 {
		return nil
	}
	e, err := s.entry(summary+":"+ownerID+":"+ids[0], ownerID, summary, emailPayload{Notice: summaryNotice(subjects)})
	if err != nil {
		return err
	}
	return s.c.Store.Summarise(ctx, e, ids, s.now())
}

func (s *Service) logErr(ctx context.Context, msg string, err error) {
	if err != nil && ctx.Err() == nil {
		s.c.Log.LogAttrs(ctx, slog.LevelError, msg, slog.String("error", err.Error()))
	}
}
