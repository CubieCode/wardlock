// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"wardlock.nz/server/internal/access"
)

const defaultAnalyticsDays = 7

var (
	badAnalyticsDays  = fail(http.StatusBadRequest, "invalid_request", "days must be 7, 30 or 90.", "days")
	badAnalyticsScope = fail(http.StatusBadRequest, "invalid_request", "Give at most one of site, group and tag.", "")
)

// GetAnalytics reports the owner's requests from the traffic rollup
// (V5.4). The owner comes from the verified request context; a missing or
// another owner's site, group or tag answers the same 404.
func (s *Server) GetAnalytics(ctx context.Context, req GetAnalyticsRequestObject) (GetAnalyticsResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return GetAnalyticsdefaultJSONResponse(*unauthenticated), nil
	}
	days := AnalyticsDays(defaultAnalyticsDays)
	if req.Params.Days != nil {
		days = *req.Params.Days
	}
	if !slices.Contains(access.AnalyticsRanges, int(days)) {
		return GetAnalyticsdefaultJSONResponse(*badAnalyticsDays), nil
	}
	scope := access.AnalyticsScope{SiteName: deref(req.Params.Site), GroupID: deref(req.Params.Group), TagID: deref(req.Params.Tag)}
	a, err := s.Access.Analytics(ctx, owner, scope, int(days))
	switch {
	case errors.Is(err, access.ErrInvalidAnalytics):
		return GetAnalyticsdefaultJSONResponse(*badAnalyticsScope), nil
	case errors.Is(err, access.ErrNotFound):
		return GetAnalyticsdefaultJSONResponse(*fail(http.StatusNotFound, "not_found", "Not found.", "")), nil
	case err != nil:
		return nil, err
	}
	return GetAnalytics200JSONResponse(analyticsBody(days, a, s.Countries)), nil
}

// analyticsBody answers a report; countries, and the lookup's credit,
// only while the country lookup is on.
func analyticsBody(days AnalyticsDays, a access.Analytics, countries *CountryLookupConfig) Analytics {
	out := Analytics{
		Days:       days,
		From:       a.From.UnixMilli(),
		To:         a.To.UnixMilli(),
		Total:      RequestCount{Allowed: a.Total.Allowed, Refused: a.Total.Refused},
		OtherPaths: RequestCount{Allowed: a.OtherPaths.Allowed, Refused: a.OtherPaths.Refused},
		Hours:      make([]AnalyticsHour, 0, len(a.Hours)),
		Via:        make([]AnalyticsVia, 0, len(a.Via)),
		Sites:      make([]AnalyticsSite, 0, len(a.Sites)),
		Groups:     make([]AnalyticsGroup, 0, len(a.Groups)),
		Tags:       make([]AnalyticsTag, 0, len(a.Tags)),
		Links:      make([]AnalyticsLink, 0, len(a.Links)),
		Paths:      make([]AnalyticsPath, 0, len(a.Paths)),
		Browsers:   make([]AnalyticsBrowser, 0, len(a.Browsers)),
	}
	for _, h := range a.Hours {
		out.Hours = append(out.Hours, AnalyticsHour{Hour: h.Hour.UnixMilli(), Allowed: h.Allowed, Refused: h.Refused})
	}
	for _, v := range a.Via {
		out.Via = append(out.Via, AnalyticsVia{Via: AccessVia(v.Via), Allowed: v.Allowed, Refused: v.Refused})
	}
	for _, site := range a.Sites {
		out.Sites = append(out.Sites, AnalyticsSite{Site: site.SiteName, Allowed: site.Allowed, Refused: site.Refused})
	}
	for _, g := range a.Groups {
		item := AnalyticsGroup{Allowed: g.Allowed, Refused: g.Refused}
		if g.GroupID != "" {
			item.GroupId = &g.GroupID
		}
		out.Groups = append(out.Groups, item)
	}
	for _, t := range a.Tags {
		out.Tags = append(out.Tags, AnalyticsTag{TagId: t.TagID, Allowed: t.Allowed, Refused: t.Refused})
	}
	for _, l := range a.Links {
		item := AnalyticsLink{Site: l.SiteName, PublicId: l.PublicID, Allowed: l.Allowed, Refused: l.Refused}
		if l.Label != "" {
			item.Label = &l.Label
		}
		out.Links = append(out.Links, item)
	}
	for _, p := range a.Paths {
		out.Paths = append(out.Paths, AnalyticsPath{Site: p.SiteName, Path: p.Path, Allowed: p.Allowed, Refused: p.Refused})
	}
	for _, b := range a.Browsers {
		out.Browsers = append(out.Browsers, AnalyticsBrowser{Browser: BrowserFamily(b.Browser), Allowed: b.Allowed, Refused: b.Refused})
	}
	if countries != nil {
		list := make([]AnalyticsCountry, 0, len(a.Countries))
		for _, c := range a.Countries {
			list = append(list, AnalyticsCountry{Country: c.Country, Allowed: c.Allowed, Refused: c.Refused})
		}
		out.Countries = &list
		out.CountryLookup = &CountryLookup{Attribution: countries.Attribution}
		if countries.ServerCountry != "" {
			out.CountryLookup.ServerCountry = &countries.ServerCountry
		}
	}
	return out
}
