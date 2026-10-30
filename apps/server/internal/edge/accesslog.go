// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"io"
	"net"
	"net/http"
	"time"

	"wardlock.nz/server/internal/access"
)

// AccessRecord is one site access, reported once its response is written.
// Path is the cleaned request path without the query string, Via and
// LinkPublicID come from the request's access decision, and RemoteIP is the
// client address after trusted-proxy resolution. The recorder still scrubs
// every field before storing it.
type AccessRecord struct {
	Time   time.Time
	SiteID string
	Host   string
	// Preview is the preview the host serves (C1.19), "" for the live site.
	Preview      string
	Path         string
	Status       int
	Via          access.Via
	LinkPublicID string
	RemoteIP     string
	UserAgent    string
}

// AccessRecorder receives access records. Record runs on the request
// goroutine, so it must not block; the access-log writer buffers.
type AccessRecorder interface {
	Record(AccessRecord)
}

// recordAccess returns w wrapped to capture the response status and a
// function, to defer, that reports the access. Every request that reaches
// the access decision is recorded, allowed or not. A handler panic is
// recorded as 500 unless a status was already sent, then re-raised for
// net/http. With no recorder it returns w unchanged.
func (e *Edge) recordAccess(w http.ResponseWriter, r *http.Request, site ServedSite, clean string, d access.Decision) (http.ResponseWriter, func()) {
	if e.cfg.Recorder == nil {
		return w, func() {}
	}
	sw := &statusWriter{ResponseWriter: w}
	start := time.Now()
	return sw, func() {
		p := recover()
		status := sw.code()
		if p != nil && sw.status == 0 {
			status = http.StatusInternalServerError
		}
		e.cfg.Recorder.Record(AccessRecord{
			Time:         start,
			SiteID:       site.ID,
			Host:         canonicalHost(r.Host),
			Preview:      site.Preview,
			Path:         clean,
			Status:       status,
			Via:          d.Via,
			LinkPublicID: d.LinkPublicID,
			RemoteIP:     clientIP(r),
			UserAgent:    r.UserAgent(),
		})
		if p != nil {
			panic(p)
		}
	}
}

// clientIP is the client address without its port. Trusted-proxy handling
// has already replaced RemoteAddr when a proxy is believed.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// statusWriter remembers the status sent through it.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// ReadFrom keeps the underlying writer's sendfile path for io.Copy.
func (w *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, src)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) code() int {
	if w.status == 0 {
		// Nothing written: net/http sends 200 with an empty body.
		return http.StatusOK
	}
	return w.status
}
