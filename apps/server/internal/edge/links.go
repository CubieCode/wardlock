// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"bytes"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"wardlock.nz/server/internal/access"
)

// Share links (A2.2) at the edge: finding the token, issuing the session it
// earns, stripping it from the address bar, the use-limited interstitial
// and /.wardlock/view.

// tokenParam is the query parameter that carries a share-link token.
const tokenParam = "t"

// maxViewForm bounds the interstitial form body.
const maxViewForm = 16 << 10

// linkToken returns the request's share-link token: the first ?t= value, or
// else an Authorization: Bearer credential. fromQuery reports that it came
// in the URL, as when a browser follows a link.
func linkToken(r *http.Request) (token string, fromQuery bool) {
	if v, ok := firstQueryValue(r.URL.RawQuery, tokenParam); ok {
		return v, true
	}
	scheme, cred, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(cred), false
	}
	return "", false
}

// firstQueryValue returns the first value of key in rawQuery, decoded.
func firstQueryValue(rawQuery, key string) (string, bool) {
	for part := range strings.SplitSeq(rawQuery, "&") {
		k, v, _ := strings.Cut(part, "=")
		if dk, err := url.QueryUnescape(k); err != nil || dk != key {
			continue
		}
		dv, err := url.QueryUnescape(v)
		return dv, err == nil
	}
	return "", false
}

// withoutToken returns the escaped path and query of r with every t
// parameter removed and the rest kept byte for byte: where the
// interstitial returns the visitor.
func withoutToken(r *http.Request) string {
	p := r.URL.EscapedPath()
	if q := queryWithoutToken(r.URL.RawQuery); q != "" {
		p += "?" + q
	}
	return p
}

// queryWithoutToken is rawQuery with every t parameter, in any spelling,
// and empty parts removed.
func queryWithoutToken(rawQuery string) string {
	var kept []string
	for part := range strings.SplitSeq(rawQuery, "&") {
		k, _, _ := strings.Cut(part, "=")
		if dk, err := url.QueryUnescape(k); part == "" || (err == nil && dk == tokenParam) {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&")
}

// navigation reports whether r is a browser loading a page: a GET that
// accepts HTML and, when the browser says, is a navigation. Only these earn
// a session from a token in the URL; curl, bots, crawlers, HEAD requests
// and subresource fetches get the content on every request with the token
// and leave no session behind.
func navigation(r *http.Request) bool {
	mode := r.Header.Get("Sec-Fetch-Mode")
	return r.Method == http.MethodGet && acceptsHTML(r) && (mode == "" || mode == "navigate")
}

// issueLinkSession creates the session an accepted token earned and
// returns its cookie. A link rotated, revoked or expired since the
// decision issues nothing, and *d becomes a refusal. ok is false when it
// has already answered 500.
func (e *Edge) issueLinkSession(w http.ResponseWriter, r *http.Request, d *access.Decision) (*http.Cookie, bool) {
	c, err := e.access.IssueSession(r.Context(), *d.Issue)
	if errors.Is(err, access.ErrLinkChanged) {
		d.Outcome, d.Via, d.LinkPublicID, d.TokenAccepted, d.Issue = access.Unauthorized, access.ViaNone, "", false, nil
		if acceptsHTML(r) {
			d.Outcome = access.Login
		}
		return nil, true
	}
	if err != nil {
		e.log.ErrorContext(r.Context(), "issue link session", slog.String("site_id", d.Issue.SiteID), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	return SessionCookie(c), true
}

// stripScript removes the token from the address bar once an HTML page has
// loaded, so it is not bookmarked, shared or sent on as a Referer by
// accident. It changes nothing else about the URL's path or fragment.
const stripScript = "<script>" + stripJS + "</script>"

// stripJS is stripScript's body, which stripCSPSource hashes.
const stripJS = `(function(){try{var u=new URL(location.href);if(u.searchParams.has("t")){u.searchParams.delete("t");history.replaceState(history.state,"",u.pathname+u.search+u.hash)}}catch(e){}})();`

// injectStrip inserts stripScript into an HTML document: before </head>,
// else before <body, else after a leading doctype, else at the start. Tags
// are matched ASCII-case-insensitively on doc itself, so every index is a
// byte offset into doc whatever its encoding (Unicode case folding changes
// byte lengths, and Latin-1 pages are not UTF-8).
func injectStrip(doc []byte) []byte {
	at := indexASCIIFold(doc, "</head")
	if at < 0 {
		at = indexASCIIFold(doc, "<body")
	}
	if at < 0 {
		at = 0
		rest := bytes.TrimPrefix(doc, []byte("\xef\xbb\xbf"))
		lead := len(doc) - len(bytes.TrimLeft(rest, " \t\r\n"))
		if hasPrefixASCIIFold(doc[lead:], "<!doctype") {
			if end := bytes.IndexByte(doc[lead:], '>'); end >= 0 {
				at = lead + end + 1
			}
		}
	}
	out := make([]byte, 0, len(doc)+len(stripScript))
	out = append(out, doc[:at]...)
	out = append(out, stripScript...)
	return append(out, doc[at:]...)
}

// indexASCIIFold is bytes.Index with A-Z folded to a-z; lowerPattern must
// be lowercase ASCII. Other bytes compare exactly.
func indexASCIIFold(doc []byte, lowerPattern string) int {
	for i := 0; i+len(lowerPattern) <= len(doc); i++ {
		if hasPrefixASCIIFold(doc[i:], lowerPattern) {
			return i
		}
	}
	return -1
}

func hasPrefixASCIIFold(b []byte, lowerPattern string) bool {
	if len(b) < len(lowerPattern) {
		return false
	}
	for j := range len(lowerPattern) {
		c := b[j]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lowerPattern[j] {
			return false
		}
	}
	return true
}

// serveStripped sends an HTML file with stripScript injected. The body is
// not the deployed file, so it goes out whole and uncompressed, without
// ETag, conditional or range handling; it is cached privately like every
// response a token opens.
func (e *Edge) serveStripped(w http.ResponseWriter, r *http.Request, s *served, f ServedFile, status int, ctype string) {
	doc, err := e.readBlob(r.Context(), f)
	if err != nil {
		// A bucket's missing object only shows when read, so a failed
		// read logs as the failed open it is.
		e.log.ErrorContext(r.Context(), "open blob",
			slog.String("site_id", s.site.ID), slog.String("sha256", f.SHA256), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	body := injectStrip(doc)
	h := w.Header()
	// A deploy's CSP (C1.17) must not block the script that removes the
	// token from the address bar.
	if csp := h.Values("Content-Security-Policy"); len(csp) > 0 {
		allowed := make([]string, len(csp))
		for i, v := range csp {
			allowed[i] = allowStripScript(v)
		}
		h["Content-Security-Policy"] = allowed
	}
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Type", ctype)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// rateLimited answers a token-bearing request refused because too many
// tokens failed for its IP or site.
func rateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Retry-After", strconv.Itoa(int(math.Ceil(max(retryAfter, time.Second).Seconds()))))
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("Too many attempts. Try again later.\n"))
	}
}

// viewData is what the interstitial shows.
type viewData struct {
	Site   string
	Ticket string
	// Gone replaces the button with "this link no longer works".
	Gone bool
	// ReportURL links to the abuse report form; "" when reports are off.
	ReportURL string
}

// viewPage renders the use-limited interstitial (or, with Gone, its
// dead-end) with status. Like the login page it is Wardlock's: never
// cached, never framed, no scripts. Its form carries a ticket, never the
// token.
func (e *Edge) viewPage(w http.ResponseWriter, r *http.Request, data viewData, status int) {
	data.Site = canonicalHost(r.Host)
	data.ReportURL = e.reportURL(data.Site, cleanPath(r.URL.Path))
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, "view.html", data); err != nil {
		e.log.ErrorContext(r.Context(), "render view page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}

// view answers POST /.wardlock/view, the interstitial's button: it spends
// one use of the link and sends the visitor, now holding a session, back
// to the page they asked for. GET never spends anything, so preview bots
// and mail scanners cannot use links up. Like logout it takes only a
// same-origin POST: otherwise a page elsewhere could post a ticket for its
// own link and swap the visitor's session for one it chose.
func (e *Edge) view(w http.ResponseWriter, r *http.Request, site ServedSite) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxViewForm)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	v, err := e.access.View(r.Context(), site.ID, r.PostForm.Get("ticket"))
	if errors.Is(err, access.ErrTicket) {
		e.viewPage(w, r, viewData{Gone: true}, http.StatusGone)
		return
	}
	if err != nil {
		e.log.ErrorContext(r.Context(), "view share link", slog.String("site_id", site.ID), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if v.Cookie.Value != "" {
		w.Header().Add("Set-Cookie", SessionCookie(v.Cookie).String())
	}
	w.Header().Set("Location", localPath(v.Return))
	w.WriteHeader(http.StatusSeeOther)
}

// localPath returns p if it is a path on this host, else "/". Tickets are
// signed, so this is only a second line against an open redirect. Browsers
// drop tabs and newlines from a Location and read a backslash as a slash,
// so neither may appear anywhere ("/\t/host" would become "//host").
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") ||
		strings.ContainsRune(p, '\\') || strings.ContainsFunc(p, unicode.IsControl) {
		return "/"
	}
	return p
}
