package imagegen

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
)

// Style names a background generator.
type Style string

// The available background styles. Each one is a different family of Persian
// decorative geometry, so consecutive challenges look meaningfully different
// rather than being recolourings of one template.
const (
	StyleGirih   Style = "girih"   // interlaced star tessellation
	StyleMosaic  Style = "mosaic"  // rotated tile field
	StyleArches  Style = "arches"  // repeated pointed arches
	StyleWaves   Style = "waves"   // flowing bands
	StyleMedal   Style = "medal"   // carpet medallion
	StyleLattice Style = "lattice" // window screen
)

// Styles lists every available style.
func Styles() []Style {
	return []Style{StyleGirih, StyleMosaic, StyleArches, StyleWaves, StyleMedal, StyleLattice}
}

// Background renders a fresh w×h background. Each call with a different rng
// state produces different geometry, colours and texture.
func Background(rng *rand.Rand, w, h int) *image.RGBA {
	return BackgroundStyle(rng, w, h, Styles()[rng.IntN(len(Styles()))], RandomPalette(rng))
}

// BackgroundStyle renders a background with an explicit style and palette.
func BackgroundStyle(rng *rand.Rand, w, h int, style Style, pal Palette) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	n := newNoise(rng)
	paintGradient(img, pal, rng)

	switch style {
	case StyleGirih:
		drawGirih(img, rng, pal)
	case StyleMosaic:
		drawMosaic(img, rng, pal)
	case StyleArches:
		drawArches(img, rng, pal)
	case StyleWaves:
		drawWaves(img, rng, pal)
	case StyleMedal:
		drawMedallion(img, rng, pal)
	case StyleLattice:
		drawLattice(img, rng, pal)
	default:
		drawMosaic(img, rng, pal)
	}

	applyTexture(img, n, rng)
	applyVignette(img)
	return img
}

// paintGradient lays down a diagonal gradient between two palette tones, with
// a few soft radial blooms on top.
func paintGradient(img *image.RGBA, pal Palette, rng *rand.Rand) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	from := pal.Base
	to := pal.Tone(rng.IntN(3))
	angle := rng.Float64() * 2 * math.Pi
	dx, dy := math.Cos(angle), math.Sin(angle)
	// Project each pixel onto the gradient axis and normalise to [0,1].
	span := math.Abs(dx)*float64(w) + math.Abs(dy)*float64(h)
	off := math.Min(0, dx*float64(w)) + math.Min(0, dy*float64(h))

	for y := range h {
		for x := range w {
			t := (dx*float64(x) + dy*float64(y) - off) / span
			c := lerpColor(from, to, t)
			i := img.PixOffset(x, y)
			img.Pix[i+0] = c.R
			img.Pix[i+1] = c.G
			img.Pix[i+2] = c.B
			img.Pix[i+3] = 255
		}
	}

	blooms := 2 + rng.IntN(3)
	for range blooms {
		cx := rng.Float64() * float64(w)
		cy := rng.Float64() * float64(h)
		r := float64(min(w, h)) * (0.3 + rng.Float64()*0.5)
		col := pal.Tone(rng.IntN(5))
		radialBloom(img, cx, cy, r, col, 0.35)
	}
}

// radialBloom adds a soft circular glow.
func radialBloom(img *image.RGBA, cx, cy, r float64, col color.RGBA, strength float64) {
	b := img.Bounds()
	x0, x1 := max(int(cx-r), 0), min(int(cx+r)+1, b.Dx())
	y0, y1 := max(int(cy-r), 0), min(int(cy+r)+1, b.Dy())
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / r
			if d >= 1 {
				continue
			}
			// Smooth falloff so the bloom has no visible edge.
			a := (1 - d) * (1 - d) * strength
			i := img.PixOffset(x, y)
			blendPixel(img.Pix[i:i+4], col, a)
		}
	}
}

// drawGirih tessellates eight-pointed stars with connecting strapwork, the
// classic khatam motif found on Safavid tilework.
func drawGirih(img *image.RGBA, rng *rand.Rand, pal Palette) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	cell := 34.0 + rng.Float64()*26
	phase := rng.Float64() * cell
	lineCol := pal.Accent
	fillCol := pal.Light

	for gy := -1.0; gy*cell < h+cell; gy++ {
		for gx := -1.0; gx*cell < w+cell; gx++ {
			cx := gx*cell + phase
			cy := gy*cell + phase
			// Alternate rows are offset half a cell, giving the interlocking
			// arrangement rather than a plain grid.
			if int(gy)%2 != 0 {
				cx += cell / 2
			}
			star := StarPath(cx, cy, cell*0.46, cell*0.19, 8, rng.Float64()*math.Pi)
			FillPath(img, star, fillCol, 0.10)
			StrokePath(img, star, 1.1, lineCol, 0.30)

			rose := PolygonPath(cx, cy, cell*0.17, 8, math.Pi/8)
			FillPath(img, rose, lineCol, 0.16)
		}
	}
}

// drawMosaic fills the frame with rotated square tiles of varying tone, in the
// manner of a haft-rang tile panel.
func drawMosaic(img *image.RGBA, rng *rand.Rand, pal Palette) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	cell := 22.0 + rng.Float64()*18
	for y := -cell; y < h+cell; y += cell {
		for x := -cell; x < w+cell; x += cell {
			jx := x + (rng.Float64()-0.5)*cell*0.2
			jy := y + (rng.Float64()-0.5)*cell*0.2
			size := cell * (0.32 + rng.Float64()*0.2)
			rot := rng.Float64() * math.Pi / 2
			tile := PolygonPath(jx, jy, size, 4, rot)
			FillPath(img, tile, pal.Tone(rng.IntN(6)), 0.35+rng.Float64()*0.3)
			if rng.IntN(5) == 0 {
				StrokePath(img, tile, 1, pal.Accent, 0.4)
			}
		}
	}
}

// drawArches repeats the pointed arch profile of an iwan facade.
func drawArches(img *image.RGBA, rng *rand.Rand, pal Palette) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	cols := 3 + rng.IntN(4)
	step := w / float64(cols)
	for i := range cols + 1 {
		cx := float64(i) * step
		width := step * (0.62 + rng.Float64()*0.2)
		top := h * (0.12 + rng.Float64()*0.2)
		arch := ArchPath(cx, h*1.05, width, h-top)
		FillPath(img, arch, pal.Tone(i), 0.30)
		StrokePath(img, arch, 1.4, pal.Accent, 0.28)
		inner := ArchPath(cx, h*1.05, width*0.6, (h-top)*0.78)
		StrokePath(img, inner, 1, pal.Light, 0.24)
	}
}

// drawWaves lays down flowing bands, echoing the cloud-and-water scrollwork
// used as border filler in miniatures.
func drawWaves(img *image.RGBA, rng *rand.Rand, pal Palette) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	bands := 5 + rng.IntN(5)
	for i := range bands {
		amp := 6 + rng.Float64()*16
		freq := (0.6 + rng.Float64()*1.6) * 2 * math.Pi / w
		phase := rng.Float64() * 2 * math.Pi
		base := h * float64(i+1) / float64(bands+1)
		thick := 4 + rng.Float64()*14

		var top, bottom []Point
		for x := -10.0; x <= w+10; x += 4 {
			y := base + amp*math.Sin(freq*x+phase)
			top = append(top, Point{x, y - thick/2})
			bottom = append(bottom, Point{x, y + thick/2})
		}
		for j := len(bottom) - 1; j >= 0; j-- {
			top = append(top, bottom[j])
		}
		FillPath(img, Path{top}, pal.Tone(i), 0.4)
	}
}

// drawMedallion renders a central carpet medallion with radiating petals.
func drawMedallion(img *image.RGBA, rng *rand.Rand, pal Palette) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	cx, cy := w/2, h/2
	r := math.Min(w, h) * (0.34 + rng.Float64()*0.12)

	for i, f := range []float64{1.25, 1.0, 0.72, 0.45} {
		pts := 8 + 2*rng.IntN(4)
		petal := StarPath(cx, cy, r*f, r*f*0.62, pts, float64(i)*0.2)
		FillPath(img, petal, pal.Tone(i), 0.30)
		StrokePath(img, petal, 1.2, pal.Accent, 0.22)
	}
	FillPath(img, PolygonPath(cx, cy, r*0.2, 6, 0), pal.Light, 0.5)

	// Corner spandrels, as on a medallion carpet.
	for _, c := range [][2]float64{{0, 0}, {w, 0}, {0, h}, {w, h}} {
		FillPath(img, StarPath(c[0], c[1], r*0.55, r*0.3, 6, rng.Float64()), pal.Tone(rng.IntN(3)), 0.25)
	}
}

// drawLattice draws an orosi-style window screen of interlocking hexagons.
func drawLattice(img *image.RGBA, rng *rand.Rand, pal Palette) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	r := 16.0 + rng.Float64()*14
	dx := r * math.Sqrt(3)
	dy := r * 1.5
	for row := -1.0; row*dy < h+dy; row++ {
		for col := -1.0; col*dx < w+dx; col++ {
			cx := col*dx + math.Mod(row, 2)*dx/2
			cy := row * dy
			hexa := PolygonPath(cx, cy, r*0.92, 6, math.Pi/6)
			FillPath(img, hexa, pal.Tone(int(row+col)), 0.22)
			StrokePath(img, hexa, 1.2, pal.Accent, 0.3)
		}
	}
}

// applyTexture modulates brightness with fractal noise and adds fine grain, so
// flat areas still carry high-frequency detail.
func applyTexture(img *image.RGBA, n *noise2D, rng *rand.Rand) {
	b := img.Bounds()
	scale := 0.018 + rng.Float64()*0.02
	grain := 5.0 + rng.Float64()*7
	for y := range b.Dy() {
		for x := range b.Dx() {
			v := n.fbm(float64(x)*scale, float64(y)*scale, 4, 2.1, 0.5)
			f := 1 + v*0.22
			g := (rng.Float64() - 0.5) * grain
			i := img.PixOffset(x, y)
			img.Pix[i+0] = clampByte(float64(img.Pix[i+0])*f + g)
			img.Pix[i+1] = clampByte(float64(img.Pix[i+1])*f + g)
			img.Pix[i+2] = clampByte(float64(img.Pix[i+2])*f + g)
		}
	}
}

// applyVignette darkens the edges, which also discourages solvers that look
// for the brightest region.
func applyVignette(img *image.RGBA) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	cx, cy := w/2, h/2
	maxD := math.Hypot(cx, cy)
	for y := range b.Dy() {
		for x := range b.Dx() {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / maxD
			f := 1 - 0.35*d*d
			i := img.PixOffset(x, y)
			img.Pix[i+0] = clampByte(float64(img.Pix[i+0]) * f)
			img.Pix[i+1] = clampByte(float64(img.Pix[i+1]) * f)
			img.Pix[i+2] = clampByte(float64(img.Pix[i+2]) * f)
		}
	}
}

func clampByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(v)
	}
}
