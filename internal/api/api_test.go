package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"avestura.dev/persian-captcha/internal/challenge"
	"avestura.dev/persian-captcha/internal/config"
	"avestura.dev/persian-captcha/internal/i18n"
	"avestura.dev/persian-captcha/internal/pow"
	"avestura.dev/persian-captcha/internal/scoring"
	"avestura.dev/persian-captcha/internal/session"
	"avestura.dev/persian-captcha/internal/store"
)

// ---- harness ---------------------------------------------------------------

type harness struct {
	t      *testing.T
	server *httptest.Server
	store  *store.Memory
	cfg    *config.Config
}

const (
	testOrigin  = "https://app.example.com"
	siteKey     = "pc_site_test"
	siteSecret  = "pc_secret_test"
	otherKey    = "pc_site_other"
	otherSecret = "pc_secret_other"
)

func newHarness(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()

	rate := 1_000_000 // effectively off, except where a test asks for limits
	cfg := &config.Config{
		Listen:        ":0",
		DefaultLocale: "en",
		SessionTTL:    config.Duration(5 * time.Minute),
		TokenTTL:      config.Duration(5 * time.Minute),
		Store:         config.StoreConfig{Driver: "memory"},
		RateLimits: config.RateLimits{
			SessionPerIP:  rate,
			SolvePerIP:    rate,
			VerifyPerSite: rate,
			FailuresPerIP: rate,
		},
		Sites: []config.Site{
			{
				Name: "Test", Key: siteKey, Secret: siteSecret,
				AllowedOrigins: []string{testOrigin},
				Difficulty:     config.DifficultyEasy,
				Locale:         "en",
				// Every test visitor gets a puzzle, so the interactive path is
				// what is exercised rather than the passive one.
				AlwaysChallenge: true,
				MaxAttempts:     3,
			},
			{
				Name: "Other", Key: otherKey, Secret: otherSecret,
				AllowedOrigins: []string{testOrigin},
				Difficulty:     config.DifficultyEasy,
				MaxAttempts:    3,
			},
		},
	}
	if mutate != nil {
		mutate(cfg)
	}
	// finalize is not exported, so the public loader is used to build the
	// indexes the server relies on.
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/captcha.json"
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	locales, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	memory := store.NewMemory()
	t.Cleanup(func() { memory.Close() })

	srv, err := New(Options{
		Config:   func() *config.Config { return loaded },
		Store:    memory,
		Locales:  locales,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		IPSecret: []byte("test-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &harness{t: t, server: ts, store: memory, cfg: loaded}
}

// postJSON sends a JSON request and decodes the reply into dst.
func (h *harness) postJSON(path string, body any, dst any) int {
	h.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	res, err := http.Post(h.server.URL+path, "application/json", bytes.NewReader(encoded))
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	if dst != nil {
		if err := json.NewDecoder(res.Body).Decode(dst); err != nil && err != io.EOF {
			h.t.Fatalf("decoding %s: %v", path, err)
		}
	}
	return res.StatusCode
}

// session reads the private session record straight out of the store. This is
// how the tests learn the answer to a challenge, which is exactly what a
// browser cannot do.
func (h *harness) session(sid string) *session.Session {
	h.t.Helper()
	raw, err := h.store.Get(context.Background(), "s:"+sid)
	if err != nil {
		h.t.Fatalf("session %s not in the store: %v", sid, err)
	}
	var s session.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		h.t.Fatal(err)
	}
	return &s
}

func solve(t *testing.T, spec powSpec) string {
	t.Helper()
	nonce, ok := pow.Solve(pow.Challenge{Algorithm: spec.Algorithm, Prefix: spec.Prefix, Bits: spec.Bits}, 20_000_000)
	if !ok {
		t.Fatalf("could not solve a %d bit proof of work", spec.Bits)
	}
	return nonce
}

// begin runs session + assess and returns the session id and the reply.
func (h *harness) begin(key string) (string, flowResp) {
	h.t.Helper()
	var started sessionResp
	if code := h.postJSON("/v1/session", sessionReq{SiteKey: key, Origin: testOrigin}, &started); code != http.StatusOK {
		h.t.Fatalf("POST /v1/session = %d", code)
	}
	var out flowResp
	code := h.postJSON("/v1/assess", assessReq{
		SID:     started.SID,
		Nonce:   solve(h.t, started.PoW),
		Signals: goodSignals(),
	}, &out)
	if code != http.StatusOK {
		h.t.Fatalf("POST /v1/assess = %d", code)
	}
	return started.SID, out
}

func goodSignals() scoring.Signals {
	return scoring.Signals{
		Cores: 8, DPR: 2, ScreenW: 2560, ScreenH: 1440,
		ViewportW: 1280, ViewportH: 900, Languages: 2,
		PointerMoves: 55, DwellMS: 3100,
	}
}

// humanTrace synthesises the motion a real drag produces: eased travel,
// vertical wobble, sub-pixel coordinates and jittery frame timing.
func humanTrace(rng *rand.Rand) scoring.Trace {
	steps := 45 + rng.IntN(20)
	pts := make([]scoring.Sample, 0, steps)
	t := 0.0
	for i := range steps {
		p := float64(i) / float64(steps-1)
		eased := 0.5 - 0.5*math.Cos(math.Pi*p)
		t += 10 + rng.Float64()*8
		pts = append(pts, scoring.Sample{
			X: eased*180 + rng.NormFloat64()*0.6,
			Y: 20 + math.Sin(p*6)*2 + rng.NormFloat64()*0.7,
			T: math.Round(t),
		})
	}
	return scoring.Trace{Mode: scoring.ModePointer, Points: pts, EndT: t, Corrections: 1}
}

func robotTrace() scoring.Trace {
	pts := make([]scoring.Sample, 0, 16)
	for i := range 16 {
		pts = append(pts, scoring.Sample{X: float64(i * 12), Y: 20, T: float64(i * 8)})
	}
	return scoring.Trace{Mode: scoring.ModePointer, Points: pts, EndT: 120}
}

// correctAnswer builds the right answer for whatever challenge is in flight,
// reading the solution from the stored session.
func correctAnswer(t *testing.T, s *session.Session) any {
	t.Helper()
	st := s.Challenge
	switch st.Kind {
	case challenge.KindSlider:
		return map[string]any{"x": st.Slider.TargetX}
	case challenge.KindRotate:
		return map[string]any{"angle": 360 - st.Rotate.OffsetDeg}
	case challenge.KindClickOrder:
		pts := make([]map[string]float64, 0, len(st.ClickOrder.Sequence))
		for _, idx := range st.ClickOrder.Sequence {
			icon := st.ClickOrder.Icons[idx]
			pts = append(pts, map[string]float64{"x": icon.X, "y": icon.Y})
		}
		return map[string]any{"points": pts}
	case challenge.KindDragDrop:
		half := float64(st.DragDrop.PieceSize) / 2
		placements := make([]map[string]any, 0, len(st.DragDrop.Pieces))
		for i, p := range st.DragDrop.Pieces {
			slot := st.DragDrop.Slots[p.Slot]
			placements = append(placements, map[string]any{
				"id": i, "x": slot.X - half, "y": slot.Y - half,
			})
		}
		return map[string]any{"placements": placements}
	case challenge.KindAccessible:
		return map[string]any{"choice": st.Accessible.Options[st.Accessible.AnswerIdx].ID}
	default:
		t.Fatalf("no answer builder for %q", st.Kind)
		return nil
	}
}

func (h *harness) verify(secret, token string) verifyResponse {
	h.t.Helper()
	form := url.Values{"secret": {secret}, "response": {token}}
	res, err := http.PostForm(h.server.URL+"/v1/siteverify", form)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	var out verifyResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		h.t.Fatal(err)
	}
	return out
}

// ---- the happy path --------------------------------------------------------

func TestFullFlowForEveryChallengeKind(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 9))

	// Pin the site to one kind at a time so every challenge is exercised
	// rather than whichever the random picker happened to choose.
	for _, kind := range append(challenge.VisualKinds(), challenge.KindAccessible) {
		t.Run(string(kind), func(t *testing.T) {
			h := newHarness(t, func(c *config.Config) {
				c.Sites[0].Challenges = []string{string(kind)}
			})

			sid, first := h.begin(siteKey)
			if kind == challenge.KindAccessible {
				// The accessible question is reached by asking for it, which
				// is what the "having trouble?" link does. The reply is
				// decoded into a fresh value so nothing carries over from the
				// visual challenge it replaces.
				var switched flowResp
				if code := h.postJSON("/v1/challenge", challengeReq{SID: sid, Kind: string(kind)}, &switched); code != http.StatusOK {
					t.Fatalf("POST /v1/challenge = %d", code)
				}
				first = switched
				if len(first.Assets) != 0 {
					t.Errorf("the accessible question declared images: %v", first.Assets)
				}
			}
			if first.Status != "challenge" {
				t.Fatalf("status = %q, want challenge", first.Status)
			}
			if first.Challenge == nil || first.Challenge.Kind != kind {
				t.Fatalf("got challenge %+v, want kind %q", first.Challenge, kind)
			}
			if first.PoW == nil {
				t.Fatal("no proof of work was issued with the challenge")
			}

			// Every declared image must actually render.
			for part, href := range first.Assets {
				res, err := http.Get(h.server.URL + href)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if res.StatusCode != http.StatusOK {
					t.Errorf("asset %q = %d", part, res.StatusCode)
				}
				if ct := res.Header.Get("Content-Type"); ct != "image/png" {
					t.Errorf("asset %q content type = %q", part, ct)
				}
				if !bytes.HasPrefix(body, []byte("\x89PNG")) {
					t.Errorf("asset %q is not a PNG", part)
				}
				if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
					t.Errorf("asset %q is cacheable (%q); a cached challenge is a replayable one", part, cc)
				}
			}

			stored := h.session(sid)
			var out flowResp
			code := h.postJSON("/v1/solve", solveReq{
				SID:    sid,
				Nonce:  solve(t, specOf(stored.PoW)),
				Answer: mustJSON(t, correctAnswer(t, stored)),
				Trace:  humanTrace(rng),
			}, &out)
			if code != http.StatusOK {
				t.Fatalf("POST /v1/solve = %d", code)
			}
			if out.Status != "pass" {
				t.Fatalf("status = %q with reason %q, want pass", out.Status, out.Reason)
			}
			if out.Token == "" {
				t.Fatal("no token was issued")
			}

			verified := h.verify(siteSecret, out.Token)
			if !verified.Success {
				t.Fatalf("siteverify failed: %v", verified.ErrorCodes)
			}
			if verified.Hostname != "app.example.com" {
				t.Errorf("hostname = %q", verified.Hostname)
			}
			if verified.Solved != string(kind) {
				t.Errorf("solved = %q, want %q", verified.Solved, kind)
			}
			if verified.Score <= 0 {
				t.Errorf("score = %v", verified.Score)
			}
		})
	}
}

// ---- tokens ----------------------------------------------------------------

func TestTokenIsSingleUse(t *testing.T) {
	h := newHarness(t, nil)
	token := h.earnToken(rand.New(rand.NewPCG(1, 1)))

	if first := h.verify(siteSecret, token); !first.Success {
		t.Fatalf("the first redemption failed: %v", first.ErrorCodes)
	}
	second := h.verify(siteSecret, token)
	if second.Success {
		t.Error("the same token was redeemed twice; a captcha response must not be replayable")
	}
	if len(second.ErrorCodes) == 0 || second.ErrorCodes[0] != errTimeoutDuplicate {
		t.Errorf("error codes = %v, want %q", second.ErrorCodes, errTimeoutDuplicate)
	}
}

func TestTokenIsBoundToItsSite(t *testing.T) {
	h := newHarness(t, nil)
	token := h.earnToken(rand.New(rand.NewPCG(2, 2)))

	// Another tenant's secret must not be able to spend this token, or one
	// customer could farm captchas on another customer's traffic.
	stolen := h.verify(otherSecret, token)
	if stolen.Success {
		t.Fatal("a token was redeemed by a different site's secret")
	}
	// Having been rejected, the token must still be spendable by its owner:
	// a failed cross-tenant attempt must not burn somebody else's token.
	if legitimate := h.verify(siteSecret, token); !legitimate.Success {
		t.Errorf("the rightful owner could not redeem the token afterwards: %v", legitimate.ErrorCodes)
	}
}

func TestSiteVerifyErrorCodes(t *testing.T) {
	h := newHarness(t, nil)
	cases := []struct {
		name          string
		secret, token string
		wantCode      string
	}{
		{"no secret", "", "something", errMissingSecret},
		{"unknown secret", "not-a-secret", "something", errInvalidSecret},
		{"no response", siteSecret, "", errMissingResponse},
		{"unknown response", siteSecret, "not-a-real-token", errTimeoutDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.verify(tc.secret, tc.token)
			if got.Success {
				t.Fatal("verification succeeded")
			}
			if len(got.ErrorCodes) == 0 || got.ErrorCodes[0] != tc.wantCode {
				t.Errorf("error codes = %v, want %q", got.ErrorCodes, tc.wantCode)
			}
		})
	}
}

func TestSiteVerifyAcceptsJSON(t *testing.T) {
	h := newHarness(t, nil)
	token := h.earnToken(rand.New(rand.NewPCG(4, 4)))

	var out verifyResponse
	code := h.postJSON("/v1/siteverify", verifyRequest{Secret: siteSecret, Response: token}, &out)
	if code != http.StatusOK || !out.Success {
		t.Errorf("JSON verification failed: %d %v", code, out.ErrorCodes)
	}
}

// earnToken runs the whole flow and returns a fresh token.
func (h *harness) earnToken(rng *rand.Rand) string {
	h.t.Helper()
	sid, first := h.begin(siteKey)
	if first.Status != "challenge" {
		h.t.Fatalf("status = %q, want challenge", first.Status)
	}
	stored := h.session(sid)
	var out flowResp
	h.postJSON("/v1/solve", solveReq{
		SID:    sid,
		Nonce:  solve(h.t, specOf(stored.PoW)),
		Answer: mustJSON(h.t, correctAnswer(h.t, stored)),
		Trace:  humanTrace(rng),
	}, &out)
	if out.Status != "pass" || out.Token == "" {
		h.t.Fatalf("could not earn a token: %+v", out)
	}
	return out.Token
}

// ---- refusals --------------------------------------------------------------

func TestUnknownSiteKeyAndDisallowedOriginLookAlike(t *testing.T) {
	h := newHarness(t, nil)

	var unknown errorBody
	unknownCode := h.postJSON("/v1/session", sessionReq{SiteKey: "pc_site_nope", Origin: testOrigin}, &unknown)

	var disallowed errorBody
	disallowedCode := h.postJSON("/v1/session", sessionReq{SiteKey: siteKey, Origin: "https://evil.example"}, &disallowed)

	if unknownCode != http.StatusForbidden || disallowedCode != http.StatusForbidden {
		t.Fatalf("status codes %d and %d, want both 403", unknownCode, disallowedCode)
	}
	// Identical replies, so the endpoint cannot be used to test whether a
	// site key exists.
	if unknown != disallowed {
		t.Errorf("an unknown key (%+v) is distinguishable from a bad origin (%+v)", unknown, disallowed)
	}
}

func TestProofOfWorkIsRequired(t *testing.T) {
	h := newHarness(t, nil)

	var started sessionResp
	h.postJSON("/v1/session", sessionReq{SiteKey: siteKey, Origin: testOrigin}, &started)
	if started.PoW.Bits <= 0 {
		t.Fatal("no proof of work was issued")
	}

	var out errorBody
	code := h.postJSON("/v1/assess", assessReq{SID: started.SID, Nonce: "0", Signals: goodSignals()}, &out)
	if code != http.StatusBadRequest || out.Error != "pow" {
		t.Errorf("a bogus nonce gave %d %+v, want 400 pow", code, out)
	}
}

// A challenge answered correctly but delivered without plausible human motion
// must be refused. This is the property that makes the puzzle worth anything:
// computing the right answer is easy, producing a convincing gesture is not.
func TestCorrectAnswerWithRobotMotionIsRefused(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Sites[0].Challenges = []string{string(challenge.KindSlider)}
	})

	sid, first := h.begin(siteKey)
	if first.Status != "challenge" {
		t.Fatalf("status = %q", first.Status)
	}
	stored := h.session(sid)

	var out flowResp
	h.postJSON("/v1/solve", solveReq{
		SID:    sid,
		Nonce:  solve(t, specOf(stored.PoW)),
		Answer: mustJSON(t, correctAnswer(t, stored)),
		Trace:  robotTrace(),
	}, &out)

	if out.Status == "pass" {
		t.Fatal("a mechanically perfect drag was accepted")
	}
	if out.Token != "" {
		t.Fatal("a token was issued despite the rejection")
	}
}

func TestSessionIsBoundToItsClient(t *testing.T) {
	h := newHarness(t, nil)
	sid, _ := h.begin(siteKey)

	// A different client address must not be able to use the session, so a
	// leaked session id is useless elsewhere.
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/v1/challenge",
		strings.NewReader(fmt.Sprintf(`{"sid":%q,"kind":""}`, sid)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	// With proxy headers untrusted the address does not change, so the
	// request succeeds; this documents that the binding depends on the
	// operator configuring trust correctly.
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("an untrusted X-Forwarded-For changed the identity: %d", res.StatusCode)
	}

	// With the header trusted, the same request is now a different client.
	trusted := newHarness(t, func(c *config.Config) { c.TrustProxy = true })
	tsid, _ := trusted.begin(siteKey)
	req2, _ := http.NewRequest(http.MethodPost, trusted.server.URL+"/v1/challenge",
		strings.NewReader(fmt.Sprintf(`{"sid":%q,"kind":""}`, tsid)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Forwarded-For", "203.0.113.7")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusForbidden {
		t.Errorf("a session was usable from a different address: %d", res2.StatusCode)
	}
}

func TestAssetsRequireTheOwningSession(t *testing.T) {
	h := newHarness(t, nil)
	sid, first := h.begin(siteKey)
	if len(first.Assets) == 0 {
		t.Skip("this challenge has no images")
	}
	var href string
	for _, v := range first.Assets {
		href = v
		break
	}

	// A made-up session id must not render anything.
	forged := strings.Replace(href, sid, "not-a-session", 1)
	res, err := http.Get(h.server.URL + forged)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown session rendered an image: %d", res.StatusCode)
	}
}

func TestBlockedAfterMaxAttempts(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Sites[0].Challenges = []string{string(challenge.KindSlider)}
		c.Sites[0].MaxAttempts = 2
	})
	rng := rand.New(rand.NewPCG(8, 8))

	sid, out := h.begin(siteKey)
	for attempt := 1; attempt <= 3; attempt++ {
		if out.Status == "blocked" {
			if attempt <= 2 {
				t.Fatalf("blocked after only %d attempts", attempt-1)
			}
			return
		}
		stored := h.session(sid)
		wrong := map[string]any{"x": float64(stored.Challenge.Slider.TargetX) + 120}
		h.postJSON("/v1/solve", solveReq{
			SID:    sid,
			Nonce:  solve(t, specOf(stored.PoW)),
			Answer: mustJSON(t, wrong),
			Trace:  humanTrace(rng),
		}, &out)
	}
	if out.Status != "blocked" {
		t.Errorf("status after exhausting the attempts = %q, want blocked", out.Status)
	}
}

func TestRerollsAreCapped(t *testing.T) {
	h := newHarness(t, nil)
	sid, out := h.begin(siteKey)

	for range maxRefreshes + 2 {
		h.postJSON("/v1/challenge", challengeReq{SID: sid}, &out)
		if out.Status == "blocked" {
			return
		}
	}
	t.Errorf("a session rerolled the puzzle more than %d times without being blocked", maxRefreshes)
}

// Switching to the accessible question must never be rationed: a visitor who
// needs it may have to reach it after several attempts.
func TestAccessibleFallbackIsNotRationed(t *testing.T) {
	h := newHarness(t, nil)
	sid, _ := h.begin(siteKey)

	var out flowResp
	for i := range maxRefreshes + 4 {
		code := h.postJSON("/v1/challenge", challengeReq{SID: sid, Kind: string(challenge.KindAccessible)}, &out)
		if code != http.StatusOK || out.Status == "blocked" {
			t.Fatalf("request %d for the accessible question was refused: %d %q", i+1, code, out.Status)
		}
		if out.Challenge == nil || out.Challenge.Kind != challenge.KindAccessible {
			t.Fatalf("request %d did not return the accessible question", i+1)
		}
	}
	if out.CanFallBack {
		t.Error("the accessible question still offers a fallback to itself")
	}
}

func TestSolvedSessionCannotMintASecondToken(t *testing.T) {
	h := newHarness(t, nil)
	rng := rand.New(rand.NewPCG(6, 6))

	sid, _ := h.begin(siteKey)
	stored := h.session(sid)
	var out flowResp
	h.postJSON("/v1/solve", solveReq{
		SID:    sid,
		Nonce:  solve(t, specOf(stored.PoW)),
		Answer: mustJSON(t, correctAnswer(t, stored)),
		Trace:  humanTrace(rng),
	}, &out)
	if out.Status != "pass" {
		t.Fatalf("status = %q", out.Status)
	}

	// Replaying the same solve must not produce a second token.
	var again errorBody
	code := h.postJSON("/v1/solve", solveReq{
		SID:    sid,
		Nonce:  solve(t, specOf(stored.PoW)),
		Answer: mustJSON(t, correctAnswer(t, stored)),
		Trace:  humanTrace(rng),
	}, &again)
	if code == http.StatusOK {
		t.Error("a spent session answered a second solve")
	}
}

func TestRateLimiting(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.RateLimits.SessionPerIP = 3 })

	var lastCode int
	for range 6 {
		lastCode = h.postJSON("/v1/session", sessionReq{SiteKey: siteKey, Origin: testOrigin}, nil)
	}
	if lastCode != http.StatusTooManyRequests {
		t.Errorf("status after exceeding the limit = %d, want 429", lastCode)
	}
}

// ---- passive pass ----------------------------------------------------------

func TestCleanVisitorCanPassWithoutAPuzzle(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Sites[0].AlwaysChallenge = false
		zero := 0.0
		c.Sites[0].ChallengeRate = &zero
	})
	_, out := h.begin(siteKey)
	if out.Status != "pass" {
		t.Fatalf("a clean visitor with sampling off got %q, want pass", out.Status)
	}
	if out.Token == "" {
		t.Fatal("no token was issued")
	}
	if got := h.verify(siteSecret, out.Token); !got.Success || got.Solved != "passive" {
		t.Errorf("verify = %+v, want a successful passive solve", got)
	}
}

func TestSamplingStillChallengesCleanVisitors(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Sites[0].AlwaysChallenge = false
		all := 1.0
		c.Sites[0].ChallengeRate = &all
	})
	_, out := h.begin(siteKey)
	if out.Status != "challenge" {
		t.Errorf("with a sampling rate of 1 a clean visitor got %q, want challenge", out.Status)
	}
}

func TestSuspiciousEnvironmentIsBlocked(t *testing.T) {
	h := newHarness(t, nil)

	var started sessionResp
	h.postJSON("/v1/session", sessionReq{SiteKey: siteKey, Origin: testOrigin}, &started)

	var out flowResp
	h.postJSON("/v1/assess", assessReq{
		SID:   started.SID,
		Nonce: solve(t, started.PoW),
		// Everything a headless browser reports by default.
		Signals: scoring.Signals{WebDriver: true},
	}, &out)

	if out.Status == "pass" {
		t.Error("a browser announcing itself as automated was passed without a challenge")
	}
}

// ---- static surface --------------------------------------------------------

func TestFrameCarriesItsSecurityPolicy(t *testing.T) {
	h := newHarness(t, nil)

	res, err := http.Get(h.server.URL + "/v1/frame?sitekey=" + siteKey + "&origin=" + url.QueryEscape(testOrigin))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/frame = %d", res.StatusCode)
	}
	csp := res.Header.Get("Content-Security-Policy")
	// frame-ancestors is the check browsers actually enforce; without it the
	// origin allow-list is advisory.
	if !strings.Contains(csp, "frame-ancestors "+testOrigin) {
		t.Errorf("frame-ancestors does not name the allowed origin: %q", csp)
	}
	if !strings.Contains(csp, "worker-src 'self'") {
		t.Errorf("the proof-of-work worker is not permitted by the policy: %q", csp)
	}
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("the policy allows eval: %q", csp)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("the frame is cacheable: %q", cc)
	}
	if !bytes.Contains(body, []byte("__PC_BOOT__")) {
		t.Error("the frame did not carry its bootstrap data")
	}
	// The site secret must never reach the browser.
	if bytes.Contains(body, []byte(siteSecret)) {
		t.Fatal("the frame document contains the site secret")
	}
}

func TestFrameRefusesADisallowedOrigin(t *testing.T) {
	h := newHarness(t, nil)
	res, err := http.Get(h.server.URL + "/v1/frame?sitekey=" + siteKey + "&origin=https%3A%2F%2Fevil.example")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", res.StatusCode)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("the error page is framable: %q", csp)
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	h := newHarness(t, nil)
	cases := map[string]string{
		"/v1/widget.js": "application/javascript; charset=utf-8",
		"/v1/frame.js":  "application/javascript; charset=utf-8",
		"/v1/pow.js":    "application/javascript; charset=utf-8",
		"/v1/frame.css": "text/css; charset=utf-8",
		"/v1/locale/fa": "application/json; charset=utf-8",
		"/healthz":      "application/json; charset=utf-8",
	}
	for path, wantType := range cases {
		res, err := http.Get(h.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", path, res.StatusCode)
		}
		if got := res.Header.Get("Content-Type"); got != wantType {
			t.Errorf("GET %s content type = %q, want %q", path, got, wantType)
		}
		if len(body) == 0 {
			t.Errorf("GET %s returned nothing", path)
		}
	}
}

func TestStaticAssetsRevalidate(t *testing.T) {
	h := newHarness(t, nil)
	res, err := http.Get(h.server.URL + "/v1/widget.js")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag was set")
	}

	req, _ := http.NewRequest(http.MethodGet, h.server.URL+"/v1/widget.js", nil)
	req.Header.Set("If-None-Match", etag)
	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Errorf("a matching ETag gave %d, want 304", second.StatusCode)
	}
}

func TestMalformedRequestsAreRejected(t *testing.T) {
	h := newHarness(t, nil)
	for _, path := range []string{"/v1/session", "/v1/assess", "/v1/solve", "/v1/challenge"} {
		res, err := http.Post(h.server.URL+path, "application/json", strings.NewReader("{not json"))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s with junk = %d, want 400", path, res.StatusCode)
		}
	}
}

func TestWrongMethodIsRejected(t *testing.T) {
	h := newHarness(t, nil)
	res, err := http.Get(h.server.URL + "/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET on a POST-only route = %d, want 405", res.StatusCode)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
