package challenge

import (
	"bytes"
	"encoding/json"
	"image/png"
	"math"
	"testing"
)

// seeds gives every table-driven case a spread of boards rather than one
// lucky layout.
var seeds = []uint64{1, 2, 3, 7, 11, 40_000, 1 << 40, ^uint64(0)}

func TestEveryKindGeneratesAndRenders(t *testing.T) {
	for _, kind := range append(VisualKinds(), KindAccessible) {
		for _, level := range []Level{LevelEasy, LevelNormal, LevelHard} {
			t.Run(string(kind)+"/"+string(level), func(t *testing.T) {
				st, err := New(kind, level, seeds[0])
				if err != nil {
					t.Fatal(err)
				}
				if st.Kind != kind || st.Level != level {
					t.Fatalf("state carries kind %q level %q", st.Kind, st.Level)
				}
				if _, err := st.Spec(); err != nil {
					t.Fatalf("Spec: %v", err)
				}
				for _, part := range st.Assets() {
					data, err := st.Render(part)
					if err != nil {
						t.Fatalf("Render(%q): %v", part, err)
					}
					img, err := png.Decode(bytes.NewReader(data))
					if err != nil {
						t.Fatalf("Render(%q) produced an undecodable PNG: %v", part, err)
					}
					if b := img.Bounds(); b.Dx() == 0 || b.Dy() == 0 {
						t.Errorf("Render(%q) produced an empty image", part)
					}
				}
				if _, err := st.Render("no-such-part"); err != ErrUnknownAsset {
					t.Errorf("Render of an unknown part = %v, want ErrUnknownAsset", err)
				}
			})
		}
	}
}

// The artwork is not stored; it is redrawn from the seed whenever the browser
// asks for it. If rendering were not deterministic the visitor would be shown
// one board and graded against another.
func TestRenderingIsDeterministic(t *testing.T) {
	for _, kind := range VisualKinds() {
		t.Run(string(kind), func(t *testing.T) {
			first, err := New(kind, LevelNormal, 12345)
			if err != nil {
				t.Fatal(err)
			}
			// Round-tripping through JSON is what really happens: the state
			// goes to the store and comes back before the image is drawn.
			encoded, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			var second State
			if err := json.Unmarshal(encoded, &second); err != nil {
				t.Fatal(err)
			}

			for _, part := range first.Assets() {
				a, err := first.Render(part)
				if err != nil {
					t.Fatal(err)
				}
				b, err := second.Render(part)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(a, b) {
					t.Errorf("%s/%s differs after a store round trip", kind, part)
				}
			}
		})
	}
}

// The public spec must not leak the solution. This is checked by serialising
// it and looking for the answer, which catches a field accidentally gaining a
// JSON tag far more reliably than reading the struct definitions.
func TestSpecDoesNotLeakTheAnswer(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindSlider, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := st.Spec()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		var generic map[string]any
		if err := json.Unmarshal(encoded, &generic); err != nil {
			t.Fatal(err)
		}
		slider, _ := generic["slider"].(map[string]any)
		if _, leaked := slider["targetX"]; leaked {
			t.Fatalf("the slider spec contains targetX: %s", encoded)
		}
		// The answer must not appear under any other name either.
		target := float64(st.Slider.TargetX)
		for key, value := range slider {
			if n, ok := value.(float64); ok && n == target && key != "travelMax" {
				t.Errorf("spec field %q equals the answer (%v)", key, target)
			}
		}
	}

	rot, err := New(KindRotate, LevelNormal, 99)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := rot.Spec()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(spec)
	if bytes.Contains(encoded, []byte("offsetDeg")) {
		t.Errorf("the rotate spec exposes the offset: %s", encoded)
	}

	acc, err := New(KindAccessible, LevelNormal, 5)
	if err != nil {
		t.Fatal(err)
	}
	accSpec, err := acc.Spec()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(accSpec)
	if bytes.Contains(encoded, []byte("answerIdx")) {
		t.Errorf("the accessible spec exposes the answer index: %s", encoded)
	}
}

func TestSliderGrading(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindSlider, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		target := float64(st.Slider.TargetX)

		check := func(x float64) Outcome {
			out, err := st.Check(json.RawMessage(`{"x":` + ftoa(x) + `}`))
			if err != nil {
				t.Fatalf("Check(%v): %v", x, err)
			}
			return out
		}

		if out := check(target); !out.Correct || out.Closeness != 1 {
			t.Errorf("seed %d: the exact answer was rejected: %+v", seed, out)
		}
		if out := check(target + 3); !out.Correct {
			t.Errorf("seed %d: a 3px miss was rejected; the tolerance is too tight", seed)
		}
		if out := check(target + 40); out.Correct {
			t.Errorf("seed %d: a 40px miss was accepted", seed)
		}
		// A near miss should still score better than a wild one, since the
		// margin feeds into the risk score.
		near, far := check(target+8), check(target+80)
		if near.Closeness <= far.Closeness {
			t.Errorf("seed %d: closeness did not decay with distance (%v vs %v)",
				seed, near.Closeness, far.Closeness)
		}
	}
}

// A slider answer must not be solvable by slamming the handle to either stop.
func TestSliderTargetIsAwayFromTheStops(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindSlider, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := st.Spec()
		if err != nil {
			t.Fatal(err)
		}
		travel := spec.Slider.TravelMax
		target := st.Slider.TargetX
		if target < travel/4 {
			t.Errorf("seed %d: target %d is too near the start of a %d track", seed, target, travel)
		}
		if target > travel-4 {
			t.Errorf("seed %d: target %d is at the end stop of a %d track", seed, target, travel)
		}
	}
}

func TestRotateGrading(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindRotate, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		// The visitor supplies the correction that cancels the offset.
		correct := 360 - st.Rotate.OffsetDeg

		out, err := st.Check(json.RawMessage(`{"angle":` + ftoa(correct) + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if !out.Correct {
			t.Errorf("seed %d: the exact correction was rejected", seed)
		}
		// Turning a whole extra revolution is the same orientation.
		out, err = st.Check(json.RawMessage(`{"angle":` + ftoa(correct+360) + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if !out.Correct {
			t.Errorf("seed %d: a full extra turn was rejected", seed)
		}
		out, err = st.Check(json.RawMessage(`{"angle":` + ftoa(correct+45) + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if out.Correct {
			t.Errorf("seed %d: a 45 degree error was accepted", seed)
		}
	}
}

func TestRotateOffsetIsNeverNearlyUpright(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindRotate, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		if d := angularDistance(st.Rotate.OffsetDeg); d < 35 {
			t.Errorf("seed %d: the disc starts only %.1f degrees from upright", seed, d)
		}
	}
}

func TestClickOrderGrading(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindClickOrder, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		c := st.ClickOrder

		correct := make([]Point, 0, len(c.Sequence))
		for _, idx := range c.Sequence {
			correct = append(correct, Point{X: c.Icons[idx].X, Y: c.Icons[idx].Y})
		}
		if out := checkPoints(t, st, correct); !out.Correct {
			t.Errorf("seed %d: clicking every target dead centre was rejected", seed)
		}

		// The order matters: the same points in reverse must fail, unless the
		// sequence is palindromic by coincidence of length one.
		if len(correct) > 1 {
			reversed := make([]Point, len(correct))
			for i, p := range correct {
				reversed[len(correct)-1-i] = p
			}
			if out := checkPoints(t, st, reversed); out.Correct {
				t.Errorf("seed %d: the reversed order was accepted", seed)
			}
		}

		// A decoy is on the board but not in the sequence; clicking it must
		// not pass.
		if len(c.Icons) > len(c.Sequence) {
			inSequence := map[int]bool{}
			for _, idx := range c.Sequence {
				inSequence[idx] = true
			}
			var decoy Point
			for i, icon := range c.Icons {
				if !inSequence[i] {
					decoy = Point{X: icon.X, Y: icon.Y}
					break
				}
			}
			wrong := append([]Point(nil), correct...)
			wrong[0] = decoy
			if out := checkPoints(t, st, wrong); out.Correct {
				t.Errorf("seed %d: clicking a decoy was accepted", seed)
			}
		}

		// Too few points is a malformed answer, not a lucky one.
		if out := checkPoints(t, st, correct[:len(correct)-1]); out.Correct {
			t.Errorf("seed %d: a short answer was accepted", seed)
		}
	}
}

func checkPoints(t *testing.T, st *State, pts []Point) Outcome {
	t.Helper()
	body, err := json.Marshal(map[string]any{"points": pts})
	if err != nil {
		t.Fatal(err)
	}
	out, err := st.Check(body)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return out
}

// Two icons of the same shape would make a named target ambiguous, and the
// grader would mark a reasonable answer wrong.
func TestClickOrderShapesAreDistinct(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindClickOrder, LevelHard, seed)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, icon := range st.ClickOrder.Icons {
			if seen[icon.Shape] {
				t.Fatalf("seed %d: %q appears twice on the board", seed, icon.Shape)
			}
			seen[icon.Shape] = true
		}
		if len(st.ClickOrder.Sequence) == 0 {
			t.Fatalf("seed %d: nothing to click", seed)
		}
	}
}

func TestDragDropGrading(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindDragDrop, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		d := st.DragDrop
		half := float64(d.PieceSize) / 2

		placements := make([]map[string]any, 0, len(d.Pieces))
		for i, p := range d.Pieces {
			slot := d.Slots[p.Slot]
			placements = append(placements, map[string]any{
				"id": i, "x": slot.X - half, "y": slot.Y - half,
			})
		}
		if out := checkPlacements(t, st, placements); !out.Correct {
			t.Errorf("seed %d: perfectly placed pieces were rejected", seed)
		}

		// Swapping two pieces must fail; shapes and slots are matched pairs.
		if len(placements) > 1 {
			swapped := append([]map[string]any(nil), placements...)
			swapped[0], swapped[1] = copyPlacement(placements[1]), copyPlacement(placements[0])
			swapped[0]["id"], swapped[1]["id"] = placements[0]["id"], placements[1]["id"]
			if out := checkPlacements(t, st, swapped); out.Correct {
				t.Errorf("seed %d: two swapped pieces were accepted", seed)
			}
		}

		// Pieces left in the tray must fail.
		untouched := make([]map[string]any, 0, len(d.Pieces))
		for i, p := range d.Pieces {
			untouched = append(untouched, map[string]any{"id": i, "x": p.StartX, "y": p.StartY})
		}
		if out := checkPlacements(t, st, untouched); out.Correct {
			t.Errorf("seed %d: unmoved pieces were accepted", seed)
		}
	}
}

func copyPlacement(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func checkPlacements(t *testing.T, st *State, placements []map[string]any) Outcome {
	t.Helper()
	body, err := json.Marshal(map[string]any{"placements": placements})
	if err != nil {
		t.Fatal(err)
	}
	out, err := st.Check(body)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return out
}

func TestDragDropRejectsDuplicateIDs(t *testing.T) {
	st, err := New(KindDragDrop, LevelNormal, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.DragDrop.Pieces) < 2 {
		t.Skip("this seed produced a single piece")
	}
	// The list is the right length, so this is not caught by the count check:
	// one piece is reported twice and another not at all.
	placements := make([]map[string]any, 0, len(st.DragDrop.Pieces))
	for range st.DragDrop.Pieces {
		placements = append(placements, map[string]any{"id": 0, "x": 10.0, "y": 10.0})
	}
	body, err := json.Marshal(map[string]any{"placements": placements})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Check(body); err != ErrBadAnswer {
		t.Errorf("duplicate ids gave %v, want ErrBadAnswer", err)
	}

	if _, err := st.Check([]byte(`{"placements":[{"id":-1,"x":0,"y":0}]}`)); err == nil {
		t.Error("a negative piece id was accepted")
	}
}

func TestAccessibleGrading(t *testing.T) {
	for _, seed := range seeds {
		st, err := New(KindAccessible, LevelNormal, seed)
		if err != nil {
			t.Fatal(err)
		}
		a := st.Accessible
		if len(a.Options) != 4 {
			t.Fatalf("seed %d: %d options, want 4", seed, len(a.Options))
		}
		if a.AnswerIdx < 0 || a.AnswerIdx >= len(a.Options) {
			t.Fatalf("seed %d: answer index %d is out of range", seed, a.AnswerIdx)
		}

		right := a.Options[a.AnswerIdx].ID
		out, err := st.Check(json.RawMessage(`{"choice":"` + right + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if !out.Correct {
			t.Errorf("seed %d: the correct choice was rejected", seed)
		}
		for i, opt := range a.Options {
			if i == a.AnswerIdx {
				continue
			}
			out, err := st.Check(json.RawMessage(`{"choice":"` + opt.ID + `"}`))
			if err != nil {
				t.Fatal(err)
			}
			if out.Correct {
				t.Errorf("seed %d: wrong option %d was accepted", seed, i)
			}
		}
	}
}

// Every word in the question bank needs a translation, and the distractors
// must genuinely not belong to the category being asked about.
func TestAccessibleCategoriesAreUnambiguous(t *testing.T) {
	membership := map[string][]string{}
	for category, words := range wordCategories {
		for _, w := range words {
			membership[w] = append(membership[w], category)
		}
	}
	for word, categories := range membership {
		if len(categories) > 1 {
			t.Errorf("%q belongs to more than one category (%v), so a question naming "+
				"either could have two correct answers", word, categories)
		}
	}
	if len(categoryNames) != len(wordCategories) {
		t.Errorf("categoryNames lists %d entries but there are %d categories",
			len(categoryNames), len(wordCategories))
	}
	for _, name := range categoryNames {
		if _, ok := wordCategories[name]; !ok {
			t.Errorf("categoryNames mentions %q, which has no word list", name)
		}
	}
}

func TestMalformedAnswers(t *testing.T) {
	cases := map[Kind][]string{
		KindSlider:     {`{}`, `{"x":"left"}`, `[]`, `null`},
		KindRotate:     {`{}`, `{"angle":null}`, `"nope"`},
		KindAccessible: {`{}`, `{"choice":12}`},
	}
	for kind, bodies := range cases {
		for _, body := range bodies {
			st, err := New(kind, LevelNormal, 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.Check(json.RawMessage(body)); err != ErrBadAnswer {
				t.Errorf("%s: Check(%s) = %v, want ErrBadAnswer", kind, body, err)
			}
		}
	}
}

func TestParseKind(t *testing.T) {
	for _, kind := range append(VisualKinds(), KindAccessible) {
		if got, ok := ParseKind(string(kind)); !ok || got != kind {
			t.Errorf("ParseKind(%q) = %q, %v", kind, got, ok)
		}
	}
	if _, ok := ParseKind("definitely_not_a_kind"); ok {
		t.Error("ParseKind accepted an unknown name")
	}
}

func TestUnknownKind(t *testing.T) {
	if _, err := New("nope", LevelNormal, 1); err == nil {
		t.Error("New accepted an unknown kind")
	}
	st := &State{Kind: "nope"}
	if _, err := st.Spec(); err == nil {
		t.Error("Spec accepted an unknown kind")
	}
	if _, err := st.Check(json.RawMessage(`{}`)); err == nil {
		t.Error("Check accepted an unknown kind")
	}
	if parts := st.Assets(); parts != nil {
		t.Errorf("Assets on an unknown kind = %v, want nil", parts)
	}
}

// Difficulty has to mean something: a hard challenge must be graded at least
// as strictly as an easy one.
func TestHarderLevelsAreStricter(t *testing.T) {
	const miss = 7.0
	results := map[Level]bool{}
	for _, level := range []Level{LevelEasy, LevelNormal, LevelHard} {
		st, err := New(KindSlider, level, 4242)
		if err != nil {
			t.Fatal(err)
		}
		out, err := st.Check(json.RawMessage(`{"x":` + ftoa(float64(st.Slider.TargetX)+miss) + `}`))
		if err != nil {
			t.Fatal(err)
		}
		results[level] = out.Correct
	}
	if !results[LevelEasy] {
		t.Errorf("a %.0fpx miss was rejected at the easy level", miss)
	}
	if results[LevelHard] {
		t.Errorf("a %.0fpx miss was accepted at the hard level", miss)
	}
}

func ftoa(v float64) string {
	b, _ := json.Marshal(math.Round(v*1000) / 1000)
	return string(b)
}
