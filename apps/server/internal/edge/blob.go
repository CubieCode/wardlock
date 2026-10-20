// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
)

// serveBlob answers r with the blob sha256 (size bytes from the manifest):
// send sets the response headers and writes the status and body from
// content, as http.ServeContent does.
//
// The blob is opened on content's first Read, so a response with no body
// (304, 412, HEAD, an empty file) never opens it: http.ServeContent
// answers conditional requests from the ETag and modification time before
// reading. The status is held back until the first body byte has been
// read from the blob, so a blob that cannot be opened or whose first read
// fails (a missing object in a bucket surfaces there) is the same logged
// 500 as a failed open, with the headers send set taken back, never a 200
// with a short body. A failure later in the body can only cut the
// response short, as before.
func (e *Edge) serveBlob(w http.ResponseWriter, r *http.Request, s *served, sha256 string, size int64, msg string,
	send func(w http.ResponseWriter, content io.ReadSeeker)) {
	before := w.Header().Clone()
	content := &lazyBlob{ctx: r.Context(), open: e.blobs, sha256: sha256, size: size}
	defer content.close()
	hw := &holdWriter{ResponseWriter: w, blob: content}
	send(hw, content)
	if hw.failed == nil || hw.sent {
		hw.flushHeader()
		return
	}
	e.log.ErrorContext(r.Context(), msg,
		slog.String("site_id", s.site.ID), slog.String("sha256", sha256), slog.Any("error", hw.failed))
	h := w.Header()
	clear(h)
	maps.Copy(h, before)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

// lazyBlob is a blob of known size that is opened on its first Read.
// Seeks before then only move the position. http.ServeContent reads a
// multipart body in another goroutine that may still be reading when the
// handler returns, so mu orders those reads before close.
type lazyBlob struct {
	ctx    context.Context
	open   BlobOpener
	sha256 string
	size   int64

	mu     sync.Mutex
	off    int64
	rc     io.ReadSeekCloser
	closed bool
	// started is set once a Read has returned a byte.
	started atomic.Bool
}

func (b *lazyBlob) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, errors.New("read blob: closed")
	}
	if b.rc == nil {
		if b.off >= b.size {
			return 0, io.EOF
		}
		rc, err := b.open.Open(b.ctx, b.sha256, b.size)
		if err != nil {
			return 0, err
		}
		b.rc = rc
		if b.off != 0 {
			if _, err := rc.Seek(b.off, io.SeekStart); err != nil {
				return 0, err
			}
		}
	}
	n, err := b.rc.Read(p)
	b.off += int64(n)
	if n > 0 {
		b.started.Store(true)
	}
	return n, err
}

func (b *lazyBlob) Seek(offset int64, whence int) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rc != nil {
		off, err := b.rc.Seek(offset, whence)
		if err == nil {
			b.off = off
		}
		return off, err
	}
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += b.off
	case io.SeekEnd:
		offset += b.size
	default:
		return 0, errors.New("seek blob: invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("seek blob: negative position")
	}
	b.off = offset
	return offset, nil
}

func (b *lazyBlob) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.rc != nil {
		_ = b.rc.Close()
	}
}

// file returns the open blob if it is a file.
func (b *lazyBlob) file() (*os.File, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f, ok := b.rc.(*os.File)
	return f, ok
}

// holdWriter holds WriteHeader back until the blob's first byte has been
// read. http.ServeContent and io.CopyN write bodies through ReadFrom,
// which holds what comes before that byte (a multipart boundary is written
// before the part's content is read) and records a read that fails first
// in failed.
type holdWriter struct {
	http.ResponseWriter
	blob   *lazyBlob
	status int
	sent   bool
	failed error
}

func (w *holdWriter) WriteHeader(status int) {
	if !w.sent && w.status == 0 {
		w.status = status
	}
}

// flushHeader sends the held status, if any is still unsent.
func (w *holdWriter) flushHeader() {
	if !w.sent {
		w.sent = true
		if w.status != 0 {
			w.ResponseWriter.WriteHeader(w.status)
		}
	}
}

func (w *holdWriter) Write(p []byte) (int, error) {
	w.flushHeader()
	return w.ResponseWriter.Write(p)
}

// firstChunk is the most ReadFrom reads at a time before sending the
// status.
const firstChunk = 32 << 10

func (w *holdWriter) ReadFrom(src io.Reader) (int64, error) {
	var total int64
	if !w.sent {
		var held []byte
		buf := make([]byte, firstChunk)
		for !w.blob.started.Load() {
			n, err := src.Read(buf)
			held = append(held, buf[:n]...)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				w.failed = err
				return 0, err
			}
		}
		if len(held) == 0 {
			return 0, nil
		}
		m, err := w.Write(held)
		total += int64(m)
		if err != nil {
			return total, err
		}
	}
	// The rest goes through the server's ReadFrom; a file blob behind a
	// LimitedReader is handed over as one, so it is still sent with
	// sendfile.
	if lr, ok := src.(*io.LimitedReader); ok {
		if b, ok := lr.R.(*lazyBlob); ok {
			if f, ok := b.file(); ok {
				src = &io.LimitedReader{R: f, N: lr.N}
			}
		}
	}
	n, err := io.Copy(w.ResponseWriter, src)
	return total + n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *holdWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// readBlob reads the whole of f.
func (e *Edge) readBlob(ctx context.Context, f ServedFile) ([]byte, error) {
	blob, err := e.blobs.Open(ctx, f.SHA256, f.Size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = blob.Close() }()
	return io.ReadAll(io.LimitReader(blob, f.Size))
}
