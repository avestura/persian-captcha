package challenge

import (
	"encoding/json"
	"image"
	"image/color"
	"math"
	"math/rand/v2"

	"avestura.dev/persian-captcha/internal/imagegen"
)

// discSize is the side of the square rotate-challenge image. It matches the
// width the widget displays the disc at, so the artwork is never upscaled:
// a blurred motif would make the upright orientation harder to judge, and the
// grader has no way to be more forgiving about that.
const discSize = 320

// RotateState is the private state of the "turn it upright" challenge.
type RotateState struct {
	// OffsetDeg is how far clockwise the artwork was turned away from
	// upright. The visitor has to undo exactly this much.
	OffsetDeg float64 `json:"offsetDeg"`
	// Radius is the visible disc radius in pixels.
	Radius int `json:"radius"`
	// EmblemSeed varies the upright motif independently of the background.
	EmblemSeed uint64 `json:"emblemSeed"`

	Style   string `json:"style"`
	Palette string `json:"palette"`
}

// RotateSpec tells the browser how large the disc is. It deliberately says
// nothing about the current orientation.
type RotateSpec struct {
	Radius int `json:"radius"`
	// StepDeg is the angular granularity of keyboard arrow-key control.
	StepDeg float64 `json:"stepDeg"`
}

type rotateGen struct{}

func (rotateGen) generate(rng *rand.Rand, level Level) *State {
	// Stay well away from upright so the starting position is never almost
	// correct, and away from a half turn, which is easy to guess.
	offset := 40 + rng.Float64()*280
	if math.Abs(offset-180) < 12 {
		offset += 24
	}
	styles := imagegen.Styles()
	palettes := imagegen.PaletteNames()
	return &State{
		Width:  discSize,
		Height: discSize,
		Rotate: &RotateState{
			OffsetDeg:  math.Round(offset*10) / 10,
			Radius:     discSize/2 - 4,
			EmblemSeed: rng.Uint64(),
			Style:      string(styles[rng.IntN(len(styles))]),
			Palette:    palettes[rng.IntN(len(palettes))],
		},
	}
}

func (rotateGen) spec(st *State) *Spec {
	return &Spec{Rotate: &RotateSpec{
		Radius:  st.Rotate.Radius,
		StepDeg: pick(st.Level, 3.0, 2.0, 1.0),
	}}
}

func (rotateGen) assets(*State) []string { return []string{"disc"} }

func (rotateGen) render(st *State, part string) ([]byte, error) {
	if part != "disc" {
		return nil, ErrUnknownAsset
	}
	r := st.Rotate
	rng := st.rngFor()
	pal := imagegen.PaletteByName(r.Palette)
	img := imagegen.BackgroundStyle(rng, st.Width, st.Height, imagegen.Style(r.Style), pal)

	emblemRng := rand.New(rand.NewPCG(r.EmblemSeed, r.EmblemSeed^0x5851F42D4C957F2D))
	drawUprightEmblem(img, emblemRng, pal, float64(st.Width), float64(st.Height))

	turned := imagegen.RotateRGBA(img, r.OffsetDeg*math.Pi/180)
	mask := imagegen.CircleMask(st.Width, st.Height,
		float64(st.Width)/2, float64(st.Height)/2, float64(r.Radius))
	imagegen.ApplyAlpha(turned, mask)
	imagegen.Outline(turned, mask, color.RGBA{255, 255, 255, 255}, 0.35)
	return imagegen.EncodePNG(turned)
}

// drawUprightEmblem paints a motif with an unmistakable "up". Without it the
// challenge would be unsolvable on a rotationally symmetric background: the
// visitor has to be able to tell which way is up, and so does the grader.
func drawUprightEmblem(img *image.RGBA, rng *rand.Rand, pal imagegen.Palette, w, h float64) {
	ink := pal.Accent
	shadow := color.RGBA{0, 0, 0, 255}

	// Ground: a band across the lower third, which alone fixes the horizon.
	ground := imagegen.Path{{
		imagegen.Pt(0, h*0.72), imagegen.Pt(w, h*0.68),
		imagegen.Pt(w, h), imagegen.Pt(0, h),
	}}
	imagegen.FillPath(img, ground, shadow, 0.40)
	horizon := imagegen.Path{{imagegen.Pt(0, h*0.72), imagegen.Pt(w, h*0.68)}}
	imagegen.StrokePath(img, horizon, 2, ink, 0.55)

	// A pointed arch standing on the ground line.
	archW := w * (0.34 + rng.Float64()*0.12)
	archH := h * (0.44 + rng.Float64()*0.10)
	arch := imagegen.ArchPath(w*0.5, h*0.70, archW, archH)
	imagegen.FillPath(img, arch, shadow, 0.55)
	imagegen.StrokePath(img, arch, 2.2, ink, 0.85)

	// A star above the arch, offset to one side so the motif is not mirror
	// symmetric either.
	sx := w * (0.34 + rng.Float64()*0.32)
	star := imagegen.StarPath(sx, h*0.16, w*0.075, w*0.032, 6, -math.Pi/2)
	imagegen.FillPath(img, star, ink, 0.9)

	// A small doorway inside the arch, near the base.
	door := imagegen.ArchPath(w*0.5, h*0.70, archW*0.38, archH*0.42)
	imagegen.FillPath(img, door, ink, 0.55)
}

// rotateAnswer is the total clockwise correction the visitor applied, in
// degrees.
type rotateAnswer struct {
	Angle *float64 `json:"angle"`
}

func (rotateGen) check(st *State, raw json.RawMessage) (Outcome, error) {
	var a rotateAnswer
	if err := json.Unmarshal(raw, &a); err != nil || a.Angle == nil {
		return Outcome{}, ErrBadAnswer
	}
	tol := pick(st.Level, 15.0, 10.0, 7.0)
	// Correct when the visitor's correction cancels the offset.
	d := angularDistance(st.Rotate.OffsetDeg + *a.Angle)
	return Outcome{
		Correct:   d <= tol,
		Closeness: closeness(d, tol),
		Reason:    "angle",
	}, nil
}

// angularDistance returns how far a is from a whole number of turns, in
// degrees, always in [0, 180].
func angularDistance(a float64) float64 {
	d := math.Mod(math.Abs(a), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}
