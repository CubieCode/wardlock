// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// NotificationChannel says how an outbox row leaves the server.
type NotificationChannel string

const (
	// NotificationEmail is the owner's email address.
	NotificationEmail NotificationChannel = "email"
	// NotificationWebhook is one of the owner's webhooks (I6.3). Each row
	// has a WebhookDelivery with the same ID.
	NotificationWebhook NotificationChannel = "webhook"
)

// NotificationStatus is where an outbox row stands. The migration describes
// each one.
type NotificationStatus string

const (
	NotificationPending    NotificationStatus = "pending"
	NotificationSending    NotificationStatus = "sending"
	NotificationHeld       NotificationStatus = "held"
	NotificationSent       NotificationStatus = "sent"
	NotificationFailed     NotificationStatus = "failed"
	NotificationDropped    NotificationStatus = "dropped"
	NotificationSummarised NotificationStatus = "summarised"
)

// OutboxEntry is one row of the notification outbox: one delivery of one
// event, unique by Channel and EventKey.
type OutboxEntry struct {
	ID       string
	Channel  NotificationChannel
	EventKey string
	OwnerID  string
	Event    string
	// Payload is what the channel needs to send it, as JSON.
	Payload   string
	Status    NotificationStatus
	Attempts  int64
	CreatedAt time.Time
	// NextAttemptAt is set while the row is pending.
	NextAttemptAt time.Time
	FinishedAt    time.Time
}

// NotificationRecipient is an owner as the notification worker needs them.
type NotificationRecipient struct {
	Email     string
	Suspended bool
}

// NoticeSubject is one thing a notification scan found. Which fields are
// set depends on the scan.
type NoticeSubject struct {
	OwnerID string
	// SiteID is empty for a failing domain and an expiring link or token.
	SiteID   string
	SiteName string // empty for an API token
	// ID is the domain's, link's or token's ID; empty for a preview.
	ID string
	// Name is the hostname, the link's label (perhaps empty), the token's
	// name or the preview's name.
	Name     string
	PublicID string // a link's public ID
	// At is when it happened or will: verified, failing since, used up or
	// expiring.
	At time.Time
}

// Notifications stores owners' notification opt-outs and the durable
// outbox. Every method but the first two is instance-wide: the worker, not
// a request, drives them.
type Notifications interface {
	// OptOuts lists the events ownerID has turned off.
	OptOuts(ctx context.Context, ownerID string) ([]string, error)
	// SetOptOuts turns each event in off off (true) or on (false) for
	// ownerID, in one transaction, leaving every other event as it is.
	SetOptOuts(ctx context.Context, ownerID string, off map[string]bool) error

	// Recipient returns the owner of a row (ErrNotFound if deleted).
	Recipient(ctx context.Context, ownerID string) (NotificationRecipient, error)
	// Enqueue adds entries in one transaction, due at their CreatedAt,
	// skipping any whose Channel and EventKey already have a row, and
	// returns how many it added.
	Enqueue(ctx context.Context, entries ...OutboxEntry) (int, error)
	// Due returns up to limit pending rows of channel due at now, oldest
	// due first.
	Due(ctx context.Context, channel NotificationChannel, now time.Time, limit int) ([]OutboxEntry, error)
	// Claim starts an attempt on a pending row, counting it; ErrNotFound
	// if the row is not pending.
	Claim(ctx context.Context, id string) error
	// Reschedule returns a row in an attempt to pending, due at next;
	// ErrNotFound if it is in no attempt.
	Reschedule(ctx context.Context, id string, next time.Time) error
	// Settle moves a row that has not reached an end state to status at
	// at; ErrNotFound if it has.
	Settle(ctx context.Context, id string, status NotificationStatus, at time.Time) error
	// FailInterrupted fails every row of channel left in an attempt and
	// returns how many. Only the one process that delivers may call it,
	// before it starts delivering.
	FailInterrupted(ctx context.Context, channel NotificationChannel, at time.Time) (int64, error)
	// RequeueInterrupted returns every row of channel left in an attempt to
	// pending, due at at, and returns how many. As with FailInterrupted,
	// only the one process that delivers may call it.
	RequeueInterrupted(ctx context.Context, channel NotificationChannel, at time.Time) (int64, error)
	// CountSent counts ownerID's rows on channel sent at or after since.
	CountSent(ctx context.Context, ownerID string, channel NotificationChannel, since time.Time) (int64, error)
	// HeldOwners lists the owners with held rows on channel.
	HeldOwners(ctx context.Context, channel NotificationChannel) ([]string, error)
	// Held lists ownerID's held rows on channel, oldest first.
	Held(ctx context.Context, ownerID string, channel NotificationChannel) ([]OutboxEntry, error)
	// Summarise, in one transaction, enqueues summary and marks the held
	// rows named by heldIDs summarised at at, so a crash cannot lose them
	// or summarise them twice.
	Summarise(ctx context.Context, summary OutboxEntry, heldIDs []string, at time.Time) error
	// Prune deletes rows that reached an end state at or before before and
	// returns how many.
	Prune(ctx context.Context, before time.Time) (int64, error)

	// The scans skip suspended owners, taken-down sites and owners who
	// turned event off; no owner can turn off the empty event.

	// VerifiedDomains lists domains that became active at or after since.
	VerifiedDomains(ctx context.Context, event string, since time.Time) ([]NoticeSubject, error)
	// FailingDomains lists active domains whose checks have been failing
	// for at least failingFor, measured to their last failed check.
	FailingDomains(ctx context.Context, event string, failingFor time.Duration) ([]NoticeSubject, error)
	// LapsedDomains lists lapsed domains that began failing at or after
	// since.
	LapsedDomains(ctx context.Context, event string, since time.Time) ([]NoticeSubject, error)
	// UsedUpLinks lists unrevoked links used up at or after since.
	UsedUpLinks(ctx context.Context, event string, since time.Time) ([]NoticeSubject, error)
	// ExpiringLinks lists working links expiring after now and by by that
	// live longer than minLifetime, by owner, soonest first.
	ExpiringLinks(ctx context.Context, event string, now, by time.Time, minLifetime time.Duration) ([]NoticeSubject, error)
	// ExpiringTokens lists usable API tokens expiring after now and by by
	// that live longer than minLifetime.
	ExpiringTokens(ctx context.Context, event string, now, by time.Time, minLifetime time.Duration) ([]NoticeSubject, error)
	// ExpiringPreviews lists previews expiring after now and by by whose
	// current deploy lives longer than minLifetime.
	ExpiringPreviews(ctx context.Context, event string, now, by time.Time, minLifetime time.Duration) ([]NoticeSubject, error)
}

// Notifications returns the SQLite Notifications repository.
func (db *DB) Notifications() Notifications { return notificationsRepo{db} }

type notificationsRepo struct{ db *DB }

func (r notificationsRepo) OptOuts(ctx context.Context, ownerID string) ([]string, error) {
	events, err := r.db.q.ListNotificationOptOuts(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list notification opt-outs", err)
	}
	return events, nil
}

func (r notificationsRepo) SetOptOuts(ctx context.Context, ownerID string, off map[string]bool) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		for event, turnOff := range off {
			var err error
			if turnOff {
				err = q.AddNotificationOptOut(ctx, sqlcgen.AddNotificationOptOutParams{OwnerID: ownerID, Event: event})
			} else {
				err = q.DeleteNotificationOptOut(ctx, sqlcgen.DeleteNotificationOptOutParams{OwnerID: ownerID, Event: event})
			}
			if err != nil {
				return mapErr("set notification opt-out", err)
			}
		}
		return nil
	})
}

func (r notificationsRepo) Recipient(ctx context.Context, ownerID string) (NotificationRecipient, error) {
	row, err := r.db.q.GetNotificationRecipient(ctx, ownerID)
	if err != nil {
		return NotificationRecipient{}, mapErr("get notification recipient", err)
	}
	return NotificationRecipient{Email: row.Email, Suspended: row.SuspendedAt.Valid}, nil
}

func enqueueParams(e OutboxEntry) sqlcgen.EnqueueNotificationParams {
	return sqlcgen.EnqueueNotificationParams{
		ID: e.ID, Channel: string(e.Channel), EventKey: e.EventKey, OwnerID: e.OwnerID,
		Event: e.Event, Payload: e.Payload, CreatedAt: ms(e.CreatedAt),
	}
}

func (r notificationsRepo) Enqueue(ctx context.Context, entries ...OutboxEntry) (int, error) {
	added := 0
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		for _, e := range entries {
			n, err := q.EnqueueNotification(ctx, enqueueParams(e))
			if err != nil {
				return mapErr("enqueue notification", err)
			}
			added += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

func outboxFrom(row sqlcgen.NotificationOutbox) OutboxEntry {
	return OutboxEntry{
		ID: row.ID, Channel: NotificationChannel(row.Channel), EventKey: row.EventKey, OwnerID: row.OwnerID,
		Event: row.Event, Payload: row.Payload, Status: NotificationStatus(row.Status), Attempts: row.Attempts,
		CreatedAt: fromMs(row.CreatedAt), NextAttemptAt: fromNullMs(row.NextAttemptAt), FinishedAt: fromNullMs(row.FinishedAt),
	}
}

func outboxList(rows []sqlcgen.NotificationOutbox) []OutboxEntry {
	out := make([]OutboxEntry, len(rows))
	for i, row := range rows {
		out[i] = outboxFrom(row)
	}
	return out
}

func (r notificationsRepo) Due(ctx context.Context, channel NotificationChannel, now time.Time, limit int) ([]OutboxEntry, error) {
	rows, err := r.db.q.ListDueNotifications(ctx, sqlcgen.ListDueNotificationsParams{
		Channel: string(channel), Now: nullMs(now), RowLimit: int64(limit),
	})
	if err != nil {
		return nil, mapErr("list due notifications", err)
	}
	return outboxList(rows), nil
}

func (r notificationsRepo) Claim(ctx context.Context, id string) error {
	n, err := r.db.q.ClaimNotification(ctx, id)
	return rowsAffected("claim notification", n, err)
}

func (r notificationsRepo) Reschedule(ctx context.Context, id string, next time.Time) error {
	n, err := r.db.q.RescheduleNotification(ctx, sqlcgen.RescheduleNotificationParams{
		ID: id, NextAttemptAt: nullMs(next),
	})
	return rowsAffected("reschedule notification", n, err)
}

func (r notificationsRepo) Settle(ctx context.Context, id string, status NotificationStatus, at time.Time) error {
	n, err := r.db.q.SettleNotification(ctx, sqlcgen.SettleNotificationParams{
		ID: id, Status: string(status), At: nullMs(at),
	})
	return rowsAffected("settle notification", n, err)
}

func (r notificationsRepo) FailInterrupted(ctx context.Context, channel NotificationChannel, at time.Time) (int64, error) {
	n, err := r.db.q.FailInterruptedNotifications(ctx, sqlcgen.FailInterruptedNotificationsParams{
		Channel: string(channel), At: nullMs(at),
	})
	if err != nil {
		return 0, mapErr("fail interrupted notifications", err)
	}
	return n, nil
}

func (r notificationsRepo) RequeueInterrupted(ctx context.Context, channel NotificationChannel, at time.Time) (int64, error) {
	n, err := r.db.q.RequeueInterruptedNotifications(ctx, sqlcgen.RequeueInterruptedNotificationsParams{
		Channel: string(channel), At: nullMs(at),
	})
	if err != nil {
		return 0, mapErr("requeue interrupted notifications", err)
	}
	return n, nil
}

func (r notificationsRepo) CountSent(ctx context.Context, ownerID string, channel NotificationChannel, since time.Time) (int64, error) {
	n, err := r.db.q.CountSentNotifications(ctx, sqlcgen.CountSentNotificationsParams{
		OwnerID: ownerID, Channel: string(channel), Since: nullMs(since),
	})
	if err != nil {
		return 0, mapErr("count sent notifications", err)
	}
	return n, nil
}

func (r notificationsRepo) HeldOwners(ctx context.Context, channel NotificationChannel) ([]string, error) {
	ids, err := r.db.q.ListHeldNotificationOwners(ctx, string(channel))
	if err != nil {
		return nil, mapErr("list owners with held notifications", err)
	}
	return ids, nil
}

func (r notificationsRepo) Held(ctx context.Context, ownerID string, channel NotificationChannel) ([]OutboxEntry, error) {
	rows, err := r.db.q.ListHeldNotifications(ctx, sqlcgen.ListHeldNotificationsParams{OwnerID: ownerID, Channel: string(channel)})
	if err != nil {
		return nil, mapErr("list held notifications", err)
	}
	return outboxList(rows), nil
}

func (r notificationsRepo) Summarise(ctx context.Context, summary OutboxEntry, heldIDs []string, at time.Time) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if _, err := q.EnqueueNotification(ctx, enqueueParams(summary)); err != nil {
			return mapErr("enqueue summary", err)
		}
		for _, id := range heldIDs {
			if err := q.MarkNotificationSummarised(ctx, sqlcgen.MarkNotificationSummarisedParams{
				ID: id, At: nullMs(at),
			}); err != nil {
				return mapErr("mark notification summarised", err)
			}
		}
		return nil
	})
}

func (r notificationsRepo) Prune(ctx context.Context, before time.Time) (int64, error) {
	n, err := r.db.q.DeleteFinishedNotifications(ctx, nullMs(before))
	if err != nil {
		return 0, mapErr("delete finished notifications", err)
	}
	return n, nil
}

func (r notificationsRepo) VerifiedDomains(ctx context.Context, event string, since time.Time) ([]NoticeSubject, error) {
	rows, err := r.db.q.ListVerifiedDomainNotices(ctx, sqlcgen.ListVerifiedDomainNoticesParams{
		Since: nullMs(since), Event: event,
	})
	if err != nil {
		return nil, mapErr("list verified domains", err)
	}
	out := make([]NoticeSubject, len(rows))
	for i, row := range rows {
		out[i] = NoticeSubject{OwnerID: row.OwnerID, SiteID: row.SiteID, SiteName: row.SiteName, ID: row.ID, Name: row.Hostname, At: fromNullMs(row.VerifiedAt)}
	}
	return out, nil
}

func (r notificationsRepo) FailingDomains(ctx context.Context, event string, failingFor time.Duration) ([]NoticeSubject, error) {
	rows, err := r.db.q.ListFailingDomainNotices(ctx, sqlcgen.ListFailingDomainNoticesParams{
		FailingFor: failingFor.Milliseconds(), Event: event,
	})
	if err != nil {
		return nil, mapErr("list failing domains", err)
	}
	out := make([]NoticeSubject, len(rows))
	for i, row := range rows {
		out[i] = NoticeSubject{OwnerID: row.OwnerID, SiteName: row.SiteName, ID: row.ID, Name: row.Hostname, At: fromNullMs(row.FailingSince)}
	}
	return out, nil
}

func (r notificationsRepo) LapsedDomains(ctx context.Context, event string, since time.Time) ([]NoticeSubject, error) {
	rows, err := r.db.q.ListLapsedDomainNotices(ctx, sqlcgen.ListLapsedDomainNoticesParams{
		Since: nullMs(since), Event: event,
	})
	if err != nil {
		return nil, mapErr("list lapsed domains", err)
	}
	out := make([]NoticeSubject, len(rows))
	for i, row := range rows {
		out[i] = NoticeSubject{OwnerID: row.OwnerID, SiteID: row.SiteID, SiteName: row.SiteName, ID: row.ID, Name: row.Hostname, At: fromNullMs(row.FailingSince)}
	}
	return out, nil
}

func (r notificationsRepo) UsedUpLinks(ctx context.Context, event string, since time.Time) ([]NoticeSubject, error) {
	rows, err := r.db.q.ListUsedUpLinkNotices(ctx, sqlcgen.ListUsedUpLinkNoticesParams{
		Since: nullMs(since), Event: event,
	})
	if err != nil {
		return nil, mapErr("list used-up links", err)
	}
	out := make([]NoticeSubject, len(rows))
	for i, row := range rows {
		out[i] = NoticeSubject{OwnerID: row.OwnerID, SiteID: row.SiteID, SiteName: row.SiteName, ID: row.ID, Name: row.Label.String, PublicID: row.PublicID, At: fromNullMs(row.UsedUpAt)}
	}
	return out, nil
}

func (r notificationsRepo) ExpiringLinks(ctx context.Context, event string, now, by time.Time, minLifetime time.Duration) ([]NoticeSubject, error) {
	rows, err := r.db.q.ListExpiringLinkNotices(ctx, sqlcgen.ListExpiringLinkNoticesParams{
		Now: ms(now), ExpiringBy: ms(by), MinLifetime: minLifetime.Milliseconds(), Event: event,
	})
	if err != nil {
		return nil, mapErr("list expiring links", err)
	}
	out := make([]NoticeSubject, len(rows))
	for i, row := range rows {
		out[i] = NoticeSubject{OwnerID: row.OwnerID, SiteName: row.SiteName, ID: row.ID, Name: row.Label.String, PublicID: row.PublicID, At: fromNullMs(row.ExpiresAt)}
	}
	return out, nil
}

func (r notificationsRepo) ExpiringTokens(ctx context.Context, event string, now, by time.Time, minLifetime time.Duration) ([]NoticeSubject, error) {
	rows, err := r.db.q.ListExpiringTokenNotices(ctx, sqlcgen.ListExpiringTokenNoticesParams{
		Now: ms(now), ExpiringBy: ms(by), MinLifetime: minLifetime.Milliseconds(), Event: event,
	})
	if err != nil {
		return nil, mapErr("list expiring tokens", err)
	}
	out := make([]NoticeSubject, len(rows))
	for i, row := range rows {
		out[i] = NoticeSubject{OwnerID: row.OwnerID, ID: row.ID, Name: row.Name, At: fromNullMs(row.ExpiresAt)}
	}
	return out, nil
}

func (r notificationsRepo) ExpiringPreviews(ctx context.Context, event string, now, by time.Time, minLifetime time.Duration) ([]NoticeSubject, error) {
	rows, err := r.db.q.ListExpiringPreviewNotices(ctx, sqlcgen.ListExpiringPreviewNoticesParams{
		Now: ms(now), ExpiringBy: ms(by), MinLifetime: minLifetime.Milliseconds(), Event: event,
	})
	if err != nil {
		return nil, mapErr("list expiring previews", err)
	}
	out := make([]NoticeSubject, len(rows))
	for i, row := range rows {
		out[i] = NoticeSubject{OwnerID: row.OwnerID, SiteID: row.SiteID, SiteName: row.SiteName, Name: row.Name, At: fromNullMs(row.ExpiresAt)}
	}
	return out, nil
}
