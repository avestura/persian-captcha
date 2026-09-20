// Package api implements the HTTP surface of the captcha service.
//
// There are two audiences. The browser talks to /v1/session, /v1/assess,
// /v1/solve and the image routes; every one of those calls comes from the
// service's own iframe, so they are same-origin and need no CORS. The tenant's
// backend talks to /v1/siteverify with its secret key, server to server.
//
// Routing uses the standard library's ServeMux method-and-pattern support, so
// the service has no web framework dependency.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"avestura.dev/persian-captcha/internal/challenge"
	"avestura.dev/persian-captcha/internal/config"
	"avestura.dev/persian-captcha/internal/i18n"
	"avestura.dev/persian-captcha/internal/ratelimit"
	"avestura.dev/persian-captcha/internal/scoring"
	"avestura.dev/persian-captcha/internal/session"
	"avestura.dev/persian-captcha/internal/store"
)

// Options configures a Server.
type Options struct {
	// Config is the live configuration accessor. It is a function so that a
	// hot reload is picked up without restarting the server.
	Config func() *config.Config
	// Store backs sessions, tokens and rate-limit counters.
	Store store.Store
	// Locales is the loaded message bundle.
	Locales *i18n.Bundle
	// Logger receives operational events.
	Logger *slog.Logger
	// IPSecret salts the client-address hash. See session.HashIP.
	IPSecret []byte
	// Thresholds are the score cut-offs. Zero values use the defaults.
	Thresholds scoring.Thresholds
}

// Server is the captcha HTTP service.
type Server struct {
	cfg        func() *config.Config
	sessions   *session.Manager
	limiter    *ratelimit.Limiter
	locales    *i18n.Bundle
	log        *slog.Logger
	ipSecret   []byte
	thresholds scoring.Thresholds
	mux        *http.ServeMux
	assets     *assetSet
}

// New builds a server and registers its routes.
func New(opt Options) (*Server, error) {
	if opt.Config == nil {
		return nil, errors.New("api: Config accessor is required")
	}
	if opt.Store == nil {
		return nil, errors.New("api: Store is required")
	}
	if opt.Locales == nil {
		return nil, errors.New("api: Locales are required")
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	if opt.Thresholds == (scoring.Thresholds{}) {
		opt.Thresholds = scoring.DefaultThresholds()
	}

	cfg := opt.Config()
	assets, err := loadAssets()
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:        opt.Config,
		sessions:   session.NewManager(opt.Store, cfg.SessionTTL.D(), cfg.TokenTTL.D()),
		limiter:    ratelimit.New(opt.Store),
		locales:    opt.Locales,
		log:        opt.Logger,
		ipSecret:   opt.IPSecret,
		thresholds: opt.Thresholds,
		mux:        http.NewServeMux(),
		assets:     assets,
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	// Browser-facing API. Every one of these is called from the service's own
	// iframe, so they are same-origin requests.
	s.mux.HandleFunc("POST /v1/session", s.handleSession)
	s.mux.HandleFunc("POST /v1/assess", s.handleAssess)
	s.mux.HandleFunc("POST /v1/challenge", s.handleChallenge)
	s.mux.HandleFunc("POST /v1/solve", s.handleSolve)
	s.mux.HandleFunc("GET /v1/c/{sid}/{part}", s.handleAsset)

	// Backend-facing verification.
	s.mux.HandleFunc("POST /v1/siteverify", s.handleSiteVerify)

	// Static pieces of the widget.
	s.mux.HandleFunc("GET /v1/widget.js", s.handleWidgetScript)
	s.mux.HandleFunc("GET /v1/frame", s.handleFrame)
	s.mux.HandleFunc("GET /v1/frame.js", s.handleFrameScript)
	s.mux.HandleFunc("GET /v1/frame.css", s.handleFrameStyle)
	s.mux.HandleFunc("GET /v1/pow.js", s.handlePowWorker)
	s.mux.HandleFunc("GET /v1/font/{name}", s.handleFont)
	s.mux.HandleFunc("GET /v1/locales", s.handleLocales)
	s.mux.HandleFunc("GET /v1/locale/{tag}", s.handleLocale)

	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleReady)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.mux.ServeHTTP(w, r)
}

// ---- request helpers -------------------------------------------------------

// maxBody caps request bodies. Interaction traces are the largest thing a
// browser sends and comfortably fit; anything bigger is not a real widget.
const maxBody = 256 << 10

// decodeJSON reads and validates a JSON request body.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "malformed request body")
		return false
	}
	return true
}

// writeJSON sends a JSON response. Captcha replies must never be cached: a
// cached challenge or verdict would be a replay waiting to happen.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody is the shape of every error reply. The code is machine-readable
// and matches a translation key the widget already has.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: code, Message: msg})
}

// clientIP extracts the caller's address, honouring X-Forwarded-For only when
// the operator has declared that the service sits behind a trusted proxy.
// Trusting the header unconditionally would let anyone forge their identity
// and slip every per-address rate limit.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg().TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The left-most entry is the original client, as appended by the
			// first proxy in the chain.
			if first, _, found := strings.Cut(xff, ","); found {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
		if real := r.Header.Get("X-Real-IP"); real != "" {
			return strings.TrimSpace(real)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipHash is the privacy-preserving client identifier used for rate limiting.
func (s *Server) ipHash(r *http.Request) string {
	return session.HashIP(s.ipSecret, s.clientIP(r))
}

// site resolves a public site key.
func (s *Server) site(key string) (*config.Site, bool) {
	return s.cfg().SiteByKey(key)
}

// allow applies a rate limit and writes the 429 reply itself when exceeded.
func (s *Server) allow(w http.ResponseWriter, r *http.Request, bucket string, limit int) bool {
	res, err := s.limiter.Allow(r.Context(), bucket, limit, time.Minute)
	if err != nil {
		s.log.Error("rate limit check failed", "bucket", bucket, "err", err)
		// A store outage should not become an open door; fail closed.
		writeError(w, http.StatusServiceUnavailable, "unavailable", "service temporarily unavailable")
		return false
	}
	if !res.Allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return false
	}
	return true
}

// hostOf returns the host part of an origin, for echoing back at verify time.
func hostOf(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

// normaliseOrigin trims an origin to scheme://host[:port], discarding any path
// a caller may have sent.
func normaliseOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// levelOf converts the configured difficulty into a challenge level.
func levelOf(d config.Difficulty) challenge.Level {
	switch d {
	case config.DifficultyEasy:
		return challenge.LevelEasy
	case config.DifficultyHard:
		return challenge.LevelHard
	default:
		return challenge.LevelNormal
	}
}

// ---- health ----------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.sessions.Ping(ctx); err != nil {
		s.log.Warn("readiness check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "degraded",
			"store":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"sites":   len(s.cfg().Sites),
		"locales": s.locales.Tags(),
	})
}

func (s *Server) handleLocales(w http.ResponseWriter, _ *http.Request) {
	type localeInfo struct {
		Tag  string `json:"tag"`
		Name string `json:"name"`
		Dir  string `json:"dir"`
	}
	tags := s.locales.Tags()
	out := make([]localeInfo, 0, len(tags))
	for _, tag := range tags {
		l, _ := s.locales.Get(tag)
		out = append(out, localeInfo{Tag: tag, Name: l.Meta.Name, Dir: string(l.Meta.Dir)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"locales": out})
}

// handleLocale serves one message bundle, so the widget can switch language
// without reloading the frame and losing the session in flight.
func (s *Server) handleLocale(w http.ResponseWriter, r *http.Request) {
	locale, ok := s.locales.Get(r.PathValue("tag"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Bundles change only when the service is upgraded, and the frame is
	// versioned, so they can be cached for a while.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(locale.JSON())
}
