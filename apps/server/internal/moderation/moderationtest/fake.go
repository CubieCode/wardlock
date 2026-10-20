// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package moderationtest is a fake Moderator for tests: it answers from a
// table keyed by content and records every input it was sent.
package moderationtest

import (
	"context"
	"strings"
	"sync"

	"wardlock.nz/server/internal/moderation"
)

// Fake is a moderation.Moderator. Verdicts maps a marker to the categories
// to flag: an input whose text or image bytes contain the marker gets them.
// Errors queued with Fail are returned first, one per call.
type Fake struct {
	mu       sync.Mutex
	verdicts map[string][]string
	errs     []error
	sent     []moderation.Input
}

var _ moderation.Moderator = (*Fake)(nil)

// New returns a Fake flagging nothing.
func New() *Fake { return &Fake{verdicts: map[string][]string{}} }

// Flag makes inputs containing marker flag categories.
func (f *Fake) Flag(marker string, categories ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verdicts[marker] = categories
}

// Fail queues errors for the next calls, in order.
func (f *Fake) Fail(errs ...error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, errs...)
}

// Sent returns every input the fake was sent, in order, failed calls
// included.
func (f *Fake) Sent() []moderation.Input {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]moderation.Input(nil), f.sent...)
}

// Moderate records in and answers from the queued errors, then the table.
func (f *Fake) Moderate(_ context.Context, in moderation.Input) (moderation.Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, in)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return moderation.Verdict{}, err
	}
	content := strings.Join(in.Text, "\n") + string(in.Image)
	var v moderation.Verdict
	for marker, cats := range f.verdicts {
		if strings.Contains(content, marker) {
			v.Flagged = append(v.Flagged, cats...)
		}
	}
	return v, nil
}
