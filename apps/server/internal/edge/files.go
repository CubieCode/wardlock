// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// result is how a request path maps onto the active deploy.
type result struct {
	file ServedFile
	// status is 200, or 404 for 404.html; for a redirect, its status.
	status int
	// redirect is the Location: a directory's escaped absolute path with
	// its slash, or where one of the deploy's redirects sends the request
	// (C1.17), with the query.
	redirect string
}

// resolve applies SPEC "Serving" to name, a cleaned request path without its
// leading slash ("" for the root; a trailing slash is kept), with the
// request's raw query. ok is false when nothing in the deploy answers,
// which is a plain 404. The deploy's redirects apply only once no file
// answers, ahead of the SPA fallback and 404.html.
func resolve(s *served, name, rawQuery string) (res result, ok bool) {
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
			// An absolute path: cleaning leaves no empty first segment, so
			// it cannot become a scheme-relative //host URL.
			loc := escapePath(name) + "/"
			if rawQuery != "" {
				loc += "?" + rawQuery
			}
			return result{redirect: loc, status: http.StatusMovedPermanently}, true
		}
	}
	if loc, status, ok := ownerRedirect(s, "/"+name, rawQuery); ok {
		return result{redirect: loc, status: status}, true
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
// response varies by Accept-Encoding). On a screened site only a gzip
// sibling screening found to decompress to exactly f is a sibling (A2.15):
// screening saw f's bytes, not the sibling's.
func negotiate(s *served, f ServedFile, acceptEncoding string) (ServedFile, string, bool) {
	send, coding, best, vary := f, "", 0.0, false
	for _, e := range encodings {
		sibling, ok := s.files[f.Path+e.suffix]
		if !ok || s.site.Screened && (e.token != "gzip" || sibling.Decoded != f.SHA256) {
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

// pendingCacheControl is the Cache-Control for a file content screening has
// not screened yet (A2.15): stored only if revalidated on every use, so a
// shared cache cannot keep serving it once it is withheld.
func pendingCacheControl(protected bool) string {
	if protected {
		return "private, no-cache"
	}
	return "public, no-cache"
}

// immutableCacheControl is the Cache-Control for a hashed asset of a deploy
// that opts in (SPEC "Serving"): a year, never revalidated.
func immutableCacheControl(protected bool) string {
	if protected {
		return "private, max-age=31536000, immutable"
	}
	return "public, max-age=31536000, immutable"
}

// minHashLen is the shortest name segment hashedAsset takes for a content
// hash: webpack and Parcel's 8 hex digits, Vite's 8 base64url characters.
const minHashLen = 8

// hashedAsset reports whether name, a deploy path, is a file under assets/
// whose name carries a content hash: the part before its extension ends in
// a "-" or "." followed by at least minHashLen of [A-Za-z0-9_], with
// something before the separator (index-BdK3j2Lp.js, main.3f2a9c1b.js,
// assets/fonts/inter.5d1c2e8f.woff2). It is deliberately simple: a long
// word such as my-component.js matches too, which is why owners opt in per
// deploy.
func hashedAsset(name string) bool {
	if !strings.HasPrefix(name, "assets/") {
		return false
	}
	base := path.Base(name)
	ext := path.Ext(base)
	if len(ext) < 2 || len(ext) == len(base) {
		return false
	}
	stem := base[:len(base)-len(ext)]
	i := strings.LastIndexAny(stem, "-.")
	if i < 1 || len(stem)-i-1 < minHashLen {
		return false
	}
	for _, c := range []byte(stem[i+1:]) {
		if !hashChar(c) {
			return false
		}
	}
	return true
}

// hashChar reports whether c is in [A-Za-z0-9_].
func hashChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_'
}

// serveFile writes f (or a precompressed sibling, or the rendition
// ?format= asks for) with status. With strip, an HTML file gets the
// share-link strip script (serveStripped); other types, renditions
// included, are never altered. With immutable, f itself and its siblings
// are cached for a year; renditions and stripped pages keep their headers.
func (e *Edge) serveFile(w http.ResponseWriter, r *http.Request, s *served, f ServedFile, status int, protected, strip, immutable bool) {
	if rd, rtype, ok := rendition(r, f); ok {
		e.serveRendition(w, r, s, rd, rtype, status, protected)
		return
	}
	ctype := contentType(f.Path)
	if status == http.StatusOK && strings.HasPrefix(ctype, "text/html") {
		alternateLinks(w.Header(), r, f)
	}
	if strip && strings.HasPrefix(ctype, "text/html") {
		e.serveStripped(w, r, s, f, status, ctype)
		return
	}
	send, coding, vary := negotiate(s, f, r.Header.Get("Accept-Encoding"))
	e.serveBlob(w, r, s, send.SHA256, send.Size, "open blob", func(w http.ResponseWriter, blob io.ReadSeeker) {
		h := w.Header()
		switch {
		case f.Pending:
			h.Set("Cache-Control", pendingCacheControl(protected))
		case immutable:
			h.Set("Cache-Control", immutableCacheControl(protected))
		default:
			h.Set("Cache-Control", cacheControl(protected))
		}
		if vary {
			h.Add("Vary", "Accept-Encoding")
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
		r := r
		if coding != "" && strings.Contains(r.Header.Get("Range"), ",") {
			// One range of an encoded variant at most: multipart byteranges of
			// compressed bytes help nobody, so send the whole variant.
			r = r.Clone(r.Context())
			r.Header.Del("Range")
		}
		// ServeContent answers 304 and 412 before reading, so they never
		// open the blob (serveBlob).
		http.ServeContent(w, r, "", s.site.ActivatedAt, blob)
	})
}
