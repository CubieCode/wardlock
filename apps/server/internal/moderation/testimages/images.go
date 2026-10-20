// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package testimages builds real, tiny images for screening tests: PNGs
// that carry a marker the moderationtest Fake can flag, and GIFs.
package testimages

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"math"
)

// PNG returns a real 4×4 grey PNG that carries marker in a tEXt chunk, so
// the Fake flags it when sent as it is. pad adds that many bytes of a
// second tEXt chunk, to make it as large as a test needs.
func PNG(marker string, pad int) string {
	return SizedPNG(marker, 4, 4, pad)
}

// SizedPNG is PNG with a w×h canvas.
func SizedPNG(marker string, w, h, pad int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0x80, 0x80, 0x80, 0xff}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	b := buf.Bytes()
	// After the signature (8) and IHDR (8 + 13 + 4).
	at := 8 + 25
	chunks := chunk("tEXt", []byte("Comment\x00"+marker))
	if pad > 0 {
		chunks = append(chunks, chunk("tEXt", append([]byte("Padding\x00"), bytes.Repeat([]byte("p"), pad)...))...)
	}
	return string(b[:at]) + string(chunks) + string(b[at:])
}

// PNGHeader returns the start of a PNG claiming a w×h canvas, with pad
// bytes of a tEXt chunk and no image data: enough for its size to be
// read, without building the canvas.
func PNGHeader(w, h uint32, pad int) string {
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // 8-bit RGBA
	out := append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", ihdr)...)
	out = append(out, chunk("tEXt", append([]byte("Padding\x00"), bytes.Repeat([]byte("p"), pad)...))...)
	return string(out)
}

func chunk(typ string, data []byte) []byte {
	n := len(data)
	if n < 0 || n > math.MaxUint32 {
		panic("PNG chunk too large")
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(n))
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

// GIF returns a real GIF with one 4×4 frame per colour.
func GIF(colours ...color.Color) string {
	g := &gif.GIF{}
	for _, c := range colours {
		f := image.NewPaletted(image.Rect(0, 0, 4, 4), palette.Plan9)
		draw.Draw(f, f.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		panic(err)
	}
	return buf.String()
}
