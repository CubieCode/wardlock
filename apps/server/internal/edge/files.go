// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// result is how a request path maps onto the active deploy.
type result struct {
	file     ServedFile
	status   int    // 200, or 404 for 404.html
	redirect string // escaped absolute path of a directory, with its slash
}

// resolve applies SPEC "Serving" to name, a cleaned request path without its
// leading slash ("" for the root; a trailing slash is kept). ok is false
// when nothing in the deploy answers, which is a plain 404.
func resolve(s *served, name string) (res result, ok bool) {
	if name == "" || strings.HasSuffix(name, "/") {
		if f, ok := s.files[name+"index.html"]; ok {
			return result{file: f, status: http.StatusOK}, true
		}
	} else {
		if f, ok := s.files[name]; ok {
			return result{file: f, status: http.StatusOK}, true
		}
		if f, ok := s.files[name+".html"]; ok {
			return result{file: f, status: http.StatusOK}, true
		}
		if _, ok := s.files[name+"/index.html"]; ok {
			return result{redirect: escapePath(name) + "/"}, true
		}
	}
	if s.site.SPA && path.Ext(name) == "" {
		if f, ok := s.files["index.html"]; ok {
			return result{file: f, status: http.StatusOK}, true
		}
	}
	if f, ok := s.files["404.html"]; ok {
		return result{file: f, status: http.StatusNotFound}, true
	}
	return result{}, false
}

// escapePath escapes each segment of name and makes it absolute.
func escapePath(name string) string {
	segs := strings.Split(name, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return "/" + strings.Join(segs, "/")
}

// encodings are the precompressed siblings the edge serves; on equal q, the
// earlier one wins.
var encodings = []struct{ token, suffix string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// negotiate picks f or the precompressed sibling with the highest q the
// client gives it (br on a tie). It returns the file to send and its
// Content-Encoding ("" for f itself), and whether any sibling exists (so the
// response varies by Accept-Encoding).
func negotiate(s *served, f ServedFile, acceptEncoding string) (ServedFile, string, bool) {
	send, coding, best, vary := f, "", 0.0, false
	for _, e := range encodings {
		sibling, ok := s.files[f.Path+e.suffix]
		if !ok {
			continue
		}
		vary = true
		if q := qvalue(acceptEncoding, e.token); q > best {
			send, coding, best = sibling, e.token, q
		}
	}
	return send, coding, vary
}

// qvalue is the q an Accept-Encoding header gives coding: its own entry, else
// the * wildcard, else 0. A missing q is 1; an unparseable or out-of-range q
// is 0.
func qvalue(header, coding string) float64 {
	own, wildcard := -1.0, 0.0
	for part := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != coding && name != "*" {
			continue
		}
		q := 1.0
		for p := range strings.SplitSeq(params, ";") {
			k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
			if strings.EqualFold(strings.TrimSpace(k), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					parsed = 0
				}
				q = parsed
			}
		}
		if name == coding {
			own = q
		} else {
			wildcard = q
		}
	}
	if own >= 0 {
		return own
	}
	return wildcard
}

// cacheControl is the Cache-Control for deploy content. Every file
// revalidates (ETag and Last-Modified make that cheap); protected sites are
// cached privately.
func cacheControl(protected bool) string {
	if protected {
		return "private, max-age=0, must-revalidate"
	}
	return "public, max-age=0, must-revalidate"
}

// serveFile writes f (or a precompressed sibling) with status.
func (e *Edge) serveFile(w http.ResponseWriter, r *http.Request, s *served, f ServedFile, status int, protected bool) {
	send, coding, vary := negotiate(s, f, r.Header.Get("Accept-Encoding"))
	blob, err := e.blobs.Open(r.Context(), send.SHA256)
	if err != nil {
		e.log.ErrorContext(r.Context(), "open blob",
			slog.String("site_id", s.site.ID), slog.String("sha256", send.SHA256), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = blob.Close() }()

	h := w.Header()
	h.Set("Cache-Control", cacheControl(protected))
	if vary {
		h.Add("Vary", "Accept-Encoding")
	}
	ctype := mime.TypeByExtension(path.Ext(f.Path))
	if ctype == "" {
		ctype = "application/octet-stream" // never sniff
	}
	h.Set("Content-Type", ctype)
	if coding != "" {
		h.Set("Content-Encoding", coding)
	}
	h.Set("Content-Length", strconv.FormatInt(send.Size, 10))

	if status != http.StatusOK {
		// 404.html: no conditional or range handling.
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = io.CopyN(w, blob, send.Size)
		}
		return
	}
	h.Set("ETag", `"`+send.SHA256+`"`)
	if coding != "" && strings.Contains(r.Header.Get("Range"), ",") {
		// One range of an encoded variant at most: multipart byteranges of
		// compressed bytes help nobody, so send the whole variant.
		r = r.Clone(r.Context())
		r.Header.Del("Range")
	}
	http.ServeContent(w, r, "", s.site.ActivatedAt, blob)
}
