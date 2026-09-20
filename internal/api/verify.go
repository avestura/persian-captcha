package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"avestura.dev/persian-captcha/internal/session"
)

// verifyRequest is what a tenant's backend posts to /v1/siteverify. Both JSON
// and form encoding are accepted, because the form shape is what every
// existing reCAPTCHA and hCaptcha integration already sends.
type verifyRequest struct {
	Secret   string `json:"secret"`
	Response string `json:"response"`
	RemoteIP string `json:"remoteip"`
}

// verifyResponse mirrors the reCAPTCHA/hCaptcha reply shape, so an existing
// integration can be repointed at this service with minimal change.
type verifyResponse struct {
	Success bool `json:"success"`
	// ChallengeTS is when the token was issued, in RFC 3339.
	ChallengeTS string `json:"challenge_ts,omitempty"`
	// Hostname is the site the captcha was solved on.
	Hostname string `json:"hostname,omitempty"`
	// Score is the human-likeness score, 0 to 1. Higher is more human.
	Score float64 `json:"score,omitempty"`
	// Solved names the challenge kind that earned the token, or "passive"
	// when no puzzle was needed.
	Solved string `json:"solved,omitempty"`
	// ErrorCodes explains a failure.
	ErrorCodes []string `json:"error-codes,omitempty"`
}

// The error codes a verification can return.
const (
	errMissingSecret    = "missing-input-secret"
	errInvalidSecret    = "invalid-input-secret"
	errMissingResponse  = "missing-input-response"
	errInvalidResponse  = "invalid-input-response"
	errTimeoutDuplicate = "timeout-or-duplicate"
	errHostnameMismatch = "hostname-mismatch"
	errBadRequest       = "bad-request"
	errInternal         = "internal-error"
)

func (s *Server) handleSiteVerify(w http.ResponseWriter, r *http.Request) {
	req, ok := s.parseVerifyRequest(w, r)
	if !ok {
		return
	}

	if req.Secret == "" {
		verifyFail(w, errMissingSecret)
		return
	}
	site, found := s.cfg().SiteBySecret(req.Secret)
	if !found {
		// Compare against nothing in constant time anyway, so a wrong secret
		// takes the same path as a right one as far as timing goes.
		subtle.ConstantTimeCompare([]byte(req.Secret), []byte(req.Secret))
		verifyFail(w, errInvalidSecret)
		return
	}
	if !s.allow(w, r, "verify:"+site.Key, s.cfg().RateLimits.VerifyPerSite) {
		return
	}
	if req.Response == "" {
		verifyFail(w, errMissingResponse)
		return
	}

	// Ownership is checked before the token is spent. A token is only valid
	// for the site it was earned on, and redeeming first would mean a caller
	// with the wrong secret destroys a token belonging to someone else.
	peeked, err := s.sessions.PeekToken(r.Context(), req.Response)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			// One code covers "never existed", "expired" and "already used".
			// Distinguishing them would tell an attacker which of their
			// guesses were real tokens.
			verifyFail(w, errTimeoutDuplicate)
			return
		}
		s.log.Error("token lookup failed", "err", err)
		verifyFailStatus(w, http.StatusInternalServerError, errInternal)
		return
	}
	if peeked.SiteKey != site.Key {
		s.log.Warn("token presented by the wrong site", "site", site.Name)
		verifyFail(w, errInvalidResponse)
		return
	}

	// Now spend it. Two concurrent redemptions both reach here; the store's
	// atomic take is what guarantees only one of them succeeds.
	token, err := s.sessions.RedeemToken(r.Context(), req.Response)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			verifyFail(w, errTimeoutDuplicate)
			return
		}
		s.log.Error("token redemption failed", "err", err)
		verifyFailStatus(w, http.StatusInternalServerError, errInternal)
		return
	}

	writeJSON(w, http.StatusOK, verifyResponse{
		Success:     true,
		ChallengeTS: token.IssuedAt.UTC().Format(time.RFC3339),
		Hostname:    token.Hostname,
		Score:       round2(token.Score),
		Solved:      token.Solved,
	})
}

// parseVerifyRequest accepts either JSON or form encoding.
func (s *Server) parseVerifyRequest(w http.ResponseWriter, r *http.Request) (verifyRequest, bool) {
	var req verifyRequest
	contentType := r.Header.Get("Content-Type")
	if mediaType, _, _ := strings.Cut(contentType, ";"); strings.TrimSpace(mediaType) == "application/json" {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			verifyFailStatus(w, http.StatusBadRequest, errBadRequest)
			return req, false
		}
		return req, true
	}
	if err := r.ParseForm(); err != nil {
		verifyFailStatus(w, http.StatusBadRequest, errBadRequest)
		return req, false
	}
	req.Secret = r.PostForm.Get("secret")
	req.Response = r.PostForm.Get("response")
	req.RemoteIP = r.PostForm.Get("remoteip")
	return req, true
}

// verifyFail writes an unsuccessful verification. The HTTP status stays 200
// because the request itself was well-formed; the outcome is in the body, as
// the reCAPTCHA-compatible shape requires.
func verifyFail(w http.ResponseWriter, codes ...string) {
	writeJSON(w, http.StatusOK, verifyResponse{Success: false, ErrorCodes: codes})
}

func verifyFailStatus(w http.ResponseWriter, status int, codes ...string) {
	writeJSON(w, status, verifyResponse{Success: false, ErrorCodes: codes})
}

// round2 trims a score to two decimals, which is all the precision a caller
// should act on and avoids leaking the exact internal weighting.
func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
