// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
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
	ModerationSkipped     ModerationStatus = "skipped"     // nothing in it to screen
)

// ModerationUnscreenable is the reason recorded for a flagged hash holding
// an image that could not be screened (A2.15): it is served and listed for
// the operator to review.
const ModerationUnscreenable = "unscreenable"

// ModerationKind says what screening covered for a hash, decided from its
// bytes and the paths that serve it.
type ModerationKind string

const (
	ModerationNone      ModerationKind = ""           // nothing to screen, or not screened yet
	ModerationImage     ModerationKind = "image"      // the file as an image
	ModerationText      ModerationKind = "text"       // its text, as a page or text file
	ModerationImageText ModerationKind = "image,text" // both
)

// HasText reports whether k covers text.
func (k ModerationKind) HasText() bool { return k == ModerationText || k == ModerationImageText }

// ModerationResult is one content hash's row: the queue entry while
// NextAttemptAt is set, and the verdict.
type ModerationResult struct {
	SHA256     string
	Kind       ModerationKind
	Status     ModerationStatus
	Categories []string // the OpenAI categories the endpoint flagged
	// Reason qualifies the status: ModerationUnscreenable for a flagged
	// image that could not be screened, else "".
	Reason string
	// Decoded is, for a gzip file, the SHA-256 of what it decompresses to;
	// "" otherwise.
	Decoded  string
	Attempts int
	QueuedAt time.Time
	// NextAttemptAt is when the hash is next due for screening; zero when
	// it is not queued.
	NextAttemptAt time.Time
	CheckedAt     time.Time
	// The operator's decisions; zero when not made.
	ConfirmedAt, DiscardedAt, ReleasedAt time.Time
}

// ModerationVerdict is what screening a queued hash came to.
type ModerationVerdict struct {
	// Status is the new verdict; "" keeps the current one (screening found
	// nothing the endpoint could judge), which a hash never screened
	// cannot do.
	Status     ModerationStatus
	Categories []string
	Reason     string
	// Kind is what was covered; Decoded what a gzip file decompresses to.
	Kind    ModerationKind
	Decoded string
}

// ModerationSource is what screening needs about a hash that a serving
// deploy of an owner who is not exempt lists.
type ModerationSource struct {
	Size int64
	// TextServed means a path the edge serves as displayed text lists it
	// (edge.TextTypes: .html, .htm, .xhtml, .svg, .txt, .text, .md,
	// .markdown, .csv, .json, .xml).
	TextServed bool
}

// ModerationUse is a site whose active deploy lists a hash.
type ModerationUse struct {
	SiteID, SiteName    string
	OwnerID, OwnerEmail string
	Path                string
	// Unavailable means the site is taken down or its owner suspended.
	Unavailable bool
}

// ModerationState is what the edge needs about one file of a deploy.
type ModerationState struct {
	SHA256 string
	// Status is ModerationPending for a hash never queued.
	Status ModerationStatus
	// Decoded is, for a gzip file, the SHA-256 of what it decompresses to.
	Decoded string
}

// Moderation stores content screening's queue and verdicts and the owners'
// exemptions. It is instance-wide: it backs the screening worker, the
// edge's quarantine list and `wardlockd admin`, never an owner API handler.
type Moderation interface {
	// EnqueueSite queues, due at at, every file the site's active deploy
	// lists that was never queued, and queues again each screened hash it
	// lists under a path served as displayed text (the edge.TextTypes
	// extensions, any letter case) whose text was never screened, unless it
	// is quarantined. Nothing when the site is taken down or has no active
	// deploy, or its owner is exempt or suspended. It returns how many
	// hashes it queued.
	EnqueueSite(ctx context.Context, siteID string, at time.Time) (int, error)
	// ScreenedSites returns up to limit site IDs after after, in order, of
	// the sites EnqueueSite would queue files for.
	ScreenedSites(ctx context.Context, after string, limit int) ([]string, error)
	// Due returns up to limit queued hashes due at or before now, the
	// longest waiting first, with their kind and current status.
	Due(ctx context.Context, now time.Time, limit int) ([]ModerationResult, error)
	// Source returns what screening needs about sha256; ErrNotFound when no
	// serving deploy of an owner who is not exempt lists it any more.
	Source(ctx context.Context, sha256 string) (ModerationSource, error)
	// Record stores a verdict for a queued hash and takes it off the queue.
	// A new status clears any earlier operator decision; the same status
	// keeps it. A verdict with no Status keeps the current one and records
	// only Kind and Decoded. ErrNotFound when the hash is not queued (or,
	// keeping the verdict, was never screened).
	Record(ctx context.Context, sha256 string, v ModerationVerdict, at time.Time) error
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
	// DeployScreening returns each file hash of a deploy with its status
	// and what a gzip file decompresses to.
	DeployScreening(ctx context.Context, deployID string) ([]ModerationState, error)
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

func (r moderationRepo) EnqueueSite(ctx context.Context, siteID string, at time.Time) (int, error) {
	n, err := r.db.q.EnqueueSite(ctx, sqlcgen.EnqueueSiteParams{SiteID: siteID, At: ms(at)})
	if err != nil {
		return 0, mapErr("queue site files", err)
	}
	m, err := r.db.q.RequeueSiteText(ctx, sqlcgen.RequeueSiteTextParams{SiteID: siteID, At: nullMs(at)})
	if err != nil {
		return 0, mapErr("queue site text", err)
	}
	return int(n + m), nil
}

func (r moderationRepo) ScreenedSites(ctx context.Context, after string, limit int) ([]string, error) {
	ids, err := r.db.q.ListScreenedSites(ctx, sqlcgen.ListScreenedSitesParams{After: after, RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list screened sites", err)
	}
	return ids, nil
}

func (r moderationRepo) Due(ctx context.Context, now time.Time, limit int) ([]ModerationResult, error) {
	rows, err := r.db.q.DueModerationResults(ctx, sqlcgen.DueModerationResultsParams{Now: nullMs(now), RowLimit: int64(limit)})
	if err != nil {
		return nil, mapErr("list due screening", err)
	}
	out := make([]ModerationResult, len(rows))
	for i, row := range rows {
		out[i] = ModerationResult{SHA256: row.Sha256, Kind: ModerationKind(row.Kind), Status: ModerationStatus(row.Status), Attempts: int(row.Attempts)}
	}
	return out, nil
}

func (r moderationRepo) Source(ctx context.Context, sha256 string) (ModerationSource, error) {
	size, err := r.db.q.ModerationSource(ctx, sha256)
	if err != nil {
		return ModerationSource{}, mapErr("find screening source", err)
	}
	texts, err := r.db.q.CountModerationTextPaths(ctx, sha256)
	if err != nil {
		return ModerationSource{}, mapErr("count screening text paths", err)
	}
	return ModerationSource{Size: size, TextServed: texts > 0}, nil
}

func (r moderationRepo) Record(ctx context.Context, sha256 string, v ModerationVerdict, at time.Time) error {
	decoded := sql.NullString{String: v.Decoded, Valid: v.Decoded != ""}
	if v.Status == "" {
		n, err := r.db.q.KeepModerationVerdict(ctx, sqlcgen.KeepModerationVerdictParams{Sha256: sha256, Kind: string(v.Kind), DecodedSha256: decoded})
		return rowsAffected("record screening coverage", n, err)
	}
	if v.Status == ModerationPending {
		return fmt.Errorf("record screening verdict: %w: pending is not a verdict", ErrConstraint)
	}
	n, err := r.db.q.RecordModerationVerdict(ctx, sqlcgen.RecordModerationVerdictParams{
		Sha256: sha256, Status: string(v.Status), Categories: strings.Join(v.Categories, ","), Reason: v.Reason,
		Kind: string(v.Kind), DecodedSha256: decoded, CheckedAt: nullMs(at),
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
		Reason: row.Reason, Decoded: row.DecodedSha256.String, Attempts: int(row.Attempts), QueuedAt: fromMs(row.QueuedAt), NextAttemptAt: fromNullMs(row.NextAttemptAt),
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
		out[i] = ModerationUse{
			SiteID: row.SiteID, SiteName: row.SiteName, OwnerID: row.OwnerID, OwnerEmail: row.OwnerEmail, Path: row.Path,
			Unavailable: row.TakenDownAt.Valid || row.SuspendedAt.Valid,
		}
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

func (r moderationRepo) DeployScreening(ctx context.Context, deployID string) ([]ModerationState, error) {
	rows, err := r.db.q.ListDeployScreening(ctx, deployID)
	if err != nil {
		return nil, mapErr("list deploy screening", err)
	}
	out := make([]ModerationState, len(rows))
	for i, row := range rows {
		out[i] = ModerationState{SHA256: row.Sha256, Status: ModerationStatus(row.Status), Decoded: row.DecodedSha256}
	}
	return out, nil
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
