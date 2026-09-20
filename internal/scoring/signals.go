package scoring

import "math"

// Signals are passive environment hints the widget reports when it loads.
//
// These are intentionally limited to properties a page can read about its own
// rendering environment. There is no canvas, WebGL or audio fingerprinting,
// no font enumeration and no attempt to build a stable cross-site identifier:
// those raise real privacy problems and are the reason several captcha
// vendors are hard to deploy under the GDPR. What is collected here cannot
// meaningfully single out a person, and none of it is stored beyond the few
// minutes a session lives.
type Signals struct {
	// WebDriver is navigator.webdriver, which honest automation sets.
	WebDriver bool `json:"webdriver"`
	// HasTouch reports whether the device exposes touch points.
	HasTouch bool `json:"touch"`
	// Cores is navigator.hardwareConcurrency, 0 when unavailable.
	Cores int `json:"cores"`
	// DevicePixelRatio, screen and viewport size.
	DPR       float64 `json:"dpr"`
	ScreenW   int     `json:"sw"`
	ScreenH   int     `json:"sh"`
	ViewportW int     `json:"vw"`
	ViewportH int     `json:"vh"`
	// Languages is the length of navigator.languages.
	Languages int `json:"langs"`
	// PointerMoves counts pointer movement seen before the visitor engaged.
	PointerMoves int `json:"moves"`
	// DwellMS is how long the widget had been on screen before the visitor
	// interacted with it.
	DwellMS float64 `json:"dwell"`
}

// Timezone and page-visibility history are deliberately absent. Both were
// easy to collect and neither earned its place: the timezone is among the
// most identifying things a page can read, and whether someone switched tabs
// says nothing useful about whether they are a person. Collecting a signal
// that is never scored is the kind of thing that turns a captcha into a
// tracker by accident.

// ScoreSignals grades the environment. It is a weaker signal than the motion
// trace, so its penalties are correspondingly smaller: plenty of real people
// browse with unusual settings, and being unusual is not being a robot.
func ScoreSignals(s Signals) Assessment {
	a := Assessment{Score: 1}

	if s.WebDriver {
		// Not conclusive on its own: accessibility tooling and some corporate
		// browsers set it too. It is, however, worth most of the budget.
		a.penalise(0.55, "webdriver")
	}

	// A viewport or screen of zero size means the reported environment is
	// fabricated or the page is being rendered headless without a display.
	if s.ScreenW <= 0 || s.ScreenH <= 0 || s.ViewportW <= 0 || s.ViewportH <= 0 {
		a.penalise(0.25, "no_viewport")
	} else if s.ViewportW > s.ScreenW+64 || s.ViewportH > s.ScreenH+400 {
		// A viewport larger than the screen it supposedly sits in.
		a.penalise(0.15, "viewport_exceeds_screen")
	}

	if s.Languages == 0 {
		a.penalise(0.15, "no_languages")
	}
	if s.DPR <= 0 {
		a.penalise(0.10, "no_dpr")
	}
	// Headless defaults cluster at exactly 1; so do plenty of desktops, hence
	// the small weight.
	if s.Cores == 0 {
		a.penalise(0.05, "no_cores")
	}

	if !s.HasTouch && s.PointerMoves == 0 {
		// A mouse that never moved anywhere near the widget before clicking
		// it. Keyboard users reach the widget without pointer moves too, so
		// this stays cheap.
		a.penalise(0.15, "no_pointer_activity")
	}

	if s.DwellMS > 0 && s.DwellMS < 40 {
		a.penalise(0.15, "instant_engagement")
	}

	a.clamp()
	return a
}

// traceWeight is how much of the combined score comes from the motion trace.
// The environment gets the rest. The split is lopsided because the trace is
// far harder to fake convincingly than a set of navigator properties.
const traceWeight = 0.8

// rescueLimit is how far above its worse half a combined score may sit. It is
// what stops one dimension from covering for the other: a robotic trace with
// a flawless environment, or a flawless trace reported from a headless
// browser, both stay below the threshold at which a correct answer is
// accepted.
const rescueLimit = 0.25

// Combine merges the trace and signal assessments into one score.
func Combine(trace, signals Assessment) Assessment {
	weighted := trace.Score*traceWeight + signals.Score*(1-traceWeight)
	worst := math.Min(trace.Score, signals.Score)

	out := Assessment{
		Score: math.Min(weighted, worst+rescueLimit),
		Flags: append(append([]string{}, trace.Flags...), signals.Flags...),
	}
	out.clamp()
	return out
}

// Verdict is the decision derived from a score.
type Verdict string

// The possible verdicts.
const (
	// VerdictPass issues a token without further interaction.
	VerdictPass Verdict = "pass"
	// VerdictChallenge asks for a puzzle.
	VerdictChallenge Verdict = "challenge"
	// VerdictBlock refuses, at least for this session.
	VerdictBlock Verdict = "block"
)

// Thresholds are the score cut-offs used to pick a verdict.
type Thresholds struct {
	// Pass is the score at or above which no puzzle is needed.
	Pass float64
	// Block is the score below which the session is refused outright.
	Block float64
}

// DefaultThresholds are tuned to show a puzzle to a clear minority of real
// visitors while never silently passing something that looks scripted.
func DefaultThresholds() Thresholds {
	return Thresholds{Pass: 0.82, Block: 0.12}
}

// Decide maps a score onto a verdict.
func (t Thresholds) Decide(score float64) Verdict {
	switch {
	case score < t.Block:
		return VerdictBlock
	case score >= t.Pass:
		return VerdictPass
	default:
		return VerdictChallenge
	}
}
