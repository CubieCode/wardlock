// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"io"
	"net/http"
	"strconv"
)

// renditionTypes are the ?format= values (SPEC "AI-readable renditions")
// with their content types, in the order HTML responses link to them.
var renditionTypes = []struct{ format, ctype, linkType string }{
	{"md", "text/markdown; charset=utf-8", "text/markdown"},
	{"txt", "text/plain; charset=utf-8", "text/plain"},
}

// rendition returns the rendition of f that r asks for with ?format=, if
// it has one. Any other format value, and a file without renditions,
// serves the file itself.
func rendition(r *http.Request, f ServedFile) (Rendition, string, bool) {
	if len(f.Renditions) == 0 {
		return Rendition{}, "", false
	}
	format := r.URL.Query().Get("format")
	for _, t := range renditionTypes {
		if t.format == format {
			rd, ok := f.Renditions[format]
			return rd, t.ctype, ok
		}
	}
	return Rendition{}, "", false
}

// alternateLinks advertises f's renditions on its HTML response. The link
// is the request path alone: no other query parameter, so a share-link
// token in the request never reaches it.
func alternateLinks(h http.Header, r *http.Request, f ServedFile) {
	for _, t := range renditionTypes {
		if _, ok := f.Renditions[t.format]; ok {
			h.Add("Link", "<"+r.URL.EscapedPath()+"?format="+t.format+`>; rel="alternate"; type="`+t.linkType+`"`)
		}
	}
}

// serveRendition writes a rendition with status, as serveFile writes a
// file: the same caching, and conditional and range requests on 200s. It
// is never precompressed.
func (e *Edge) serveRendition(w http.ResponseWriter, r *http.Request, s *served, rd Rendition, ctype string, status int, protected bool) {
	e.serveBlob(w, r, s, rd.SHA256, rd.Size, "open rendition", func(w http.ResponseWriter, blob io.ReadSeeker) {
		h := w.Header()
		h.Set("Cache-Control", cacheControl(protected))
		h.Set("Content-Type", ctype)
		h.Set("Content-Length", strconv.FormatInt(rd.Size, 10))
		if status != http.StatusOK {
			w.WriteHeader(status)
			if r.Method != http.MethodHead {
				_, _ = io.CopyN(w, blob, rd.Size)
			}
			return
		}
		h.Set("ETag", `"`+rd.SHA256+`"`)
		http.ServeContent(w, r, "", s.site.ActivatedAt, blob)
	})
}
