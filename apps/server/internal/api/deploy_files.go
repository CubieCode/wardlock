// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// Reading a deploy's files back (C1.14). Every handler takes the owner from
// the verified request context, never from the request.

// maxCursorPath is the longest path a deploy lists, so the longest
// ListDeployFiles cursor.
const maxCursorPath = 1024

// ListDeployFiles lists one page of a deploy's files, ordered by path.
func (s *Server) ListDeployFiles(ctx context.Context, req ListDeployFilesRequestObject) (ListDeployFilesResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return ListDeployFilesdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListDeployFilesdefaultJSONResponse(*f), nil
	}
	after, ok := decodeCursor(req.Params.Cursor)
	if !ok || len(after) > maxCursorPath {
		return ListDeployFilesdefaultJSONResponse(*badCursor), nil
	}
	files, err := s.Deploys.Files(ctx, actor, req.Site, req.DeployId, after, limit+1)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return ListDeployFilesdefaultJSONResponse(*f), nil
	}
	page := ListDeployFiles200JSONResponse{Items: []DeployFile{}}
	if len(files) > limit {
		files = files[:limit]
		page.NextCursor = encodeCursor(files[limit-1].Path)
	}
	for _, file := range files {
		page.Items = append(page.Items, DeployFile{Path: file.Path, Sha256: file.SHA256, Size: file.Size})
	}
	return page, nil
}

// DownloadDeployBlob sends one file body the deploy lists, with Range and
// If-None-Match support, as an attachment that never renders on the
// dashboard origin.
func (s *Server) DownloadDeployBlob(ctx context.Context, req DownloadDeployBlobRequestObject) (DownloadDeployBlobResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return DownloadDeployBlobdefaultJSONResponse(*unauthenticated), nil
	}
	r, ok := ctx.Value(downloadRequestKey{}).(*http.Request)
	if !ok {
		return nil, errNoDownloadRequest
	}
	body, _, err := s.Deploys.Open(ctx, actor, req.Site, req.DeployId, req.Sha256)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return DownloadDeployBlobdefaultJSONResponse(*f), nil
	}
	return blobDownload{r: r, body: body, sha256: req.Sha256}, nil
}

// downloadRequestKey carries the request into DownloadDeployBlob, which
// http.ServeContent needs for Range and conditional headers.
type downloadRequestKey struct{}

var errNoDownloadRequest = errors.New("download request missing from context")

// withDownloadRequest puts the request in DownloadDeployBlob's context.
func withDownloadRequest(next StrictHandlerFunc, operationID string) StrictHandlerFunc {
	if operationID != "DownloadDeployBlob" {
		return next
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		return next(context.WithValue(ctx, downloadRequestKey{}, r), w, r, request)
	}
}

// downloadCSP lets nothing in the body load, run or be framed, should a
// browser ever render it despite the attachment and content type.
const downloadCSP = "default-src 'none'; frame-ancestors 'none'; sandbox"

// blobDownload answers DownloadDeployBlob with the body through
// http.ServeContent: 200, 206 for ranges, 304 and 416.
type blobDownload struct {
	r      *http.Request
	body   io.ReadSeekCloser
	sha256 string
}

func (d blobDownload) VisitDownloadDeployBlobResponse(w http.ResponseWriter) error {
	defer func() { _ = d.body.Close() }()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", "attachment")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", downloadCSP)
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Cache-Control", "private, no-store")
	h.Set("ETag", `"`+d.sha256+`"`)
	http.ServeContent(w, d.r, "", time.Time{}, d.body)
	return nil
}
