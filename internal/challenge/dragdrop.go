package challenge

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"

	"avestura.dev/persian-captcha/internal/imagegen"
)

// trayHeight is the strip along the bottom of the board where the loose
// pieces start out.
const trayHeight = 56

// dragSlot is an empty outline waiting for its matching piece.
type dragSlot struct {
	Shape string  `json:"shape"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Rot   float64 `json:"rot"`
}

// dragPiece is a loose icon the visitor drags.
type dragPiece struct {
	Shape  string  `json:"shape"`
	Slot   int     `json:"slot"`   // index into Slots
	StartX float64 `json:"startX"` // top-left of the piece image
	StartY float64 `json:"startY"`
	Tone   int     `json:"tone"`
}

// DragDropState is the private state of the drag-and-drop challenge.
type DragDropState struct {
	Slots  []dragSlot  `json:"slots"`
	Pieces []dragPiece `json:"pieces"`
	// Radius is the icon radius; PieceSize is the side of a piece image.
	Radius    float64 `json:"radius"`
	PieceSize int     `json:"pieceSize"`

	Style   string `json:"style"`
	Palette string `json:"palette"`
}

// DragPieceSpec describes one draggable piece to the browser.
type DragPieceSpec struct {
	// ID is the index the browser echoes back when reporting where it
	// dropped this piece.
	ID int `json:"id"`
	// Shape is the icon identifier, used for the accessible label.
	Shape string `json:"shape"`
	// Asset is the image part name to request for this piece.
	Asset string `json:"asset"`
	// StartX and StartY are the piece image's initial top-left position.
	StartX float64 `json:"startX"`
	StartY float64 `json:"startY"`
	Size   int     `json:"size"`
}

// DragDropSpec is the public description. The slot positions are visible in
// the board artwork, so they are not repeated here.
type DragDropSpec struct {
	Pieces []DragPieceSpec `json:"pieces"`
	// TrayY is the top of the starting tray, so the browser can draw its own
	// boundary and snap pieces back on a miss.
	TrayY float64 `json:"trayY"`
}

type dragDropGen struct{}

func (dragDropGen) generate(rng *rand.Rand, level Level) *State {
	n := pick(level, 2, 3, 4)
	radius := pick(level, 22.0, 20.0, 18.0)
	pieceSize := int(math.Ceil(radius*2)) + 8

	kinds := imagegen.ShapeKinds()
	rng.Shuffle(len(kinds), func(i, j int) { kinds[i], kinds[j] = kinds[j], kinds[i] })
	n = min(n, len(kinds))

	// Slots live above the tray; keep them apart so a sloppy drop cannot be
	// within tolerance of two different slots at once.
	slots := make([]dragSlot, 0, n)
	placed := make([]clickTarget, 0, n)
	minDist := radius * 3.1
	pad := radius + 10
	for i := range n {
		var pt Point
		ok := false
		for range 300 {
			p := Point{
				X: pad + rng.Float64()*(BoardWidth-2*pad),
				Y: pad + rng.Float64()*(BoardHeight-trayHeight-2*pad),
			}
			clear := true
			for _, q := range placed {
				if math.Hypot(p.X-q.X, p.Y-q.Y) < minDist {
					clear = false
					break
				}
			}
			if clear {
				pt, ok = p, true
				break
			}
		}
		if !ok {
			break
		}
		placed = append(placed, clickTarget{X: pt.X, Y: pt.Y})
		slots = append(slots, dragSlot{
			Shape: string(kinds[i]),
			X:     pt.X,
			Y:     pt.Y,
			Rot:   (rng.Float64() - 0.5) * 0.5,
		})
	}

	// Pieces are laid out along the tray in shuffled order, so their position
	// in the tray carries no hint about which slot they belong to.
	order := rng.Perm(len(slots))
	pieces := make([]dragPiece, len(slots))
	trayY := float64(BoardHeight - trayHeight)
	slotW := float64(BoardWidth) / float64(max(1, len(slots)))
	for i, slotIdx := range order {
		cx := slotW*(float64(i)+0.5) + (rng.Float64()-0.5)*8
		pieces[i] = dragPiece{
			Shape:  slots[slotIdx].Shape,
			Slot:   slotIdx,
			StartX: math.Round(cx - float64(pieceSize)/2),
			StartY: math.Round(trayY + (trayHeight-float64(pieceSize))/2),
			Tone:   rng.IntN(6),
		}
	}

	styles := imagegen.Styles()
	palettes := imagegen.PaletteNames()
	return &State{
		DragDrop: &DragDropState{
			Slots:     slots,
			Pieces:    pieces,
			Radius:    radius,
			PieceSize: pieceSize,
			Style:     string(styles[rng.IntN(len(styles))]),
			Palette:   palettes[rng.IntN(len(palettes))],
		},
	}
}

func (dragDropGen) spec(st *State) *Spec {
	d := st.DragDrop
	out := make([]DragPieceSpec, len(d.Pieces))
	for i, p := range d.Pieces {
		out[i] = DragPieceSpec{
			ID:     i,
			Shape:  p.Shape,
			Asset:  fmt.Sprintf("piece%d", i),
			StartX: p.StartX,
			StartY: p.StartY,
			Size:   d.PieceSize,
		}
	}
	return &Spec{DragDrop: &DragDropSpec{
		Pieces: out,
		TrayY:  float64(st.Height - trayHeight),
	}}
}

func (dragDropGen) assets(st *State) []string {
	parts := []string{"board"}
	for i := range st.DragDrop.Pieces {
		parts = append(parts, fmt.Sprintf("piece%d", i))
	}
	return parts
}

func (g dragDropGen) render(st *State, part string) ([]byte, error) {
	d := st.DragDrop
	pal := imagegen.PaletteByName(d.Palette)

	if strings.HasPrefix(part, "piece") {
		var idx int
		if _, err := fmt.Sscanf(part, "piece%d", &idx); err != nil || idx < 0 || idx >= len(d.Pieces) {
			return nil, ErrUnknownAsset
		}
		return renderDragPiece(d, pal, idx)
	}
	if part != "board" {
		return nil, ErrUnknownAsset
	}

	rng := st.rngFor()
	img := imagegen.BackgroundStyle(rng, st.Width, st.Height, imagegen.Style(d.Style), pal)

	// Empty slots: a recessed outline, so it is obvious something is missing.
	for _, s := range d.Slots {
		p := imagegen.ShapePath(imagegen.ShapeKind(s.Shape), s.X, s.Y, d.Radius, s.Rot)
		imagegen.FillPath(img, p, color.RGBA{0, 0, 0, 255}, 0.5)
		imagegen.StrokePath(img, p, 2.2, pal.Light, 0.95)
	}

	// The tray, drawn as a darker band with a rule along its top edge.
	trayY := float64(st.Height - trayHeight)
	tray := imagegen.Path{{
		imagegen.Pt(0, trayY), imagegen.Pt(float64(st.Width), trayY),
		imagegen.Pt(float64(st.Width), float64(st.Height)), imagegen.Pt(0, float64(st.Height)),
	}}
	imagegen.FillPath(img, tray, color.RGBA{0, 0, 0, 255}, 0.55)
	rule := imagegen.Path{{imagegen.Pt(0, trayY), imagegen.Pt(float64(st.Width), trayY)}}
	imagegen.StrokePath(img, rule, 1.6, pal.Accent, 0.85)

	return imagegen.EncodePNG(img)
}

// renderDragPiece draws one loose icon on a transparent square.
func renderDragPiece(d *DragDropState, pal imagegen.Palette, idx int) ([]byte, error) {
	p := d.Pieces[idx]
	size := d.PieceSize
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := float64(size) / 2
	path := imagegen.ShapePath(imagegen.ShapeKind(p.Shape), c, c, d.Radius, d.Slots[p.Slot].Rot)

	imagegen.FillPath(img, path.Translate(1, 2), color.RGBA{0, 0, 0, 255}, 0.35)
	imagegen.FillPath(img, path, pal.Tone(p.Tone), 1)
	imagegen.StrokePath(img, path, 1.8, pal.Accent, 0.95)
	return imagegen.EncodePNG(img)
}

// dragPlacement is where the visitor dropped one piece: the top-left of the
// piece image, in board coordinates.
type dragPlacement struct {
	ID int     `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

type dragAnswer struct {
	Placements []dragPlacement `json:"placements"`
}

func (dragDropGen) check(st *State, raw json.RawMessage) (Outcome, error) {
	var a dragAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return Outcome{}, ErrBadAnswer
	}
	d := st.DragDrop

	// Structural validation comes before grading: an answer naming a piece
	// that does not exist, or naming one twice, is malformed rather than
	// merely wrong, and saying so plainly beats letting it fall through to a
	// distance check that happens to fail.
	seen := make(map[int]bool, len(d.Pieces))
	for _, pl := range a.Placements {
		if pl.ID < 0 || pl.ID >= len(d.Pieces) || seen[pl.ID] {
			return Outcome{}, ErrBadAnswer
		}
		seen[pl.ID] = true
	}
	if len(a.Placements) != len(d.Pieces) {
		return Outcome{Reason: "wrong_count"}, nil
	}

	tol := d.Radius * pick(st.Level, 0.95, 0.8, 0.65)
	half := float64(d.PieceSize) / 2

	worst := 0.0
	for _, pl := range a.Placements {
		slot := d.Slots[d.Pieces[pl.ID].Slot]
		dist := math.Hypot(pl.X+half-slot.X, pl.Y+half-slot.Y)
		worst = math.Max(worst, dist)
		if dist > tol {
			return Outcome{Closeness: closeness(worst, tol), Reason: "misplaced"}, nil
		}
	}
	return Outcome{Correct: true, Closeness: closeness(worst, tol), Reason: "placements"}, nil
}
