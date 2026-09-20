package imagegen

import "math"

// ShapeKind identifies one of the icons used by the click-order and
// drag-and-drop challenges.
//
// Challenges name shapes by these stable identifiers and the browser renders
// the localised label, so the image itself carries no text. That keeps the
// renderer free of font and text-shaping dependencies, and means Persian,
// Arabic or any other script works without server-side shaping.
type ShapeKind string

// The icon set. These are chosen to stay distinguishable at 28 pixels and to
// have names that translate unambiguously.
const (
	ShapeStar     ShapeKind = "star"
	ShapeCrescent ShapeKind = "crescent"
	ShapeTriangle ShapeKind = "triangle"
	ShapeSquare   ShapeKind = "square"
	ShapePentagon ShapeKind = "pentagon"
	ShapeHexagon  ShapeKind = "hexagon"
	ShapeDiamond  ShapeKind = "diamond"
	ShapeCircle   ShapeKind = "circle"
	ShapeHeart    ShapeKind = "heart"
	ShapeDroplet  ShapeKind = "droplet"
	ShapeArrow    ShapeKind = "arrow"
	ShapeFlower   ShapeKind = "flower"
)

var allShapes = []ShapeKind{
	ShapeStar, ShapeCrescent, ShapeTriangle, ShapeSquare, ShapePentagon,
	ShapeHexagon, ShapeDiamond, ShapeCircle, ShapeHeart, ShapeDroplet,
	ShapeArrow, ShapeFlower,
}

// ShapeKinds returns every available icon.
func ShapeKinds() []ShapeKind {
	out := make([]ShapeKind, len(allShapes))
	copy(out, allShapes)
	return out
}

// ShapePath builds the outline of an icon centred at (cx, cy) with radius r,
// rotated by rot radians.
func ShapePath(kind ShapeKind, cx, cy, r, rot float64) Path {
	var p Path
	switch kind {
	case ShapeStar:
		p = StarPath(cx, cy, r, r*0.44, 5, -math.Pi/2)
	case ShapeCrescent:
		p = CrescentPath(cx, cy, r)
	case ShapeTriangle:
		p = PolygonPath(cx, cy, r, 3, -math.Pi/2)
	case ShapeSquare:
		p = PolygonPath(cx, cy, r, 4, math.Pi/4)
	case ShapePentagon:
		p = PolygonPath(cx, cy, r, 5, -math.Pi/2)
	case ShapeHexagon:
		p = PolygonPath(cx, cy, r, 6, 0)
	case ShapeDiamond:
		p = PolygonPath(cx, cy, r, 4, 0)
	case ShapeCircle:
		p = Path{circlePoints(cx, cy, r, 48)}
	case ShapeHeart:
		p = HeartPath(cx, cy, r)
	case ShapeDroplet:
		p = DropletPath(cx, cy, r)
	case ShapeArrow:
		p = ArrowPath(cx, cy, r)
	case ShapeFlower:
		p = FlowerPath(cx, cy, r, 6)
	default:
		p = Path{circlePoints(cx, cy, r, 48)}
	}
	if rot != 0 {
		p = p.Rotate(rot, cx, cy)
	}
	return p
}

// PolygonPath returns a regular n-gon.
func PolygonPath(cx, cy, r float64, n int, rot float64) Path {
	if n < 3 {
		n = 3
	}
	pts := make([]Point, n)
	for i := range n {
		a := rot + 2*math.Pi*float64(i)/float64(n)
		sin, cos := math.Sincos(a)
		pts[i] = Point{cx + r*cos, cy + r*sin}
	}
	return Path{pts}
}

// StarPath returns a star alternating between outer radius rOuter and inner
// radius rInner across n points.
func StarPath(cx, cy, rOuter, rInner float64, n int, rot float64) Path {
	if n < 3 {
		n = 3
	}
	pts := make([]Point, 0, n*2)
	for i := range n * 2 {
		r := rOuter
		if i%2 == 1 {
			r = rInner
		}
		a := rot + math.Pi*float64(i)/float64(n)
		sin, cos := math.Sincos(a)
		pts = append(pts, Point{cx + r*cos, cy + r*sin})
	}
	return Path{pts}
}

// CrescentPath returns a hilal: the region inside one circle and outside a
// second, offset circle. It is traced as two arcs rather than as a hole so
// that the even-odd fill rule produces the expected shape.
func CrescentPath(cx, cy, r float64) Path {
	const (
		innerR = 0.88 // relative to the outer radius
		offset = 0.52
	)
	// Where the two circles intersect, in units of the outer radius.
	ix := (offset*offset + 1 - innerR*innerR) / (2 * offset)
	iy := math.Sqrt(math.Max(0, 1-ix*ix))

	outerStart := math.Atan2(iy, ix)
	outerEnd := 2*math.Pi - outerStart
	innerStart := math.Atan2(iy, ix-offset)
	innerEnd := 2*math.Pi - innerStart

	pts := make([]Point, 0, 96)
	// Outer edge, the long way round through the far side.
	pts = appendArc(pts, cx, cy, r, outerStart, outerEnd, 48)
	// Concave inner edge, traced back.
	pts = appendArc(pts, cx+offset*r, cy, innerR*r, innerEnd, innerStart, 40)
	// Point the horns upward, which reads more like a crescent moon.
	return Path{pts}.Rotate(-math.Pi/2, cx, cy)
}

// appendArc appends points along a circular arc from angle a0 to a1.
func appendArc(dst []Point, cx, cy, r, a0, a1 float64, steps int) []Point {
	for i := range steps + 1 {
		a := a0 + (a1-a0)*float64(i)/float64(steps)
		sin, cos := math.Sincos(a)
		dst = append(dst, Point{cx + r*cos, cy + r*sin})
	}
	return dst
}

// HeartPath uses the classic parametric heart curve, normalised to radius r.
func HeartPath(cx, cy, r float64) Path {
	const steps = 64
	pts := make([]Point, 0, steps)
	s := r / 17
	for i := range steps {
		t := 2 * math.Pi * float64(i) / float64(steps)
		x := 16 * math.Pow(math.Sin(t), 3)
		y := 13*math.Cos(t) - 5*math.Cos(2*t) - 2*math.Cos(3*t) - math.Cos(4*t)
		pts = append(pts, Point{cx + x*s, cy - y*s})
	}
	return Path{pts}
}

// DropletPath returns a teardrop: a circular base rising to a point.
func DropletPath(cx, cy, r float64) Path {
	apex := Point{cx, cy - r}
	left := Point{cx - r*0.62, cy + r*0.18}
	right := Point{cx + r*0.62, cy + r*0.18}

	pts := []Point{apex}
	pts = QuadTo(pts, apex, Point{cx - r*0.30, cy - r*0.45}, left, 14)
	// Round the bottom with an arc between the two flanks.
	pts = appendArc(pts, cx, cy+r*0.18, r*0.62, math.Pi, 0, 24)
	pts = QuadTo(pts, right, Point{cx + r*0.30, cy - r*0.45}, apex, 14)
	return Path{pts}
}

// ArrowPath returns an arrow pointing right.
func ArrowPath(cx, cy, r float64) Path {
	return Path{{
		{cx - r*0.85, cy - r*0.30},
		{cx + r*0.10, cy - r*0.30},
		{cx + r*0.10, cy - r*0.72},
		{cx + r*0.95, cy},
		{cx + r*0.10, cy + r*0.72},
		{cx + r*0.10, cy + r*0.30},
		{cx - r*0.85, cy + r*0.30},
	}}
}

// FlowerPath returns a rosette with the given number of petals.
func FlowerPath(cx, cy, r float64, petals int) Path {
	const steps = 120
	pts := make([]Point, 0, steps)
	for i := range steps {
		t := 2 * math.Pi * float64(i) / float64(steps)
		rr := r * (0.46 + 0.54*math.Pow(math.Abs(math.Cos(float64(petals)*t/2)), 0.7))
		sin, cos := math.Sincos(t)
		pts = append(pts, Point{cx + rr*cos, cy + rr*sin})
	}
	return Path{pts}
}

// ArchPath returns a pointed (two-centred) arch standing on baseY.
func ArchPath(cx, baseY, width, height float64) Path {
	hw := width / 2
	spring := baseY - height*0.52
	apex := Point{cx, baseY - height}
	pts := []Point{
		{cx - hw, baseY},
		{cx - hw, spring},
	}
	pts = QuadTo(pts, Point{cx - hw, spring}, Point{cx - hw*0.86, baseY - height*0.94}, apex, 18)
	pts = QuadTo(pts, apex, Point{cx + hw*0.86, baseY - height*0.94}, Point{cx + hw, spring}, 18)
	pts = append(pts, Point{cx + hw, baseY})
	return Path{pts}
}

// TabTop, TabRight, TabBottom and TabLeft index the sides of a jigsaw piece,
// traversed clockwise from the top-left corner.
const (
	TabTop = iota
	TabRight
	TabBottom
	TabLeft
)

// JigsawPiecePath builds a classic puzzle-piece outline whose top-left corner
// is at (x, y) and whose body is size×size.
//
// tabs holds one value per side in TabTop..TabLeft order: +1 for a knob
// pointing outward, -1 for a matching blank cut inward, and 0 for a flat edge.
func JigsawPiecePath(x, y, size float64, tabs [4]int) Path {
	var pts []Point
	// Each side is generated in (u, w) space where u runs 0..1 along the side
	// and w is the outward offset, then mapped onto the actual side.
	for side := range 4 {
		edge := jigsawEdge(tabs[side])
		for _, e := range edge {
			u, w := e.X*size, e.Y*size
			switch side {
			case TabTop:
				pts = append(pts, Point{x + u, y - w})
			case TabRight:
				pts = append(pts, Point{x + size + w, y + u})
			case TabBottom:
				pts = append(pts, Point{x + size - u, y + size + w})
			case TabLeft:
				pts = append(pts, Point{x - w, y + size - u})
			}
		}
	}
	return Path{pts}
}

// jigsawEdge returns one side in unit space, excluding its end point (which is
// the next side's start). A knob is a circle whose neck is narrower than its
// bulb, which is what gives a puzzle piece its unmistakable silhouette.
func jigsawEdge(tab int) []Point {
	pts := []Point{{0, 0}}
	if tab == 0 {
		return pts
	}
	const (
		knobR  = 0.17
		knobCY = 0.135 // centre offset from the edge, outward
	)
	// Half-width of the neck, where the knob circle crosses the edge line.
	dx := math.Sqrt(math.Max(0, knobR*knobR-knobCY*knobCY))
	start, end := 0.5-dx, 0.5+dx
	dir := float64(tab) // +1 knob, -1 blank

	pts = append(pts, Point{start, 0})
	// Angles about the knob centre, which sits at w = knobCY. The sweep runs
	// the long way round (285 degrees or so) so the path traces the bulb; the
	// short way would cut straight across the neck.
	from := math.Atan2(-knobCY, -dx)
	to := math.Atan2(-knobCY, dx) - 2*math.Pi
	const steps = 28
	for i := range steps + 1 {
		a := from + (to-from)*float64(i)/float64(steps)
		sin, cos := math.Sincos(a)
		pts = append(pts, Point{0.5 + knobR*cos, (knobCY + knobR*sin) * dir})
	}
	pts = append(pts, Point{end, 0})
	return pts
}
