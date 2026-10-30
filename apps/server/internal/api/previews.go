// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// Preview operations (C1.19). The owner comes from the verified request
// context; a missing or expired preview, and another owner's, answer the
// same 404.

// previewDays parses previewExpires: "<days>d", 1 to store.MaxPreviewDays,
// or "never" (0).
func previewDays(v string) (int, bool) {
	if v == "never" {
		return 0, true
	}
	n, ok := strings.CutSuffix(v, "d")
	days, err := strconv.Atoi(n)
	if !ok || err != nil || strconv.Itoa(days) != n || days < 1 || days > store.MaxPreviewDays {
		return 0, false
	}
	return days, true
}

// previewBody answers preview p of site, serving d.
func (s *Server) previewBody(site store.Site, p store.Preview, d store.Deploy) Preview {
	host := s.Sites.Hostname(deploys.PreviewLabel(site.Name, p.Name))
	out := Preview{
		Name:       p.Name,
		Hostname:   host,
		Url:        s.siteURL(host),
		CreatedAt:  p.CreatedAt.UnixMilli(),
		DeployedAt: p.DeployedAt.UnixMilli(),
		Deploy:     deployBody(site, d),
	}
	if !p.ExpiresAt.IsZero() {
		out.ExpiresAt = millis(p.ExpiresAt)
	}
	return out
}

// ListPreviews lists a site's previews by name.
func (s *Server) ListPreviews(ctx context.Context, req ListPreviewsRequestObject) (ListPreviewsResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListPreviewsdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListPreviewsdefaultJSONResponse(*f), nil
	}
	after, ok := decodeCursor(req.Params.Cursor)
	if !ok || (after != "" && !deploys.ValidPreviewName(after)) {
		return ListPreviewsdefaultJSONResponse(*badCursor), nil
	}
	site, list, err := s.Deploys.Previews(ctx, owner, req.Site, after, limit+1)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return ListPreviewsdefaultJSONResponse(*f), nil
	}
	page := ListPreviews200JSONResponse{Items: []Preview{}}
	if len(list) > limit {
		list = list[:limit]
		page.NextCursor = encodeCursor(list[limit-1].Name)
	}
	for _, p := range list {
		d, err := s.Deploys.Deploy(ctx, owner, site, p.DeployID)
		if errors.Is(err, deploys.ErrNotFound) {
			continue // deleted with its preview since the listing
		}
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, s.previewBody(site, p, d))
	}
	return page, nil
}

// DeletePreview removes one of the site's previews.
func (s *Server) DeletePreview(ctx context.Context, req DeletePreviewRequestObject) (DeletePreviewResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return DeletePreviewdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Deploys.DeletePreview(ctx, owner, req.Site, req.Preview); err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return DeletePreviewdefaultJSONResponse(*f), nil
	}
	return DeletePreview204Response{}, nil
}

// PromotePreview activates a preview's deploy on the live site.
func (s *Server) PromotePreview(ctx context.Context, req PromotePreviewRequestObject) (PromotePreviewResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return PromotePreviewdefaultJSONResponse(*unauthenticated), nil
	}
	site, d, p, err := s.Deploys.Promote(ctx, owner, req.Site, req.Preview)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return PromotePreviewdefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, owner, site)
	if err != nil {
		return nil, err
	}
	pb := s.previewBody(site, p, d)
	return PromotePreview200JSONResponse{Site: body, Deploy: deployBody(site, d), Preview: &pb}, nil
}
