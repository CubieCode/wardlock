// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"mime"
	"path"
	"strings"
)

// TextTypes are the media types the edge sends for files browsers show as
// text or a page, by lowercase extension. They are pinned here rather than
// read from the host's mime.types, so what a visitor's browser displays,
// and what content screening treats as text (A2.15), is the same on every
// host. Scripts and stylesheets are left to the standard table: browsers
// show them too, but screening does not read them as text.
var TextTypes = map[string]string{
	".html":     "text/html; charset=utf-8",
	".htm":      "text/html; charset=utf-8",
	".xhtml":    "application/xhtml+xml",
	".svg":      "image/svg+xml",
	".txt":      "text/plain; charset=utf-8",
	".text":     "text/plain; charset=utf-8",
	".md":       "text/markdown; charset=utf-8",
	".markdown": "text/markdown; charset=utf-8",
	".csv":      "text/csv; charset=utf-8",
	".json":     "application/json",
	".xml":      "text/xml; charset=utf-8",
}

// contentType is the Content-Type for a deploy path: TextTypes, then the
// standard table, then application/octet-stream (never sniffed).
func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := TextTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}
