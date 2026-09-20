// Package session holds the short-lived record of one visitor working through
// a captcha, and the single-use token they receive when they succeed.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"avestura.dev/persian-captcha/internal/challenge"
	"avestura.dev/persian-captcha/internal/pow"
	"avestura.dev/persian-captcha/internal/store"
)

// Stage tracks how far a visitor has progressed.
type Stage string

// The session stages, in order.
const (
	// StageOpened means the widget has loaded and been given a proof of work.
	StageOpened Stage = "opened"
	// StageChallenged means a puzzle has been issued.
	StageChallenged Stage = "challenged"
	// StageSolved means a token has been issued and the session is spent.
	StageSolved Stage = "solved"
	// StageBlocked means the session was refused and cannot continue.
	StageBlocked Stage = "blocked"
)

// ErrNotFound is returned when a session or token is unknown or expired.
var ErrNotFound = errors.New("session: not found")

// Session is the server-side record of one captcha attempt.
type Session struct {
	ID      string `json:"id"`
	SiteKey string `json:"siteKey"`
	// Origin is the page that embedded the widget, as reported by the browser
	// and checked against the site's allow-list.
	Origin string `json:"origin"`
	// Hostname is the host part of Origin, echoed back in the verify reply so
	// a backend can confirm where the token was earned.
	Hostname string `json:"hostname"`
	Locale   string `json:"locale"`
	// IPHash identifies the client for rate limiting without storing an
	// address. See HashIP.
	IPHash string `json:"ipHash"`

	CreatedAt time.Time `json:"createdAt"`
	Stage     Stage     `json:"stage"`

	// PoW is the outstanding proof of work, and PoWSolved records whether it
	// has been met. A new one is issued for each retry so the work cannot be
	// computed once and replayed.
	PoW       pow.Challenge `json:"pow"`
	PoWSolved bool          `json:"powSolved"`

	// Attempts counts challenge submissions, correct or not.
	Attempts int `json:"attempts"`
	// MaxAttempts is copied from the site config at creation time so that a
	// config reload mid-session cannot move the goalposts.
	MaxAttempts int `json:"maxAttempts"`

	// Refreshes counts how many times a new puzzle was handed out without an
	// answer being submitted, which caps rerolling until an easy one appears.
	Refreshes int `json:"refreshes"`

	// SignalScore is the passive environment score from the opening
	// assessment, kept so the final decision can combine it with the motion
	// score recorded at solve time.
	SignalScore float64 `json:"signalScore"`
	// Score is the latest combined human-likeness score.
	Score float64 `json:"score"`
	// Flags records why, for operator logs. Never sent to the browser.
	Flags []string `json:"flags,omitempty"`

	// Challenge is the puzzle in flight, if any.
	Challenge *challenge.State `json:"challenge,omitempty"`
}

// Expired reports whether the session has outlived ttl.
func (s *Session) Expired(now time.Time, ttl time.Duration) bool {
	return now.Sub(s.CreatedAt) > ttl
}

// Token is the single-use proof of a solved captcha, exchanged by the
// tenant's backend at /v1/siteverify.
type Token struct {
	Value    string    `json:"value"`
	SiteKey  string    `json:"siteKey"`
	Hostname string    `json:"hostname"`
	IssuedAt time.Time `json:"issuedAt"`
	Score    float64   `json:"score"`
	// Solved names how the token was earned: a challenge kind, or "passive"
	// when the visitor was cleared without a puzzle.
	Solved string `json:"solved"`
	// IPHash lets a backend optionally require that the token be redeemed for
	// the same client that earned it.
	IPHash string `json:"ipHash"`
}

// Manager stores and retrieves sessions and tokens.
type Manager struct {
	store      store.Store
	sessionTTL time.Duration
	tokenTTL   time.Duration
	now        func() time.Time
}

// NewManager builds a manager over a store.
func NewManager(s store.Store, sessionTTL, tokenTTL time.Duration) *Manager {
	return &Manager{store: s, sessionTTL: sessionTTL, tokenTTL: tokenTTL, now: time.Now}
}

// SessionTTL reports how long a session lives.
func (m *Manager) SessionTTL() time.Duration { return m.sessionTTL }

// TokenTTL reports how long an issued token stays redeemable.
func (m *Manager) TokenTTL() time.Duration { return m.tokenTTL }

// Now returns the manager's clock, which tests can replace.
func (m *Manager) Now() time.Time { return m.now() }

func sessionKey(id string) string { return "s:" + id }
func tokenKey(v string) string    { return "t:" + v }

// Save writes a session, refreshing its remaining lifetime up to the
// configured TTL. The deadline is anchored to creation, so a visitor cannot
// keep a session alive indefinitely by interacting with it.
func (m *Manager) Save(ctx context.Context, s *Session) error {
	remaining := m.sessionTTL - m.now().Sub(s.CreatedAt)
	if remaining <= 0 {
		return ErrNotFound
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}
	return m.store.Set(ctx, sessionKey(s.ID), data, remaining)
}

// Get loads a session.
func (m *Manager) Get(ctx context.Context, id string) (*Session, error) {
	data, err := m.store.Get(ctx, sessionKey(id))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session: unmarshal: %w", err)
	}
	if s.Expired(m.now(), m.sessionTTL) {
		return nil, ErrNotFound
	}
	return &s, nil
}

// Ping checks that the backing store is reachable.
func (m *Manager) Ping(ctx context.Context) error { return m.store.Ping(ctx) }

// Delete removes a session.
func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.store.Delete(ctx, sessionKey(id))
}

// IssueToken stores a freshly minted token and returns its value.
func (m *Manager) IssueToken(ctx context.Context, t *Token) error {
	if t.Value == "" {
		v, err := NewID()
		if err != nil {
			return err
		}
		t.Value = v
	}
	t.IssuedAt = m.now()
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("session: marshal token: %w", err)
	}
	return m.store.Set(ctx, tokenKey(t.Value), data, m.tokenTTL)
}

// PeekToken reads a token without consuming it.
//
// It exists so a caller can check that a token belongs to it before spending
// it. Validating after redemption would mean an unauthorised attempt destroys
// somebody else's token, and the rightful owner would then be told their
// perfectly good response had already been used.
func (m *Manager) PeekToken(ctx context.Context, value string) (*Token, error) {
	return m.readToken(ctx, value, m.store.Get)
}

// RedeemToken consumes a token, returning it exactly once. A second
// redemption of the same value fails, which is what makes a captcha response
// non-replayable.
func (m *Manager) RedeemToken(ctx context.Context, value string) (*Token, error) {
	return m.readToken(ctx, value, m.store.Take)
}

func (m *Manager) readToken(
	ctx context.Context,
	value string,
	read func(context.Context, string) ([]byte, error),
) (*Token, error) {
	if value == "" {
		return nil, ErrNotFound
	}
	data, err := read(ctx, tokenKey(value))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("session: unmarshal token: %w", err)
	}
	return &t, nil
}

// NewID returns a 256-bit random identifier, URL-safe and unpadded. It is used
// for both session ids and token values; at this length they are unguessable
// and need no additional signing.
func NewID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("session: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// HashIP derives a stable, non-reversible identifier for a client address.
//
// Rate limiting needs to recognise a repeat visitor; it does not need to know
// who they are. Hashing with a per-process secret means the stored value is
// useless to anyone who reads the session store, and becomes meaningless when
// the service restarts.
func HashIP(secret []byte, ip string) string {
	h := sha256.New()
	h.Write(secret)
	h.Write([]byte(ip))
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// NewSecret returns a random per-process secret for HashIP.
func NewSecret() ([]byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("session: random: %w", err)
	}
	return buf, nil
}
