package scoring

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// humanDrag synthesises a trace with the properties a real drag has: a
// bell-shaped speed profile, vertical wobble, sub-pixel coordinates and
// irregular frame timing.
func humanDrag(rng *rand.Rand, distance float64) Trace {
	steps := 40 + rng.IntN(25)
	pts := make([]Sample, 0, steps)
	t := 0.0
	for i := range steps {
		p := float64(i) / float64(steps-1)
		// Ease-in-out travel, so the hand accelerates and then slows down on
		// approach.
		eased := 0.5 - 0.5*math.Cos(math.Pi*p)
		x := eased*distance + rng.NormFloat64()*0.6
		y := 14 + math.Sin(p*5)*1.8 + rng.NormFloat64()*0.7
		t += 10 + rng.Float64()*9
		pts = append(pts, Sample{X: x, Y: y, T: math.Round(t)})
	}
	return Trace{Mode: ModePointer, Points: pts, StartT: 0, EndT: t, Corrections: rng.IntN(2)}
}

// linearBot is the naive automation: a straight line, equal steps, perfectly
// even timing, whole-pixel coordinates.
func linearBot(distance float64) Trace {
	const steps = 20
	pts := make([]Sample, 0, steps)
	for i := range steps {
		pts = append(pts, Sample{
			X: math.Trunc(distance * float64(i) / float64(steps-1)),
			Y: 14,
			T: float64(i * 10),
		})
	}
	return Trace{Mode: ModePointer, Points: pts, EndT: (steps - 1) * 10}
}

// teleportBot answers instantly with no motion at all.
func teleportBot(distance float64) Trace {
	return Trace{
		Mode:   ModePointer,
		Points: []Sample{{X: 0, Y: 0, T: 0}, {X: distance, Y: 0, T: 4}},
		EndT:   4,
	}
}

func TestHumanTracesOutscoreBots(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 22))

	var humanScores []float64
	for range 200 {
		humanScores = append(humanScores, ScoreTrace(humanDrag(rng, 120+rng.Float64()*100)).Score)
	}
	slices.Sort(humanScores)
	median := humanScores[len(humanScores)/2]
	worst := humanScores[0]

	linear := ScoreTrace(linearBot(180)).Score
	teleport := ScoreTrace(teleportBot(180)).Score

	if median < 0.85 {
		t.Errorf("median human score %.2f is too low; real visitors would be challenged repeatedly", median)
	}
	// The gate the API applies to a correct answer.
	const gate = 0.34
	if worst < gate {
		t.Errorf("the worst of 200 synthetic human drags scored %.2f, below the %.2f gate", worst, gate)
	}
	if linear >= gate {
		t.Errorf("a perfectly straight, evenly timed drag scored %.2f and would be accepted", linear)
	}
	if teleport >= gate {
		t.Errorf("an instant jump scored %.2f and would be accepted", teleport)
	}
}

func TestFlagsExplainTheScore(t *testing.T) {
	a := ScoreTrace(linearBot(200))
	want := []string{"path_perfectly_straight", "no_lateral_wobble", "uniform_intervals"}
	for _, flag := range want {
		if !slices.Contains(a.Flags, flag) {
			t.Errorf("a straight, uniform bot trace did not raise %q; flags were %v", flag, a.Flags)
		}
	}
}

// Keyboard input is regular by nature. Judging it by the pointer rules would
// reject every visitor who cannot use a mouse.
func TestKeyboardInputIsNotPenalisedForRegularity(t *testing.T) {
	pts := make([]Sample, 0, 30)
	for i := range 30 {
		pts = append(pts, Sample{X: float64(i * 3), Y: 0, T: float64(i * 60)})
	}
	kb := Trace{Mode: ModeKeyboard, Points: pts, StartT: 0, EndT: 29 * 60}

	keyboard := ScoreTrace(kb).Score
	if keyboard < 0.8 {
		t.Errorf("steady arrow-key input scored %.2f; keyboard users would be locked out", keyboard)
	}

	// The same data, declared as pointer input, should score much worse.
	asPointer := kb
	asPointer.Mode = ModePointer
	if pointer := ScoreTrace(asPointer).Score; pointer >= keyboard {
		t.Errorf("identical data scored %.2f as a pointer and %.2f as a keyboard; "+
			"the modes are not being judged differently", pointer, keyboard)
	}
}

func TestKeyboardStillRejectsImpossibleSpeed(t *testing.T) {
	// Thirty key presses in twenty milliseconds is beyond auto-repeat.
	pts := make([]Sample, 0, 30)
	for i := range 30 {
		pts = append(pts, Sample{X: float64(i), T: float64(i) * 0.6})
	}
	a := ScoreTrace(Trace{Mode: ModeKeyboard, Points: pts, StartT: 0, EndT: 18})
	if a.Score > 0.7 {
		t.Errorf("impossibly fast key input scored %.2f", a.Score)
	}
}

func TestTouchIsJudgedMoreLeniently(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	trace := humanDrag(rng, 150)
	// Thin the samples the way a touch stream is naturally coarser.
	var thinned []Sample
	for i, p := range trace.Points {
		if i%3 == 0 {
			thinned = append(thinned, p)
		}
	}
	asPointer := Trace{Mode: ModePointer, Points: thinned, EndT: trace.EndT}
	asTouch := asPointer
	asTouch.Mode = ModeTouch

	if ScoreTrace(asTouch).Score <= ScoreTrace(asPointer).Score {
		t.Error("a sparse touch trace was not given the touch allowance")
	}
}

func TestOversizedTraceIsTruncated(t *testing.T) {
	pts := make([]Sample, 0, MaxPoints*3)
	for i := range MaxPoints * 3 {
		pts = append(pts, Sample{X: float64(i), Y: float64(i % 7), T: float64(i * 8)})
	}
	// The point is that this returns rather than doing unbounded work; the
	// score itself is not the assertion.
	if got := ScoreTrace(Trace{Mode: ModePointer, Points: pts, EndT: 1000}); got.Score < 0 || got.Score > 1 {
		t.Errorf("score %v is out of range", got.Score)
	}
}

func TestSignalScoring(t *testing.T) {
	clean := Signals{
		Cores: 8, DPR: 2, ScreenW: 2560, ScreenH: 1440,
		ViewportW: 1280, ViewportH: 900, Languages: 2, PointerMoves: 40, DwellMS: 2400,
	}
	if got := ScoreSignals(clean).Score; got < 0.95 {
		t.Errorf("an ordinary desktop scored %.2f", got)
	}

	driven := clean
	driven.WebDriver = true
	if got := ScoreSignals(driven).Score; got >= ScoreSignals(clean).Score {
		t.Errorf("navigator.webdriver did not cost anything: %.2f", got)
	}

	headless := Signals{Cores: 0, DPR: 0, Languages: 0}
	if got := ScoreSignals(headless).Score; got > 0.5 {
		t.Errorf("an empty environment scored %.2f", got)
	}

	// Being merely unusual should not be fatal: a touch device with one core
	// and no pointer movement is a perfectly ordinary phone.
	phone := Signals{
		HasTouch: true, Cores: 4, DPR: 3, ScreenW: 390, ScreenH: 844,
		ViewportW: 390, ViewportH: 700, Languages: 1, DwellMS: 1800,
	}
	if got := ScoreSignals(phone).Score; got < 0.9 {
		t.Errorf("a normal phone scored %.2f and would be challenged needlessly", got)
	}
}

func TestCombineIsNotRescuedByOneGoodHalf(t *testing.T) {
	good := Assessment{Score: 1}
	bad := Assessment{Score: 0.05}

	// The gate the API applies before accepting a correct answer.
	const gate = 0.34

	if got := Combine(bad, good).Score; got >= gate {
		t.Errorf("a robotic trace from a clean-looking browser scored %.2f, at or above the %.2f gate", got, gate)
	}
	if got := Combine(good, bad).Score; got >= gate {
		t.Errorf("a flawless trace from a headless-looking browser scored %.2f, at or above the %.2f gate", got, gate)
	}
	if got := Combine(good, good).Score; got != 1 {
		t.Errorf("two perfect halves combined to %.2f", got)
	}

	// An ordinary visitor with a slightly odd environment must still clear
	// the gate comfortably, or the check is simply a tax on unusual setups.
	ordinary := Combine(Assessment{Score: 0.9}, Assessment{Score: 0.75})
	if ordinary.Score < 0.8 {
		t.Errorf("a good trace from a slightly unusual browser scored %.2f", ordinary.Score)
	}

	if got := Combine(good, good); got.Score > 1 || Combine(bad, bad).Score < 0 {
		t.Errorf("Combine produced an out-of-range score %.2f", got.Score)
	}
}

func TestThresholds(t *testing.T) {
	th := DefaultThresholds()
	cases := map[float64]Verdict{
		0.0:  VerdictBlock,
		0.05: VerdictBlock,
		0.5:  VerdictChallenge,
		0.81: VerdictChallenge,
		0.95: VerdictPass,
		1.0:  VerdictPass,
	}
	for score, want := range cases {
		if got := th.Decide(score); got != want {
			t.Errorf("Decide(%.2f) = %q, want %q", score, got, want)
		}
	}
}

func TestStraightnessAndDeviation(t *testing.T) {
	straight := []Sample{{X: 0, Y: 0}, {X: 50, Y: 0}, {X: 100, Y: 0}}
	if got := straightness(straight); math.Abs(got-1) > 1e-9 {
		t.Errorf("straightness of a straight line = %v, want 1", got)
	}
	if got := lateralDeviation(straight); got != 0 {
		t.Errorf("lateralDeviation of a straight line = %v, want 0", got)
	}

	wobbly := []Sample{{X: 0, Y: 0}, {X: 50, Y: 20}, {X: 100, Y: 0}}
	if got := straightness(wobbly); got <= 1 {
		t.Errorf("straightness of a bent path = %v, want more than 1", got)
	}
	if got := lateralDeviation(wobbly); got <= 0 {
		t.Errorf("lateralDeviation of a bent path = %v, want more than 0", got)
	}
}

func TestIntervalVariation(t *testing.T) {
	even := []Sample{{T: 0}, {T: 10}, {T: 20}, {T: 30}}
	if got := intervalVariation(even); got != 0 {
		t.Errorf("intervalVariation of even timing = %v, want 0", got)
	}
	uneven := []Sample{{T: 0}, {T: 7}, {T: 26}, {T: 31}}
	if got := intervalVariation(uneven); got <= 0 {
		t.Errorf("intervalVariation of jittery timing = %v, want more than 0", got)
	}
}
