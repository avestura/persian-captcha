package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"avestura.dev/persian-captcha/internal/challenge"
	"avestura.dev/persian-captcha/internal/config"
	"avestura.dev/persian-captcha/internal/pow"
	"avestura.dev/persian-captcha/internal/scoring"
	"avestura.dev/persian-captcha/internal/session"
)

// powSpec is the proof of work as the browser sees it.
type powSpec struct {
	Algorithm string `json:"alg"`
	Prefix    string `json:"prefix"`
	Bits      int    `json:"bits"`
}

func specOf(c pow.Challenge) powSpec {
	return powSpec{Algorithm: c.Algorithm, Prefix: c.Prefix, Bits: c.Bits}
}

// basePoWBits is the starting difficulty per configured level. Sixteen bits is
// roughly 65,000 hashes: tens of milliseconds in a browser worker, and a real
// cost when multiplied by a farm's throughput.
func basePoWBits(d config.Difficulty) int {
	switch d {
	case config.DifficultyEasy:
		return 14
	case config.DifficultyHard:
		return 18
	default:
		return 16
	}
}

// ---- POST /v1/session ------------------------------------------------------

type sessionReq struct {
	SiteKey string `json:"sitekey"`
	Origin  string `json:"origin"`
	Locale  string `json:"locale"`
}

type sessionResp struct {
	SID    string  `json:"sid"`
	PoW    powSpec `json:"pow"`
	Locale string  `json:"locale"`
	TTL    int     `json:"ttl"`
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	var req sessionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg := s.cfg()
	ipHash := s.ipHash(r)
	if !s.allow(w, r, "sess:"+ipHash, cfg.RateLimits.SessionPerIP) {
		return
	}

	site, ok := s.site(req.SiteKey)
	if !ok {
		// The same reply for an unknown key and a disallowed origin, so the
		// endpoint cannot be used to enumerate valid site keys.
		writeError(w, http.StatusForbidden, "origin", "site key not valid for this origin")
		return
	}
	origin := normaliseOrigin(req.Origin)
	if origin == "" {
		origin = normaliseOrigin(r.Header.Get("Referer"))
	}
	if !site.OriginAllowed(origin) {
		s.log.Warn("origin rejected", "site", site.Name, "origin", origin)
		writeError(w, http.StatusForbidden, "origin", "site key not valid for this origin")
		return
	}

	locale := s.locales.Negotiate(req.Locale, site.Locale, r.Header.Get("Accept-Language"))
	powChallenge, err := pow.New(basePoWBits(site.Difficulty))
	if err != nil {
		s.fail(w, "pow", err)
		return
	}
	id, err := session.NewID()
	if err != nil {
		s.fail(w, "id", err)
		return
	}

	sess := &session.Session{
		ID:          id,
		SiteKey:     site.Key,
		Origin:      origin,
		Hostname:    hostOf(origin),
		Locale:      locale.Tag,
		IPHash:      ipHash,
		CreatedAt:   s.sessions.Now(),
		Stage:       session.StageOpened,
		PoW:         powChallenge,
		MaxAttempts: site.MaxAttempts,
	}
	if err := s.sessions.Save(r.Context(), sess); err != nil {
		s.fail(w, "save session", err)
		return
	}

	writeJSON(w, http.StatusOK, sessionResp{
		SID:    sess.ID,
		PoW:    specOf(powChallenge),
		Locale: locale.Tag,
		TTL:    int(s.sessions.SessionTTL().Seconds()),
	})
}

// ---- POST /v1/assess -------------------------------------------------------

type assessReq struct {
	SID     string          `json:"sid"`
	Nonce   string          `json:"nonce"`
	Signals scoring.Signals `json:"signals"`
}

// flowResp is the reply shared by /v1/assess, /v1/challenge and /v1/solve. The
// browser switches on Status.
type flowResp struct {
	// Status is "pass", "challenge", "retry" or "blocked".
	Status string `json:"status"`

	// Token is set when Status is "pass".
	Token     string `json:"token,omitempty"`
	ExpiresIn int    `json:"expiresIn,omitempty"`

	// Challenge and Assets are set when a puzzle is being handed out.
	Challenge *challenge.Spec   `json:"challenge,omitempty"`
	Assets    map[string]string `json:"assets,omitempty"`
	// PoW must be solved before the answer to this challenge is accepted.
	PoW *powSpec `json:"pow,omitempty"`

	// AttemptsLeft is how many submissions remain before the session is
	// blocked. It is shown to the visitor so a near-miss does not feel
	// arbitrary.
	AttemptsLeft int `json:"attemptsLeft,omitempty"`
	// Reason is a coarse tag for a failed attempt, such as "offset".
	Reason string `json:"reason,omitempty"`
	// CanFallBack reports whether the accessible question is still offered.
	CanFallBack bool `json:"canFallBack"`
}

func (s *Server) handleAssess(w http.ResponseWriter, r *http.Request) {
	var req assessReq
	if !decodeJSON(w, r, &req) {
		return
	}
	ipHash := s.ipHash(r)
	if !s.allow(w, r, "assess:"+ipHash, s.cfg().RateLimits.SolvePerIP) {
		return
	}

	sess, site, ok := s.loadSession(w, r, req.SID)
	if !ok {
		return
	}
	if sess.Stage != session.StageOpened {
		writeError(w, http.StatusConflict, "session", "this session has already been assessed")
		return
	}
	if !sess.PoW.Verify(req.Nonce) {
		s.countFailure(r.Context(), ipHash)
		writeError(w, http.StatusBadRequest, "pow", "proof of work is not valid")
		return
	}
	sess.PoWSolved = true

	assessment := scoring.ScoreSignals(req.Signals)
	sess.SignalScore = assessment.Score
	sess.Score = assessment.Score
	sess.Flags = assessment.Flags

	verdict := s.thresholds.Decide(assessment.Score)
	// Even a clean-looking visitor is challenged some of the time. Passive
	// signals are cheap to fake; sampling means an attacker cannot rely on
	// never meeting a puzzle, and it keeps the interactive path exercised.
	if verdict == scoring.VerdictPass && sampleChallenge(site.SampleRate()) {
		verdict = scoring.VerdictChallenge
		sess.Flags = append(sess.Flags, "sampled")
	}
	if site.AlwaysChallenge && verdict == scoring.VerdictPass {
		verdict = scoring.VerdictChallenge
	}

	switch verdict {
	case scoring.VerdictBlock:
		s.block(w, r, sess, "signals")
	case scoring.VerdictPass:
		s.pass(w, r, sess, "passive")
	default:
		s.issueChallenge(w, r, sess, site, "")
	}
}

// sampleChallenge reports whether this visitor is in the sampled fraction.
func sampleChallenge(rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// If the system cannot produce randomness, err towards showing the
		// puzzle rather than towards letting traffic through unchecked.
		return true
	}
	return float64(binary.BigEndian.Uint32(b[:]))/float64(1<<32) < rate
}

// ---- POST /v1/challenge ----------------------------------------------------

type challengeReq struct {
	SID string `json:"sid"`
	// Kind may be "accessible" to switch to the non-visual question, or empty
	// to reroll the visual puzzle.
	Kind string `json:"kind"`
}

// maxRefreshes caps how many puzzles one session may be handed without ever
// answering. Rerolling is legitimate when a puzzle is unreadable, but
// unlimited rerolls would let a solver shop for an easy board.
const maxRefreshes = 5

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	var req challengeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	ipHash := s.ipHash(r)
	if !s.allow(w, r, "refresh:"+ipHash, s.cfg().RateLimits.SolvePerIP) {
		return
	}
	sess, site, ok := s.loadSession(w, r, req.SID)
	if !ok {
		return
	}
	if sess.Stage != session.StageChallenged && sess.Stage != session.StageOpened {
		writeError(w, http.StatusConflict, "session", "this session is finished")
		return
	}
	// Switching to the accessible question is always allowed and is never
	// counted as a reroll: a visitor who needs it must not be rationed.
	kind := ""
	if req.Kind == string(challenge.KindAccessible) {
		kind = req.Kind
	} else {
		sess.Refreshes++
		if sess.Refreshes > maxRefreshes {
			s.block(w, r, sess, "refresh_limit")
			return
		}
	}
	s.issueChallenge(w, r, sess, site, kind)
}

// ---- POST /v1/solve --------------------------------------------------------

type solveReq struct {
	SID    string          `json:"sid"`
	Nonce  string          `json:"nonce"`
	Answer json.RawMessage `json:"answer"`
	Trace  scoring.Trace   `json:"trace"`
}

// minInteractionScore is the combined score below which a correct answer is
// still refused. Getting the geometry right is easy for a script; producing a
// plausible human motion trace alongside it is the part that costs.
const minInteractionScore = 0.34

func (s *Server) handleSolve(w http.ResponseWriter, r *http.Request) {
	var req solveReq
	if !decodeJSON(w, r, &req) {
		return
	}
	ipHash := s.ipHash(r)
	if !s.allow(w, r, "solve:"+ipHash, s.cfg().RateLimits.SolvePerIP) {
		return
	}
	sess, site, ok := s.loadSession(w, r, req.SID)
	if !ok {
		return
	}
	if sess.Stage != session.StageChallenged || sess.Challenge == nil {
		writeError(w, http.StatusConflict, "session", "no challenge is in flight")
		return
	}
	if !sess.PoW.Verify(req.Nonce) {
		s.countFailure(r.Context(), ipHash)
		writeError(w, http.StatusBadRequest, "pow", "proof of work is not valid")
		return
	}

	outcome, err := sess.Challenge.Check(req.Answer)
	if err != nil {
		if errors.Is(err, challenge.ErrBadAnswer) {
			writeError(w, http.StatusBadRequest, "bad_request", "malformed answer")
			return
		}
		s.fail(w, "check answer", err)
		return
	}

	sess.Attempts++
	traceScore := scoring.ScoreTrace(req.Trace)
	combined := scoring.Combine(traceScore, scoring.Assessment{Score: sess.SignalScore})
	sess.Score = combined.Score
	sess.Flags = combined.Flags

	// The accessible question has no motion to grade, so it is judged on the
	// answer and the proof of work alone. Holding it to a motion threshold
	// would make the fallback unusable for exactly the people who need it.
	isAccessible := sess.Challenge.Kind == challenge.KindAccessible
	humanEnough := isAccessible || combined.Score >= minInteractionScore

	switch {
	case outcome.Correct && humanEnough:
		s.pass(w, r, sess, string(sess.Challenge.Kind))
		return
	case outcome.Correct:
		// Right answer, machine-like delivery. Logged distinctly because a
		// rise in this number is the clearest sign of an active solver.
		s.log.Info("answer correct but interaction rejected",
			"site", site.Name, "score", combined.Score,
			"kind", sess.Challenge.Kind, "flags", combined.Flags)
		s.countFailure(r.Context(), ipHash)
	default:
		s.countFailure(r.Context(), ipHash)
	}

	if sess.Attempts >= sess.MaxAttempts {
		s.block(w, r, sess, "attempts")
		return
	}
	// Escalate: each failure buys a harder proof of work for the next go.
	s.issueChallengeWithReason(w, r, sess, site, "", "retry", outcome.Reason)
}

// ---- shared steps ----------------------------------------------------------

// loadSession fetches the session named in a request along with its site,
// writing the error reply itself when either is missing.
func (s *Server) loadSession(w http.ResponseWriter, r *http.Request, sid string) (*session.Session, *config.Site, bool) {
	if sid == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing session id")
		return nil, nil, false
	}
	sess, err := s.sessions.Get(r.Context(), sid)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session", "session expired or unknown")
			return nil, nil, false
		}
		s.fail(w, "load session", err)
		return nil, nil, false
	}
	if sess.Stage == session.StageBlocked {
		writeJSON(w, http.StatusOK, flowResp{Status: "blocked"})
		return nil, nil, false
	}
	site, ok := s.site(sess.SiteKey)
	if !ok {
		// The site key was removed from the configuration mid-session.
		writeError(w, http.StatusNotFound, "session", "session expired or unknown")
		return nil, nil, false
	}
	// Bind the session to the client that opened it. A stolen session id is
	// then useless from anywhere else.
	if sess.IPHash != s.ipHash(r) {
		s.log.Warn("session used from a different client", "site", site.Name)
		writeError(w, http.StatusForbidden, "session", "session does not belong to this client")
		return nil, nil, false
	}
	return sess, site, true
}

// issueChallenge generates a puzzle, stores it and replies with its spec.
func (s *Server) issueChallenge(w http.ResponseWriter, r *http.Request, sess *session.Session, site *config.Site, kind string) {
	s.issueChallengeWithReason(w, r, sess, site, kind, "challenge", "")
}

func (s *Server) issueChallengeWithReason(w http.ResponseWriter, r *http.Request, sess *session.Session, site *config.Site, kind, status, reason string) {
	chosen, err := s.pickKind(site, kind)
	if err != nil {
		s.fail(w, "pick challenge", err)
		return
	}
	seed, err := randomSeed()
	if err != nil {
		s.fail(w, "seed", err)
		return
	}
	st, err := challenge.New(chosen, levelOf(site.Difficulty), seed)
	if err != nil {
		s.fail(w, "generate challenge", err)
		return
	}
	spec, err := st.Spec()
	if err != nil {
		s.fail(w, "challenge spec", err)
		return
	}

	// A fresh proof of work per challenge, escalating with failed attempts, so
	// that grinding through retries costs progressively more.
	bits := basePoWBits(site.Difficulty) + sess.Attempts
	next, err := pow.New(pow.Difficulty(bits, 1-sess.Score))
	if err != nil {
		s.fail(w, "pow", err)
		return
	}

	sess.Stage = session.StageChallenged
	sess.Challenge = st
	sess.PoW = next
	sess.PoWSolved = false
	if err := s.sessions.Save(r.Context(), sess); err != nil {
		s.fail(w, "save session", err)
		return
	}

	assets := make(map[string]string, len(st.Assets()))
	for _, part := range st.Assets() {
		// The seed is in the URL only as a cache-buster; the server reads the
		// challenge from the session, never from the query string.
		assets[part] = fmt.Sprintf("/v1/c/%s/%s?v=%d", sess.ID, part, seed&0xffff)
	}

	spec2 := specOf(next)
	writeJSON(w, http.StatusOK, flowResp{
		Status:       status,
		Challenge:    spec,
		Assets:       assets,
		PoW:          &spec2,
		AttemptsLeft: max(sess.MaxAttempts-sess.Attempts, 0),
		Reason:       reason,
		CanFallBack:  chosen != challenge.KindAccessible,
	})
}

// pickKind chooses which challenge to present, honouring an explicit request
// and the site's configured allow-list.
func (s *Server) pickKind(site *config.Site, requested string) (challenge.Kind, error) {
	if requested != "" {
		k, ok := challenge.ParseKind(requested)
		if !ok {
			return "", fmt.Errorf("api: unknown challenge kind %q", requested)
		}
		return k, nil
	}
	allowed := challenge.VisualKinds()
	if len(site.Challenges) > 0 {
		allowed = allowed[:0]
		for _, name := range site.Challenges {
			if k, ok := challenge.ParseKind(name); ok && k != challenge.KindAccessible {
				allowed = append(allowed, k)
			}
		}
		if len(allowed) == 0 {
			allowed = challenge.VisualKinds()
		}
	}
	n, err := randomSeed()
	if err != nil {
		return "", err
	}
	return allowed[int(n%uint64(len(allowed)))], nil
}

// pass issues a token and closes the session.
func (s *Server) pass(w http.ResponseWriter, r *http.Request, sess *session.Session, solved string) {
	token := &session.Token{
		SiteKey:  sess.SiteKey,
		Hostname: sess.Hostname,
		Score:    sess.Score,
		Solved:   solved,
		IPHash:   sess.IPHash,
	}
	if err := s.sessions.IssueToken(r.Context(), token); err != nil {
		s.fail(w, "issue token", err)
		return
	}
	// The session is deleted rather than marked solved: it has served its
	// purpose, and removing it means the same session can never mint a second
	// token.
	if err := s.sessions.Delete(r.Context(), sess.ID); err != nil {
		s.log.Warn("could not delete solved session", "err", err)
	}
	writeJSON(w, http.StatusOK, flowResp{
		Status:    "pass",
		Token:     token.Value,
		ExpiresIn: int(s.sessions.TokenTTL().Seconds()),
	})
}

// block ends a session and refuses further attempts.
func (s *Server) block(w http.ResponseWriter, r *http.Request, sess *session.Session, why string) {
	sess.Stage = session.StageBlocked
	sess.Challenge = nil
	if err := s.sessions.Save(r.Context(), sess); err != nil {
		s.log.Warn("could not save blocked session", "err", err)
	}
	s.log.Info("session blocked", "why", why, "score", sess.Score, "attempts", sess.Attempts)
	writeJSON(w, http.StatusOK, flowResp{Status: "blocked", Reason: why})
}

// countFailure records a failed attempt against the client address. The
// counter feeds a separate, stricter limit than ordinary traffic.
func (s *Server) countFailure(ctx context.Context, ipHash string) {
	limits := s.cfg().RateLimits
	if limits.FailuresPerIP <= 0 {
		return
	}
	if _, err := s.limiter.Allow(ctx, "fail:"+ipHash, limits.FailuresPerIP, time.Minute); err != nil {
		s.log.Warn("could not record failure", "err", err)
	}
}

// fail logs an internal error and returns an opaque reply. Internal details
// are never sent to the browser.
func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	s.log.Error("request failed", "at", what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

// randomSeed returns a cryptographically random 64-bit value. Challenge seeds
// must be unguessable: the seed reproduces the entire board, including where
// the puzzle piece belongs.
func randomSeed() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}

// ---- GET /v1/c/{sid}/{part} ------------------------------------------------

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	part := r.PathValue("part")

	ipHash := s.ipHash(r)
	// Rendering is the most expensive thing the service does, so image
	// requests carry their own limit rather than sharing the solve budget.
	if !s.allow(w, r, "asset:"+ipHash, s.cfg().RateLimits.SolvePerIP*4) {
		return
	}

	sess, err := s.sessions.Get(r.Context(), sid)
	if err != nil || sess.Challenge == nil {
		http.NotFound(w, r)
		return
	}
	if sess.IPHash != ipHash {
		http.NotFound(w, r)
		return
	}

	data, err := sess.Challenge.Render(part)
	if err != nil {
		if errors.Is(err, challenge.ErrUnknownAsset) {
			http.NotFound(w, r)
			return
		}
		s.log.Error("render failed", "part", part, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	// Never cached: the image is the challenge, and a cached copy would
	// outlive the session it belongs to.
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if _, err := w.Write(data); err != nil {
		s.log.Debug("asset write failed", "err", err)
	}
}
