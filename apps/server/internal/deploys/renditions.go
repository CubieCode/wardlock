// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"wardlock.nz/server/internal/store"
)

// Rendition formats (SPEC "AI-readable renditions"), as ?format= names them.
const (
	FormatMarkdown = "md"
	FormatText     = "txt"
)

// HasRenditions reports whether a manifest path is an HTML file, which
// gets renditions.
func HasRenditions(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".html" || ext == ".htm"
}

// Render converts page, an HTML document, to Markdown and plain text by the
// conversion contract in SPEC "AI-readable renditions". pageURL is the
// address the page is served at; references resolve against it, so links
// are absolute when it is.
func Render(page []byte, pageURL *url.URL) (md, txt []byte) {
	blocks := convert(page, pageURL)
	// convert redacted every token the page holds; these passes also catch
	// a token-shaped string the output only forms by joining pieces, such as
	// text followed by a bare link.
	return []byte(scrubMarkdown(markdown(blocks))), []byte(scrubTokens(plainText(blocks)))
}

// scrubMarkdown redacts tokens in md as a reader sees it: backslash
// escapes stand for the character they escape, and emphasis,
// strikethrough and code delimiters (*, ~, `) are not shown. A token's
// secret, with any delimiters inside it, becomes "redacted".
func scrubMarkdown(md string) string {
	if !strings.Contains(strings.ToLower(md), "wl") {
		return md
	}
	var view strings.Builder
	at := make([]int, 0, len(md)) // at[i] is where view byte i starts in md
	for i := 0; i < len(md); i++ {
		switch c := md[i]; {
		case c == '\\' && i+1 < len(md) && md[i+1] < 0x80 && md[i+1] > ' ':
			at = append(at, i)
			i++
			view.WriteByte(md[i])
		case c == '*' || c == '~' || c == '`':
		default:
			at = append(at, i)
			view.WriteByte(c)
		}
	}
	toks := findTokens(view.String())
	for t := len(toks) - 1; t >= 0; t-- {
		from, to := at[toks[t][0]+tokenSecretAt], at[toks[t][1]-1]+1
		if md[to-1] == '\\' { // the last character was escaped
			to++
		}
		md = md[:from] + "redacted" + md[to:]
	}
	return md
}

// PageURL returns the address of a manifest path on a site at origin (such
// as https://docs.example.com; "" for a root-relative address). index.html
// is addressed by its directory.
func PageURL(origin, p string) (*url.URL, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return nil, fmt.Errorf("parse site origin: %w", err)
	}
	if strings.EqualFold(path.Base(p), "index.html") {
		p = strings.TrimSuffix(p, path.Base(p))
	}
	u.Path = "/" + p
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return u, nil
}

// DefaultMaxRenditionSource is the largest HTML file that gets renditions
// unless Config.MaxRenditionSource says otherwise. Conversion holds the
// parsed page in memory, several times its size; a larger page keeps only
// its HTML.
const DefaultMaxRenditionSource = 5 << 20

// renderSlots is how many pages one Service converts at once, across every
// activation, bounding the memory rendering takes.
const renderSlots = 2

// rendered is one source page and its renditions, Markdown first.
type rendered struct {
	source     store.DeployFile
	renditions []store.Rendition
}

// render generates the renditions of every HTML file in the deploy up to
// the size limit, stores them as blobs and records them, so the edge can
// serve them once the deploy is active. It is idempotent: retrying after a
// failure renders the same bytes again. It stops when ctx ends.
func (s *Service) render(ctx context.Context, ownerID string, site store.Site, deployID string) error {
	if s.c.Renditions == nil {
		return nil
	}
	sources, err := s.c.Renditions.Sources(ctx, ownerID, site.ID, deployID)
	if err != nil {
		return storeErr("list rendition sources", err)
	}
	origin := ""
	if s.c.SiteOrigin != nil {
		origin = s.c.SiteOrigin(site.Name)
	}
	limit := s.c.MaxRenditionSource
	if limit <= 0 {
		limit = DefaultMaxRenditionSource
	}
	var done []rendered
	var rs []store.Rendition
	for _, f := range sources {
		if f.Size > limit {
			continue // served as HTML only, even for ?format=
		}
		out, err := s.renderFile(ctx, origin, f)
		if err != nil {
			return err
		}
		done = append(done, rendered{f, out})
		rs = append(rs, out...)
	}
	if err := s.c.Renditions.Put(ctx, ownerID, site.ID, deployID, rs, s.now()); err != nil {
		return storeErr("record renditions", err)
	}
	// Garbage collection may have removed a blob that already existed, with
	// no reference, between storing it and recording the rendition. Now that
	// the rendition references it, it is safe from collection: store again
	// whatever went missing.
	for _, r := range done {
		for _, rd := range r.renditions {
			ok, err := s.c.Blobs.Has(ctx, rd.SHA256)
			if err != nil {
				return fmt.Errorf("check rendition blob: %w", err)
			}
			if !ok {
				if _, err := s.renderFile(ctx, origin, r.source); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// renderFile renders one HTML file and stores both renditions that are not
// stored yet, returning them Markdown first. It waits for one of the
// Service's render slots.
func (s *Service) renderFile(ctx context.Context, origin string, f store.DeployFile) ([]store.Rendition, error) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, fmt.Errorf("render %s: %w", f.Path, ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("render %s: %w", f.Path, err)
	}
	page, err := s.readBlob(ctx, f)
	if err != nil {
		return nil, err
	}
	u, err := PageURL(origin, f.Path)
	if err != nil {
		return nil, err
	}
	md, txt := Render(page, u)
	out := make([]store.Rendition, 0, 2)
	for _, r := range []struct {
		format string
		body   []byte
	}{{FormatMarkdown, md}, {FormatText, txt}} {
		sum := sha256.Sum256(r.body)
		h := hex.EncodeToString(sum[:])
		size := int64(len(r.body))
		held, err := s.c.Blobs.Has(ctx, h)
		if err != nil {
			return nil, fmt.Errorf("check rendition: %w", err)
		}
		if !held {
			if err := s.c.Blobs.Put(ctx, h, size, bytes.NewReader(r.body)); err != nil {
				return nil, fmt.Errorf("store rendition: %w", err)
			}
		}
		out = append(out, store.Rendition{Path: f.Path, Format: r.format, SHA256: h, Size: size})
	}
	return out, nil
}

func (s *Service) readBlob(ctx context.Context, f store.DeployFile) ([]byte, error) {
	b, err := s.c.Blobs.Open(ctx, f.SHA256)
	if err != nil {
		return nil, fmt.Errorf("open page: %w", err)
	}
	defer func() { _ = b.Close() }()
	page, err := io.ReadAll(io.LimitReader(b, f.Size))
	if err != nil {
		return nil, fmt.Errorf("read page: %w", err)
	}
	return page, nil
}
