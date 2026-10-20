// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"bytes"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxImageSize is the largest image sent: OpenAI's limit for image inputs
// to the moderation endpoint. Larger images are recorded as skipped.
const MaxImageSize = 20 << 20

// Text limits. The endpoint documents no input limit; a page's extracted
// text is sent as pieces of at most TextChunk characters, split at
// whitespace where possible, and only the first MaxTextChunks pieces: text
// beyond MaxTextChunks*TextChunk characters (100,000) is not screened.
const (
	TextChunk     = 10_000
	MaxTextChunks = 10
)

// imageType returns the media type of an image the endpoint accepts (PNG,
// JPEG, WEBP or a GIF that is not animated), judged by its bytes rather
// than its name; ok is false for anything else.
func imageType(b []byte) (string, bool) {
	switch t := http.DetectContentType(b); t {
	case "image/png", "image/jpeg", "image/webp":
		return t, true
	case "image/gif":
		return t, gifFrames(b) == 1
	}
	return "", false
}

// gifFrames counts the image descriptors in a GIF without decoding it; 0
// for a malformed file.
func gifFrames(b []byte) int {
	if len(b) < 13 || (!bytes.HasPrefix(b, []byte("GIF87a")) && !bytes.HasPrefix(b, []byte("GIF89a"))) {
		return 0
	}
	i := 13
	if b[10]&0x80 != 0 { // global colour table
		i += 3 << (int(b[10]&0x07) + 1)
	}
	// skipBlocks skips data sub-blocks from j, returning the index after
	// the terminator or -1.
	skipBlocks := func(j int) int {
		for j < len(b) {
			n := int(b[j])
			j++
			if n == 0 {
				return j
			}
			j += n
		}
		return -1
	}
	frames := 0
	for i < len(b) {
		switch b[i] {
		case 0x21: // extension: label, then sub-blocks
			if i+2 > len(b) {
				return 0
			}
			if i = skipBlocks(i + 2); i < 0 {
				return 0
			}
		case 0x2C: // image descriptor
			if i+10 > len(b) {
				return 0
			}
			flags := b[i+9]
			i += 10
			if flags&0x80 != 0 { // local colour table
				i += 3 << (int(flags&0x07) + 1)
			}
			if i >= len(b) {
				return 0
			}
			if i = skipBlocks(i + 1); i < 0 { // after the LZW minimum code size
				return 0
			}
			frames++
		case 0x3B: // trailer
			return frames
		default:
			return 0
		}
	}
	return frames
}

// chunkText splits text into at most MaxTextChunks pieces of at most
// TextChunk characters each, breaking at whitespace when there is some in
// a piece's second half. Whitespace-only text gives no pieces.
func chunkText(text string) []string {
	text = strings.ToValidUTF8(text, "�")
	var out []string
	for len(out) < MaxTextChunks {
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
		if text == "" {
			break
		}
		if utf8.RuneCountInString(text) <= TextChunk {
			out = append(out, strings.TrimRightFunc(text, unicode.IsSpace))
			break
		}
		// end is the byte offset after TextChunk characters.
		end, n := 0, 0
		for end < len(text) && n < TextChunk {
			_, size := utf8.DecodeRuneInString(text[end:])
			end += size
			n++
		}
		cut := end
		if i := strings.LastIndexFunc(text[:end], unicode.IsSpace); i > end/2 {
			cut = i
		}
		out = append(out, strings.TrimRightFunc(text[:cut], unicode.IsSpace))
		text = text[cut:]
	}
	return out
}
