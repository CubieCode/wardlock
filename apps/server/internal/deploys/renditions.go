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
	return []byte(markdown(blocks)), []byte(plainText(blocks))
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

// render generates the renditions of every HTML file in the deploy, stores
// them as blobs and records them, so the edge can serve them once the
// deploy is active. It is idempotent: retrying after a failure renders the
// same bytes again.
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
	rs := make([]store.Rendition, 0, 2*len(sources))
	for _, f := range sources {
		out, err := s.renderFile(ctx, origin, f)
		if err != nil {
			return err
		}
		rs = append(rs, out...)
	}
	if err := s.c.Renditions.Put(ctx, ownerID, site.ID, deployID, rs, s.now()); err != nil {
		return storeErr("record renditions", err)
	}
	// Garbage collection may have removed a blob that already existed, with
	// no reference, between storing it and recording the rendition. Now that
	// the rendition references it, it is safe from collection: store again
	// whatever went missing.
	for i, f := range sources {
		for _, rd := range rs[2*i : 2*i+2] {
			ok, err := s.c.Blobs.Has(ctx, rd.SHA256)
			if err != nil {
				return fmt.Errorf("check rendition blob: %w", err)
			}
			if !ok {
				if _, err := s.renderFile(ctx, origin, f); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// renderFile renders one HTML file and stores both renditions, returning
// them Markdown first.
func (s *Service) renderFile(ctx context.Context, origin string, f store.DeployFile) ([]store.Rendition, error) {
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
		if err := s.c.Blobs.Put(ctx, h, size, bytes.NewReader(r.body)); err != nil {
			return nil, fmt.Errorf("store rendition: %w", err)
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
