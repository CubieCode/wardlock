// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"wardlock.nz/server/internal/store"
)

// Errors returned by Service.
var (
	// ErrNotFound covers a missing site and another owner's site alike.
	ErrNotFound = errors.New("site not found")
	// ErrInvalidName means the name is not a DNS label.
	ErrInvalidName = errors.New("invalid site name")
	// ErrReservedName means the name is a valid label Wardlock keeps.
	ErrReservedName = errors.New("reserved site name")
	// ErrNameTaken means any site on the instance, of any owner, holds the
	// name. Site names are public through DNS, so saying so reveals nothing.
	ErrNameTaken = errors.New("site name taken")
	// ErrUnavailable means the operator has taken the site down; only the
	// operator can restore or tear it down (A2.13).
	ErrUnavailable = errors.New("site taken down by the operator")
	// ErrLimitExceeded means the owner already has as many sites as their
	// quota allows (A2.14); the error is a *LimitError.
	ErrLimitExceeded = errors.New("site limit reached")
)

// LimitError is ErrLimitExceeded with the owner's site limit. Message is
// safe to show the owner.
type LimitError struct{ Limit int64 }

func (e *LimitError) Error() string { return e.Message() }
func (e *LimitError) Unwrap() error { return ErrLimitExceeded }

// Message explains the refusal to the owner.
func (e *LimitError) Message() string {
	sites := "sites"
	if e.Limit == 1 {
		sites = "site"
	}
	return fmt.Sprintf("You already have %d %s, the most this server allows you. Delete a site you no longer need or ask the operator for a higher limit.", e.Limit, sites)
}

// reserved are the names SPEC "Sites" keeps for Wardlock itself.
var reserved = map[string]bool{
	"auth": true, "www": true, "api": true, "admin": true, "app": true,
	"dashboard": true, "mail": true, "wardlock": true,
}

// notifier is the part of deploys.Notifier teardown needs.
type notifier interface {
	Notify(ctx context.Context, siteID string)
}

// Config holds a Service's dependencies.
type Config struct {
	Sites    store.Sites
	Notifier notifier
	// Organisation backs groups, tags and pins (V5.3).
	Organisation store.Organisation
	// Domains, Resolver and Certs back custom domains (C1.9). Certs may be
	// nil when Wardlock holds no certificates (local and proxy TLS modes).
	Domains  store.Domains
	Resolver Resolver
	Certs    CertRemover
	// CheckTimeout bounds each DNS lookup of a verification check;
	// DefaultCheckTimeout when zero.
	CheckTimeout time.Duration
	Log          *slog.Logger
	// BaseDomain and DashboardHost are lowercase hostnames without a
	// trailing dot, as platform.Config holds them.
	BaseDomain    string
	DashboardHost string
	Now           func() time.Time
	// Quotas are the instance's per-owner limits (A2.14); Create checks
	// Sites, with the owner's own override in its place when set.
	Quotas store.Quotas
}

// Service manages owners' sites. Every method takes the verified owner ID
// and addresses sites by name.
type Service struct{ c Config }

// NewService returns a Service over c.
func NewService(c Config) *Service { return &Service{c: c} }

// ValidName reports whether name is a DNS label: 1-63 characters of a-z,
// 0-9 and '-', not starting or ending with '-'.
func ValidName(name string) bool {
	if name == "" || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for i := range len(name) {
		if c := name[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// CheckName returns ErrInvalidName or ErrReservedName for a name no site
// may take, or nil.
func (s *Service) CheckName(name string) error {
	if !ValidName(name) {
		return ErrInvalidName
	}
	if reserved[name] || strings.HasPrefix(name, "xn--") || name == s.dashboardLabel() {
		return ErrReservedName
	}
	return nil
}

// dashboardLabel returns the label directly under the base domain on the
// way to the dashboard host ("dash" for dash.<base> or a.dash.<base>), or ""
// when the dashboard host is not under the base domain. A site with that
// name would be the dashboard host or a parent of it, and could set cookies
// the dashboard receives.
func (s *Service) dashboardLabel() string {
	prefix, ok := strings.CutSuffix(s.c.DashboardHost, "."+s.c.BaseDomain)
	if !ok {
		return ""
	}
	return prefix[strings.LastIndexByte(prefix, '.')+1:]
}

// Hostname returns the site's host on the base domain.
func (s *Service) Hostname(name string) string { return name + "." + s.c.BaseDomain }

// Create creates an empty site for ownerID, or returns a *LimitError when
// they already have as many sites as their quota allows.
func (s *Service) Create(ctx context.Context, ownerID, name string) (store.Site, error) {
	if err := s.CheckName(name); err != nil {
		return store.Site{}, err
	}
	site := store.Site{
		ID:        store.NewID(),
		OwnerID:   ownerID,
		Name:      name,
		CreatedAt: s.now(),
	}
	err := s.c.Sites.Create(ctx, site, s.c.Quotas)
	if errors.Is(err, store.ErrConflict) {
		return store.Site{}, ErrNameTaken
	}
	if qe := (*store.QuotaError)(nil); errors.As(err, &qe) {
		return store.Site{}, &LimitError{Limit: qe.Limit}
	}
	if err != nil {
		return store.Site{}, fmt.Errorf("create site: %w", err)
	}
	return site, nil
}

// Get returns ownerID's site called name.
func (s *Service) Get(ctx context.Context, ownerID, name string) (store.Site, error) {
	site, err := s.c.Sites.GetByOwnerName(ctx, ownerID, name)
	return site, storeErr("get site", err)
}

// List returns up to limit of ownerID's sites ordered by name, after
// afterName ("" for the first page).
func (s *Service) List(ctx context.Context, ownerID, afterName string, limit int) ([]store.Site, error) {
	sites, err := s.c.Sites.List(ctx, ownerID, afterName, limit)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	return sites, nil
}

// ForHostname returns ownerID's site served at hostname: <name>.<base
// domain>, or one of the site's custom domains whatever its status.
func (s *Service) ForHostname(ctx context.Context, ownerID, hostname string) (store.Site, error) {
	host := strings.TrimSuffix(strings.ToLower(hostname), ".")
	if name, ok := strings.CutSuffix(host, "."+s.c.BaseDomain); ok && ValidName(name) {
		return s.Get(ctx, ownerID, name)
	}
	if h, err := NormaliseHostname(host); err == nil {
		host = h // a custom domain typed in Unicode
	}
	site, err := s.c.Sites.GetByOwnerDomain(ctx, ownerID, host)
	return site, storeErr("get site by domain", err)
}

// Delete tears down ownerID's site called name, with its deploys, links,
// policies and custom domains, frees the name and notifies subscribers so
// serving stops at once. Unreferenced blobs wait for garbage collection.
func (s *Service) Delete(ctx context.Context, ownerID, name string) error {
	site, err := s.Get(ctx, ownerID, name)
	if err != nil {
		return err
	}
	if !site.TakenDownAt.IsZero() {
		return ErrUnavailable
	}
	// The site's custom domains go with it, and so do their certificates.
	hostnames, err := s.hostnames(ctx, site)
	if err != nil {
		return err
	}
	if err := s.c.Sites.Delete(ctx, ownerID, site.ID); err != nil {
		return storeErr("delete site", err)
	}
	s.c.Notifier.Notify(ctx, site.ID)
	for _, h := range hostnames {
		s.removeCert(ctx, h)
	}
	return nil
}

func storeErr(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	default:
		return fmt.Errorf("%s: %w", what, err)
	}
}
