package challenge

import (
	"encoding/json"
	"image"
	"image/draw"
	"math"
	"math/rand/v2"

	"avestura.dev/persian-captcha/internal/imagegen"
)

// SliderState is the private state of the jigsaw slider challenge.
type SliderState struct {
	// Size is the length of the piece body, excluding knobs.
	Size int `json:"size"`
	// Margin is the blank border the piece image carries so that outward
	// knobs are not clipped.
	Margin int `json:"margin"`
	// TargetX and TopY are the correct position of the piece image, in board
	// coordinates. TargetX is the answer.
	TargetX int `json:"targetX"`
	TopY    int `json:"topY"`
	// Tabs describes the piece silhouette: one of -1, 0 or +1 per side.
	Tabs [4]int `json:"tabs"`
	// Style and Palette pin the artwork so re-rendering is reproducible even
	// if the generator's random choices change order in a future version.
	Style   string `json:"style"`
	Palette string `json:"palette"`
}

// SliderSpec is what the browser is told: the geometry of the track and the
// piece, but not where the piece belongs.
type SliderSpec struct {
	// PieceSize is the side of the square piece image.
	PieceSize int `json:"pieceSize"`
	// PieceTop is the vertical position of the piece image; it never moves.
	PieceTop int `json:"pieceTop"`
	// TravelMax is the largest valid x for the piece image.
	TravelMax int `json:"travelMax"`
}

type sliderGen struct{}

func (sliderGen) generate(rng *rand.Rand, level Level) *State {
	size := pick(level, 58, 50, 44)
	// A knob reaches about a third of the body beyond the edge.
	margin := int(math.Ceil(float64(size) * 0.34))

	var tabs [4]int
	for i := range tabs {
		// Every side gets a knob or a blank, never a flat edge: a flat side
		// would make the silhouette less distinctive and the notch easier to
		// locate by a simple edge filter.
		if rng.IntN(2) == 0 {
			tabs[i] = 1
		} else {
			tabs[i] = -1
		}
	}

	imgSize := size + 2*margin
	travelMax := BoardWidth - imgSize
	// Keep the target in the right two-thirds of the track so the visitor has
	// a meaningful distance to drag, and away from the very end so that
	// slamming the slider to the stop is never the answer.
	lo := travelMax / 3
	hi := travelMax - 6
	targetX := lo + rng.IntN(max(1, hi-lo))

	topLo := 0
	topHi := BoardHeight - imgSize
	topY := topLo + rng.IntN(max(1, topHi-topLo))

	styles := imagegen.Styles()
	palettes := imagegen.PaletteNames()
	return &State{
		Slider: &SliderState{
			Size:    size,
			Margin:  margin,
			TargetX: targetX,
			TopY:    topY,
			Tabs:    tabs,
			Style:   string(styles[rng.IntN(len(styles))]),
			Palette: palettes[rng.IntN(len(palettes))],
		},
	}
}

func (sliderGen) spec(st *State) *Spec {
	s := st.Slider
	return &Spec{Slider: &SliderSpec{
		PieceSize: s.Size + 2*s.Margin,
		PieceTop:  s.TopY,
		TravelMax: st.Width - (s.Size + 2*s.Margin),
	}}
}

func (sliderGen) assets(*State) []string { return []string{"board", "piece"} }

func (g sliderGen) render(st *State, part string) ([]byte, error) {
	s := st.Slider
	rng := st.rngFor()
	bg := imagegen.BackgroundStyle(rng, st.Width, st.Height,
		imagegen.Style(s.Style), imagegen.PaletteByName(s.Palette))

	imgSize := s.Size + 2*s.Margin
	// The piece outline, expressed in the piece image's own coordinates.
	piecePath := imagegen.JigsawPiecePath(float64(s.Margin), float64(s.Margin), float64(s.Size), s.Tabs)
	mask := imagegen.MaskFromPath(imgSize, imgSize, piecePath)

	switch part {
	case "board":
		board := image.NewRGBA(bg.Bounds())
		draw.Draw(board, bg.Bounds(), bg, image.Point{}, draw.Src)
		imagegen.CarveNotch(board, mask, s.TargetX, s.TopY)
		return imagegen.EncodePNG(board)
	case "piece":
		piece := imagegen.CutPiece(bg, mask, s.TargetX, s.TopY)
		imagegen.Bevel(piece, mask)
		return imagegen.EncodePNG(piece)
	default:
		return nil, ErrUnknownAsset
	}
}

// sliderAnswer is the browser's submission: the x it left the piece at.
type sliderAnswer struct {
	X *float64 `json:"x"`
}

func (sliderGen) check(st *State, raw json.RawMessage) (Outcome, error) {
	var a sliderAnswer
	if err := json.Unmarshal(raw, &a); err != nil || a.X == nil {
		return Outcome{}, ErrBadAnswer
	}
	tol := pick(st.Level, 9.0, 6.0, 4.0)
	d := math.Abs(*a.X - float64(st.Slider.TargetX))
	return Outcome{
		Correct:   d <= tol,
		Closeness: closeness(d, tol),
		Reason:    "offset",
	}, nil
}

// closeness turns an error distance into a 0..1 score that decays smoothly
// past the tolerance, so a near miss still scores better than a wild guess.
func closeness(d, tol float64) float64 {
	if d <= 0 {
		return 1
	}
	return math.Max(0, 1-d/(tol*4))
}
