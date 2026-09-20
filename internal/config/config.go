// Package config loads and validates the service configuration.
//
// Configuration comes from a single file (YAML subset or JSON) plus
// environment overrides. There is deliberately no database and no admin API:
// site keys are declared in the file, which keeps deployments GitOps friendly
// and the attack surface small.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Difficulty selects how much work a visitor has to do.
type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyNormal Difficulty = "normal"
	DifficultyHard   Difficulty = "hard"
)

// Site is one tenant: a public site key handed to the browser, and a secret
// used by that tenant backend when calling /v1/siteverify.
type Site struct {
	Key             string     `json:"key"`
	Secret          string     `json:"secret"`
	Name            string     `json:"name"`
	AllowedOrigins  []string   `json:"allowed_origins"`
	Difficulty      Difficulty `json:"difficulty"`
	Challenges      []string   `json:"challenges"`
	Locale          string     `json:"locale"`
	Theme           string     `json:"theme"`
	AlwaysChallenge bool       `json:"always_challenge"`
	MaxAttempts     int        `json:"max_attempts"`

	// ChallengeRate is the fraction of visitors who are shown a puzzle even
	// though their passive signals looked fine. Without it, a headless browser
	// that reports a convincing environment would sail through every time and
	// the interactive defence would never fire. Set it to 0 to trust the
	// signals completely, or 1 for the same effect as always_challenge.
	ChallengeRate *float64 `json:"challenge_rate"`
}

// SampleRate returns the configured challenge sampling rate, or the default.
func (s *Site) SampleRate() float64 {
	if s.ChallengeRate == nil {
		return DefaultChallengeRate
	}
	switch {
	case *s.ChallengeRate < 0:
		return 0
	case *s.ChallengeRate > 1:
		return 1
	default:
		return *s.ChallengeRate
	}
}

// DefaultChallengeRate is used when a site does not set challenge_rate.
const DefaultChallengeRate = 0.25

// OriginAllowed reports whether origin may embed this site key.
// An empty allow-list means "any origin", which is only sensible in dev.
func (s *Site) OriginAllowed(origin string) bool {
	if len(s.AllowedOrigins) == 0 {
		return true
	}
	origin = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(origin)), "/")
	for _, a := range s.AllowedOrigins {
		a = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a)), "/")
		switch {
		case a == "*", a == origin:
			return true
		case strings.HasPrefix(a, "*."):
			// "*.example.com" matches any subdomain but not the bare host.
			if strings.HasSuffix(origin, a[1:]) {
				return true
			}
		}
	}
	return false
}

// RedisConfig describes the optional Redis backend.
type RedisConfig struct {
	Addr     string `json:"addr"`
	Password string `json:"password"`
	DB       int    `json:"db"`
	Prefix   string `json:"prefix"`
}

// StoreConfig selects the session/token backend.
type StoreConfig struct {
	Driver string      `json:"driver"` // memory | redis
	Redis  RedisConfig `json:"redis"`
}

// RateLimits are fixed-window counters, all per minute.
type RateLimits struct {
	SessionPerIP  int `json:"session_per_ip_per_min"`
	SolvePerIP    int `json:"solve_per_ip_per_min"`
	VerifyPerSite int `json:"verify_per_site_per_min"`
	FailuresPerIP int `json:"failures_per_ip_per_min"`
}

// Config is the whole service configuration.
type Config struct {
	Listen        string      `json:"listen"`
	BaseURL       string      `json:"base_url"`
	TrustProxy    bool        `json:"trust_proxy_headers"`
	SessionTTL    Duration    `json:"session_ttl"`
	TokenTTL      Duration    `json:"token_ttl"`
	DefaultLocale string      `json:"default_locale"`
	LogLevel      string      `json:"log_level"`
	Store         StoreConfig `json:"store"`
	RateLimits    RateLimits  `json:"rate_limits"`
	Sites         []Site      `json:"sites"`

	siteIndex   map[string]*Site
	secretIndex map[string]*Site
}

// Duration is a time.Duration that unmarshals from "5m", "30s" or a number of
// seconds.
type Duration time.Duration

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case float64:
		*d = Duration(time.Duration(t) * time.Second)
	case string:
		parsed, err := time.ParseDuration(t)
		if err != nil {
			return err
		}
		*d = Duration(parsed)
	default:
		return fmt.Errorf("config: cannot parse duration from %T", v)
	}
	return nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Default returns a usable development configuration with no sites.
func Default() *Config {
	return &Config{
		Listen:        ":8080",
		BaseURL:       "http://localhost:8080",
		SessionTTL:    Duration(5 * time.Minute),
		TokenTTL:      Duration(5 * time.Minute),
		DefaultLocale: "en",
		LogLevel:      "info",
		Store: StoreConfig{
			Driver: "memory",
			Redis:  RedisConfig{Addr: "127.0.0.1:6379", Prefix: "pcaptcha:"},
		},
		RateLimits: RateLimits{
			SessionPerIP:  30,
			SolvePerIP:    90,
			VerifyPerSite: 1200,
			FailuresPerIP: 20,
		},
	}
}

// Load reads path (YAML subset or JSON), applies environment overrides and
// validates the result. A missing file is not an error when environment
// variables supply a site key, which keeps container deploys simple.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			doc, err := yamlToJSON(string(raw))
			if err != nil {
				return nil, fmt.Errorf("config %s: %w", path, err)
			}
			if err := json.Unmarshal(doc, cfg); err != nil {
				return nil, fmt.Errorf("config %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// Fall through to environment-only configuration.
		default:
			return nil, err
		}
	}
	applyEnv(cfg)
	if err := cfg.finalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	setString(&cfg.Listen, "PCAPTCHA_LISTEN")
	setString(&cfg.BaseURL, "PCAPTCHA_BASE_URL")
	setString(&cfg.LogLevel, "PCAPTCHA_LOG_LEVEL")
	setString(&cfg.DefaultLocale, "PCAPTCHA_DEFAULT_LOCALE")
	setString(&cfg.Store.Driver, "PCAPTCHA_STORE_DRIVER")
	setString(&cfg.Store.Redis.Password, "PCAPTCHA_REDIS_PASSWORD")

	if v := os.Getenv("PCAPTCHA_TRUST_PROXY"); v != "" {
		cfg.TrustProxy = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PCAPTCHA_REDIS_ADDR"); v != "" {
		cfg.Store.Driver = "redis"
		cfg.Store.Redis.Addr = v
	}
	if v := os.Getenv("PCAPTCHA_REDIS_DB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Store.Redis.DB = n
		}
	}

	// A single site can be declared entirely through the environment.
	key, secret := os.Getenv("PCAPTCHA_SITE_KEY"), os.Getenv("PCAPTCHA_SITE_SECRET")
	if key == "" || secret == "" {
		return
	}
	site := Site{
		Key:        key,
		Secret:     secret,
		Name:       orDefault(os.Getenv("PCAPTCHA_SITE_NAME"), "default"),
		Difficulty: Difficulty(orDefault(os.Getenv("PCAPTCHA_SITE_DIFFICULTY"), string(DifficultyNormal))),
		Locale:     os.Getenv("PCAPTCHA_SITE_LOCALE"),
	}
	if v := os.Getenv("PCAPTCHA_SITE_ORIGINS"); v != "" {
		site.AllowedOrigins = splitAndTrim(v)
	}
	if v := os.Getenv("PCAPTCHA_SITE_CHALLENGES"); v != "" {
		site.Challenges = splitAndTrim(v)
	}
	for i := range cfg.Sites {
		if cfg.Sites[i].Key == key {
			cfg.Sites[i] = site
			return
		}
	}
	cfg.Sites = append(cfg.Sites, site)
}

func (c *Config) finalize() error {
	if len(c.Sites) == 0 {
		return errors.New("config: no sites configured; declare at least one site, or set PCAPTCHA_SITE_KEY and PCAPTCHA_SITE_SECRET")
	}
	if c.DefaultLocale == "" {
		c.DefaultLocale = "en"
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = Duration(5 * time.Minute)
	}
	if c.TokenTTL <= 0 {
		c.TokenTTL = Duration(5 * time.Minute)
	}
	if c.Store.Driver == "" {
		c.Store.Driver = "memory"
	}
	if c.Store.Redis.Prefix == "" {
		c.Store.Redis.Prefix = "pcaptcha:"
	}
	c.BaseURL = strings.TrimSuffix(c.BaseURL, "/")

	c.siteIndex = make(map[string]*Site, len(c.Sites))
	c.secretIndex = make(map[string]*Site, len(c.Sites))
	for i := range c.Sites {
		s := &c.Sites[i]
		switch {
		case s.Key == "" || s.Secret == "":
			return fmt.Errorf("config: site %q needs both key and secret", s.Name)
		case s.Secret == s.Key:
			return fmt.Errorf("config: site %q has a secret equal to its public key", s.Name)
		}
		if _, dup := c.siteIndex[s.Key]; dup {
			return fmt.Errorf("config: duplicate site key %q", s.Key)
		}
		if s.Difficulty == "" {
			s.Difficulty = DifficultyNormal
		}
		switch s.Difficulty {
		case DifficultyEasy, DifficultyNormal, DifficultyHard:
		default:
			return fmt.Errorf("config: site %q has unknown difficulty %q", s.Name, s.Difficulty)
		}
		if s.MaxAttempts <= 0 {
			s.MaxAttempts = 4
		}
		if s.Locale == "" {
			s.Locale = c.DefaultLocale
		}
		c.siteIndex[s.Key] = s
		c.secretIndex[s.Secret] = s
	}
	return nil
}

// SiteByKey returns the tenant owning the given public key.
func (c *Config) SiteByKey(key string) (*Site, bool) {
	s, ok := c.siteIndex[key]
	return s, ok
}

// SiteBySecret returns the tenant owning the given secret key.
func (c *Config) SiteBySecret(secret string) (*Site, bool) {
	s, ok := c.secretIndex[secret]
	return s, ok
}

func setString(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func splitAndTrim(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Watcher re-reads the config file when it changes on disk and publishes the
// new value atomically. Callers always read through Current.
type Watcher struct {
	path    string
	current atomic.Pointer[Config]
	onError func(error)

	mu      sync.Mutex
	modTime time.Time
	size    int64
}

// NewWatcher builds a watcher around an already-loaded config.
func NewWatcher(path string, initial *Config, onError func(error)) *Watcher {
	w := &Watcher{path: path, onError: onError}
	w.current.Store(initial)
	if fi, err := os.Stat(path); err == nil {
		w.modTime, w.size = fi.ModTime(), fi.Size()
	}
	return w
}

// Current returns the most recently loaded configuration.
func (w *Watcher) Current() *Config { return w.current.Load() }

// Run polls the file for changes until stop is closed.
func (w *Watcher) Run(stop <-chan struct{}, interval time.Duration) {
	if w.path == "" {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			w.reloadIfChanged()
		}
	}
}

func (w *Watcher) reloadIfChanged() {
	w.mu.Lock()
	defer w.mu.Unlock()
	fi, err := os.Stat(w.path)
	if err != nil {
		return
	}
	if fi.ModTime().Equal(w.modTime) && fi.Size() == w.size {
		return
	}
	// Record the stamp either way, so a broken file is not retried every tick.
	w.modTime, w.size = fi.ModTime(), fi.Size()

	cfg, err := Load(w.path)
	if err != nil {
		if w.onError != nil {
			w.onError(err)
		}
		return // keep serving the last good config
	}
	w.current.Store(cfg)
}
