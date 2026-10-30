// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

// Sites and deploys operations (C1.2). Every handler takes the owner from
// the verified request context, never from the request.

// failure is the shape of every generated default (any status) response.
type failure struct {
	Body       Error
	StatusCode int
}

func fail(status int, code, message, target string) *failure {
	f := &failure{Body: Error{Code: code, Message: message}, StatusCode: status}
	if target != "" {
		f.Body.Target = &target
	}
	return f
}

var unauthenticated = fail(http.StatusUnauthorized, "unauthenticated", "Sign in or provide an API token.", "")

// failed maps a domain error to its response. Anything unmapped is returned
// as is and answered 500 internal by responseError.
func failed(err error) (*failure, error) {
	var te *deploys.TargetError
	target, message := "", ""
	if errors.As(err, &te) {
		target, message = te.Target, te.Message
	}
	if ie := (*sites.InvalidError)(nil); errors.As(err, &ie) {
		target, message = ie.Target, ie.Message
	}
	if tm := (*sites.TooManyError)(nil); errors.As(err, &tm) {
		target, message = tm.Target, tm.Message
	}
	siteLimit := ""
	if le := (*sites.LimitError)(nil); errors.As(err, &le) {
		siteLimit = le.Message()
	}
	for _, m := range []struct {
		err     error
		status  int
		code    string
		message string
		target  string
	}{
		{sites.ErrNotFound, http.StatusNotFound, "not_found", "Not found.", ""},
		{sites.ErrInvalid, http.StatusBadRequest, "invalid_request", message, target},
		{sites.ErrGroupNameTaken, http.StatusConflict, "group_name_taken", "You already have a group with this name.", "name"},
		{sites.ErrTagNameTaken, http.StatusConflict, "tag_name_taken", "You already have a tag with this name.", "name"},
		{sites.ErrTooMany, http.StatusRequestEntityTooLarge, "limit_exceeded", message, target},
		{deploys.ErrNotFound, http.StatusNotFound, "not_found", "Not found.", ""},
		{sites.ErrInvalidName, http.StatusBadRequest, "invalid_site_name", "name must be 1-63 characters of a-z, 0-9 and '-', not starting or ending with '-' and without '--'.", "name"},
		{sites.ErrReservedName, http.StatusBadRequest, "reserved_site_name", "This site name is reserved.", "name"},
		{sites.ErrNameTaken, http.StatusConflict, "site_name_taken", "This site name is taken.", "name"},
		{deploys.ErrInvalidPath, http.StatusBadRequest, "invalid_path", message, target},
		{deploys.ErrInvalidManifest, http.StatusBadRequest, "invalid_manifest", message, target},
		{deploys.ErrInvalidConfig, http.StatusBadRequest, "invalid_config", message, target},
		{deploys.ErrInvalidPreview, http.StatusBadRequest, "invalid_preview_name", message, target},
		{deploys.ErrInvalidPreviewExpiry, http.StatusBadRequest, "invalid_request", message, target},
		{deploys.ErrPreviewNameTaken, http.StatusConflict, "site_name_taken", message, target},
		{deploys.ErrInvalidPreview, http.StatusBadRequest, "invalid_preview_name", message, target},
		{deploys.ErrInvalidPreviewExpiry, http.StatusBadRequest, "invalid_request", message, target},
		{deploys.ErrPreviewNameTaken, http.StatusConflict, "site_name_taken", message, target},
		{sites.ErrLimitExceeded, http.StatusRequestEntityTooLarge, "limit_exceeded", siteLimit, "name"},
		{deploys.ErrLimitExceeded, http.StatusRequestEntityTooLarge, "limit_exceeded", message, target},
		{deploys.ErrBlockedType, http.StatusUnprocessableEntity, "file_type_blocked", message, target},
		{deploys.ErrHashMismatch, http.StatusBadRequest, "hash_mismatch", "The body does not match the sha256 and size in the manifest.", ""},
		{deploys.ErrUnexpectedBlob, http.StatusBadRequest, "unexpected_blob", "This hash is not in the deploy's manifest.", ""},
		{deploys.ErrBlobsMissing, http.StatusConflict, "blobs_missing", "Some blobs have not been uploaded; upload every missing hash first.", target},
		{deploys.ErrNotPending, http.StatusConflict, "deploy_not_pending", "This deploy has been activated before; use rollback to return to it.", ""},
		{deploys.ErrNotActivated, http.StatusConflict, "deploy_not_activated", "This deploy was never activated.", ""},
		{deploys.ErrNoPreviousDeploy, http.StatusConflict, "no_previous_deploy", "The site has no earlier deploy to roll back to.", ""},
		{deploys.ErrStaleBase, http.StatusConflict, "stale_base", "Another deploy went live on this site after this path deploy was created; create it again (for a preview, deploy the preview again).", ""},
		{deploys.ErrWithheld, http.StatusUnavailableForLegalReasons, "file_withheld", "The operator of this server has withheld this file, so it cannot be downloaded.", ""},
		{deploys.ErrSiteUnavailable, http.StatusConflict, "site_unavailable", siteUnavailable, ""},
		{sites.ErrUnavailable, http.StatusConflict, "site_unavailable", siteUnavailable, ""},
	} {
		if errors.Is(err, m.err) {
			return fail(m.status, m.code, m.message, m.target), nil
		}
	}
	return nil, err
}

// siteUnavailable explains a site_unavailable refusal.
const siteUnavailable = "The operator of this server has taken this site down. Its deploys, domains and access records are kept as they are until the operator restores it."

const (
	defaultLimit = 50
	maxLimit     = 100
)

func pageLimit(limit *Limit) (int, *failure) {
	if limit == nil {
		return defaultLimit, nil
	}
	if *limit < 1 || *limit > maxLimit {
		return 0, fail(http.StatusBadRequest, "invalid_request", fmt.Sprintf("limit must be 1 to %d.", maxLimit), "limit")
	}
	return int(*limit), nil
}

var badCursor = fail(http.StatusBadRequest, "invalid_request", "cursor is not one this server issued.", "cursor")

func encodeCursor(s string) *Cursor {
	c := base64.RawURLEncoding.EncodeToString([]byte(s))
	return &c
}

func decodeCursor(c *Cursor) (string, bool) {
	if c == nil {
		return "", true
	}
	raw, err := base64.RawURLEncoding.DecodeString(*c)
	return string(raw), err == nil && len(raw) > 0
}

// siteBody answers site with tagIDs, its tags in tag-name order.
func (s *Server) siteBody(site store.Site, tagIDs []string) Site {
	host := s.Sites.Hostname(site.Name)
	if tagIDs == nil {
		tagIDs = []string{}
	}
	out := Site{
		Id:        site.ID,
		Name:      site.Name,
		Hostname:  host,
		Url:       s.siteURL(host),
		CreatedAt: site.CreatedAt.UnixMilli(),
		// Owners see that their site is down, never why (A2.13).
		Unavailable: !site.TakenDownAt.IsZero(),
		TagIds:      tagIDs,
		Pinned:      !site.PinnedAt.IsZero(),
	}
	if site.GroupID != "" {
		out.GroupId = &site.GroupID
	}
	if site.ActiveDeployID != "" {
		out.ActiveDeployId = &site.ActiveDeployID
		out.ActivatedAt = millis(site.ActivatedAt)
	}
	if primary := s.Sites.PrimaryHost(site); primary != "" {
		out.PrimaryHost = &primary
	}
	return out
}

func (s *Server) siteURL(host string) string {
	if s.SiteURL != nil {
		return s.SiteURL(host)
	}
	return "https://" + host + "/"
}

func deployBody(site store.Site, d store.Deploy) Deploy {
	out := Deploy{
		Id:              d.ID,
		Status:          DeployStatus(deploys.StatusOf(site, d)),
		CreatedAt:       d.CreatedAt.UnixMilli(),
		FileCount:       math.MaxInt32, // the file limit keeps real counts far below
		TotalSize:       d.TotalSize,
		Spa:             d.SPA,
		ImmutableAssets: d.ImmutableAssets,
	}
	if d.FileCount >= 0 && d.FileCount <= math.MaxInt32 {
		out.FileCount = int32(d.FileCount)
	}
	if d.Message != "" {
		out.Message = &d.Message
	}
	if d.PathPrefix != "" {
		out.Path = &d.PathPrefix
	}
	if len(d.Redirects) > 0 {
		redirects := make([]Redirect, len(d.Redirects))
		for i, r := range d.Redirects {
			status := RedirectStatus(r.Status)
			redirects[i] = Redirect{From: r.From, To: r.To, Status: &status}
		}
		out.Redirects = &redirects
	}
	if len(d.Headers) > 0 {
		headers := make([]HeaderRule, len(d.Headers))
		for i, h := range d.Headers {
			headers[i] = HeaderRule{Path: h.Path, Set: h.Set}
		}
		out.Headers = &headers
	}
	if !d.ActivatedAt.IsZero() {
		out.ActivatedAt = millis(d.ActivatedAt)
	}
	if d.Preview != "" {
		out.Preview = &d.Preview
	}
	return out
}

func millis(t time.Time) *Timestamp {
	ms := t.UnixMilli()
	return &ms
}

// ListSites lists the owner's sites by name, or the one at a hostname.
func (s *Server) ListSites(ctx context.Context, req ListSitesRequestObject) (ListSitesResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListSitesdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Params.Hostname != nil {
		site, err := s.Sites.ForHostname(ctx, owner, *req.Params.Hostname)
		if errors.Is(err, sites.ErrNotFound) {
			return ListSites200JSONResponse{Items: []Site{}}, nil
		}
		if err != nil {
			return nil, err
		}
		body, err := s.siteBodyWithTags(ctx, owner, site)
		if err != nil {
			return nil, err
		}
		return ListSites200JSONResponse{Items: []Site{body}}, nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListSitesdefaultJSONResponse(*f), nil
	}
	after, ok := decodeCursor(req.Params.Cursor)
	if !ok || (after != "" && !sites.ValidName(after)) {
		return ListSitesdefaultJSONResponse(*badCursor), nil
	}
	list, err := s.Sites.List(ctx, owner, after, limit+1)
	if err != nil {
		return nil, err
	}
	page := ListSites200JSONResponse{Items: []Site{}}
	if len(list) > limit {
		list = list[:limit]
		page.NextCursor = encodeCursor(list[limit-1].Name)
	}
	tags, err := s.Sites.TagIDs(ctx, owner, list...)
	if err != nil {
		return nil, err
	}
	for _, site := range list {
		page.Items = append(page.Items, s.siteBody(site, tags[site.ID]))
	}
	return page, nil
}

// CreateSite creates an empty site.
func (s *Server) CreateSite(ctx context.Context, req CreateSiteRequestObject) (CreateSiteResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return CreateSitedefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return CreateSitedefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "name is required.", "name")), nil
	}
	site, err := s.Sites.Create(ctx, owner, req.Body.Name)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return CreateSitedefaultJSONResponse(*f), nil
	}
	return CreateSite201JSONResponse(s.siteBody(site, nil)), nil
}

// GetSite returns one of the owner's sites.
func (s *Server) GetSite(ctx context.Context, req GetSiteRequestObject) (GetSiteResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return GetSitedefaultJSONResponse(*unauthenticated), nil
	}
	site, err := s.Sites.Get(ctx, owner, req.Site)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return GetSitedefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, owner, site)
	if err != nil {
		return nil, err
	}
	return GetSite200JSONResponse(body), nil
}

// DeleteSite tears a site down.
func (s *Server) DeleteSite(ctx context.Context, req DeleteSiteRequestObject) (DeleteSiteResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return DeleteSitedefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Sites.Delete(ctx, owner, req.Site); err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return DeleteSitedefaultJSONResponse(*f), nil
	}
	return DeleteSite204Response{}, nil
}

// ListDeploys lists a site's deploys, newest first.
func (s *Server) ListDeploys(ctx context.Context, req ListDeploysRequestObject) (ListDeploysResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListDeploysdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListDeploysdefaultJSONResponse(*f), nil
	}
	before, ok := deployCursor(req.Params.Cursor)
	if !ok {
		return ListDeploysdefaultJSONResponse(*badCursor), nil
	}
	site, list, err := s.Deploys.List(ctx, owner, req.Site, before, limit+1)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return ListDeploysdefaultJSONResponse(*f), nil
	}
	page := ListDeploys200JSONResponse{Items: []Deploy{}}
	if len(list) > limit {
		list = list[:limit]
		last := list[limit-1]
		page.NextCursor = encodeCursor(strconv.FormatInt(last.CreatedAt.UnixMilli(), 10) + ":" + last.ID)
	}
	for _, d := range list {
		page.Items = append(page.Items, deployBody(site, d))
	}
	return page, nil
}

// deployCursor decodes a ListDeploys cursor: "<created at ms>:<id>".
func deployCursor(c *Cursor) (store.DeployCursor, bool) {
	raw, ok := decodeCursor(c)
	if !ok || raw == "" {
		return store.DeployCursor{}, ok
	}
	at, id, found := strings.Cut(raw, ":")
	ms, err := strconv.ParseInt(at, 10, 64)
	if !found || err != nil || ms <= 0 || id == "" {
		return store.DeployCursor{}, false
	}
	return store.DeployCursor{CreatedAt: time.UnixMilli(ms).UTC(), ID: id}, true
}

// CreateDeploy records a manifest and answers with the hashes to upload.
func (s *Server) CreateDeploy(ctx context.Context, req CreateDeployRequestObject) (CreateDeployResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return CreateDeploydefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return CreateDeploydefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "files is required.", "files")), nil
	}
	m := deploys.Manifest{Files: make([]store.DeployFile, 0, len(req.Body.Files))}
	for _, f := range req.Body.Files {
		m.Files = append(m.Files, store.DeployFile{Path: f.Path, SHA256: f.Sha256, Size: f.Size})
	}
	if req.Body.Message != nil {
		m.Message = *req.Body.Message
	}
	m.SPA, m.ImmutableAssets = req.Body.Spa, req.Body.ImmutableAssets
	if req.Body.Redirects != nil {
		redirects := make([]store.Redirect, len(*req.Body.Redirects))
		for i, r := range *req.Body.Redirects {
			redirects[i] = store.Redirect{From: r.From, To: r.To}
			if r.Status != nil {
				redirects[i].Status = int(*r.Status)
			}
		}
		m.Redirects = &redirects
	}
	if req.Body.Headers != nil {
		headers := make([]store.HeaderRule, len(*req.Body.Headers))
		for i, h := range *req.Body.Headers {
			headers[i] = store.HeaderRule{Path: h.Path, Set: h.Set}
		}
		m.Headers = &headers
	}
	if req.Body.Path != nil {
		if *req.Body.Path == "" {
			return CreateDeploydefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_path", "path must be a prefix such as /mockup-x/; leave it out to deploy the whole site.", "path")), nil
		}
		m.Path = *req.Body.Path
	}
	if req.Body.Preview != nil {
		m.Preview = *req.Body.Preview
	}
	if req.Body.PreviewExpires != nil {
		days, ok := previewDays(*req.Body.PreviewExpires)
		if !ok {
			return CreateDeploydefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("previewExpires must be 1d to %dd, or never.", store.MaxPreviewDays), "previewExpires")), nil
		}
		m.PreviewDays = &days
	}
	d, missing, err := s.Deploys.Create(ctx, owner, req.Site, m)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return CreateDeploydefaultJSONResponse(*f), nil
	}
	return CreateDeploy201JSONResponse{
		Deploy:  deployBody(store.Site{}, d),
		Missing: append([]Sha256{}, missing...),
	}, nil
}

// UploadDeployBlob stores one file body listed in a deploy's manifest.
func (s *Server) UploadDeployBlob(ctx context.Context, req UploadDeployBlobRequestObject) (UploadDeployBlobResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return UploadDeployBlobdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Deploys.Upload(ctx, owner, req.Site, req.DeployId, req.Sha256, req.Body); err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return UploadDeployBlobdefaultJSONResponse(*f), nil
	}
	return UploadDeployBlob204Response{}, nil
}

// ActivateDeploy makes a pending deploy the site's active deploy.
func (s *Server) ActivateDeploy(ctx context.Context, req ActivateDeployRequestObject) (ActivateDeployResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ActivateDeploydefaultJSONResponse(*unauthenticated), nil
	}
	site, d, err := s.Deploys.Activate(ctx, owner, req.Site, req.DeployId)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return ActivateDeploydefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, owner, site)
	if err != nil {
		return nil, err
	}
	out := ActivateDeploy200JSONResponse{Site: body, Deploy: deployBody(site, d)}
	if d.Preview != "" {
		_, p, err := s.Deploys.Preview(ctx, owner, req.Site, d.Preview)
		if err != nil {
			return nil, err
		}
		pb := s.previewBody(site, p, d)
		out.Preview = &pb
	}
	return out, nil
}

// RollbackSite reactivates an earlier deploy.
func (s *Server) RollbackSite(ctx context.Context, req RollbackSiteRequestObject) (RollbackSiteResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return RollbackSitedefaultJSONResponse(*unauthenticated), nil
	}
	deployID := ""
	if req.Body != nil && req.Body.DeployId != nil {
		deployID = *req.Body.DeployId
	}
	site, d, err := s.Deploys.Rollback(ctx, owner, req.Site, deployID)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return RollbackSitedefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, owner, site)
	if err != nil {
		return nil, err
	}
	return RollbackSite200JSONResponse{Site: body, Deploy: deployBody(site, d)}, nil
}
