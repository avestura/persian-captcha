package imagegen

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"
	"testing"
)

func TestRasterizeCoversASquareExactly(t *testing.T) {
	cov := NewCoverage(20, 20)
	cov.Rasterize(Path{{Pt(5, 5), Pt(15, 5), Pt(15, 15), Pt(5, 15)}})

	// The interior is fully covered, the exterior not at all, and the total
	// coverage equals the square's area.
	if got := cov.At(10, 10); got != 1 {
		t.Errorf("the middle of the square has coverage %v, want 1", got)
	}
	if got := cov.At(2, 2); got != 0 {
		t.Errorf("a pixel outside the square has coverage %v, want 0", got)
	}
	var total float64
	for _, v := range cov.A {
		total += float64(v)
	}
	if math.Abs(total-100) > 0.5 {
		t.Errorf("total coverage %v, want 100 (a 10x10 square)", total)
	}
}

// Anti-aliasing is what keeps the puzzle-piece edge from looking like a
// staircase, and a staircase edge is far easier for a solver to find.
func TestRasterizeAntiAliasesDiagonals(t *testing.T) {
	cov := NewCoverage(40, 40)
	cov.Rasterize(Path{{Pt(5, 5), Pt(35, 35), Pt(5, 35)}})

	partial := 0
	for _, v := range cov.A {
		if v > 0.05 && v < 0.95 {
			partial++
		}
	}
	if partial < 15 {
		t.Errorf("only %d pixels are partially covered; the edge is not being antialiased", partial)
	}
}

// Even-odd filling is what makes a ring a ring rather than a disc.
func TestRasterizeHonoursEvenOdd(t *testing.T) {
	cov := NewCoverage(40, 40)
	cov.Rasterize(Path{
		{Pt(5, 5), Pt(35, 5), Pt(35, 35), Pt(5, 35)},
		{Pt(15, 15), Pt(25, 15), Pt(25, 25), Pt(15, 25)},
	})
	if got := cov.At(20, 20); got != 0 {
		t.Errorf("the hole has coverage %v, want 0", got)
	}
	if got := cov.At(10, 20); got != 1 {
		t.Errorf("the ring has coverage %v, want 1", got)
	}
}

func TestCoverageClampsOutOfBounds(t *testing.T) {
	cov := NewCoverage(10, 10)
	cov.Rasterize(Path{{Pt(-50, -50), Pt(60, -50), Pt(60, 60), Pt(-50, 60)}})
	for i, v := range cov.A {
		if v != 1 {
			t.Fatalf("pixel %d has coverage %v; a shape larger than the canvas should fill it", i, v)
		}
	}
	// Reads and writes outside the buffer must not panic.
	cov.Set(-1, -1, 1)
	if got := cov.At(999, 999); got != 0 {
		t.Errorf("At out of bounds = %v, want 0", got)
	}
}

func TestBlurSpreadsAndConserves(t *testing.T) {
	cov := NewCoverage(41, 41)
	cov.Set(20, 20, 1)
	before := sum(cov.A)
	cov.Blur(3, 2)

	if cov.At(20, 20) >= 1 {
		t.Error("the blurred peak was not reduced")
	}
	if cov.At(23, 20) <= 0 {
		t.Error("the blur did not spread horizontally")
	}
	if cov.At(20, 23) <= 0 {
		t.Error("the blur did not spread vertically")
	}
	// A box blur is normalised, so the total should survive, give or take
	// what leaks past the clamped edges.
	if after := sum(cov.A); math.Abs(after-before) > 0.05 {
		t.Errorf("blur changed the total coverage from %v to %v", before, after)
	}
}

func sum(v []float32) float64 {
	var total float64
	for _, x := range v {
		total += float64(x)
	}
	return total
}

// image.RGBA is alpha-premultiplied. A pixel whose colour exceeds its alpha is
// invalid, and the PNG encoder turns it into a bright fringe around the piece.
func TestCutPieceKeepsPremultipliedAlphaValid(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	bg := BackgroundStyle(rng, 120, 120, StyleMosaic, PaletteByName("lajevard"))
	mask := MaskFromPath(80, 80, JigsawPiecePath(20, 20, 40, [4]int{1, -1, 1, -1}))

	piece := CutPiece(bg, mask, 20, 20)
	Bevel(piece, mask)
	assertPremultiplied(t, piece)

	// The same must hold after the disc mask is applied for the rotate
	// challenge.
	disc := BackgroundStyle(rng, 100, 100, StyleMedal, PaletteByName("firouzeh"))
	ApplyAlpha(disc, CircleMask(100, 100, 50, 50, 45))
	assertPremultiplied(t, disc)
}

func assertPremultiplied(t *testing.T, img *image.RGBA) {
	t.Helper()
	b := img.Bounds()
	for y := range b.Dy() {
		for x := range b.Dx() {
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			a := img.Pix[i+3]
			for c := range 3 {
				if img.Pix[i+c] > a {
					t.Fatalf("pixel (%d,%d) has channel %d = %d above its alpha %d",
						x, y, c, img.Pix[i+c], a)
				}
			}
		}
	}
}

// The piece cut out of the board and the notch carved into it must be the
// same shape, or the puzzle has no correct answer.
func TestPieceAndNotchAgree(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 9))
	bg := BackgroundStyle(rng, 320, 200, StyleGirih, PaletteByName("sabz"))
	const (
		size   = 50
		margin = 18
		atX    = 150
		atY    = 60
	)
	mask := MaskFromPath(size+2*margin, size+2*margin,
		JigsawPiecePath(margin, margin, size, [4]int{1, -1, -1, 1}))

	board := image.NewRGBA(bg.Bounds())
	copy(board.Pix, bg.Pix)
	CarveNotch(board, mask, atX, atY)
	piece := CutPiece(bg, mask, atX, atY)

	// Where the mask is opaque the board must have been darkened, and the
	// piece must be opaque there.
	for y := range mask.H {
		for x := range mask.W {
			if mask.At(x, y) < 0.99 {
				continue
			}
			bi := board.PixOffset(atX+x, atY+y)
			oi := bg.PixOffset(atX+x, atY+y)
			if board.Pix[bi] >= bg.Pix[oi] && bg.Pix[oi] > 8 {
				t.Fatalf("the notch at (%d,%d) was not darkened: %d vs %d",
					x, y, board.Pix[bi], bg.Pix[oi])
			}
			if pi := piece.PixOffset(x, y); piece.Pix[pi+3] < 250 {
				t.Fatalf("the piece is transparent at (%d,%d) where the mask is solid", x, y)
			}
		}
	}
}

// The jigsaw silhouette must have knobs that stick out and blanks that cut in,
// or the piece is just a square and the challenge is trivial.
func TestJigsawTabsProtrudeAndIndent(t *testing.T) {
	const size, margin = 60.0, 25.0
	knobs := MaskFromPath(110, 110, JigsawPiecePath(margin, margin, size, [4]int{1, 1, 1, 1}))
	blanks := MaskFromPath(110, 110, JigsawPiecePath(margin, margin, size, [4]int{-1, -1, -1, -1}))

	mid := int(margin + size/2)
	above := int(margin) - 8
	if knobs.At(mid, above) < 0.9 {
		t.Errorf("a knob does not reach %d pixels beyond the body", 8)
	}
	if blanks.At(mid, above) > 0.1 {
		t.Error("a blank protrudes beyond the body")
	}
	// A blank cuts into the body just inside the edge.
	inside := int(margin) + 6
	if blanks.At(mid, inside) > 0.1 {
		t.Error("a blank did not cut into the body")
	}
	if knobs.At(mid, inside) < 0.9 {
		t.Error("a knob wrongly cut into the body")
	}
}

func TestEveryStyleAndShapeRenders(t *testing.T) {
	rng := rand.New(rand.NewPCG(4, 4))
	for _, style := range Styles() {
		img := BackgroundStyle(rng, 160, 100, style, RandomPalette(rng))
		if img.Bounds().Dx() != 160 {
			t.Fatalf("style %q produced a %v image", style, img.Bounds())
		}
		if isFlat(img) {
			t.Errorf("style %q produced a featureless image", style)
		}
		data, err := EncodePNG(img)
		if err != nil {
			t.Fatalf("style %q: %v", style, err)
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Fatalf("style %q produced an undecodable PNG: %v", style, err)
		}
	}

	for _, kind := range ShapeKinds() {
		mask := NewCoverage(80, 80)
		mask.Rasterize(ShapePath(kind, 40, 40, 30, 0))
		area := sum(mask.A)
		// Every icon should be a recognisable blob: not empty, not the whole
		// canvas. The bounds are wide because the shapes differ a lot.
		if area < 300 || area > 5000 {
			t.Errorf("shape %q covers %v pixels, which does not look like a 30px icon", kind, area)
		}
	}
}

// A background with no variation would let a solver find the notch by looking
// for the only region that is not uniform.
func isFlat(img *image.RGBA) bool {
	b := img.Bounds()
	var minV, maxV uint8 = 255, 0
	for y := range b.Dy() {
		for x := range b.Dx() {
			v := img.Pix[img.PixOffset(x, y)]
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
	}
	return maxV-minV < 20
}

func TestRotateRGBAIsReversible(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 60, 60))
	for y := range 60 {
		for x := range 60 {
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = uint8(x*4), uint8(y*4), 128, 255
		}
	}
	// A quarter turn and back must land close to where it started; bilinear
	// sampling blurs slightly, so the comparison is approximate.
	there := RotateRGBA(src, math.Pi/2)
	back := RotateRGBA(there, -math.Pi/2)

	var diff, n float64
	for y := 15; y < 45; y++ {
		for x := 15; x < 45; x++ {
			i := src.PixOffset(x, y)
			j := back.PixOffset(x, y)
			diff += math.Abs(float64(src.Pix[i]) - float64(back.Pix[j]))
			n++
		}
	}
	if avg := diff / n; avg > 6 {
		t.Errorf("a round-trip rotation drifted by %.1f per channel", avg)
	}
}

func TestCircleMaskIsRoundAndSoft(t *testing.T) {
	mask := CircleMask(80, 80, 40, 40, 30)
	if got := mask.At(40, 40); got != 1 {
		t.Errorf("the centre has coverage %v, want 1", got)
	}
	if got := mask.At(2, 2); got != 0 {
		t.Errorf("a far corner has coverage %v, want 0", got)
	}
	// Well outside the radius, past the feathered band.
	if got := mask.At(75, 40); got != 0 {
		t.Errorf("a pixel outside the radius has coverage %v, want 0", got)
	}
	// The boundary is feathered rather than hard, so the disc has no jagged
	// edge to give its orientation away.
	if got := mask.At(70, 40); got <= 0 || got >= 1 {
		t.Errorf("the edge has coverage %v, want a partial value", got)
	}
}

func TestFillPathBlendsWithAlpha(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := range img.Pix {
		img.Pix[i] = 255 // opaque white
	}
	FillPath(img, Path{{Pt(0, 0), Pt(20, 0), Pt(20, 20), Pt(0, 20)}}, color.RGBA{0, 0, 0, 255}, 0.5)

	got := img.Pix[img.PixOffset(10, 10)]
	if got < 120 || got > 136 {
		t.Errorf("half-opacity black over white gave %d, want about 128", got)
	}
}

func TestPaletteLookup(t *testing.T) {
	names := PaletteNames()
	if len(names) < 4 {
		t.Fatalf("only %d palettes", len(names))
	}
	for _, name := range names {
		if got := PaletteByName(name); got.Name != name {
			t.Errorf("PaletteByName(%q) returned %q", name, got.Name)
		}
	}
	if got := PaletteByName("not-a-palette"); got.Name == "" {
		t.Error("an unknown palette name did not fall back to a usable palette")
	}
	// Tone must be total, including for negative and large indices.
	pal := PaletteByName(names[0])
	for _, i := range []int{-5, 0, 3, 99} {
		if c := pal.Tone(i); c.A == 0 {
			t.Errorf("Tone(%d) returned a transparent colour", i)
		}
	}
}
