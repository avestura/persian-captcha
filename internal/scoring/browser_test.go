package scoring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The traces in testdata/browser were captured from a real browser driving
// the real widget: Chrome, a real mouse, real pointer events, recorded by the
// widget's own telemetry and taken from the body of the /v1/solve request.
//
// Synthetic traces prove the scorer is self-consistent. Only these prove it
// agrees with what a browser actually produces. A scoring change that starts
// rejecting them is rejecting real people, whatever the synthetic cases say.
//
// To refresh them, run the demo and replay the gestures; the capture script
// lives in the repository README under "Updating the browser fixtures".
func TestRealBrowserTracesAreAccepted(t *testing.T) {
	dir := filepath.Join("testdata", "browser")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no captured traces; the scorer is only being checked against synthetic data")
	}

	// The gate the API applies before accepting a correct answer.
	const gate = 0.34

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		t.Run(strings.TrimSuffix(entry.Name(), ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var trace Trace
			if err := json.Unmarshal(raw, &trace); err != nil {
				t.Fatal(err)
			}
			if len(trace.Points) == 0 {
				t.Fatal("the fixture has no samples")
			}

			got := ScoreTrace(trace)
			if got.Score < gate {
				t.Errorf("a real mouse gesture scored %.2f, below the %.2f gate; flags: %v",
					got.Score, gate, got.Flags)
			}
			// Combined with a perfectly ordinary desktop environment, the
			// result should be comfortable rather than marginal.
			combined := Combine(got, ScoreSignals(Signals{
				Cores: 8, DPR: 2, ScreenW: 2560, ScreenH: 1440,
				ViewportW: 1280, ViewportH: 900, Languages: 2,
				PointerMoves: 30, DwellMS: 2500,
			}))
			if combined.Score < 0.6 {
				t.Errorf("a real gesture from an ordinary desktop scored %.2f overall; flags: %v",
					combined.Score, combined.Flags)
			}
			t.Logf("%s: trace %.2f, combined %.2f, %d samples over %.0fms, flags %v",
				entry.Name(), got.Score, combined.Score, len(trace.Points),
				trace.Points[len(trace.Points)-1].T-trace.Points[0].T, got.Flags)
		})
	}
}
