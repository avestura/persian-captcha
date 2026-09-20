// Package challenge generates and grades the interactive puzzles.
//
// A challenge has two faces. The Spec is public: it is everything the browser
// needs to draw and operate the puzzle. The State is private: it additionally
// holds the solution and lives only in the session store.
//
// Artwork is never stored. State carries the random seed and the geometry
// parameters, so any image can be re-rendered deterministically when the
// browser asks for it. That keeps sessions around a kilobyte instead of the
// hundred kilobytes a cached PNG would cost, which matters when the store is
// Redis and every pending visitor holds one.
package challenge

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
)

// Kind identifies a challenge type.
type Kind string

// The available challenge kinds.
const (
	// KindSlider is the jigsaw slider: drag a puzzle piece into the notch.
	KindSlider Kind = "slider_jigsaw"
	// KindRotate asks the visitor to turn a disc back upright.
	KindRotate Kind = "rotate"
	// KindClickOrder asks for icons to be clicked in a stated order.
	KindClickOrder Kind = "click_order"
	// KindDragDrop asks for icons to be dragged into matching outlines.
	KindDragDrop Kind = "drag_drop"
	// KindAccessible is the non-visual fallback: a short localised question,
	// answerable from the keyboard and readable by a screen reader.
	KindAccessible Kind = "accessible"
)

// VisualKinds lists the pointer-driven challenges, in no particular order.
func VisualKinds() []Kind {
	return []Kind{KindSlider, KindRotate, KindClickOrder, KindDragDrop}
}

// ParseKind validates a challenge name.
func ParseKind(s string) (Kind, bool) {
	switch Kind(s) {
	case KindSlider, KindRotate, KindClickOrder, KindDragDrop, KindAccessible:
		return Kind(s), true
	}
	return "", false
}

// Level is the difficulty, which controls tolerances and item counts.
type Level string

// The available difficulty levels.
const (
	LevelEasy   Level = "easy"
	LevelNormal Level = "normal"
	LevelHard   Level = "hard"
)

// index maps a level onto 0, 1 or 2 for table lookups.
func (l Level) index() int {
	switch l {
	case LevelEasy:
		return 0
	case LevelHard:
		return 2
	default:
		return 1
	}
}

// pick returns the entry of a three-element table matching the level.
func pick[T any](l Level, easy, normal, hard T) T {
	switch l.index() {
	case 0:
		return easy
	case 2:
		return hard
	default:
		return normal
	}
}

// Board dimensions shared by the visual challenges. The width matches the
// widget so the artwork never needs scaling, which would blur the notch edge
// and make the target ambiguous.
const (
	BoardWidth  = 320
	BoardHeight = 200
)

// Errors returned by this package.
var (
	// ErrBadAnswer means the submitted answer was malformed rather than wrong.
	ErrBadAnswer = errors.New("challenge: malformed answer")
	// ErrUnknownAsset means the requested image part does not exist.
	ErrUnknownAsset = errors.New("challenge: unknown asset")
)

// State is the private, storable form of a challenge.
type State struct {
	Kind   Kind   `json:"kind"`
	Seed   uint64 `json:"seed"`
	Level  Level  `json:"level"`
	Width  int    `json:"w"`
	Height int    `json:"h"`

	Slider     *SliderState     `json:"slider,omitempty"`
	Rotate     *RotateState     `json:"rotate,omitempty"`
	ClickOrder *ClickOrderState `json:"click,omitempty"`
	DragDrop   *DragDropState   `json:"drag,omitempty"`
	Accessible *AccessibleState `json:"access,omitempty"`
}

// Spec is the public description handed to the browser. Exactly one of the
// per-kind fields is set.
type Spec struct {
	Kind   Kind `json:"kind"`
	Width  int  `json:"width"`
	Height int  `json:"height"`

	Slider     *SliderSpec     `json:"slider,omitempty"`
	Rotate     *RotateSpec     `json:"rotate,omitempty"`
	ClickOrder *ClickOrderSpec `json:"clickOrder,omitempty"`
	DragDrop   *DragDropSpec   `json:"dragDrop,omitempty"`
	Accessible *AccessibleSpec `json:"accessible,omitempty"`
}

// Outcome is the result of grading an answer.
type Outcome struct {
	// Correct reports whether the puzzle was solved within tolerance.
	Correct bool
	// Closeness is 1 for a perfect answer and falls towards 0 as the answer
	// gets worse. A near miss is a useful signal: humans overshoot slightly,
	// scripted solvers tend to be either exact or wildly wrong.
	Closeness float64
	// Reason is a short machine-readable tag for logs and metrics.
	Reason string
}

// Generator builds and grades one kind of challenge.
type generator interface {
	generate(rng *rand.Rand, level Level) *State
	spec(st *State) *Spec
	assets(st *State) []string
	render(st *State, part string) ([]byte, error)
	check(st *State, answer json.RawMessage) (Outcome, error)
}

var generators = map[Kind]generator{
	KindSlider:     sliderGen{},
	KindRotate:     rotateGen{},
	KindClickOrder: clickOrderGen{},
	KindDragDrop:   dragDropGen{},
	KindAccessible: accessibleGen{},
}

// New builds a challenge of the given kind, seeded from seed.
func New(kind Kind, level Level, seed uint64) (*State, error) {
	g, ok := generators[kind]
	if !ok {
		return nil, fmt.Errorf("challenge: unknown kind %q", kind)
	}
	// The seed is split across the two halves of PCG so that adjacent seeds
	// produce unrelated streams.
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	st := g.generate(rng, level)
	st.Kind = kind
	st.Seed = seed
	st.Level = level
	if st.Width == 0 {
		st.Width, st.Height = BoardWidth, BoardHeight
	}
	return st, nil
}

// Spec returns the public description of a challenge.
func (s *State) Spec() (*Spec, error) {
	g, ok := generators[s.Kind]
	if !ok {
		return nil, fmt.Errorf("challenge: unknown kind %q", s.Kind)
	}
	sp := g.spec(s)
	sp.Kind = s.Kind
	sp.Width, sp.Height = s.Width, s.Height
	return sp, nil
}

// Assets lists the image parts this challenge exposes.
func (s *State) Assets() []string {
	g, ok := generators[s.Kind]
	if !ok {
		return nil
	}
	return g.assets(s)
}

// Render re-draws one image part from the stored seed and geometry.
func (s *State) Render(part string) ([]byte, error) {
	g, ok := generators[s.Kind]
	if !ok {
		return nil, fmt.Errorf("challenge: unknown kind %q", s.Kind)
	}
	return g.render(s, part)
}

// Check grades an answer.
func (s *State) Check(answer json.RawMessage) (Outcome, error) {
	g, ok := generators[s.Kind]
	if !ok {
		return Outcome{}, fmt.Errorf("challenge: unknown kind %q", s.Kind)
	}
	return g.check(s, answer)
}

// rngFor returns the deterministic generator for a stored challenge, so that
// re-rendering reproduces exactly the artwork the visitor first saw.
func (s *State) rngFor() *rand.Rand {
	return rand.New(rand.NewPCG(s.Seed, s.Seed^0x9E3779B97F4A7C15))
}

// Point is a position on the board.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
