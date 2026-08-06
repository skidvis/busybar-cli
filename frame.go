package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
)

type displaySpec struct {
	index  int
	width  int
	height int
	format string
}

var displays = map[string]displaySpec{
	"front": {0, 72, 16, "RGB888"},
	"back":  {1, 160, 80, "L4"},
}

// decodeFrame turns the body of GET /screen into 8-bit RGB triples.
//
// The endpoint advertises image/bmp but actually returns base64 text wrapping a
// raw framebuffer. The back panel is 4-bit greyscale, two samples per byte, and
// the LOW nibble is the LEFT pixel.
func decodeFrame(raw []byte, format string, width, height int) ([]byte, error) {
	data := raw
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw))); err == nil {
		data = decoded
	}

	rgb := data
	if format != "RGB888" {
		out := make([]byte, 0, len(data)*6)
		for _, b := range data {
			for _, level := range [2]byte{b & 0x0F, b >> 4} {
				v := level * 17 // 0x0..0xF -> 0..255
				out = append(out, v, v, v)
			}
		}
		rgb = out
	}

	want := width * height * 3
	if len(rgb) < want {
		return nil, fail("short frame: got %d bytes, expected %d", len(rgb), want)
	}
	return rgb[:want], nil
}

// framePNG encodes RGB triples as a PNG, optionally nearest-neighbour upscaled
// (a 72x16 image is unviewable at 1x).
func framePNG(rgb []byte, width, height, scale int) ([]byte, error) {
	if scale < 1 {
		scale = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, width*scale, height*scale))
	for y := 0; y < height*scale; y++ {
		for x := 0; x < width*scale; x++ {
			src := ((y/scale)*width + x/scale) * 3
			off := img.PixOffset(x, y)
			copy(img.Pix[off:off+3], rgb[src:src+3])
			img.Pix[off+3] = 0xFF
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
