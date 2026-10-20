// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package moderation screens uploaded content (A2.15): the Moderator
// interface and its OpenAI implementation, the background worker that
// queues each unique file once per content hash after a deploy is
// activated, decides from its bytes what to send (images, text, images
// embedded in pages and SVGs) and records a verdict, the notices to the operator
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

// Input is one request: either Text, in pieces the provider classifies
// together, or one image.
type Input struct {
	Text []string
	// Image is the image's bytes; ImageType its media type (image/png,
	// image/jpeg or image/webp).
	Image     []byte
	ImageType string
}

// Verdict is a provider's answer: the categories it flagged across every
// piece of the input.
type Verdict struct {
	Flagged []string
}

var (
	// ErrRejected means the provider refused this input itself (an image
	// it cannot read, an input too large): retrying cannot help, so the
	// input counts as unscreenable.
	ErrRejected = errors.New("moderation: input rejected")
	// ErrUnavailable means the attempt failed for a reason that may pass:
	// a rate limit, a server error, a network failure, a key the provider
	// refused or a request it calls malformed (fixed by the operator or a
	// new release). The hash is retried.
	ErrUnavailable = errors.New("moderation: provider unavailable")
)

// RetryError is an ErrUnavailable with what is known about it.
type RetryError struct {
	// After is how long the provider asked to wait (Retry-After); 0 when
	// it did not say.
	After time.Duration
	// Status is the provider's HTTP status, 0 for a network failure.
	Status int
	// Global means the failure is not this input's: a refused key, a rate
	// limit or a request the provider calls malformed fails every input
	// alike, so the whole worker pauses and the operator is alerted.
	Global bool
	// Code is the provider's error code, when it gave one (letters,
	// digits, '_', '-' and '.' only; never its message).
	Code string
}

func (e *RetryError) Error() string {
	switch {
	case e.Status == 0:
		return "moderation: provider unreachable"
	case e.Code != "":
		return fmt.Sprintf("moderation: provider answered %d (%s)", e.Status, e.Code)
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
