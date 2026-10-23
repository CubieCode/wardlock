// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"wardlock.nz/server/internal/store"
)

// AnalyticsRanges are the days an analytics report may cover (V5.4).
var AnalyticsRanges = []int{7, 30, 90}

// ErrInvalidAnalytics means an analytics request named a range not in
// AnalyticsRanges or more than one scope.
var ErrInvalidAnalytics = errors.New("invalid analytics request")

// AnalyticsScope narrows a report to at most one of a site (by name), a
// group or a tag (by ID); the zero value is all the owner's sites.
type AnalyticsScope struct {
	SiteName string
	GroupID  string
	TagID    string
}

// Analytics is an owner's requests over the Days × 24 whole UTC hours
// from From to To, the last of which is the current hour.
type Analytics struct {
	Days     int
	From, To time.Time
	Total    store.TrafficCount
	store.TrafficReport
}

// Analytics reports ownerID's requests in scope over the last days days.
// A missing or foreign site, group or tag is ErrNotFound.
func (s *Service) Analytics(ctx context.Context, ownerID string, scope AnalyticsScope, days int) (Analytics, error) {
	if s.c.Traffic == nil {
		return Analytics{}, errors.New("analytics: no traffic rollup configured")
	}
	named := 0
	for _, v := range []string{scope.SiteName, scope.GroupID, scope.TagID} {
		if v != "" {
			named++
		}
	}
	if named > 1 || !slices.Contains(AnalyticsRanges, days) {
		return Analytics{}, fmt.Errorf("analytics: %w", ErrInvalidAnalytics)
	}
	q := store.TrafficScope{GroupID: scope.GroupID, TagID: scope.TagID}
	if scope.SiteName != "" {
		site, err := s.ownedSite(ctx, ownerID, scope.SiteName)
		if err != nil {
			return Analytics{}, err
		}
		q.SiteID = site.ID
	}
	to := s.c.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	from := to.Add(-time.Duration(days) * 24 * time.Hour)
	report, err := s.c.Traffic.Report(ctx, ownerID, q, from, to)
	if errors.Is(err, store.ErrNotFound) {
		return Analytics{}, fmt.Errorf("analytics: %w", ErrNotFound)
	}
	if err != nil {
		return Analytics{}, fmt.Errorf("analytics: %w", err)
	}
	out := Analytics{Days: days, From: from, To: to, TrafficReport: report}
	for _, h := range report.Hours {
		out.Total.Allowed += h.Allowed
		out.Total.Refused += h.Refused
	}
	return out, nil
}
