// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/deploys"
)

// MaxImageSize is the largest image sent as it is: OpenAI's limit for
// image inputs to the moderation endpoint. A larger PNG, JPEG or GIF is
// decoded and sent scaled down; any other larger image is unscreenable.
const MaxImageSize = 20 << 20

// Image limits for what the server decodes itself: a GIF's frames (sent as
// PNG), an icon's PNG images and images over MaxImageSize.
const (
	// MaxPixels bounds an image the server decodes, so a small file that
	// claims a huge canvas cannot exhaust memory; larger ones are
	// unscreenable.
	MaxPixels = 40_000_000
	// ScaledSide is the longest side of an image scaled down to fit
	// MaxImageSize.
	ScaledSide = 2048
	// GIFFrames is how many frames of an animated GIF are sent: half
	// evenly spaced across the whole animation (first and last included),
	// half picked at random afresh on every screening, so an uploader
	// cannot know which frames are judged. Every frame is decoded (each
	// drawn over the ones before it, as browsers do), so an animation of
	// more than MaxGIFFrames frames, whose frames add up to more than
	// MaxGIFPixels pixels, or that takes longer than MaxDecodeTime to
	// decode, is unscreenable.
	GIFFrames     = 10
	MaxGIFFrames  = 2000
	MaxGIFPixels  = 1_000_000_000
	MaxDecodeTime = time.Minute
)

// decoder does the image decoding screening needs, one image at a time
// whatever the worker's concurrency, so the memory it takes stays bounded:
// a canvas of at most MaxPixels (4 bytes a pixel, 8 for a 16-bit PNG) plus
// the scaled copy. A decode waiting its turn gives up when its context
// ends, and a GIF's checks it between frames.
type decoder struct {
	sem chan struct{}
	// pick returns n distinct indices in [0, max) at random; tests fix it.
	pick func(n, max int) []int
}

func newDecoder() *decoder {
	return &decoder{sem: make(chan struct{}, 1), pick: randomFrames}
}

// lock waits for the decoder; the returned func releases it.
func (d *decoder) lock(ctx context.Context) (func(), error) {
	select {
	case d.sem <- struct{}{}:
		return func() { <-d.sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait to decode: %w", ctx.Err())
	}
}

// randomFrames picks n distinct indices in [0, max) with crypto/rand, so
// the choice cannot be predicted: the first n steps of a Fisher-Yates
// shuffle.
func randomFrames(n, max int) []int {
	idx := make([]int, max)
	for i := range idx {
		idx[i] = i
	}
	n = min(n, max)
	for i := range n {
		j, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(max-i)))
		if err != nil {
			continue // crypto/rand does not fail on supported platforms
		}
		k := i + int(j.Int64())
		idx[i], idx[k] = idx[k], idx[i]
	}
	return idx[:n]
}

// Embedded image limits: images in data: URIs (base64, percent-encoded or
// plain) in a page, SVG, stylesheet, script or other text file are decoded
// and screened as images, at most MaxEmbedded per file and MaxEmbeddedSize
// bytes of them in all, nested SVG data URIs at most embedDepth deep. A
// file with more is unscreenable beyond those.
const (
	MaxEmbedded     = 16
	MaxEmbeddedSize = 64 << 20
	embedDepth      = 2
)

// MaxScanSize bounds how much of a page, text or SVG file is read to find
// its text and embedded images; the rest of a larger file is unscreenable.
const MaxScanSize = 64 << 20

// MaxDecodedSize bounds what a gzip file is decompressed to when checking
// it against the file it stands for; a larger one is never offered.
const MaxDecodedSize = 1 << 30

// Text limits. The endpoint documents no input limit; a page's extracted
// text is sent as pieces of at most TextChunk characters, split at
// whitespace where possible, and only the first MaxTextChunks pieces: text
// beyond MaxTextChunks*TextChunk characters (100,000) is not screened.
const (
	TextChunk     = 10_000
	MaxTextChunks = 10
)

// sniffSize is how much of a file sniff looks at.
const sniffSize = 1024

// class is what a file's bytes are, as far as screening cares.
type class int

const (
	classOther      class = iota
	classImage            // PNG, JPEG, WEBP or GIF: the endpoint's formats
	classIcon             // ICO: screened through the PNG images it holds
	classOtherImage       // an image format neither the endpoint nor the server reads
	classMarkup           // HTML, XML or SVG: text and embedded images
	classText             // other text
	classGzip
)

// sniff classifies a file by its first bytes (http.DetectContentType plus
// the image and markup formats it does not know), never by its name.
func sniff(head []byte) (class, string) {
	t := http.DetectContentType(head)
	switch t {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return classImage, t
	case "image/x-icon":
		return classIcon, t
	case "image/bmp":
		return classOtherImage, "a BMP image"
	case "application/x-gzip":
		return classGzip, t
	}
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		switch string(head[8:12]) {
		case "avif", "avis":
			return classOtherImage, "an AVIF image"
		case "heic", "heix", "hevc", "heim", "heis", "mif1", "msf1":
			return classOtherImage, "a HEIF image"
		}
	}
	if bytes.HasPrefix(head, []byte("II*\x00")) || bytes.HasPrefix(head, []byte("MM\x00*")) {
		return classOtherImage, "a TIFF image"
	}
	if bytes.HasPrefix(head, []byte("\xff\x0a")) || bytes.HasPrefix(head, []byte("\x00\x00\x00\x0cJXL \x0d\x0a\x87\x0a")) {
		return classOtherImage, "a JPEG XL image"
	}
	switch {
	case strings.HasPrefix(t, "text/html"), strings.HasPrefix(t, "text/xml"):
		return classMarkup, t
	case strings.HasPrefix(t, "text/"):
		if isSVG(head) {
			return classMarkup, "image/svg+xml"
		}
		return classText, t
	}
	return classOther, t
}

// isSVG reports whether text starts, after any whitespace, byte order mark,
// comments or doctype, with an <svg element.
func isSVG(head []byte) bool {
	b := bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	for {
		b = bytes.TrimLeftFunc(b, unicode.IsSpace)
		switch {
		case bytes.HasPrefix(b, []byte("<!--")):
			i := bytes.Index(b, []byte("-->"))
			if i < 0 {
				return false
			}
			b = b[i+3:]
		case bytes.HasPrefix(b, []byte("<!")) || bytes.HasPrefix(b, []byte("<?")):
			i := bytes.IndexByte(b, '>')
			if i < 0 {
				return false
			}
			b = b[i+1:]
		default:
			return len(b) > 4 && strings.EqualFold(string(b[:4]), "<svg") && (b[4] == '>' || b[4] == '/' || unicode.IsSpace(rune(b[4])))
		}
	}
}

// unscreenable is an image screening cannot send, with what it is.
type unscreenable struct{ what string }

func (u unscreenable) Error() string { return "unscreenable: " + u.what }

// imageInputs turns an image's bytes into what is sent: the bytes
// themselves for a PNG, JPEG or WEBP the endpoint takes, PNG frames for a
// GIF, the largest PNG an icon holds, or a scaled-down JPEG for a PNG or
// JPEG over MaxImageSize. An unscreenable error says why there is nothing.
func (d *decoder) imageInputs(ctx context.Context, b []byte) ([]Input, error) {
	c, t := sniff(head(b))
	switch c {
	case classIcon:
		p := iconPNG(b)
		if p == nil {
			return nil, unscreenable{"an icon without PNG images"}
		}
		return d.imageInputs(ctx, p)
	case classOtherImage:
		return nil, unscreenable{t}
	case classImage:
	default:
		return nil, unscreenable{"not a readable image"}
	}
	switch t {
	case "image/gif":
		return d.gifInputs(ctx, b)
	case "image/webp":
		if len(b) > MaxImageSize {
			return nil, unscreenable{"a WEBP image over 20 MB"}
		}
		return []Input{{Image: b, ImageType: t}}, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, unscreenable{"a damaged " + strings.TrimPrefix(t, "image/") + " image"}
	}
	if len(b) <= MaxImageSize {
		return []Input{{Image: b, ImageType: t}}, nil
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return nil, unscreenable{"an image too large to scale"}
	}
	unlock, err := d.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, unscreenable{"a damaged " + strings.TrimPrefix(t, "image/") + " image"}
	}
	in, err := encodeJPEG(scale(img, ScaledSide))
	if err != nil {
		return nil, err
	}
	return []Input{in}, nil
}

func head(b []byte) []byte { return b[:min(len(b), sniffSize)] }

// gifInputs decodes every frame of a GIF, each drawn over the frames
// before it, and returns up to GIFFrames of them as PNG: half evenly
// spaced from the first to the last, the rest at random. Frames are split out and decoded one at a
// time, so only the canvas and one frame are held, and anything after the
// last whole frame (a truncated file, trailing junk) is ignored, as is
// everything from a frame that does not decode.
func (d *decoder) gifInputs(ctx context.Context, b []byte) ([]Input, error) {
	hdr, frames := gifSplit(b)
	if len(frames) == 0 {
		return nil, unscreenable{"a GIF image with no readable frame"}
	}
	if len(frames) > MaxGIFFrames {
		return nil, unscreenable{fmt.Sprintf("an animation of more than %d frames", MaxGIFFrames)}
	}
	cfg, err := gif.DecodeConfig(bytes.NewReader(hdr))
	if err != nil || cfg.Width*cfg.Height > MaxPixels || cfg.Width == 0 || cfg.Height == 0 {
		return nil, unscreenable{"a GIF image too large or damaged"}
	}
	want := gifSample(len(frames), d.pick)
	unlock, err := d.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	start := time.Now()
	canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
	var out []Input
	snapshot := func() error {
		var buf bytes.Buffer
		if err := png.Encode(&buf, canvas); err != nil {
			return fmt.Errorf("encode GIF frame: %w", err)
		}
		in := Input{Image: buf.Bytes(), ImageType: "image/png"}
		if len(in.Image) > MaxImageSize {
			var err error
			if in, err = encodeJPEG(scale(canvas, ScaledSide)); err != nil {
				return err
			}
		}
		out = append(out, in)
		return nil
	}
	pixels, last := 0, -1
	for i, f := range frames {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("decode GIF: %w", err)
		}
		if time.Since(start) > MaxDecodeTime {
			return nil, unscreenable{"an animation too slow to decode"}
		}
		one := append(append(append([]byte{}, hdr...), f...), 0x3B)
		img, err := gif.Decode(bytes.NewReader(one))
		if err != nil {
			if i == 0 {
				return nil, unscreenable{"a GIF image with no readable frame"}
			}
			// Browsers stop at a damaged frame too: what was shown last
			// counts.
			if last != i-1 {
				if err := snapshot(); err != nil {
					return nil, err
				}
			}
			break
		}
		if pixels += img.Bounds().Dx() * img.Bounds().Dy(); pixels > MaxGIFPixels {
			return nil, unscreenable{"an animation too large to decode"}
		}
		draw.Draw(canvas, img.Bounds(), img, img.Bounds().Min, draw.Over)
		if want[i] {
			if err := snapshot(); err != nil {
				return nil, err
			}
			last = i
		}
	}
	return out, nil
}

// gifSample chooses which of n frames are sent: all of them when there are
// at most GIFFrames, else GIFFrames/2 evenly spaced (first and last
// included) and the rest picked at random among the others.
func gifSample(n int, pick func(n, max int) []int) map[int]bool {
	want := map[int]bool{}
	if n <= GIFFrames {
		for i := range n {
			want[i] = true
		}
		return want
	}
	even := GIFFrames / 2
	for j := range even {
		want[j*(n-1)/(even-1)] = true
	}
	var others []int
	for i := range n {
		if !want[i] {
			others = append(others, i)
		}
	}
	for _, j := range pick(GIFFrames-len(want), len(others)) {
		want[others[j]] = true
	}
	return want
}

// gifSplit walks a GIF's blocks without decoding them. hdr is the header,
// logical screen descriptor and global colour table; each frame is the
// extensions before an image descriptor (its graphic control extension
// among them), the descriptor, its local colour table and its image data.
// Walking stops at the trailer or at the first block that is not whole.
func gifSplit(b []byte) (hdr []byte, frames [][]byte) {
	if len(b) < 13 || (!bytes.HasPrefix(b, []byte("GIF87a")) && !bytes.HasPrefix(b, []byte("GIF89a"))) {
		return nil, nil
	}
	i := 13
	if b[10]&0x80 != 0 { // global colour table
		i += 3 << (int(b[10]&0x07) + 1)
	}
	if i > len(b) {
		return nil, nil
	}
	hdr = b[:i]
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
	start := i
	for i < len(b) {
		switch b[i] {
		case 0x21: // extension: label, then sub-blocks
			if i+2 > len(b) {
				return hdr, frames
			}
			if i = skipBlocks(i + 2); i < 0 {
				return hdr, frames
			}
		case 0x2C: // image descriptor
			if i+10 > len(b) {
				return hdr, frames
			}
			flags := b[i+9]
			i += 10
			if flags&0x80 != 0 { // local colour table
				i += 3 << (int(flags&0x07) + 1)
			}
			if i >= len(b) {
				return hdr, frames
			}
			if i = skipBlocks(i + 1); i < 0 { // after the LZW minimum code size
				return hdr, frames
			}
			frames = append(frames, b[start:i])
			start = i
		default: // the trailer, or junk
			return hdr, frames
		}
	}
	return hdr, frames
}

// iconPNG returns the largest PNG image an ICO or CUR file holds, nil when
// it holds none (only bitmaps, which the standard library cannot read).
func iconPNG(b []byte) []byte {
	if len(b) < 6 {
		return nil
	}
	n := int(binary.LittleEndian.Uint16(b[4:6]))
	var best []byte
	for e := range n {
		off := 6 + 16*e
		if off+16 > len(b) {
			break
		}
		size := int(binary.LittleEndian.Uint32(b[off+8 : off+12]))
		at := int(binary.LittleEndian.Uint32(b[off+12 : off+16]))
		if at < 0 || size <= 0 || at > len(b) || size > len(b)-at {
			continue
		}
		if data := b[at : at+size]; bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) && len(data) > len(best) {
			best = data
		}
	}
	return best
}

// scale draws img over white, scaled (nearest neighbour) so its longer side
// is at most side.
func scale(img image.Image, side int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > side || h > side {
		if w >= h {
			w, h = side, max(1, h*side/w)
		} else {
			w, h = max(1, w*side/h), side
		}
	}
	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := b.Dx(), b.Dy()
	for y := range h {
		sy := b.Min.Y + y*sh/h
		for x := range w {
			scaled.Set(x, y, img.At(b.Min.X+x*sw/w, sy))
		}
	}
	dst := image.NewRGBA(scaled.Bounds())
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), scaled, image.Point{}, draw.Over)
	return dst
}

func encodeJPEG(img image.Image) (Input, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return Input{}, fmt.Errorf("encode scaled image: %w", err)
	}
	if buf.Len() > MaxImageSize {
		return Input{}, unscreenable{"an image too large even scaled down"}
	}
	return Input{Image: buf.Bytes(), ImageType: "image/jpeg"}, nil
}

// dataURI finds a data: URI of any media type (data:;base64, included);
// the payload follows the match, base64 when the parameters say so,
// percent-encoded or plain otherwise. What it holds is decided by sniffing
// the payload, not by the media type it claims.
var dataURI = regexp.MustCompile(`(?i)data:([a-z0-9.+-]+/[a-z0-9.+-]+)?((?:;[a-z0-9._=-]+)*),`)

// embedded is what a file embeds in data: URIs: the images to send, the
// text of embedded SVGs, and why anything could not be.
type embedded struct {
	images []Input
	size   int
	text   []string
	why    []string
}

// errNoPayload marks a data: URI with nothing to screen: an empty payload,
// one a script assembles at runtime ("data:image/png;base64,"+b) or one
// that is never closed.
var errNoPayload = errors.New("no data: payload")

// dataPayload returns the bytes of the data: URI whose payload starts at
// b[at], given its parameters and the byte before "data:" (a quote or
// parenthesis that must close it, or none). errNoPayload when there is
// nothing to screen; another error for base64 that does not decode.
func dataPayload(b []byte, at int, params string, opener byte) ([]byte, error) {
	closes := func(c byte) bool {
		switch opener {
		case '"', '\'', '`':
			return c == opener
		case '(':
			return c == ')' || c == '"' || c == '\''
		}
		return c == '"' || c == '\'' || c == '`' || c == ')' || c == '<' || c == '>' || c <= ' '
	}
	if strings.Contains(strings.ToLower(params), ";base64") {
		end := at
		for end < len(b) && (isBase64(b[end]) || b[end] == '\n' || b[end] == '\r' || b[end] == ' ' || b[end] == '\t') {
			end++
		}
		payload := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, string(b[at:end]))
		payload = strings.TrimRight(payload, "=")
		// A quoted payload must end at its closing quote: anything else is
		// a string a script goes on to build, or a file cut short.
		if payload == "" || opener != 0 && (end == len(b) || !closes(b[end])) {
			return nil, errNoPayload
		}
		return base64.RawStdEncoding.DecodeString(payload)
	}
	end := at
	for end < len(b) && !closes(b[end]) {
		end++
	}
	if end == at || opener != 0 && end == len(b) {
		return nil, errNoPayload
	}
	raw := string(b[at:end])
	if un, err := url.PathUnescape(raw); err == nil {
		raw = un
	}
	return []byte(raw), nil
}

// findEmbedded collects the images and SVGs in b's data: URIs, up to
// MaxEmbedded and MaxEmbeddedSize in all; an embedded SVG contributes its
// text and, up to embedDepth deep, its own embedded images. Payloads that
// are neither images nor markup are ignored, as are empty, unclosed and
// script-built ones; only a payload claiming to be an image that does not
// decode, or an image that cannot be read, is unscreenable.
func (d *decoder) findEmbedded(ctx context.Context, b []byte, depth int, e *embedded, count *int) error {
	for _, m := range dataURI.FindAllSubmatchIndex(b, -1) {
		if m[0] > 0 && isWordByte(b[m[0]-1]) {
			continue // "metadata:", not a URI
		}
		var opener byte
		if m[0] > 0 {
			opener = b[m[0]-1]
		}
		claimsImage := m[2] >= 0 && strings.HasPrefix(strings.ToLower(string(b[m[2]:m[3]])), "image/")
		raw, err := dataPayload(b, m[1], string(b[m[4]:m[5]]), opener)
		switch {
		case errors.Is(err, errNoPayload):
			continue
		case err != nil || len(raw) == 0:
			if claimsImage {
				e.why = append(e.why, "an embedded image that does not decode")
			}
			continue
		}
		c, _ := sniff(head(raw))
		if c != classMarkup && c != classImage && c != classIcon && c != classOtherImage {
			continue
		}
		if *count >= MaxEmbedded {
			e.why = append(e.why, fmt.Sprintf("more than %d embedded images", MaxEmbedded))
			return nil
		}
		*count++
		if c == classMarkup {
			if depth < embedDepth {
				e.text = append(e.text, markupText(raw))
				if err := d.findEmbedded(ctx, raw, depth+1, e, count); err != nil {
					return err
				}
			} else {
				e.why = append(e.why, "embedded images nested too deep")
			}
			continue
		}
		ins, err := d.imageInputs(ctx, raw)
		var u unscreenable
		switch {
		case errors.As(err, &u):
			e.why = append(e.why, "embedded "+u.what)
		case ctx.Err() != nil:
			return fmt.Errorf("screen embedded images: %w", ctx.Err())
		case err != nil:
			e.why = append(e.why, "an embedded image that could not be read")
		default:
			for _, in := range ins {
				if e.size += len(in.Image); e.size > MaxEmbeddedSize {
					e.why = append(e.why, "embedded images over 64 MiB in all")
					return nil
				}
				e.images = append(e.images, in)
			}
		}
	}
	return nil
}

func isWordByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-'
}

// markupText is the text a reader sees in an HTML, XML or SVG file: the A2.6
// plain-text conversion for HTML, plus the text of SVG elements (which
// that conversion leaves out), and all character data of an XML or SVG
// file.
func markupText(b []byte) string {
	if c, t := sniff(head(b)); c == classMarkup && !strings.HasPrefix(t, "text/html") {
		return xmlText(b, false)
	}
	_, txt := deploys.Render(b, &url.URL{Path: "/"})
	if svg := xmlText(b, true); svg != "" {
		return string(txt) + "\n" + svg
	}
	return string(txt)
}

// xmlText collects b's character data, skipping <script> and <style>, and
// with inSVG only inside <svg> elements. The tokenizer is lenient (HTML
// entities, unclosed elements); it stops at the first error, keeping what
// it found.
func xmlText(b []byte, inSVG bool) string {
	if inSVG && !bytes.Contains(bytes.ToLower(b), []byte("<svg")) {
		return ""
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	var out strings.Builder
	svg, skip := 0, 0
	for out.Len() < MaxTextChunks*TextChunk*4 {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "svg":
				svg++
			case "script", "style":
				skip++
			}
		case xml.EndElement:
			switch strings.ToLower(t.Name.Local) {
			case "svg":
				svg = max(0, svg-1)
			case "script", "style":
				skip = max(0, skip-1)
			}
		case xml.CharData:
			if skip == 0 && (!inSVG || svg > 0) {
				if text := strings.TrimSpace(string(t)); text != "" {
					out.WriteString(text)
					out.WriteByte('\n')
				}
			}
		}
	}
	return strings.TrimSpace(out.String())
}

func isBase64(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '+' || c == '/' || c == '='
}

// gunzipHash returns the SHA-256 of what r, a gzip file, decompresses to,
// or "" when it is not exactly one gzip member with nothing after it, or
// decompresses to more than MaxDecodedSize. Browsers that are sent it as
// Content-Encoding: gzip see exactly those bytes.
func gunzipHash(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	zr, err := gzip.NewReader(br)
	if err != nil {
		var re readErr
		if errors.As(err, &re) {
			return "", re.err
		}
		return "", nil
	}
	zr.Multistream(false)
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(zr, MaxDecodedSize+1))
	if err != nil {
		var re readErr
		if errors.As(err, &re) {
			return "", re.err
		}
		return "", nil
	}
	if n > MaxDecodedSize {
		return "", nil
	}
	if _, err := br.Peek(1); !errors.Is(err, io.EOF) {
		var re readErr
		if errors.As(err, &re) {
			return "", re.err
		}
		return "", nil // a second member or trailing bytes
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readErr marks a failure to read the blob itself (retried), as opposed
// to bytes that are not gzip.
type readErr struct{ err error }

func (e readErr) Error() string { return e.err.Error() }
func (e readErr) Unwrap() error { return e.err }

// blobReader wraps blob reads so their failures are told from bad data.
type blobReader struct{ r io.Reader }

func (b blobReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = readErr{err}
	}
	return n, err
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
