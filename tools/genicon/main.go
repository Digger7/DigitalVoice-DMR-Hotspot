// Command genicon renders the application icon (digital DMR signal):
// a 4FSK burst waveform, two TDMA slot bars and a pixel-font "DMR" label.
// Pure stdlib.
//
// Usage:
//
//	go run ./tools/genicon [-png path] [-size N] [-ico path]
//
// The icon is drawn at 4x resolution and box-downsampled for antialiasing.
// The ICO output embeds PNG-compressed layers 16..256 (Vista+ compatible).
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const (
	supersample = 4
	master      = 1024
)

var (
	colBgTop    = mkrgb(0x1c, 0x24, 0x31)
	colBgBottom = mkrgb(0x0d, 0x12, 0x1a)
	colBorder   = mkrgb(0x3d, 0x4d, 0x63)
	colWaveTop  = mkrgb(0x5e, 0xd6, 0xff)
	colWaveBot  = mkrgb(0x2d, 0x6c, 0xdf)
	colSlot1    = mkrgb(0x43, 0xa0, 0x47)
	colSlot2    = mkrgb(0xd9, 0xa4, 0x41)
	colText     = mkrgb(0xd9, 0xe2, 0xef)
	dst         *image.RGBA
)

func mkrgb(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 255} }

func main() {
	pngOut := flag.String("png", "internal/webui/icon.png", "output PNG path ('0' = skip PNG)")
	size := flag.Int("size", master, "output PNG size")
	icoOut := flag.String("ico", "", "output ICO path (optional)")
	flag.Parse()

	src := render()
	if *pngOut != "0" {
		var img image.Image = src
		if *size != master*supersample {
			img = downsample(src, *size)
		}
		buf, err := encodePNG(img)
		if err != nil {
			fail(err)
		}
		if err := write(*pngOut, buf); err != nil {
			fail(err)
		}
		fmt.Printf("icon: wrote %s (%dx%d px)\n", *pngOut, *size, *size)
	}
	if *icoOut != "" {
		buf, err := buildICO(src)
		if err != nil {
			fail(err)
		}
		if err := write(*icoOut, buf); err != nil {
			fail(err)
		}
		fmt.Printf("icon: wrote %s\n", *icoOut)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "genicon:", err); os.Exit(1) }

func write(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// render draws the master icon. All geometry uses master-space coords
// (1024x1024); the primitives multiply by supersample internally.
func render() *image.RGBA {
	dst = image.NewRGBA(image.Rect(0, 0, master*supersample, master*supersample))
	g := master * supersample
	s := float64(master)

	// Background rounded square with vertical gradient and thin border.
	radius := s * 0.225
	border := s * 0.0055
	for y := 0; y < g; y++ {
		my := (float64(y) + 0.5) / supersample
		bg := lerp(colBgTop, colBgBottom, my/s)
		for x := 0; x < g; x++ {
			mx := (float64(x) + 0.5) / supersample
			d := sdRoundBox(mx, my, s/2, s/2, s/2, s/2, radius)
			switch {
			case d <= -border:
				dst.SetRGBA(x, y, bg)
			case d <= 0:
				dst.SetRGBA(x, y, lerp(colBorder, bg, 1+d/border))
			}
		}
	}

	// 4FSK burst: bars on four discrete levels (-3/-1/+1/+3 amplitudes),
	// symmetric TDMA burst envelope.
	levels := []float64{3, 1, 1, 3, 3, 1, 1, 3, 1, 3, 3, 1, 1, 3, 3, 1, 3, 3, 1, 1, 3, 1, 3, 3, 1, 1, 3, 3}
	maxH := s * 0.26
	bw := s * 0.0215
	gap := s * 0.0063
	totalW := float64(len(levels))*bw + float64(len(levels)-1)*gap
	bx := (s - totalW) / 2
	cy := s * 0.43
	for i, lv := range levels {
		h := maxH * (0.42 + 0.58*(lv/3))
		col := lerp(colWaveTop, colWaveBot, (lv-1)/2)
		capsule(bx+float64(i)*(bw+gap), cy-h/2, bw, h, col)
	}

	// Two TDMA slot bars.
	sy, sh := s*0.645, s*0.052
	margin, gapS := s*0.117, s*0.019
	sw := (s - 2*margin - gapS) / 2
	capsule(margin, sy, sw, sh, colSlot1)
	capsule(margin+sw+gapS, sy, sw, sh, colSlot2)

	// "DMR" label in a 5x7 pixel font.
	label()
	return dst
}

var font = map[rune][7]string{
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'M': {"10001", "11011", "10101", "10001", "10001", "10001", "10001"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
}

func label() {
	const cell, gapL = 18, 24
	letterW := 5 * cell
	tx := (master - (3*letterW + 2*gapL)) / 2
	ty := 750
	x := float64(tx)
	for _, ch := range "DMR" {
		for row, line := range font[ch] {
			for col, c := range line {
				if c == '1' {
					fillRect(x+float64(col*cell), float64(ty+row*cell), cell, cell, colText)
				}
			}
		}
		x += float64(letterW + gapL)
	}
}

// fillRect fills an axis-aligned rect given in master-space coords.
func fillRect(mx, my, mw, mh float64, col color.RGBA) {
	x0, y0 := int(mx*supersample), int(my*supersample)
	x1, y1 := int((mx+mw)*supersample), int((my+mh)*supersample)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			dst.SetRGBA(x, y, col)
		}
	}
}

// capsule fills a vertical rounded bar (digital burst symbol).
func capsule(mx, my, mw, mh float64, col color.RGBA) {
	r := math.Min(mw, mh) / 2
	x0, y0 := int((mx-1)*supersample), int((my-1)*supersample)
	x1, y1 := int((mx+mw+1)*supersample), int((my+mh+1)*supersample)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			px := (float64(x) + 0.5) / supersample
			py := (float64(y) + 0.5) / supersample
			if sdRoundBox(px, py, mx+mw/2, my+mh/2, mw/2, mh/2, r) <= 0 {
				dst.SetRGBA(x, y, col)
			}
		}
	}
}

// sdRoundBox returns the signed distance to a rounded box (iq's formula).
func sdRoundBox(px, py, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(px-cx) - hw + r
	qy := math.Abs(py-cy) - hh + r
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

func lerp(a, b color.RGBA, t float64) color.RGBA {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

// downsample box-filters the master render (master*supersample px) to size px.
func downsample(src *image.RGBA, size int) *image.RGBA {
	// Transparent corners of the rounded square are handled by averaging
	// premultiplied samples, then un-premultiplying for straight-alpha PNG.
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		sy0 := int(float64(y) * float64(sh) / float64(size))
		sy1 := int(float64(y+1) * float64(sh) / float64(size))
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < size; x++ {
			sx0 := int(float64(x) * float64(sw) / float64(size))
			sx1 := int(float64(x+1) * float64(sw) / float64(size))
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, b, a int
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					i := src.PixOffset(sx, sy)
					r += int(src.Pix[i])
					g += int(src.Pix[i+1])
					b += int(src.Pix[i+2])
					a += int(src.Pix[i+3])
				}
			}
			if a == 0 {
				out.SetRGBA(x, y, color.RGBA{})
				continue
			}
			n := (sx1 - sx0) * (sy1 - sy0)
			// Un-premultiply: straight C = premul v * 255 / A. The master has
			// only opaque and fully transparent pixels, (255*v)/A is exact.
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(float64(r) * 255 / float64(a)),
				G: uint8(float64(g) * 255 / float64(a)),
				B: uint8(float64(b) * 255 / float64(a)),
				A: uint8(float64(a) / float64(n)),
			})
		}
	}
	return out
}

// buildICO packs PNG layers 16..256 into an .ico container.
func buildICO(src *image.RGBA) ([]byte, error) {
	sizes := []int{16, 32, 48, 64, 128, 256}
	payloads := make([][]byte, len(sizes))
	for i, s := range sizes {
		b, err := encodePNG(downsample(src, s))
		if err != nil {
			return nil, err
		}
		payloads[i] = b
	}
	var buf bytes.Buffer
	var dir [6]byte
	binary.LittleEndian.PutUint16(dir[2:], 1)
	binary.LittleEndian.PutUint16(dir[4:], uint16(len(sizes)))
	buf.Write(dir[:])
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		var e [16]byte
		e[0], e[1] = byte(s%256), byte(s%256)
		e[4], e[5] = 1, 32
		binary.LittleEndian.PutUint32(e[8:], uint32(len(payloads[i])))
		binary.LittleEndian.PutUint32(e[12:], uint32(offset))
		buf.Write(e[:])
		offset += len(payloads[i])
	}
	for _, p := range payloads {
		buf.Write(p)
	}
	return buf.Bytes(), nil
}
