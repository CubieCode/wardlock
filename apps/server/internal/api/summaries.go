// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

var (
	badSummaryView = fail(http.StatusBadRequest, "invalid_request", "view must be pinned, expiring, public, quiet, untagged or down.", "view")
	badSummarySort = fail(http.StatusBadRequest, "invalid_request", "sort must be deploy, name, requests or links.", "sort")
)

// ListSiteSummaries answers a page of the owner's sites with what the
// dashboard's lists show of each (V5.5). The owner comes from the verified
// request context; a missing or another owner's group or tag in a filter
// answers the same 404.
func (s *Server) ListSiteSummaries(ctx context.Context, req ListSiteSummariesRequestObject) (ListSiteSummariesResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListSiteSummariesdefaultJSONResponse(*unauthenticated), nil
	}
	p := req.Params
	limit, f := pageLimit(p.Limit)
	if f != nil {
		return ListSiteSummariesdefaultJSONResponse(*f), nil
	}
	sort := sites.SortDeploy
	if p.Sort != nil {
		sort = sites.SummarySort(*p.Sort)
		if !slices.Contains([]sites.SummarySort{sites.SortDeploy, sites.SortName, sites.SortRequests, sites.SortLinks}, sort) {
			return ListSiteSummariesdefaultJSONResponse(*badSummarySort), nil
		}
	}
	filter := sites.SummaryFilter{TagID: deref(p.Tag), Query: deref(p.Q)}
	if g := deref(p.Group); g == "none" {
		filter.NoGroup = true
	} else {
		filter.GroupID = g
	}
	if p.View != nil {
		filter.View = sites.View(*p.View)
		if !slices.Contains(sites.Views, filter.View) {
			return ListSiteSummariesdefaultJSONResponse(*badSummaryView), nil
		}
	}
	var after *sites.SummaryCursor
	if p.Cursor != nil {
		c, ok := decodeSummaryCursor(p.Cursor, sort)
		if !ok {
			return ListSiteSummariesdefaultJSONResponse(*badCursor), nil
		}
		after = &c
	}
	page, err := s.Sites.SiteSummaries(ctx, owner, filter, sort, after, limit)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return ListSiteSummariesdefaultJSONResponse(*f), nil
	}
	out := ListSiteSummaries200JSONResponse{Items: make([]SiteSummary, 0, len(page.Items))}
	for _, site := range page.Items {
		out.Items = append(out.Items, s.summaryBody(site))
	}
	if page.Next != nil {
		out.NextCursor = encodeCursor(fmt.Sprintf("%s:%d:%s", sort, page.Next.Key, page.Next.Name))
	}
	return out, nil
}

// decodeSummaryCursor reads a cursor ListSiteSummaries issued for sort.
func decodeSummaryCursor(c *Cursor, sort sites.SummarySort) (sites.SummaryCursor, bool) {
	raw, ok := decodeCursor(c)
	if !ok {
		return sites.SummaryCursor{}, false
	}
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] != string(sort) || !sites.ValidName(parts[2]) {
		return sites.SummaryCursor{}, false
	}
	key, err := strconv.ParseInt(parts[1], 10, 64)
	return sites.SummaryCursor{Key: key, Name: parts[2]}, err == nil
}

func (s *Server) summaryBody(site store.SiteSummary) SiteSummary {
	host := s.Sites.Hostname(site.Name)
	out := SiteSummary{
		Id:            site.ID,
		Name:          site.Name,
		Hostname:      host,
		Url:           s.siteURL(host),
		CreatedAt:     site.CreatedAt.UnixMilli(),
		Unavailable:   sites.Down(site),
		TagIds:        site.TagIDs,
		Pinned:        !site.PinnedAt.IsZero(),
		Visibility:    PolicyVisibility(site.Default.Visibility),
		Methods:       policyBody(site.Default).Methods,
		WorkingLinks:  count32(site.WorkingLinks),
		ExpiringLinks: count32(site.ExpiringLinks),
		Requests:      site.Requests,
		Quiet:         !site.Active,
	}
	if out.TagIds == nil {
		out.TagIds = []string{}
	}
	if site.GroupID != "" {
		out.GroupId = &site.GroupID
	}
	if site.ActiveDeployID != "" {
		out.LiveDeploy = &LiveDeploy{
			Id:          site.ActiveDeployID,
			ShortId:     site.ActiveDeployID[max(0, len(site.ActiveDeployID)-8):],
			ActivatedAt: site.ActivatedAt.UnixMilli(),
		}
		if site.DeployMessage != "" {
			out.LiveDeploy.Message = &site.DeployMessage
		}
	}
	return out
}

// GetSiteCounts counts the owner's sites in each saved view, group and
// tag (V5.5).
func (s *Server) GetSiteCounts(ctx context.Context, _ GetSiteCountsRequestObject) (GetSiteCountsResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return GetSiteCountsdefaultJSONResponse(*unauthenticated), nil
	}
	c, err := s.Sites.SiteCounts(ctx, owner)
	if err != nil {
		return nil, err
	}
	n := func(v int) SiteCount { return count32(v) }
	out := GetSiteCounts200JSONResponse{
		All: n(c.All),
		Views: SiteViewCounts{
			Pinned: n(c.Views[sites.ViewPinned]), Expiring: n(c.Views[sites.ViewExpiring]), Public: n(c.Views[sites.ViewPublic]),
			Quiet: n(c.Views[sites.ViewQuiet]), Untagged: n(c.Views[sites.ViewUntagged]), Down: n(c.Views[sites.ViewDown]),
		},
		Groups:  make([]GroupSiteCount, 0, len(c.Groups)),
		NoGroup: n(c.NoGroup),
		Tags:    make([]TagSiteCount, 0, len(c.Tags)),
	}
	for _, g := range c.Groups {
		out.Groups = append(out.Groups, GroupSiteCount{GroupId: g.GroupID, Sites: n(g.Sites)})
	}
	for _, t := range c.Tags {
		out.Tags = append(out.Tags, TagSiteCount{TagId: t.TagID, Sites: n(t.Sites)})
	}
	return out, nil
}

// count32 is n as the API's int32 counts carry it, capped at MaxInt32.
func count32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n)
}
