// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// ModerationStatus is a content hash's screening verdict (A2.15).
type ModerationStatus string

const (
	ModerationPending     ModerationStatus = "pending"     // not screened yet
	ModerationAllowed     ModerationStatus = "allowed"     // nothing to act on, or released
	ModerationFlagged     ModerationStatus = "flagged"     // for the operator to review; served
	ModerationQuarantined ModerationStatus = "quarantined" // withheld on every site
	ModerationSkipped     ModerationStatus = "skipped"     // could not be sent
)

// ModerationKind says what screening sends for a hash.
type ModerationKind string

const (
	ModerationImage ModerationKind = "image" // the file itself
	ModerationText  ModerationKind = "text"  // an HTML page's extracted text
)

// ModerationResult is one content hash's row: the queue entry while
// NextAttemptAt is set, and the verdict.
type ModerationResult struct {
	SHA256     string
	Kind       ModerationKind
	Status     ModerationStatus
	Categories []string // the OpenAI categories the endpoint flagged
	Attempts   int
	QueuedAt   time.Time
	// NextAttemptAt is when the hash is next due for screening; zero when
	// it is not queued.
	NextAttemptAt time.Time
	CheckedAt     time.Time
	// The operator's decisions; zero when not made.
	ConfirmedAt, DiscardedAt, ReleasedAt time.Time
}

// ModerationSource is a file screening may send for a hash: one listing it
// in a serving deploy of an owner who is not exempt.
type ModerationSource struct {
	Path string
	Size int64
	// TextSHA256 and TextSize name the page's plain-text rendition; ""
	// when it has none.
	TextSHA256 string
	TextSize   int64
}

// ModerationUse is a site whose active deploy lists a hash.
type ModerationUse struct {
	SiteID, SiteName    string
	OwnerID, OwnerEmail string
	Path                string
}

// Moderation stores content screening's queue and verdicts and the owners'
// exemptions. It is instance-wide: it backs the screening worker, the
// edge's quarantine list and `wardlockd admin`, never an owner API handler.
type Moderation interface {
	// EnqueueUnscreened queues every image (.png, .jpg, .jpeg, .webp, .gif)
	// and HTML page (.html, .htm) in any letter case that an active deploy
	// lists, on a site neither taken down nor of a suspended owner, of an
	// owner who is not exempt, and that has never been queued, due at at.
	// It returns how many hashes it queued.
	EnqueueUnscreened(ctx context.Context, at time.Time) (int, error)
	// Due returns up to limit queued hashes due at or before now, the
	// longest waiting first.
	Due(ctx context.Context, now time.Time, limit int) ([]ModerationResult, error)
	// Source returns a file screening may send for sha256; ErrNotFound when
	// there is none any more.
	Source(ctx context.Context, sha256 string) (ModerationSource, error)
	// Record stores a verdict for a queued hash and takes it off the queue,
	// clearing any earlier operator decision. ErrNotFound when it is not
	// queued.
	Record(ctx context.Context, sha256 string, status ModerationStatus, categories []string, at time.Time) error
	// Retry counts a failed attempt and makes the queued hash due again at
	// next. ErrNotFound when it is not queued.
	Retry(ctx context.Context, sha256 string, attempts int, next time.Time) error
	// Dequeue takes a hash off the queue when nothing may be sent for it: a
	// hash never screened is forgotten (and queued again if it reappears),
	// a rescan is dropped and the verdict kept.
	Dequeue(ctx context.Context, sha256 string) error
	// Get returns a hash's row; ErrNotFound when it was never queued.
	Get(ctx context.Context, sha256 string) (ModerationResult, error)
	// Quarantined returns every quarantined hash.
	Quarantined(ctx context.Context) ([]string, error)
	// Review returns the flagged and quarantined hashes, oldest verdict
	// first.
	Review(ctx context.Context) ([]ModerationResult, error)
	// Uses returns every site whose active deploy lists sha256, by site
	// name and path.
	Uses(ctx context.Context, sha256 string) ([]ModerationUse, error)
	// Release makes a flagged or quarantined hash allowed. Confirm keeps a
	// quarantined hash held indefinitely; Discard lets garbage collection
	// remove it once no deploy lists it. Each is ErrNotFound for a hash
	// never queued and ErrConflict for one in another status.
	Release(ctx context.Context, sha256 string, at time.Time) error
	Confirm(ctx context.Context, sha256 string, at time.Time) error
	Discard(ctx context.Context, sha256 string, at time.Time) error
	// Rescan queues a hash again, due at at, keeping its verdict until the
	// new one. ErrNotFound for a hash never queued.
	Rescan(ctx context.Context, sha256 string, at time.Time) error
	// Backlog counts the queued hashes.
	Backlog(ctx context.Context) (int, error)
	// SetExempt exempts the owner from screening or ends the exemption.
	SetExempt(ctx context.Context, ownerID string, exempt bool) error
	// Exempt reports whether the owner is exempt.
	Exempt(ctx context.Context, ownerID string) (bool, error)
}

// Moderation returns the SQLite Moderation repository.
func (db *DB) Moderation() Moderation { return moderationRepo{db} }

type moderationRepo struct{ db *DB }

func (r moderationRepo) EnqueueUnscreened(ctx context.Context, at time.Time) (int, error) {
	n, err := r.db.q.EnqueueUnscreened(ctx, ms(at))
	if err != nil {
		return 0, mapErr("queue unscreened files", err)
	}
	return int(n), nil
}

func (r moderationRepo) Due(ctx context.Context, now time.Time, limit int) ([]ModerationResult, error) {
	rows, err := r.db.q.DueModerationResults(ctx, sqlcgen.DueModerationResultsParams{Now: nullMs(now), RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list due screening", err)
	}
	out := make([]ModerationResult, len(rows))
	for i, row := range rows {
		out[i] = ModerationResult{SHA256: row.Sha256, Kind: ModerationKind(row.Kind), Attempts: int(row.Attempts)}
	}
	return out, nil
}

func (r moderationRepo) Source(ctx context.Context, sha256 string) (ModerationSource, error) {
	row, err := r.db.q.ModerationSource(ctx, sha256)
	if err != nil {
		return ModerationSource{}, mapErr("find screening source", err)
	}
	return ModerationSource{Path: row.Path, Size: row.Size, TextSHA256: row.TextSha256, TextSize: row.TextSize}, nil
}

func (r moderationRepo) Record(ctx context.Context, sha256 string, status ModerationStatus, categories []string, at time.Time) error {
	if status == ModerationPending {
		return fmt.Errorf("record screening verdict: %w: pending is not a verdict", ErrConstraint)
	}
	n, err := r.db.q.RecordModerationVerdict(ctx, sqlcgen.RecordModerationVerdictParams{
		Sha256: sha256, Status: string(status), Categories: strings.Join(categories, ","), CheckedAt: nullMs(at),
	})
	return rowsAffected("record screening verdict", n, err)
}

func (r moderationRepo) Retry(ctx context.Context, sha256 string, attempts int, next time.Time) error {
	n, err := r.db.q.RetryModeration(ctx, sqlcgen.RetryModerationParams{Sha256: sha256, Attempts: int64(attempts), NextAttemptAt: nullMs(next)})
	return rowsAffected("retry screening", n, err)
}

func (r moderationRepo) Dequeue(ctx context.Context, sha256 string) error {
	if err := r.db.q.DequeueModeration(ctx, sha256); err != nil {
		return mapErr("dequeue screening", err)
	}
	return mapErr("cancel rescan", r.db.q.CancelModerationRescan(ctx, sha256))
}

func (r moderationRepo) Get(ctx context.Context, sha256 string) (ModerationResult, error) {
	row, err := r.db.q.GetModerationResult(ctx, sha256)
	if err != nil {
		return ModerationResult{}, mapErr("get screening result", err)
	}
	return moderationFromRow(row), nil
}

func moderationFromRow(row sqlcgen.ModerationResult) ModerationResult {
	var cats []string
	if row.Categories != "" {
		cats = strings.Split(row.Categories, ",")
	}
	return ModerationResult{
		SHA256: row.Sha256, Kind: ModerationKind(row.Kind), Status: ModerationStatus(row.Status), Categories: cats,
		Attempts: int(row.Attempts), QueuedAt: fromMs(row.QueuedAt), NextAttemptAt: fromNullMs(row.NextAttemptAt),
		CheckedAt: fromNullMs(row.CheckedAt), ConfirmedAt: fromNullMs(row.ConfirmedAt),
		DiscardedAt: fromNullMs(row.DiscardedAt), ReleasedAt: fromNullMs(row.ReleasedAt),
	}
}

func (r moderationRepo) Quarantined(ctx context.Context) ([]string, error) {
	hs, err := r.db.q.ListQuarantinedHashes(ctx)
	if err != nil {
		return nil, mapErr("list quarantined hashes", err)
	}
	return hs, nil
}

func (r moderationRepo) Review(ctx context.Context) ([]ModerationResult, error) {
	rows, err := r.db.q.ListModerationReview(ctx)
	if err != nil {
		return nil, mapErr("list screening review", err)
	}
	out := make([]ModerationResult, len(rows))
	for i, row := range rows {
		out[i] = moderationFromRow(row)
	}
	return out, nil
}

func (r moderationRepo) Uses(ctx context.Context, sha256 string) ([]ModerationUse, error) {
	rows, err := r.db.q.ListModerationUses(ctx, sha256)
	if err != nil {
		return nil, mapErr("list screening uses", err)
	}
	out := make([]ModerationUse, len(rows))
	for i, row := range rows {
		out[i] = ModerationUse{SiteID: row.SiteID, SiteName: row.SiteName, OwnerID: row.OwnerID, OwnerEmail: row.OwnerEmail, Path: row.Path}
	}
	return out, nil
}

func (r moderationRepo) Release(ctx context.Context, sha256 string, at time.Time) error {
	n, err := r.db.q.ReleaseModeration(ctx, sqlcgen.ReleaseModerationParams{Sha256: sha256, At: nullMs(at)})
	return r.decided(ctx, "release", sha256, n, err)
}

func (r moderationRepo) Confirm(ctx context.Context, sha256 string, at time.Time) error {
	n, err := r.db.q.ConfirmModeration(ctx, sqlcgen.ConfirmModerationParams{Sha256: sha256, At: nullMs(at)})
	return r.decided(ctx, "confirm", sha256, n, err)
}

func (r moderationRepo) Discard(ctx context.Context, sha256 string, at time.Time) error {
	n, err := r.db.q.DiscardModeration(ctx, sqlcgen.DiscardModerationParams{Sha256: sha256, At: nullMs(at)})
	return r.decided(ctx, "discard", sha256, n, err)
}

// decided tells a hash never queued (ErrNotFound) from one whose status
// does not allow the decision (ErrConflict) when nothing changed.
func (r moderationRepo) decided(ctx context.Context, what, sha256 string, n int64, err error) error {
	if err != nil || n > 0 {
		return mapErr(what+" screening result", err)
	}
	if _, err := r.Get(ctx, sha256); err != nil {
		return fmt.Errorf("%s screening result: %w", what, err)
	}
	return fmt.Errorf("%s screening result: %w", what, ErrConflict)
}

func (r moderationRepo) Rescan(ctx context.Context, sha256 string, at time.Time) error {
	n, err := r.db.q.RescanModeration(ctx, sqlcgen.RescanModerationParams{Sha256: sha256, At: nullMs(at)})
	return rowsAffected("rescan", n, err)
}

func (r moderationRepo) Backlog(ctx context.Context) (int, error) {
	n, err := r.db.q.CountQueuedModeration(ctx)
	if err != nil {
		return 0, mapErr("count screening backlog", err)
	}
	return int(n), nil
}

func (r moderationRepo) SetExempt(ctx context.Context, ownerID string, exempt bool) error {
	n, err := r.db.q.SetOwnerModerationExempt(ctx, sqlcgen.SetOwnerModerationExemptParams{ID: ownerID, ModerationExempt: boolInt(exempt)})
	return rowsAffected("set screening exemption", n, err)
}

func (r moderationRepo) Exempt(ctx context.Context, ownerID string) (bool, error) {
	v, err := r.db.q.GetOwnerModerationExempt(ctx, ownerID)
	if err != nil {
		return false, mapErr("get screening exemption", err)
	}
	return v == 1, nil
}
