// Package render draws frames for the rack display.
package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var (
	bg     = color.RGBA{0x0b, 0x0f, 0x17, 0xff}
	fg     = color.RGBA{0xe6, 0xed, 0xf3, 0xff}
	muted  = color.RGBA{0x7d, 0x8b, 0x99, 0xff}
	accent = color.RGBA{0x3f, 0xb9, 0x50, 0xff}
	bars   = []color.RGBA{
		{0xff, 0xff, 0xff, 0xff}, {0xff, 0xff, 0x00, 0xff}, {0x00, 0xff, 0xff, 0xff}, {0x00, 0xff, 0x00, 0xff},
		{0xff, 0x00, 0xff, 0xff}, {0xff, 0x00, 0x00, 0xff}, {0x00, 0x00, 0xff, 0xff}, {0x00, 0x00, 0x00, 0xff},
	}
)

// TestPattern renders a calibration frame: 1px edge border, corner markers,
// SMPTE-style bars, a grayscale ramp, a sweeping bar driven by frame (proves
// the display is refreshing), and status text.
type TestPattern struct {
	title, mono font.Face
}

// NewTestPattern loads embedded Go fonts.
func NewTestPattern() (*TestPattern, error) {
	title, err := face(gobold.TTF, 44)
	if err != nil {
		return nil, err
	}
	mono, err := face(gomono.TTF, 20)
	if err != nil {
		return nil, err
	}
	return &TestPattern{title: title, mono: mono}, nil
}

func face(ttf []byte, size float64) (font.Face, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// Draw renders the pattern into img. info is shown as a status line.
func (tp *TestPattern) Draw(img *image.RGBA, frame int, now time.Time, info string) {
	r := img.Bounds()
	w, h := r.Dx(), r.Dy()
	fill(img, r, bg)

	// Color bars on the right third, grayscale ramp beneath them.
	barsX := w * 2 / 3
	barW := (w - barsX) / len(bars)
	rampY := h * 3 / 4
	for i, c := range bars {
		fill(img, image.Rect(barsX+i*barW, 0, barsX+(i+1)*barW, rampY), c)
	}
	for x := barsX; x < w; x++ {
		v := uint8(255 * (x - barsX) / max(1, w-barsX-1))
		fill(img, image.Rect(x, rampY, x+1, h), color.RGBA{v, v, v, 0xff})
	}

	// Sweep: a bar bouncing across the left panel, eased with a sine.
	panelW := barsX - 24
	t := (math.Sin(float64(frame)*0.15) + 1) / 2
	sx := 24 + int(t*float64(panelW-80))
	fill(img, image.Rect(24, h-28, barsX-24, h-24), color.RGBA{0x21, 0x26, 0x2d, 0xff})
	fill(img, image.Rect(sx, h-32, sx+80, h-20), accent)

	text(img, tp.title, 24, 64, fg, "HOMELAB RACK DISPLAY")
	text(img, tp.mono, 26, 108, muted, info)
	text(img, tp.mono, 26, 140, muted, fmt.Sprintf("%dx%d  frame %d", w, h, frame))
	text(img, tp.mono, 26, 172, accent, now.Format("2006-01-02 15:04:05 MST"))

	// Edge border + corner markers: confirms no overscan/cropping.
	border(img, r, color.RGBA{0xff, 0x00, 0x00, 0xff})
	for _, p := range []image.Point{{0, 0}, {w - 12, 0}, {0, h - 12}, {w - 12, h - 12}} {
		fill(img, image.Rect(p.X, p.Y, p.X+12, p.Y+12), color.RGBA{0xff, 0x00, 0x00, 0xff})
	}
}

func fill(img *image.RGBA, r image.Rectangle, c color.Color) {
	draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
}

func border(img *image.RGBA, r image.Rectangle, c color.Color) {
	fill(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), c)
	fill(img, image.Rect(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y), c)
	fill(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y), c)
	fill(img, image.Rect(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y), c)
}

func text(img *image.RGBA, f font.Face, x, y int, c color.Color, s string) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
}
