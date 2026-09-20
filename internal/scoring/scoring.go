// Package scoring turns the interaction data the widget collects into a
// human-likeness score.
//
// This is the part of an interactive captcha that actually does the work. A
// script can always compute the right answer to a slider or a rotation, so
// grading the answer alone stops nobody. What is hard to fake convincingly is
// the motion: real pointer paths wobble, overshoot, decelerate on approach and
// arrive at irregular time intervals. Synthetic ones tend to be straight,
// evenly sampled and suspiciously exact.
//
// Every signal here is a heuristic, and each one alone is weak. They are
// combined into a single score, and the thresholds are deliberately forgiving:
// a false reject costs a real person their session, while a false accept costs
// an attacker one solved captcha out of the many they still have to pay for.
package scoring

import (
	"math"
	"sort"
)

// Sample is one recorded pointer position. T is milliseconds since the
// challenge was displayed.
type Sample struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	T float64 `json:"t"`
}

// InputMode is how the visitor drove the challenge.
type InputMode string

// The recognised input modes. Keyboard input is scored separately because it
// is legitimately regular: arrow keys produce evenly spaced steps, which would
// look like a bot under the pointer rules.
const (
	ModePointer  InputMode = "pointer"
	ModeTouch    InputMode = "touch"
	ModeKeyboard InputMode = "keyboard"
)

// Trace is the interaction record submitted with an answer.
type Trace struct {
	Mode   InputMode `json:"mode"`
	Points []Sample  `json:"points"`
	// StartT and EndT bracket the interaction, in milliseconds since the
	// challenge was displayed.
	StartT float64 `json:"startT"`
	EndT   float64 `json:"endT"`
	// Corrections counts how many times the visitor released and grabbed
	// again, or reversed direction deliberately.
	Corrections int `json:"corrections"`
}

// Assessment is a score in [0,1], where 1 is most human, plus the tags that
// explain it. Flags are for operators reading logs; they are never returned to
// the browser, which would just tell an attacker what to fix.
type Assessment struct {
	Score float64
	Flags []string
}

// penalise subtracts from the score and records why.
func (a *Assessment) penalise(amount float64, flag string) {
	a.Score -= amount
	a.Flags = append(a.Flags, flag)
}

// clamp bounds the score to [0,1].
func (a *Assessment) clamp() {
	a.Score = math.Max(0, math.Min(1, a.Score))
}

// MaxPoints caps how many samples are accepted, so a client cannot make the
// server do unbounded work by posting a huge trace.
const MaxPoints = 600

// ScoreTrace grades an interaction trace.
func ScoreTrace(t Trace) Assessment {
	a := Assessment{Score: 1}
	if t.Mode == ModeKeyboard {
		return scoreKeyboard(t)
	}

	pts := t.Points
	if len(pts) > MaxPoints {
		pts = pts[:MaxPoints]
	}
	if len(pts) == 0 {
		a.penalise(0.9, "no_samples")
		a.clamp()
		return a
	}
	if len(pts) < 4 {
		// Almost no motion at all: either an instant programmatic answer, or a
		// browser that never reported moves. The second case is rare enough,
		// and recoverable enough (the visitor simply sees another challenge),
		// that this is scored as the first.
		a.penalise(0.7, "too_few_samples")
		if pts[len(pts)-1].T-pts[0].T < 120 {
			a.penalise(0.25, "too_fast")
		}
		a.clamp()
		return a
	}

	duration := pts[len(pts)-1].T - pts[0].T
	switch {
	case duration < 120:
		a.penalise(0.45, "too_fast")
	case duration < 260:
		a.penalise(0.15, "fast")
	case duration > 120_000:
		a.penalise(0.20, "stale")
	}

	// Sampling density. A real pointer stream at 60Hz over half a second is
	// dozens of points; a handful of waypoints suggests a synthesised path.
	if rate := float64(len(pts)) / math.Max(duration, 1) * 1000; rate < 12 {
		a.penalise(0.25, "sparse_sampling")
	}

	straight := straightness(pts)
	switch {
	case straight < 1.002:
		// A perfectly straight line is the single strongest bot tell.
		a.penalise(0.45, "path_perfectly_straight")
	case straight < 1.01:
		a.penalise(0.20, "path_very_straight")
	case straight > 6:
		// Wildly meandering paths are usually fine, but an extreme value can
		// mean a replayed trace from a different screen size.
		a.penalise(0.10, "path_erratic")
	}

	if dev := lateralDeviation(pts); dev < 0.6 {
		a.penalise(0.25, "no_lateral_wobble")
	}

	if cv := intervalVariation(pts); cv < 0.06 {
		// Human input arrives on the browser's frame clock, which jitters.
		a.penalise(0.30, "uniform_intervals")
	} else if cv < 0.12 {
		a.penalise(0.10, "regular_intervals")
	}

	speeds := speedProfile(pts)
	if len(speeds) >= 6 {
		if !decelerates(speeds) {
			// People slow down as they approach the target; a constant-speed
			// glide that stops dead is characteristic of a scripted drag.
			a.penalise(0.20, "no_deceleration")
		}
		if constantSpeed(speeds) {
			a.penalise(0.20, "constant_speed")
		}
	}

	if allIntegers(pts) && evenlySpacedX(pts) {
		a.penalise(0.25, "quantised_path")
	}

	if t.Mode == ModeTouch {
		// Touch traces are legitimately coarser and shorter; give some of the
		// sampling-related penalties back.
		a.Score += 0.15
		a.Flags = append(a.Flags, "touch_allowance")
	}

	a.clamp()
	return a
}

// scoreKeyboard grades arrow-key driven input, where regular spacing is
// expected rather than suspicious.
func scoreKeyboard(t Trace) Assessment {
	a := Assessment{Score: 1, Flags: []string{"keyboard"}}
	steps := len(t.Points)
	duration := t.EndT - t.StartT

	switch {
	case steps < 2:
		a.penalise(0.5, "kb_no_steps")
	case duration < 80:
		// Even held-down auto-repeat cannot deliver many steps this fast.
		a.penalise(0.4, "kb_too_fast")
	case duration < 250:
		a.penalise(0.1, "kb_fast")
	}

	// Auto-repeat produces near-identical intervals, so only a completely
	// degenerate trace is penalised here.
	if steps >= 6 && intervalVariation(t.Points) < 0.005 {
		a.penalise(0.15, "kb_synthetic_intervals")
	}
	a.clamp()
	return a
}

// straightness is path length divided by start-to-end distance. It is 1 for a
// perfectly straight path and grows with wander.
func straightness(pts []Sample) float64 {
	var length float64
	for i := 1; i < len(pts); i++ {
		length += math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
	}
	direct := math.Hypot(pts[len(pts)-1].X-pts[0].X, pts[len(pts)-1].Y-pts[0].Y)
	if direct < 1 {
		return 1
	}
	return length / direct
}

// lateralDeviation is the root-mean-square distance of the path from the
// straight line joining its endpoints, in pixels.
func lateralDeviation(pts []Sample) float64 {
	x0, y0 := pts[0].X, pts[0].Y
	x1, y1 := pts[len(pts)-1].X, pts[len(pts)-1].Y
	dx, dy := x1-x0, y1-y0
	l := math.Hypot(dx, dy)
	if l < 1 {
		return 0
	}
	var sum float64
	for _, p := range pts {
		// Distance from a point to a line, via the 2D cross product.
		d := math.Abs((p.X-x0)*dy-(p.Y-y0)*dx) / l
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(pts)))
}

// intervalVariation is the coefficient of variation of the inter-sample times.
func intervalVariation(pts []Sample) float64 {
	if len(pts) < 3 {
		return 1
	}
	deltas := make([]float64, 0, len(pts)-1)
	for i := 1; i < len(pts); i++ {
		d := pts[i].T - pts[i-1].T
		if d >= 0 {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) < 2 {
		return 1
	}
	mean, sd := meanStdDev(deltas)
	if mean <= 0 {
		return 0
	}
	return sd / mean
}

// speedProfile returns instantaneous speeds in pixels per millisecond.
func speedProfile(pts []Sample) []float64 {
	out := make([]float64, 0, len(pts)-1)
	for i := 1; i < len(pts); i++ {
		dt := pts[i].T - pts[i-1].T
		if dt <= 0 {
			continue
		}
		out = append(out, math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)/dt)
	}
	return out
}

// decelerates reports whether the path slows down noticeably before it ends.
func decelerates(speeds []float64) bool {
	tail := speeds[len(speeds)*3/4:]
	peak := 0.0
	for _, s := range speeds {
		peak = math.Max(peak, s)
	}
	if peak == 0 {
		return false
	}
	tailMean, _ := meanStdDev(tail)
	return tailMean < peak*0.6
}

// constantSpeed reports whether the speed barely varies across the path.
func constantSpeed(speeds []float64) bool {
	mean, sd := meanStdDev(speeds)
	if mean <= 0 {
		return false
	}
	return sd/mean < 0.12
}

// allIntegers reports whether every coordinate is a whole number. Real pointer
// events on high-DPI displays carry fractional coordinates.
func allIntegers(pts []Sample) bool {
	for _, p := range pts {
		if p.X != math.Trunc(p.X) || p.Y != math.Trunc(p.Y) {
			return false
		}
	}
	return true
}

// evenlySpacedX reports whether the horizontal steps are near-identical, which
// is what a naive "move in n equal steps" script produces.
func evenlySpacedX(pts []Sample) bool {
	if len(pts) < 5 {
		return false
	}
	steps := make([]float64, 0, len(pts)-1)
	for i := 1; i < len(pts); i++ {
		steps = append(steps, math.Abs(pts[i].X-pts[i-1].X))
	}
	sort.Float64s(steps)
	median := steps[len(steps)/2]
	if median < 0.5 {
		return false
	}
	same := 0
	for _, s := range steps {
		if math.Abs(s-median) < 0.5 {
			same++
		}
	}
	return float64(same)/float64(len(steps)) > 0.9
}

func meanStdDev(v []float64) (mean, sd float64) {
	if len(v) == 0 {
		return 0, 0
	}
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	for _, x := range v {
		sd += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(sd / float64(len(v)))
}
