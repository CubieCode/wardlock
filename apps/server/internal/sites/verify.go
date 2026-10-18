// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"wardlock.nz/server/internal/store"
)

// Verification timing (SPEC "Custom domains").
const (
	// CheckWindow is how long a pending domain is checked automatically
	// after it is added; afterwards only on request.
	CheckWindow = 48 * time.Hour
	// MinBackoff and MaxBackoff bound the wait between a pending domain's
	// checks: it doubles from MinBackoff with the domain's age.
	MinBackoff = time.Minute
	MaxBackoff = time.Hour
	// RecheckInterval separates checks of active and lapsed domains.
	RecheckInterval = 24 * time.Hour
	// LapseAfter is how long an active domain may keep failing before it
	// lapses.
	LapseAfter = 72 * time.Hour
	// VerifyCooldown is the shortest gap between checks an owner can ask
	// for.
	VerifyCooldown = 10 * time.Second
	// DefaultCheckTimeout bounds each DNS lookup of a check.
	DefaultCheckTimeout = 10 * time.Second
)

// TXTPrefix starts the verification TXT record's value.
const TXTPrefix = "wardlock-verify="

// TXTName is where the verification TXT record for hostname lives.
func TXTName(hostname string) string { return "_wardlock." + hostname }

// NextCheck returns when d's next automatic check is due, or false when
// none is: a pending domain past its CheckWindow waits for its owner. A
// pending domain is checked straight away, then after waits that double
// with its age from MinBackoff to MaxBackoff (1, 2, 4, … 32 minutes, then
// hourly); active and lapsed domains every RecheckInterval.
func NextCheck(d store.Domain) (time.Time, bool) {
	if d.LastCheckedAt.IsZero() {
		return d.CreatedAt, true
	}
	if d.Status != store.DomainPending {
		return d.LastCheckedAt.Add(RecheckInterval), true
	}
	wait := min(max(d.LastCheckedAt.Sub(d.CreatedAt), MinBackoff), MaxBackoff)
	next := d.LastCheckedAt.Add(wait)
	if next.After(d.CreatedAt.Add(CheckWindow)) {
		return time.Time{}, false
	}
	return next, true
}

// checkResult is one verification check's outcome.
type checkResult struct {
	ok     bool
	reason store.DomainFailure
	detail string
}

// verify checks whether hostname is pointed at target, the site's host on
// the base domain: its CNAME chain ends at target, or _wardlock.<hostname>
// holds the claim's token. The chain is compared through canonical names:
// the sites wildcard has address records, so target is its own canonical
// name and a chain through it must end there. If target is itself a CNAME
// the chain cannot be confirmed and only the TXT record verifies.
func verify(ctx context.Context, r Resolver, timeout time.Duration, hostname, target, token string) checkResult {
	// Each lookup gets its own time limit, so a CNAME lookup that hangs
	// does not also cost the TXT fallback its chance.
	canonicalName := func(name string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return r.CanonicalName(ctx, name)
	}
	canonHost, hostErr := canonicalName(hostname)
	canonTarget, targetErr := canonicalName(target)
	if hostErr == nil && targetErr == nil && canonHost == target && canonTarget == target {
		return checkResult{ok: true}
	}
	txtCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	txts, txtErr := r.LookupTXT(txtCtx, TXTName(hostname))
	mismatch := false
	for _, t := range txts {
		t = strings.TrimSpace(t)
		if t == TXTPrefix+token {
			return checkResult{ok: true}
		}
		if strings.HasPrefix(t, TXTPrefix) {
			mismatch = true
		}
	}
	lookupFailed := func(err error) bool { return err != nil && !errors.Is(err, ErrNoRecord) }
	switch {
	case mismatch:
		return checkResult{reason: store.DomainTXTMismatch, detail: fmt.Sprintf(
			"The TXT record at %s holds a different token. Replace its value with the one shown for this domain.", TXTName(hostname))}
	case hostErr == nil && canonHost != hostname:
		detail := fmt.Sprintf("%s points to %s, not %s.", hostname, canonHost, target)
		if canonHost == canonTarget {
			detail = fmt.Sprintf("%s leads to %s, but Wardlock cannot confirm it goes through %s. Add the TXT record instead.", hostname, canonHost, target)
		}
		return checkResult{reason: store.DomainCNAMEElsewhere, detail: detail}
	case lookupFailed(hostErr) || lookupFailed(txtErr) || lookupFailed(targetErr):
		return checkResult{reason: store.DomainDNSError, detail: fmt.Sprintf(
			"The DNS lookup for %s failed or timed out. Wardlock will try again.", hostname)}
	}
	return checkResult{reason: store.DomainNoRecord, detail: fmt.Sprintf("No CNAME or TXT record found for %s.", hostname)}
}

// apply records a check's outcome at now on d. A pass makes the domain
// active (setting VerifiedAt when it was not); a failure keeps pending and
// lapsed domains as they are and lapses an active domain that has failed
// every check for LapseAfter.
func apply(d store.Domain, res checkResult, now time.Time) store.Domain {
	d.LastCheckedAt = now
	if res.ok {
		if d.Status != store.DomainActive {
			d.VerifiedAt = now
		}
		d.Status = store.DomainActive
		d.FailingSince, d.LastFailureReason, d.FailureDetail = time.Time{}, "", ""
		return d
	}
	if d.FailingSince.IsZero() {
		d.FailingSince = now
	}
	d.LastFailureReason, d.FailureDetail = res.reason, res.detail
	if d.Status == store.DomainActive && now.Sub(d.FailingSince) >= LapseAfter {
		d.Status = store.DomainLapsed
	}
	return d
}
