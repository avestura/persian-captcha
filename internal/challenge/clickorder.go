package challenge

import (
	"encoding/json"
	"image/color"
	"math"
	"math/rand/v2"

	"avestura.dev/persian-captcha/internal/imagegen"
)

// clickTarget is one icon placed on the board.
type clickTarget struct {
	Shape string  `json:"shape"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Rot   float64 `json:"rot"`
	Tone  int     `json:"tone"`
}

// ClickOrderState is the private state of the click-in-order challenge.
type ClickOrderState struct {
	// Icons holds every icon drawn on the board, including decoys.
	Icons []clickTarget `json:"icons"`
	// Sequence lists the indices into Icons that must be clicked, in order.
	Sequence []int `json:"seq"`
	// Radius is the drawn icon radius in pixels.
	Radius float64 `json:"radius"`

	Style   string `json:"style"`
	Palette string `json:"palette"`
}

// ClickOrderSpec tells the browser which icons to ask for, by name and in
// order. Positions are not disclosed: they are only in the artwork.
type ClickOrderSpec struct {
	// Sequence holds shape identifiers such as "star" or "crescent". The
	// browser looks up the localised name for each and builds the prompt.
	Sequence []string `json:"sequence"`
	// Radius lets the browser size its click markers to match the artwork.
	Radius float64 `json:"radius"`
}

type clickOrderGen struct{}

func (clickOrderGen) generate(rng *rand.Rand, level Level) *State {
	wanted := pick(level, 3, 3, 4)
	decoys := pick(level, 2, 3, 4)
	radius := pick(level, 23.0, 21.0, 19.0)
	total := wanted + decoys

	// Every icon on the board is a different shape, so naming one in the
	// prompt always identifies exactly one target.
	kinds := imagegen.ShapeKinds()
	rng.Shuffle(len(kinds), func(i, j int) { kinds[i], kinds[j] = kinds[j], kinds[i] })
	if total > len(kinds) {
		total = len(kinds)
		wanted = min(wanted, total)
	}

	icons := make([]clickTarget, 0, total)
	for i := range total {
		pt, ok := placeAway(rng, icons, radius, radius*2.35)
		if !ok {
			break
		}
		icons = append(icons, clickTarget{
			Shape: string(kinds[i]),
			X:     pt.X,
			Y:     pt.Y,
			Rot:   (rng.Float64() - 0.5) * 0.7,
			Tone:  rng.IntN(6),
		})
	}
	if wanted > len(icons) {
		wanted = len(icons)
	}

	// The icons to click are a random subset in a random order, so neither
	// drawing order nor position leaks the sequence.
	perm := rng.Perm(len(icons))
	seq := perm[:wanted]

	styles := imagegen.Styles()
	palettes := imagegen.PaletteNames()
	return &State{
		ClickOrder: &ClickOrderState{
			Icons:    icons,
			Sequence: seq,
			Radius:   radius,
			Style:    string(styles[rng.IntN(len(styles))]),
			Palette:  palettes[rng.IntN(len(palettes))],
		},
	}
}

// placeAway samples a position that keeps a minimum distance from every icon
// placed so far, and from the board edges.
func placeAway(rng *rand.Rand, placed []clickTarget, radius, minDist float64) (Point, bool) {
	pad := radius + 8
	for range 200 {
		p := Point{
			X: pad + rng.Float64()*(BoardWidth-2*pad),
			Y: pad + rng.Float64()*(BoardHeight-2*pad),
		}
		ok := true
		for _, q := range placed {
			if math.Hypot(p.X-q.X, p.Y-q.Y) < minDist {
				ok = false
				break
			}
		}
		if ok {
			return p, true
		}
	}
	return Point{}, false
}

func (clickOrderGen) spec(st *State) *Spec {
	c := st.ClickOrder
	seq := make([]string, len(c.Sequence))
	for i, idx := range c.Sequence {
		seq[i] = c.Icons[idx].Shape
	}
	return &Spec{ClickOrder: &ClickOrderSpec{Sequence: seq, Radius: c.Radius}}
}

func (clickOrderGen) assets(*State) []string { return []string{"board"} }

func (clickOrderGen) render(st *State, part string) ([]byte, error) {
	if part != "board" {
		return nil, ErrUnknownAsset
	}
	c := st.ClickOrder
	rng := st.rngFor()
	pal := imagegen.PaletteByName(c.Palette)
	img := imagegen.BackgroundStyle(rng, st.Width, st.Height, imagegen.Style(c.Style), pal)

	for _, ic := range c.Icons {
		p := imagegen.ShapePath(imagegen.ShapeKind(ic.Shape), ic.X, ic.Y, c.Radius, ic.Rot)
		// A dark drop shadow first, then the icon, then a light rim: enough
		// separation from any background for the icon to stay legible.
		imagegen.FillPath(img, p.Translate(2, 3), color.RGBA{0, 0, 0, 255}, 0.55)
		imagegen.FillPath(img, p, pal.Light, 0.55)
		imagegen.FillPath(img, p, pal.Tone(ic.Tone), 0.8)
		imagegen.StrokePath(img, p, 2, pal.Accent, 1)
	}
	return imagegen.EncodePNG(img)
}

// clickAnswer is the ordered list of points the visitor clicked.
type clickAnswer struct {
	Points []Point `json:"points"`
}

func (clickOrderGen) check(st *State, raw json.RawMessage) (Outcome, error) {
	var a clickAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return Outcome{}, ErrBadAnswer
	}
	c := st.ClickOrder
	if len(a.Points) != len(c.Sequence) {
		return Outcome{Reason: "wrong_count"}, nil
	}
	tol := c.Radius * pick(st.Level, 1.5, 1.3, 1.15)

	worst := 0.0
	for i, idx := range c.Sequence {
		want := c.Icons[idx]
		d := math.Hypot(a.Points[i].X-want.X, a.Points[i].Y-want.Y)
		worst = math.Max(worst, d)
		if d > tol {
			// Report which step went wrong only as a coarse tag; the browser
			// is never told where the icon actually was.
			return Outcome{Closeness: closeness(worst, tol), Reason: "wrong_icon"}, nil
		}
	}
	return Outcome{Correct: true, Closeness: closeness(worst, tol), Reason: "sequence"}, nil
}
