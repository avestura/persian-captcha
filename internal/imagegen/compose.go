package imagegen

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// EncodePNG serialises an image with the fast compression setting, since
// challenge images are generated per request and never cached.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CutPiece extracts the region of src covered by mask into a new image of the
// mask's size, with the mask as the alpha channel. The result is the draggable
// puzzle piece.
func CutPiece(src *image.RGBA, mask *Coverage, offsetX, offsetY int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, mask.W, mask.H))
	sb := src.Bounds()
	for y := range mask.H {
		for x := range mask.W {
			a := mask.A[y*mask.W+x]
			if a <= 0 {
				continue
			}
			sx, sy := x+offsetX, y+offsetY
			if sx < sb.Min.X || sy < sb.Min.Y || sx >= sb.Max.X || sy >= sb.Max.Y {
				continue
			}
			si := src.PixOffset(sx, sy)
			di := out.PixOffset(x, y)
			// image.RGBA is alpha-premultiplied, so the colour channels are
			// scaled by the coverage alongside the alpha channel. Copying the
			// colour unscaled would leave R > A on antialiased edges, which
			// the PNG encoder turns into a bright fringe.
			out.Pix[di+0] = uint8(float32(src.Pix[si+0]) * a)
			out.Pix[di+1] = uint8(float32(src.Pix[si+1]) * a)
			out.Pix[di+2] = uint8(float32(src.Pix[si+2]) * a)
			out.Pix[di+3] = uint8(a * 255)
		}
	}
	return out
}

// CarveNotch darkens the masked region of dst and adds an inner shadow, so the
// hole reads as a recess the piece can drop into.
func CarveNotch(dst *image.RGBA, mask *Coverage, offsetX, offsetY int) {
	// A blurred copy of the mask, subtracted from the sharp one, gives a band
	// that hugs the inside of the edge: a cheap inner shadow.
	blurred := &Coverage{W: mask.W, H: mask.H, A: append([]float32(nil), mask.A...)}
	blurred.Blur(2, 2)

	b := dst.Bounds()
	for y := range mask.H {
		dy := y + offsetY
		if dy < b.Min.Y || dy >= b.Max.Y {
			continue
		}
		for x := range mask.W {
			a := mask.A[y*mask.W+x]
			if a <= 0 {
				continue
			}
			dx := x + offsetX
			if dx < b.Min.X || dx >= b.Max.X {
				continue
			}
			i := dst.PixOffset(dx, dy)
			// Flatten the hole towards a dark, low-contrast version of what
			// was there, keeping a hint of the background so the notch is
			// visible without being a solid silhouette.
			edge := float64(a - blurred.A[y*mask.W+x]) // >0 near the border
			f := 0.34 - 0.5*math.Max(0, edge)
			for c := range 3 {
				v := float64(dst.Pix[i+c])
				dst.Pix[i+c] = clampByte(v*(1-float64(a)) + v*f*float64(a))
			}
		}
	}
}

// Bevel adds a light edge on one side of the shape and a dark edge on the
// other, giving the cut piece a raised, physical appearance.
func Bevel(img *image.RGBA, mask *Coverage) {
	blurred := &Coverage{W: mask.W, H: mask.H, A: append([]float32(nil), mask.A...)}
	blurred.Blur(2, 1)
	for y := range mask.H {
		for x := range mask.W {
			a := mask.A[y*mask.W+x]
			if a <= 0.02 {
				continue
			}
			// Gradient of the blurred mask points outward from the shape; its
			// dot product with the light direction gives the shading.
			gx := float64(blurred.At(x+1, y) - blurred.At(x-1, y))
			gy := float64(blurred.At(x, y+1) - blurred.At(x, y-1))
			l := -(gx*0.7071 + gy*0.7071) // light from the top-left
			if math.Abs(l) < 0.01 {
				continue
			}
			i := img.PixOffset(x, y)
			alpha := float64(img.Pix[i+3]) / 255
			if alpha <= 0 {
				continue
			}
			strength := math.Min(1, math.Abs(l)*2.0)
			target := color.RGBA{255, 255, 255, 255}
			if l < 0 {
				target = color.RGBA{0, 0, 0, 255}
			}
			// Shading is applied to the straight colour, then premultiplied
			// again, so the piece keeps a clean edge.
			for c := range 3 {
				v := float64(img.Pix[i+c]) / alpha
				v += (float64(targetChannel(target, c)) - v) * strength * 0.38
				img.Pix[i+c] = clampByte(v * alpha)
			}
		}
	}
}

func targetChannel(c color.RGBA, i int) uint8 {
	switch i {
	case 0:
		return c.R
	case 1:
		return c.G
	default:
		return c.B
	}
}

// Outline strokes the boundary of a mask onto an image.
func Outline(img *image.RGBA, mask *Coverage, col color.RGBA, alpha float64) {
	edge := NewCoverage(mask.W, mask.H)
	for y := range mask.H {
		for x := range mask.W {
			a := mask.At(x, y)
			// A pixel is on the boundary when its neighbourhood is not uniform.
			lo := min4(mask.At(x-1, y), mask.At(x+1, y), mask.At(x, y-1), mask.At(x, y+1))
			if a-lo > 0.08 {
				edge.Set(x, y, a-lo)
			}
		}
	}
	CompositeColor(img, edge, col, alpha)
}

func min4(a, b, c, d float32) float32 {
	m := a
	for _, v := range [3]float32{b, c, d} {
		if v < m {
			m = v
		}
	}
	return m
}

// RotateRGBA rotates src by theta radians about its centre, sampling
// bilinearly. Pixels that fall outside the source are left transparent.
func RotateRGBA(src *image.RGBA, theta float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	cx, cy := float64(w)/2, float64(h)/2
	// Rotate the sampling coordinate by -theta, which rotates the image by
	// +theta without leaving gaps.
	sin, cos := math.Sincos(-theta)
	for y := range h {
		for x := range w {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			sx := cx + dx*cos - dy*sin - 0.5
			sy := cy + dx*sin + dy*cos - 0.5
			r, g, bl, a := sampleBilinear(src, sx, sy)
			i := out.PixOffset(x, y)
			out.Pix[i+0], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = r, g, bl, a
		}
	}
	return out
}

func sampleBilinear(src *image.RGBA, x, y float64) (r, g, b, a uint8) {
	bd := src.Bounds()
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	var acc [4]float64
	for dy := range 2 {
		for dx := range 2 {
			px, py := x0+dx, y0+dy
			if px < bd.Min.X || py < bd.Min.Y || px >= bd.Max.X || py >= bd.Max.Y {
				continue
			}
			wx := fx
			if dx == 0 {
				wx = 1 - fx
			}
			wy := fy
			if dy == 0 {
				wy = 1 - fy
			}
			w := wx * wy
			i := src.PixOffset(px, py)
			for c := range 4 {
				acc[c] += w * float64(src.Pix[i+c])
			}
		}
	}
	return clampByte(acc[0]), clampByte(acc[1]), clampByte(acc[2]), clampByte(acc[3])
}

// CircleMask returns a soft-edged disc mask.
func CircleMask(w, h int, cx, cy, r float64) *Coverage {
	cov := NewCoverage(w, h)
	for y := range h {
		for x := range w {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			switch {
			case d <= r-1:
				cov.A[y*w+x] = 1
			case d < r+1:
				cov.A[y*w+x] = float32((r + 1 - d) / 2)
			}
		}
	}
	return cov
}

// ApplyAlpha multiplies an image's alpha by a mask. All four channels are
// scaled, since image.RGBA stores premultiplied colour.
func ApplyAlpha(img *image.RGBA, mask *Coverage) {
	b := img.Bounds()
	for y := range b.Dy() {
		for x := range b.Dx() {
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			a := mask.At(x, y)
			for c := range 4 {
				img.Pix[i+c] = uint8(float32(img.Pix[i+c]) * a)
			}
		}
	}
}

// MaskFromPath rasterizes a path into a mask of the given size.
func MaskFromPath(w, h int, p Path) *Coverage {
	cov := NewCoverage(w, h)
	cov.Rasterize(p)
	return cov
}
