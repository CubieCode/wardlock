// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
)

// Primary hostname operations (C1.18). The owner comes from the verified
// request context; a hostname that is not one of the site's answers the
// same 404 as a missing site.

// SetPrimaryHost makes one of the site's served hostnames its primary.
func (s *Server) SetPrimaryHost(ctx context.Context, req SetPrimaryHostRequestObject) (SetPrimaryHostResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return SetPrimaryHostdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil || req.Body.Host == "" {
		return SetPrimaryHostdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "host is required.", "host")), nil
	}
	site, err := s.Sites.SetPrimaryHost(ctx, actor, req.Site, req.Body.Host)
	if err != nil {
		f, err := domainFailed(err)
		if f == nil {
			return nil, err
		}
		return SetPrimaryHostdefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, actor, site)
	if err != nil {
		return nil, err
	}
	return SetPrimaryHost200JSONResponse(body), nil
}

// ClearPrimaryHost leaves the site with no primary hostname.
func (s *Server) ClearPrimaryHost(ctx context.Context, req ClearPrimaryHostRequestObject) (ClearPrimaryHostResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return ClearPrimaryHostdefaultJSONResponse(*unauthenticated), nil
	}
	site, err := s.Sites.ClearPrimaryHost(ctx, actor, req.Site)
	if err != nil {
		f, err := domainFailed(err)
		if f == nil {
			return nil, err
		}
		return ClearPrimaryHostdefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, actor, site)
	if err != nil {
		return nil, err
	}
	return ClearPrimaryHost200JSONResponse(body), nil
}
