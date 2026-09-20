// Package imagegen draws the challenge artwork.
//
// Every background, shape and puzzle piece is generated procedurally at
// request time. Nothing is loaded from an asset library, which means there is
// no fixed set of images an attacker can scrape, hash and precompute answers
// for, and no image licensing to worry about.
//
// The drawing primitives here are deliberately small: an anti-aliased
// scanline polygon filler, curve flattening, and a handful of compositing
// helpers built on image/draw.
package imagegen

import (
	"image"
	"image/color"
	"math"
	"sort"
)

// Point is a position in device space, in pixels.
type Point struct{ X, Y float64 }

// Pt is shorthand for building a Point, so callers in other packages can write
// path literals without naming both fields every time.
func Pt(x, y float64) Point { return Point{x, y} }

// Path is a set of closed sub-paths, filled with the even-odd rule. A path
// with a hole is expressed as an outer ring plus an inner ring.
type Path [][]Point

// Translate returns the path shifted by (dx, dy).
func (p Path) Translate(dx, dy float64) Path {
	out := make(Path, len(p))
	for i, sub := range p {
		ns := make([]Point, len(sub))
		for j, pt := range sub {
			ns[j] = Point{pt.X + dx, pt.Y + dy}
		}
		out[i] = ns
	}
	return out
}

// Rotate returns the path rotated by theta radians about (cx, cy).
func (p Path) Rotate(theta, cx, cy float64) Path {
	sin, cos := math.Sincos(theta)
	out := make(Path, len(p))
	for i, sub := range p {
		ns := make([]Point, len(sub))
		for j, pt := range sub {
			dx, dy := pt.X-cx, pt.Y-cy
			ns[j] = Point{cx + dx*cos - dy*sin, cy + dx*sin + dy*cos}
		}
		out[i] = ns
	}
	return out
}

// subSamples is the number of scanlines sampled per pixel row. Horizontal
// coverage is computed exactly, so vertical supersampling alone is enough for
// smooth edges.
const subSamples = 5

// Coverage is an 8-bit alpha mask produced by rasterizing a path.
type Coverage struct {
	W, H int
	A    []float32 // len W*H, values in [0,1]
}

// NewCoverage allocates an empty mask.
func NewCoverage(w, h int) *Coverage {
	return &Coverage{W: w, H: h, A: make([]float32, w*h)}
}

// At returns the coverage at a pixel, or 0 when out of bounds.
func (c *Coverage) At(x, y int) float32 {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return 0
	}
	return c.A[y*c.W+x]
}

// Set writes the coverage at a pixel.
func (c *Coverage) Set(x, y int, v float32) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return
	}
	c.A[y*c.W+x] = v
}

// Rasterize accumulates the path's coverage into the mask, clamped to 1.
func (c *Coverage) Rasterize(p Path) {
	type edge struct{ x0, y0, x1, y1 float64 }
	var edges []edge
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, sub := range p {
		n := len(sub)
		if n < 2 {
			continue
		}
		for i := range n {
			a, b := sub[i], sub[(i+1)%n]
			if a.Y == b.Y {
				continue // horizontal edges never produce a crossing
			}
			edges = append(edges, edge{a.X, a.Y, b.X, b.Y})
			minY = math.Min(minY, math.Min(a.Y, b.Y))
			maxY = math.Max(maxY, math.Max(a.Y, b.Y))
		}
	}
	if len(edges) == 0 {
		return
	}
	y0 := max(int(math.Floor(minY)), 0)
	y1 := min(int(math.Ceil(maxY))+1, c.H)

	weight := float32(1) / float32(subSamples)
	xs := make([]float64, 0, len(edges))
	for y := y0; y < y1; y++ {
		for s := range subSamples {
			yy := float64(y) + (float64(s)+0.5)/float64(subSamples)
			xs = xs[:0]
			for _, e := range edges {
				// Half-open test on y avoids double-counting shared vertices.
				if (e.y0 <= yy) == (e.y1 <= yy) {
					continue
				}
				t := (yy - e.y0) / (e.y1 - e.y0)
				xs = append(xs, e.x0+t*(e.x1-e.x0))
			}
			if len(xs) < 2 {
				continue
			}
			sort.Float64s(xs)
			for i := 0; i+1 < len(xs); i += 2 {
				c.addSpan(y, xs[i], xs[i+1], weight)
			}
		}
	}
}

// addSpan adds horizontal coverage for the span [xa, xb) on one pixel row.
func (c *Coverage) addSpan(y int, xa, xb float64, weight float32) {
	if xb <= xa {
		return
	}
	xa, xb = math.Max(xa, 0), math.Min(xb, float64(c.W))
	if xb <= xa {
		return
	}
	row := y * c.W
	px0, px1 := int(math.Floor(xa)), int(math.Ceil(xb))
	for px := px0; px < px1 && px < c.W; px++ {
		if px < 0 {
			continue
		}
		overlap := math.Min(xb, float64(px+1)) - math.Max(xa, float64(px))
		if overlap <= 0 {
			continue
		}
		v := c.A[row+px] + weight*float32(overlap)
		if v > 1 {
			v = 1
		}
		c.A[row+px] = v
	}
}

// Blur applies a separable box blur, repeated for an approximate Gaussian.
// It is used to soften mask edges and to build drop shadows.
func (c *Coverage) Blur(radius int, passes int) {
	if radius <= 0 || passes <= 0 {
		return
	}
	tmp := make([]float32, len(c.A))
	for range passes {
		boxBlurH(c.A, tmp, c.W, c.H, radius)
		boxBlurH(tmp, c.A, c.H, c.W, radius) // transposed pass
	}
}

// boxBlurH blurs along x and writes the transposed result, so calling it
// twice blurs both axes and restores the original orientation.
func boxBlurH(src, dst []float32, w, h, r int) {
	norm := float32(1) / float32(2*r+1)
	for y := range h {
		row := y * w
		var sum float32
		for i := -r; i <= r; i++ {
			sum += src[row+clampInt(i, 0, w-1)]
		}
		for x := range w {
			dst[x*h+y] = sum * norm
			sum += src[row+clampInt(x+r+1, 0, w-1)] - src[row+clampInt(x-r, 0, w-1)]
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// FillPath composites a solid colour through the path onto dst.
func FillPath(dst *image.RGBA, p Path, col color.RGBA, alpha float64) {
	b := dst.Bounds()
	cov := NewCoverage(b.Dx(), b.Dy())
	cov.Rasterize(p)
	CompositeColor(dst, cov, col, alpha)
}

// CompositeColor blends col over dst using the mask as alpha.
func CompositeColor(dst *image.RGBA, cov *Coverage, col color.RGBA, alpha float64) {
	b := dst.Bounds()
	for y := range cov.H {
		dy := b.Min.Y + y
		if dy >= b.Max.Y {
			break
		}
		for x := range cov.W {
			a := float64(cov.A[y*cov.W+x]) * alpha
			if a <= 0 {
				continue
			}
			dx := b.Min.X + x
			if dx >= b.Max.X {
				break
			}
			i := dst.PixOffset(dx, dy)
			blendPixel(dst.Pix[i:i+4], col, a)
		}
	}
}

// blendPixel does source-over compositing on a non-premultiplied RGBA pixel
// whose destination is assumed opaque, which holds for our backgrounds.
func blendPixel(p []byte, col color.RGBA, a float64) {
	if a > 1 {
		a = 1
	}
	ia := 1 - a
	p[0] = byte(float64(col.R)*a + float64(p[0])*ia)
	p[1] = byte(float64(col.G)*a + float64(p[1])*ia)
	p[2] = byte(float64(col.B)*a + float64(p[2])*ia)
	if p[3] < 255 {
		p[3] = byte(255*a + float64(p[3])*ia)
	}
}

// StrokePath approximates a stroke by filling the path outline as a ribbon of
// quads, one per segment, plus round joins. Good enough for decorative line
// work and far simpler than a general stroker.
func StrokePath(dst *image.RGBA, p Path, width float64, col color.RGBA, alpha float64) {
	half := width / 2
	var ribbon Path
	for _, sub := range p {
		n := len(sub)
		if n < 2 {
			continue
		}
		for i := range n {
			a, b := sub[i], sub[(i+1)%n]
			dx, dy := b.X-a.X, b.Y-a.Y
			l := math.Hypot(dx, dy)
			if l == 0 {
				continue
			}
			nx, ny := -dy/l*half, dx/l*half
			ribbon = append(ribbon, []Point{
				{a.X + nx, a.Y + ny}, {b.X + nx, b.Y + ny},
				{b.X - nx, b.Y - ny}, {a.X - nx, a.Y - ny},
			})
			ribbon = append(ribbon, circlePoints(b.X, b.Y, half, 10))
		}
	}
	// Each quad is rasterized separately and merged with max, so overlapping
	// segments do not cancel each other out under the even-odd rule.
	b := dst.Bounds()
	acc := NewCoverage(b.Dx(), b.Dy())
	piece := NewCoverage(b.Dx(), b.Dy())
	for _, quad := range ribbon {
		for i := range piece.A {
			piece.A[i] = 0
		}
		piece.Rasterize(Path{quad})
		for i, v := range piece.A {
			if v > acc.A[i] {
				acc.A[i] = v
			}
		}
	}
	CompositeColor(dst, acc, col, alpha)
}

func circlePoints(cx, cy, r float64, steps int) []Point {
	pts := make([]Point, steps)
	for i := range steps {
		a := 2 * math.Pi * float64(i) / float64(steps)
		sin, cos := math.Sincos(a)
		pts[i] = Point{cx + r*cos, cy + r*sin}
	}
	return pts
}

// QuadTo flattens a quadratic Bezier from p0 through control c to p1 and
// appends the points (excluding p0) to dst.
func QuadTo(dst []Point, p0, c, p1 Point, steps int) []Point {
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mt := 1 - t
		dst = append(dst, Point{
			X: mt*mt*p0.X + 2*mt*t*c.X + t*t*p1.X,
			Y: mt*mt*p0.Y + 2*mt*t*c.Y + t*t*p1.Y,
		})
	}
	return dst
}
