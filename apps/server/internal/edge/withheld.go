// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"io"
	"net/http"
	"strings"
)

// withheld reports whether f must not be served because content screening
// quarantined it (A2.15). A precompressed sibling (page.html.gz) is
// withheld with the file it compresses, since it is the same content in
// bytes screening never saw.
func (e *Edge) withheld(s *served, f ServedFile) bool {
	if e.cfg.Withheld == nil {
		return false
	}
	if e.cfg.Withheld.Has(f.SHA256) {
		return true
	}
	for _, enc := range encodings {
		if base, ok := strings.CutSuffix(f.Path, enc.suffix); ok {
			if orig, ok := s.files[base]; ok && e.cfg.Withheld.Has(orig.SHA256) {
				return true
			}
		}
	}
	return false
}

// withheldFile answers a request that would serve a quarantined file with
// 451, whatever the method, range or validators: an HTML page gets the
// neutral withheld page, anything else a line of plain text, with the
// protected-site headers on a protected site. The answer is never stored,
// so releasing the file takes effect at once.
func (e *Edge) withheldFile(w http.ResponseWriter, r *http.Request, f ServedFile, protected bool) {
	h := w.Header()
	if protected {
		// decide set these already; set again so this answer never
		// depends on that.
		protectedHeaders(h)
		addVary(h, "Cookie")
	}
	if strings.HasPrefix(contentType(f.Path), "text/html") {
		e.page(w, r, "withheld.html", unavailableData{Site: canonicalHost(r.Host)}, http.StatusUnavailableForLegalReasons)
		return
	}
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnavailableForLegalReasons)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, "This file is unavailable.\n")
	}
}
