// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// Share-link operations (A2.2). Every handler takes the owner from the
// verified request context, never from the request.

// linkFailed maps an access error to its response, or returns it as is for
// 500 internal.
func linkFailed(err error) (*failure, error) {
	var te *access.TargetError
	target, message := "", ""
	if errors.As(err, &te) {
		target, message = te.Target, te.Message
	}
	switch {
	case errors.Is(err, access.ErrNotFound):
		return fail(http.StatusNotFound, "not_found", "Not found.", ""), nil
	case errors.Is(err, access.ErrInvalidPath), errors.Is(err, access.ErrInvalidLink):
		return fail(http.StatusBadRequest, "invalid_share_link", message, target), nil
	}
	return nil, err
}

func (s *Server) shareLinkBody(l store.ShareLink) ShareLink {
	out := ShareLink{
		Id:             l.ID,
		PublicId:       l.PublicID,
		Path:           l.PathPrefix,
		Status:         ShareLinkStatus(s.Access.Status(l)),
		Uses:           clampInt32(l.UseCount),
		CreatedAt:      l.CreatedAt.UnixMilli(),
		ExpiresAt:      optionalMillis(l.ExpiresAt),
		LastAccessedAt: optionalMillis(l.LastAccessedAt),
		RotatedAt:      optionalMillis(l.RotatedAt),
	}
	if l.Label != "" {
		out.Label = &l.Label
	}
	if l.MaxUses > 0 {
		n := clampInt32(l.MaxUses)
		out.MaxUses = &n
	}
	return out
}

// clampInt32 converts a non-negative count, saturating at math.MaxInt32.
func clampInt32(n int) int32 {
	if n >= 0 && n <= math.MaxInt32 {
		return int32(n)
	}
	return math.MaxInt32
}

// optionalMillis is millis, or nil (absent) for the zero time.
func optionalMillis(t time.Time) *Timestamp {
	if t.IsZero() {
		return nil
	}
	return millis(t)
}

// createdShareLink is a link with its token and URL, shown once.
func (s *Server) createdShareLink(site string, l store.ShareLink, token string) CreatedShareLink {
	base := strings.TrimSuffix(s.siteURL(s.Sites.Hostname(site)), "/")
	u := base + (&url.URL{Path: l.PathPrefix}).EscapedPath() + "?t=" + url.QueryEscape(token)
	return CreatedShareLink{ShareLink: s.shareLinkBody(l), Token: token, Url: u}
}

// ListShareLinks lists a site's unrevoked links, newest first.
func (s *Server) ListShareLinks(ctx context.Context, req ListShareLinksRequestObject) (ListShareLinksResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListShareLinksdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListShareLinksdefaultJSONResponse(*f), nil
	}
	before, beforeID, ok := linkCursor(req.Params.Cursor)
	if !ok {
		return ListShareLinksdefaultJSONResponse(*badCursor), nil
	}
	list, err := s.Access.ListLinks(ctx, owner, req.Site, before, beforeID, limit+1)
	if err != nil {
		f, err := linkFailed(err)
		if f == nil {
			return nil, err
		}
		return ListShareLinksdefaultJSONResponse(*f), nil
	}
	page := ListShareLinks200JSONResponse{Items: []ShareLink{}}
	if len(list) > limit {
		list = list[:limit]
		last := list[limit-1]
		page.NextCursor = encodeCursor(strconv.FormatInt(last.CreatedAt.UnixMilli(), 10) + ":" + last.ID)
	}
	for _, l := range list {
		page.Items = append(page.Items, s.shareLinkBody(l))
	}
	return page, nil
}

func linkCursor(c *Cursor) (time.Time, string, bool) {
	raw, ok := decodeCursor(c)
	if !ok || raw == "" {
		return time.Time{}, "", ok
	}
	at, id, found := strings.Cut(raw, ":")
	ms, err := strconv.ParseInt(at, 10, 64)
	if !found || err != nil || ms <= 0 || id == "" {
		return time.Time{}, "", false
	}
	return time.UnixMilli(ms).UTC(), id, true
}

// CreateShareLink creates a link and shows its token once.
func (s *Server) CreateShareLink(ctx context.Context, req CreateShareLinkRequestObject) (CreateShareLinkResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return CreateShareLinkdefaultJSONResponse(*unauthenticated), nil
	}
	var n access.NewLink
	if b := req.Body; b != nil {
		if b.Label != nil {
			n.Label = *b.Label
		}
		if b.Path != nil {
			n.PathPrefix = *b.Path
		}
		if b.ExpiresAt != nil {
			n.ExpiresAt = time.UnixMilli(*b.ExpiresAt).UTC()
		}
		if b.MaxUses != nil {
			if *b.MaxUses < 1 {
				return CreateShareLinkdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_share_link",
					"maxUses must be 1 to 1000000; omit it for unlimited uses.", "maxUses")), nil
			}
			n.MaxUses = int(*b.MaxUses)
		}
	}
	l, token, err := s.Access.CreateLink(ctx, owner, req.Site, n)
	if err != nil {
		f, err := linkFailed(err)
		if f == nil {
			return nil, err
		}
		return CreateShareLinkdefaultJSONResponse(*f), nil
	}
	return CreateShareLink201JSONResponse(s.createdShareLink(req.Site, l, token)), nil
}

// RevokeShareLink revokes a link by its public ID.
func (s *Server) RevokeShareLink(ctx context.Context, req RevokeShareLinkRequestObject) (RevokeShareLinkResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return RevokeShareLinkdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Access.RevokeLink(ctx, owner, req.Site, req.LinkId); err != nil {
		f, err := linkFailed(err)
		if f == nil {
			return nil, err
		}
		return RevokeShareLinkdefaultJSONResponse(*f), nil
	}
	return RevokeShareLink204Response{}, nil
}

// RotateShareLink gives a link a new token and shows it once.
func (s *Server) RotateShareLink(ctx context.Context, req RotateShareLinkRequestObject) (RotateShareLinkResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return RotateShareLinkdefaultJSONResponse(*unauthenticated), nil
	}
	l, token, err := s.Access.RotateLink(ctx, owner, req.Site, req.LinkId)
	if err != nil {
		f, err := linkFailed(err)
		if f == nil {
			return nil, err
		}
		return RotateShareLinkdefaultJSONResponse(*f), nil
	}
	return RotateShareLink200JSONResponse(s.createdShareLink(req.Site, l, token)), nil
}
