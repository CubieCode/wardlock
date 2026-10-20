// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package moderation screens uploaded content (A2.15): the Moderator
// interface and its OpenAI implementation, the background worker that
// queues each unique image and HTML page once per content hash after a
// deploy is activated and records a verdict, the notices to the operator
// and owners, the operator's decisions, and the quarantine list the edge
// consults so a withheld file is served nowhere. Screening is off unless the
// operator configures a provider; the quarantine list and operator
// commands work either way.
package moderation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Moderator classifies one input. Implementations are safe for concurrent
// use.
type Moderator interface {
	Moderate(ctx context.Context, in Input) (Verdict, error)
}

// Input is what is sent: either Text, in pieces the provider classifies
// together, or one image.
type Input struct {
	Text []string
	// Image is the file's bytes; ImageType its media type (image/png,
	// image/jpeg, image/webp or image/gif).
	Image     []byte
	ImageType string
}

// Verdict is a provider's answer: the categories it flagged across every
// piece of the input.
type Verdict struct {
	Flagged []string
}

var (
	// ErrRejected means the provider refused the input itself (HTTP 400):
	// retrying cannot help, so the hash is recorded as skipped.
	ErrRejected = errors.New("moderation: input rejected")
	// ErrUnavailable means the attempt failed for a reason that may pass:
	// a rate limit, a server error, a network failure, or a key the
	// provider refused (fixed by the operator). The hash is retried.
	ErrUnavailable = errors.New("moderation: provider unavailable")
)

// RetryError is an ErrUnavailable that says how long to wait before trying
// again (a Retry-After header), when the provider gave one.
type RetryError struct {
	After time.Duration
	// Status is the provider's HTTP status, 0 for a network failure.
	Status int
}

func (e *RetryError) Error() string {
	if e.Status == 0 {
		return "moderation: provider unreachable"
	}
	return fmt.Sprintf("moderation: provider answered %d", e.Status)
}

func (e *RetryError) Unwrap() error { return ErrUnavailable }

// MinorsCategory is the category whose quarantine notice says to preserve
// and report rather than review.
const MinorsCategory = "sexual/minors"

// Policy turns flagged categories into an outcome.
type Policy struct {
	// Quarantine lists the categories that withhold a file.
	Quarantine []string
	// Flag lists the categories that mark a file for review; nil means
	// every flagged category not in Quarantine.
	Flag []string
}

// Outcome is a verdict under a Policy.
type Outcome string

const (
	OutcomeAllowed     Outcome = "allowed"
	OutcomeFlagged     Outcome = "flagged"
	OutcomeQuarantined Outcome = "quarantined"
)

// Decide returns the outcome of flagged categories: quarantined when any is
// in Quarantine, flagged when any is in Flag (or Flag is nil), allowed
// otherwise.
func (p Policy) Decide(flagged []string) Outcome {
	out := OutcomeAllowed
	for _, c := range flagged {
		switch {
		case slices.Contains(p.Quarantine, c):
			return OutcomeQuarantined
		case p.Flag == nil || slices.Contains(p.Flag, c):
			out = OutcomeFlagged
		}
	}
	return out
}
