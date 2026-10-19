// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"net/http"

	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

// Custom domain operations (C1.9). Every handler takes the owner from the
// verified request context, never from the request.

// domainFailed maps a sites error to its response, or returns it as is for
// 500 internal.
func domainFailed(err error) (*failure, error) {
	if he, ok := errors.AsType[*sites.HostnameError](err); ok {
		return fail(http.StatusBadRequest, "invalid_hostname", he.Reason, "hostname"), nil
	}
	switch {
	case errors.Is(err, sites.ErrNotFound):
		return fail(http.StatusNotFound, "not_found", "Not found.", ""), nil
	case errors.Is(err, sites.ErrDomainTaken):
		// Never which site, or whose.
		return fail(http.StatusConflict, "domain_taken", "This hostname is already attached to a site on this server.", "hostname"), nil
	}
	return nil, err
}

func (s *Server) domainBody(siteName string, d store.Domain) Domain {
	out := Domain{
		Id:       d.ID,
		Hostname: d.Hostname,
		Status:   DomainStatus(d.Status),
		RoutingRecord: DnsRecord{
			Type: DnsRecordTypeCNAME, Name: d.Hostname, Value: s.Sites.RoutingTarget(siteName),
		},
		VerificationRecord: DnsRecord{
			Type: DnsRecordTypeTXT, Name: sites.TXTName(d.Hostname), Value: sites.TXTPrefix + d.VerificationToken,
		},
		CreatedAt:     d.CreatedAt.UnixMilli(),
		VerifiedAt:    optionalMillis(d.VerifiedAt),
		LastCheckedAt: optionalMillis(d.LastCheckedAt),
		FailingSince:  optionalMillis(d.FailingSince),
	}
	if at, ok := sites.NextCheck(d); ok {
		out.NextCheckAt = millis(at)
	}
	if d.LastFailureReason != "" {
		reason := DomainFailureReason(d.LastFailureReason)
		out.FailureReason = &reason
	}
	if d.FailureDetail != "" {
		out.FailureDetail = &d.FailureDetail
	}
	return out
}

// ListDomains lists a site's custom domains by hostname.
func (s *Server) ListDomains(ctx context.Context, req ListDomainsRequestObject) (ListDomainsResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListDomainsdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListDomainsdefaultJSONResponse(*f), nil
	}
	after, ok := decodeCursor(req.Params.Cursor)
	if h, err := sites.NormaliseHostname(after); !ok || (after != "" && (err != nil || h != after)) {
		return ListDomainsdefaultJSONResponse(*badCursor), nil
	}
	list, err := s.Sites.ListDomains(ctx, owner, req.Site, after, limit+1)
	if err != nil {
		f, err := domainFailed(err)
		if f == nil {
			return nil, err
		}
		return ListDomainsdefaultJSONResponse(*f), nil
	}
	page := ListDomains200JSONResponse{Items: []Domain{}}
	if len(list) > limit {
		list = list[:limit]
		page.NextCursor = encodeCursor(list[limit-1].Hostname)
	}
	for _, d := range list {
		page.Items = append(page.Items, s.domainBody(req.Site, d))
	}
	return page, nil
}

// AddDomain attaches a custom domain and checks it straight away.
func (s *Server) AddDomain(ctx context.Context, req AddDomainRequestObject) (AddDomainResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return AddDomaindefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return AddDomaindefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "hostname is required.", "hostname")), nil
	}
	d, err := s.Sites.AddDomain(ctx, owner, req.Site, req.Body.Hostname)
	if err != nil {
		f, err := domainFailed(err)
		if f == nil {
			return nil, err
		}
		return AddDomaindefaultJSONResponse(*f), nil
	}
	return AddDomain201JSONResponse(s.domainBody(req.Site, d)), nil
}

// GetDomain returns one of a site's custom domains.
func (s *Server) GetDomain(ctx context.Context, req GetDomainRequestObject) (GetDomainResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return GetDomaindefaultJSONResponse(*unauthenticated), nil
	}
	d, err := s.Sites.GetDomain(ctx, owner, req.Site, req.Hostname)
	if err != nil {
		f, err := domainFailed(err)
		if f == nil {
			return nil, err
		}
		return GetDomaindefaultJSONResponse(*f), nil
	}
	return GetDomain200JSONResponse(s.domainBody(req.Site, d)), nil
}

// VerifyDomain checks a custom domain's DNS now.
func (s *Server) VerifyDomain(ctx context.Context, req VerifyDomainRequestObject) (VerifyDomainResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return VerifyDomaindefaultJSONResponse(*unauthenticated), nil
	}
	d, err := s.Sites.VerifyDomain(ctx, owner, req.Site, req.Hostname)
	if soon, ok := errors.AsType[*sites.CheckTooSoonError](err); ok {
		return VerifyDomain429JSONResponse{CheckedRecentlyJSONResponse{
			Body:    Error{Code: "checked_recently", Message: "This domain was checked moments ago. Try again shortly."},
			Headers: CheckedRecentlyResponseHeaders{RetryAfter: retryAfter(soon.RetryAfter)},
		}}, nil
	}
	if err != nil {
		f, err := domainFailed(err)
		if f == nil {
			return nil, err
		}
		return VerifyDomaindefaultJSONResponse(*f), nil
	}
	return VerifyDomain200JSONResponse(s.domainBody(req.Site, d)), nil
}

// RemoveDomain detaches a custom domain.
func (s *Server) RemoveDomain(ctx context.Context, req RemoveDomainRequestObject) (RemoveDomainResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return RemoveDomaindefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Sites.RemoveDomain(ctx, owner, req.Site, req.Hostname); err != nil {
		f, err := domainFailed(err)
		if f == nil {
			return nil, err
		}
		return RemoveDomaindefaultJSONResponse(*f), nil
	}
	return RemoveDomain204Response{}, nil
}
