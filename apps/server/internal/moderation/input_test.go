// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image/color"
	"image/color/palette"
	"image/png"
	"strings"
	"testing"

	"wardlock.nz/server/internal/moderation/testimages"
)

func TestSniff(t *testing.T) {
	for b, want := range map[string]class{
		"\x89PNG\r\n\x1a\nrest":                    classImage,
		"\xff\xd8\xff\xe0rest":                     classImage,
		"RIFF\x00\x00\x00\x00WEBPVP":               classImage,
		"GIF89a\x01\x00":                           classImage,
		"\x00\x00\x01\x00\x01\x00":                 classIcon,
		"BM bitmap":                                classOtherImage,
		"\x00\x00\x00\x1cftypavif\x00\x00":         classOtherImage,
		"\x00\x00\x00\x18ftypheic\x00\x00":         classOtherImage,
		"II*\x00tiff":                              classOtherImage,
		"\x00\x00\x00\x18ftypisom\x00\x00":         classOther, // MP4 video
		"<!DOCTYPE html><p>hi":                     classMarkup,
		"<?xml version=\"1.0\"?><svg/>":            classMarkup,
		"<svg xmlns='http://www.w3.org/2000/svg'>": classMarkup,
		"\xef\xbb\xbf<!-- c --> <svg>":             classMarkup,
		"plain words":                              classText,
		"<svgx>":                                   classText,
		"\x1f\x8b\x08\x00":                         classGzip,
		"\x00\x01\x02\x03":                         classOther,
	} {
		if got, _ := sniff([]byte(b)); got != want {
			t.Errorf("sniff(%.16q) = %d, want %d", b, got, want)
		}
	}
}

func TestGIFSplit(t *testing.T) {
	g := testimages.GIF(color.Black, color.White, color.Black)
	hdr, frames := gifSplit([]byte(g))
	if len(hdr) < 13 || len(frames) != 3 {
		t.Fatalf("split: header %d, %d frames", len(hdr), len(frames))
	}
	if _, frames := gifSplit([]byte(g + "junk")); len(frames) != 3 {
		t.Fatalf("trailing junk: %d frames", len(frames))
	}
	if _, frames := gifSplit([]byte(g[:len(g)-6])); len(frames) != 2 {
		t.Fatalf("cut short: %d frames", len(frames))
	}
	if _, frames := gifSplit([]byte("GIF89a\x01")); frames != nil {
		t.Fatal("short header split")
	}
}

// TestGIFSampling checks half the frames sent are evenly spaced across
// the whole animation, first and last included, and half picked at random,
// that an animation over the frame cap is unscreenable, and that a decode
// stops when its context ends.
func TestGIFSampling(t *testing.T) {
	cs := make([]color.Color, 31)
	for i := range cs {
		cs[i] = palette.Plan9[i*8] // exact palette colours, all different
	}
	d := newDecoder()
	d.pick = func(n, max int) []int { return []int{0, 1, 2, 3, 4}[:n] } // the first others: 1, 2, 3, 4, 5
	ins, err := d.gifInputs(t.Context(), []byte(testimages.GIF(cs...)))
	if err != nil || len(ins) != GIFFrames {
		t.Fatalf("%d frames, %v; want %d", len(ins), err, GIFFrames)
	}
	// Even: 0, 7, 15, 22, 30; random: 1 to 5. Sent in frame order.
	for j, frame := range []int{0, 1, 2, 3, 4, 5, 7, 15, 22, 30} {
		img, err := png.Decode(bytes.NewReader(ins[j].Image))
		if err != nil {
			t.Fatal(err)
		}
		want := color.RGBAModel.Convert(cs[frame])
		if got := color.RGBAModel.Convert(img.At(0, 0)); got != want {
			t.Errorf("sent %d: %v, want frame %d's %v", j, got, frame, want)
		}
	}
	// A short animation is sent whole.
	if ins, err := newDecoder().gifInputs(t.Context(), []byte(testimages.GIF(cs[:4]...))); err != nil || len(ins) != 4 {
		t.Fatalf("short animation: %d, %v", len(ins), err)
	}
	long := make([]color.Color, MaxGIFFrames+1)
	for i := range long {
		long[i] = color.Black
	}
	var u unscreenable
	if _, err := newDecoder().gifInputs(t.Context(), []byte(testimages.GIF(long...))); !errors.As(err, &u) {
		t.Fatalf("over the frame cap: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := newDecoder().gifInputs(ctx, []byte(testimages.GIF(cs...))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled decode: %v", err)
	}
}

// TestRandomFrames checks the random frames are distinct, in range, and
// differ from one screening to the next.
func TestRandomFrames(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		got := randomFrames(5, 1000)
		set := map[int]bool{}
		for _, i := range got {
			if i < 0 || i >= 1000 || set[i] {
				t.Fatalf("randomFrames = %v", got)
			}
			set[i] = true
		}
		seen[fmt.Sprint(got)] = true
	}
	if len(seen) < 19 {
		t.Fatalf("only %d different picks in 20", len(seen))
	}
	if got := gifSample(1000, randomFrames); len(got) != GIFFrames || !got[0] || !got[999] {
		t.Fatalf("gifSample = %v", got)
	}
}

func TestDataPayload(t *testing.T) {
	for _, c := range []struct {
		doc, want string
	}{
		{`url("data:image/svg+xml;utf8,<svg a='1'>x y</svg>")`, `<svg a='1'>x y</svg>`},
		{`url('data:image/svg+xml,%3Csvg%3E')`, `<svg>`},
		{`url(data:image/svg+xml,%3Csvg%3Ehi%3C/svg%3E) more`, `<svg>hi</svg>`},
		{`src=data:image/svg+xml,%3Csvg%3E next`, `<svg>`},
		{`"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("PNG!")) + `"`, "PNG!"},
	} {
		m := dataURI.FindSubmatchIndex([]byte(c.doc))
		var opener byte
		if m[0] > 0 {
			opener = c.doc[m[0]-1]
		}
		got, err := dataPayload([]byte(c.doc), m[1], c.doc[m[4]:m[5]], opener)
		if err != nil || string(got) != c.want {
			t.Errorf("dataPayload(%q) = %q, %v; want %q", c.doc, got, err, c.want)
		}
	}
}

func TestImageInputs(t *testing.T) {
	png := testimages.PNG("x", 0)
	ins, err := newDecoder().imageInputs(t.Context(), []byte(png))
	if err != nil || len(ins) != 1 || string(ins[0].Image) != png || ins[0].ImageType != "image/png" {
		t.Fatalf("PNG: %+v, %v", ins, err)
	}
	for b, what := range map[string]string{
		"\x89PNG\r\n\x1a\ndamaged": "a damaged png image",
		"BM bitmap":                "a BMP image",
		"RIFF\x00\x00\x00\x00WEBPVP" + strings.Repeat("x", MaxImageSize): "a WEBP image over 20 MB",
		"GIF89a\x01\x00\x01\x00\x00\x00\x00\x3b":                         "a GIF image with no readable frame",
		"\x00\x00\x01\x00\x00\x00":                                       "an icon without PNG images",
	} {
		_, err := newDecoder().imageInputs(t.Context(), []byte(b))
		var u unscreenable
		if !errors.As(err, &u) || u.what != what {
			t.Errorf("imageInputs(%.16q) = %v, want unscreenable %q", b, err, what)
		}
	}
	// A canvas too large to decode is unscreenable, however small the file.
	huge := testimages.PNGHeader(10_000, 10_000, MaxImageSize)
	var u unscreenable
	if _, err := newDecoder().imageInputs(t.Context(), []byte(huge)); !errors.As(err, &u) {
		t.Fatalf("huge canvas: %v", err)
	}
}

func TestFindEmbedded(t *testing.T) {
	png := testimages.PNG("x", 0)
	// Line breaks inside the payload and missing padding are tolerated.
	payload := base64.StdEncoding.EncodeToString([]byte(png))
	wrapped := payload[:20] + "\n  " + strings.TrimRight(payload[20:], "=")
	page := `<img src="data:image/png;base64,` + wrapped + `"> <div style="background:url(DATA:image/bmp;base64,Qk0gYml0bWFw)">`
	var e embedded
	n := 0
	_ = newDecoder().findEmbedded(t.Context(), []byte(page), 0, &e, &n)
	if len(e.images) != 1 || string(e.images[0].Image) != png || len(e.why) != 1 || e.why[0] != "embedded a BMP image" {
		t.Fatalf("embedded: %d images, why %q", len(e.images), e.why)
	}
}

func TestGunzipHash(t *testing.T) {
	gz := func(members ...string) []byte {
		var buf bytes.Buffer
		for _, m := range members {
			w := gzip.NewWriter(&buf)
			_, _ = w.Write([]byte(m))
			_ = w.Close()
		}
		return buf.Bytes()
	}
	sum := sha256.Sum256([]byte("hello"))
	for b, want := range map[string]string{
		string(gz("hello")):          hex.EncodeToString(sum[:]),
		string(gz("hel", "lo")):      "",
		string(gz("hello")) + "junk": "",
		"\x1f\x8b\x08 not gzip":      "",
		string(gz("hello")[:10]):     "",
	} {
		got, err := gunzipHash(blobReader{strings.NewReader(b)})
		if err != nil || got != want {
			t.Errorf("gunzipHash(%.12q) = %q, %v; want %q", b, got, err, want)
		}
	}
	// A failure to read the blob is an error, not bad bytes.
	if _, err := gunzipHash(blobReader{failing{}}); err == nil {
		t.Fatal("read failure not reported")
	}
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestMarkupText(t *testing.T) {
	svg := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><title>Logo</title><style>.a{}</style><text>Hello &amp; welcome</text></svg>`
	if got := markupText([]byte(svg)); got != "Logo\nHello & welcome" {
		t.Errorf("SVG text = %q", got)
	}
	html := `<p>Para</p><svg><text>Drawn words</text></svg><script>no()</script>`
	if got := markupText([]byte(html)); !strings.Contains(got, "Para") || !strings.Contains(got, "Drawn words") || strings.Contains(got, "no()") {
		t.Errorf("HTML text = %q", got)
	}
}
