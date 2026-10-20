// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"wardlock.nz/server/internal/store"
)

// Custom domain errors. A missing domain is ErrNotFound, like a missing
// site.
var (
	// ErrDomainTaken means the hostname is attached to a site on the
	// instance; which site, and whose, is never said.
	ErrDomainTaken = errors.New("domain taken")
	// ErrCheckTooSoon means the domain was checked moments ago; the
	// CheckTooSoonError wrapping it says when to ask again.
	ErrCheckTooSoon = errors.New("domain checked too recently")
)

// CheckTooSoonError carries how long to wait before asking for a check.
type CheckTooSoonError struct{ RetryAfter time.Duration }

func (e *CheckTooSoonError) Error() string { return "domain checked too recently" }
func (e *CheckTooSoonError) Unwrap() error { return ErrCheckTooSoon }

// CertRemover deletes a custom domain's certificate, from memory and
// storage, when the domain stops being served. The TLS manager satisfies
// it in automatic TLS mode.
type CertRemover interface {
	RemoveCertificate(ctx context.Context, hostname string) error
}

// RoutingTarget is where a custom domain's CNAME must lead: the site's host
// on the base domain.
func (s *Service) RoutingTarget(siteName string) string { return s.Hostname(siteName) }

// AddDomain attaches hostname to ownerID's site and checks its DNS straight
// away. The returned domain is pending unless that first check passed. A
// taken-down site takes no new domains (ErrUnavailable).
func (s *Service) AddDomain(ctx context.Context, ownerID, siteName, hostname string) (store.Domain, error) {
	h, err := s.CheckHostname(hostname)
	if err != nil {
		return store.Domain{}, err
	}
	site, err := s.Get(ctx, ownerID, siteName)
	if err != nil {
		return store.Domain{}, err
	}
	if !site.TakenDownAt.IsZero() {
		return store.Domain{}, ErrUnavailable
	}
	d := store.Domain{
		ID:       store.NewID(),
		SiteID:   site.ID,
		Hostname: h,
		Status:   store.DomainPending,
		// A fresh token for every claim, so a TXT record left from an
		// earlier claim never verifies a new one.
		VerificationToken: rand.Text(),
		CreatedAt:         s.now(),
	}
	err = s.c.Domains.Create(ctx, ownerID, d)
	switch {
	case errors.Is(err, store.ErrConflict):
		return store.Domain{}, ErrDomainTaken
	case err != nil:
		return store.Domain{}, storeErr("create domain", err)
	}
	checked, err := s.check(ctx, d, site.Name)
	if errors.Is(err, errSuperseded) {
		// The worker got there first; answer with what it recorded.
		return s.reread(ctx, ownerID, d)
	}
	if err != nil {
		// The domain exists; the worker checks it on its next pass.
		s.log().LogAttrs(ctx, slog.LevelWarn, "first domain check failed",
			slog.String("hostname", d.Hostname), slog.String("error", err.Error()))
		return d, nil
	}
	return checked, nil
}

// GetDomain returns one of ownerID's site's domains, whatever its status.
func (s *Service) GetDomain(ctx context.Context, ownerID, siteName, hostname string) (store.Domain, error) {
	_, d, err := s.ownedDomain(ctx, ownerID, siteName, hostname)
	return d, err
}

// ListDomains returns up to limit of ownerID's site's domains ordered by
// hostname, after afterHostname ("" for the first page).
func (s *Service) ListDomains(ctx context.Context, ownerID, siteName, afterHostname string, limit int) ([]store.Domain, error) {
	site, err := s.Get(ctx, ownerID, siteName)
	if err != nil {
		return nil, err
	}
	ds, err := s.c.Domains.List(ctx, ownerID, site.ID, afterHostname, limit)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	return ds, nil
}

// VerifyDomain checks one of ownerID's domains now and returns the
// outcome. It refuses with a CheckTooSoonError within VerifyCooldown of the
// last check.
func (s *Service) VerifyDomain(ctx context.Context, ownerID, siteName, hostname string) (store.Domain, error) {
	site, d, err := s.ownedDomain(ctx, ownerID, siteName, hostname)
	if err != nil {
		return store.Domain{}, err
	}
	if wait := d.LastCheckedAt.Add(VerifyCooldown).Sub(s.now()); !d.LastCheckedAt.IsZero() && wait > 0 {
		return store.Domain{}, &CheckTooSoonError{RetryAfter: wait}
	}
	checked, err := s.check(ctx, d, site.Name)
	if errors.Is(err, errSuperseded) {
		// Another check (the worker's, or a concurrent request) was
		// recorded while this one ran: answer with the newer result.
		return s.reread(ctx, ownerID, d)
	}
	return checked, storeErr("verify domain", err)
}

// reread returns d as currently stored.
func (s *Service) reread(ctx context.Context, ownerID string, d store.Domain) (store.Domain, error) {
	cur, err := s.c.Domains.Get(ctx, ownerID, d.SiteID, d.Hostname)
	return cur, storeErr("get domain", err)
}

// RemoveDomain detaches one of ownerID's domains: serving it stops at once
// and its certificate is deleted. A taken-down site's domains cannot be
// removed (ErrUnavailable).
func (s *Service) RemoveDomain(ctx context.Context, ownerID, siteName, hostname string) error {
	site, d, err := s.ownedDomain(ctx, ownerID, siteName, hostname)
	if err != nil {
		return err
	}
	if !site.TakenDownAt.IsZero() {
		// A taken-down site's domains are frozen with it (A2.13).
		return ErrUnavailable
	}
	if err := s.c.Domains.Delete(ctx, ownerID, site.ID, d.Hostname); err != nil {
		return storeErr("remove domain", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	s.removeCert(ctx, d.Hostname)
	s.log().LogAttrs(ctx, slog.LevelInfo, "custom domain removed",
		slog.String("site_id", site.ID), slog.String("hostname", d.Hostname))
	return nil
}

// ownedDomain finds ownerID's site and one of its domains. Hostnames are
// matched in their normalised form, so any spelling of one finds it; one
// that cannot be normalised is simply not found.
func (s *Service) ownedDomain(ctx context.Context, ownerID, siteName, hostname string) (store.Site, store.Domain, error) {
	site, err := s.Get(ctx, ownerID, siteName)
	if err != nil {
		return store.Site{}, store.Domain{}, err
	}
	h, err := NormaliseHostname(hostname)
	if err != nil {
		return store.Site{}, store.Domain{}, fmt.Errorf("get domain: %w", ErrNotFound)
	}
	d, err := s.c.Domains.Get(ctx, ownerID, site.ID, h)
	if err != nil {
		return store.Site{}, store.Domain{}, storeErr("get domain", err)
	}
	return site, d, nil
}

// errSuperseded means a check's snapshot of its domain was out of date by
// the time it finished: the domain was removed, or another check was
// recorded meanwhile. Nothing was written.
var errSuperseded = errors.New("domain changed during the check")

// check runs one verification check of d, a domain of the site called
// siteName, and records it only if d is still the stored state, so a slow
// check can never overwrite a newer result. The edge is told about every
// status change and every result that is not active (so it can never keep
// serving a host from a stale positive), and a domain that lapses loses its
// certificate.
func (s *Service) check(ctx context.Context, d store.Domain, siteName string) (store.Domain, error) {
	timeout := s.c.CheckTimeout
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	res := verify(ctx, s.c.Resolver, timeout, d.Hostname, s.RoutingTarget(siteName), d.VerificationToken)
	next := apply(d, res, s.now())
	err := s.c.Domains.RecordCheck(ctx, d, next)
	if errors.Is(err, store.ErrNotFound) {
		return store.Domain{}, fmt.Errorf("record domain check: %w", errSuperseded)
	}
	if err != nil {
		return store.Domain{}, fmt.Errorf("record domain check: %w", err)
	}
	if next.Status != d.Status || next.Status != store.DomainActive {
		s.c.Notifier.Notify(ctx, d.SiteID)
	}
	if next.Status != d.Status {
		if next.Status == store.DomainLapsed {
			s.removeCert(ctx, d.Hostname)
		}
		attrs := []slog.Attr{
			slog.String("site_id", d.SiteID), slog.String("hostname", d.Hostname),
			slog.String("from", string(d.Status)), slog.String("to", string(next.Status)),
		}
		if !res.ok {
			attrs = append(attrs, slog.String("reason", string(res.reason)))
		}
		s.log().LogAttrs(ctx, slog.LevelInfo, "custom domain status changed", attrs...)
	}
	return next, nil
}

func (s *Service) removeCert(ctx context.Context, hostname string) {
	if s.c.Certs == nil {
		return
	}
	if err := s.c.Certs.RemoveCertificate(ctx, hostname); err != nil {
		s.log().LogAttrs(ctx, slog.LevelError, "remove certificate",
			slog.String("hostname", hostname), slog.String("error", err.Error()))
	}
}

// checkPage and checkWorkers bound one pass of the verification worker:
// candidates are read a page at a time and checked a few at once, so slow
// DNS for one domain does not hold up the rest.
const (
	checkPage    = 200
	checkWorkers = 8
)

// CheckDue runs every automatic check that is due now and returns how many
// ran. Failures to record a check are logged and left for the next pass.
func (s *Service) CheckDue(ctx context.Context) (int, error) {
	now := s.now()
	var (
		wg      sync.WaitGroup
		sem     = make(chan struct{}, checkWorkers)
		mu      sync.Mutex
		checked int
	)
	defer wg.Wait()
	after := ""
	for {
		page, err := s.c.Domains.ListToCheck(ctx, now.Add(-CheckWindow), now.Add(-RecheckInterval), after, checkPage)
		if err != nil {
			return checked, fmt.Errorf("list domains to check: %w", err)
		}
		for _, d := range page {
			if at, ok := NextCheck(d); !ok || at.After(now) {
				continue
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return checked, ctx.Err()
			}
			wg.Go(func() {
				defer func() { <-sem }()
				if s.checkOne(ctx, d) {
					mu.Lock()
					checked++
					mu.Unlock()
				}
			})
		}
		if len(page) < checkPage {
			wg.Wait()
			return checked, nil
		}
		after = page[len(page)-1].ID
	}
}

func (s *Service) checkOne(ctx context.Context, d store.Domain) bool {
	site, err := s.c.Sites.GetByID(ctx, d.SiteID)
	if err == nil {
		_, err = s.check(ctx, d, site.Name)
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, errSuperseded) {
		return false // removed, or checked by someone else, meanwhile
	}
	if err != nil {
		s.log().LogAttrs(ctx, slog.LevelError, "domain check failed",
			slog.String("hostname", d.Hostname), slog.String("error", err.Error()))
		return false
	}
	return true
}

// ExpirePending deletes every claim still pending PendingExpiry after it
// was added, as if its owner had removed it: the edge is told, any
// certificate is deleted and the hostname is free for a new claim, which
// gets a new token. A claim verified meanwhile is never deleted. It returns
// how many were.
func (s *Service) ExpirePending(ctx context.Context) (int, error) {
	before := s.now().Add(-PendingExpiry)
	expired := 0
	for after := ""; ; {
		page, err := s.c.Domains.ListExpiredPending(ctx, before, after, checkPage)
		if err != nil {
			return expired, fmt.Errorf("list expired domains: %w", err)
		}
		for _, d := range page {
			err := s.c.Domains.DeleteExpiredPending(ctx, d.ID, before)
			if errors.Is(err, store.ErrNotFound) {
				continue // verified or removed meanwhile
			}
			if err != nil {
				return expired, fmt.Errorf("delete expired domain: %w", err)
			}
			expired++
			s.c.Notifier.Notify(ctx, d.SiteID)
			s.removeCert(ctx, d.Hostname)
			s.log().LogAttrs(ctx, slog.LevelInfo, "unverified custom domain expired",
				slog.String("site_id", d.SiteID), slog.String("hostname", d.Hostname))
		}
		if len(page) < checkPage {
			return expired, nil
		}
		after = page[len(page)-1].ID
	}
}

// RunChecks runs ExpirePending and CheckDue now and then every interval
// until ctx ends.
func (s *Service) RunChecks(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.ExpirePending(ctx); err != nil && ctx.Err() == nil {
			s.log().LogAttrs(ctx, slog.LevelError, "expiring custom domains failed", slog.String("error", err.Error()))
		}
		if _, err := s.CheckDue(ctx); err != nil && ctx.Err() == nil {
			s.log().LogAttrs(ctx, slog.LevelError, "domain checks failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) now() time.Time { return s.c.Now().UTC().Truncate(time.Millisecond) }

func (s *Service) log() *slog.Logger {
	if s.c.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.c.Log
}
