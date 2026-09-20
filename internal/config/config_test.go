package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sample = `
listen: ":9090"
base_url: "https://captcha.example.com/"
trust_proxy_headers: true
session_ttl: "90s"
token_ttl: 120
default_locale: "fa"

store:
  driver: redis
  redis:
    addr: "redis:6379"
    db: 3

rate_limits:
  session_per_ip_per_min: 11

sites:
  - name: "Example"      # trailing comment
    key: "pc_site_a"
    secret: "pc_secret_a"
    allowed_origins: ["https://example.com", "*.example.org"]
    difficulty: hard
    challenges: [slider_jigsaw, rotate]
    locale: "en"
    always_challenge: true
    challenge_rate: 0.5
  - name: "Minimal"
    key: "pc_site_b"
    secret: "pc_secret_b"
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "captcha.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadYAML(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Listen != ":9090" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.BaseURL != "https://captcha.example.com" {
		t.Errorf("BaseURL = %q; the trailing slash should be trimmed", cfg.BaseURL)
	}
	if !cfg.TrustProxy {
		t.Error("TrustProxy was not parsed")
	}
	if cfg.SessionTTL.D() != 90*time.Second {
		t.Errorf("SessionTTL = %v, want 90s", cfg.SessionTTL.D())
	}
	// A bare number is read as seconds.
	if cfg.TokenTTL.D() != 2*time.Minute {
		t.Errorf("TokenTTL = %v, want 2m", cfg.TokenTTL.D())
	}
	if cfg.Store.Driver != "redis" || cfg.Store.Redis.Addr != "redis:6379" || cfg.Store.Redis.DB != 3 {
		t.Errorf("store parsed as %+v", cfg.Store)
	}
	if cfg.Store.Redis.Prefix != "pcaptcha:" {
		t.Errorf("the default Redis prefix was not applied: %q", cfg.Store.Redis.Prefix)
	}
	if cfg.RateLimits.SessionPerIP != 11 {
		t.Errorf("SessionPerIP = %d", cfg.RateLimits.SessionPerIP)
	}
	// Unspecified limits keep their defaults rather than becoming zero, which
	// would silently disable them.
	if cfg.RateLimits.SolvePerIP == 0 {
		t.Error("an unspecified rate limit was zeroed rather than defaulted")
	}

	if len(cfg.Sites) != 2 {
		t.Fatalf("parsed %d sites, want 2", len(cfg.Sites))
	}
	a, ok := cfg.SiteByKey("pc_site_a")
	if !ok {
		t.Fatal("pc_site_a is missing")
	}
	if a.Difficulty != DifficultyHard || !a.AlwaysChallenge {
		t.Errorf("site A parsed as %+v", a)
	}
	if got := a.AllowedOrigins; len(got) != 2 || got[0] != "https://example.com" {
		t.Errorf("allowed_origins = %v", got)
	}
	if got := a.Challenges; len(got) != 2 || got[0] != "slider_jigsaw" {
		t.Errorf("challenges = %v", got)
	}
	if got := a.SampleRate(); got != 0.5 {
		t.Errorf("SampleRate = %v, want 0.5", got)
	}

	b, _ := cfg.SiteByKey("pc_site_b")
	if b.Difficulty != DifficultyNormal {
		t.Errorf("an unspecified difficulty became %q", b.Difficulty)
	}
	if b.MaxAttempts <= 0 {
		t.Errorf("MaxAttempts = %d", b.MaxAttempts)
	}
	if b.Locale != "fa" {
		t.Errorf("a site with no locale should inherit the default, got %q", b.Locale)
	}
	if got := b.SampleRate(); got != DefaultChallengeRate {
		t.Errorf("SampleRate = %v, want the default %v", got, DefaultChallengeRate)
	}

	// Secrets must resolve back to their site, and only to their own.
	if site, ok := cfg.SiteBySecret("pc_secret_b"); !ok || site.Key != "pc_site_b" {
		t.Error("SiteBySecret did not resolve a known secret")
	}
	if _, ok := cfg.SiteBySecret("pc_site_a"); ok {
		t.Error("a public key was accepted as a secret")
	}
}

func TestLoadRejectsBadConfigurations(t *testing.T) {
	cases := map[string]string{
		"no sites":           "listen: \":80\"\n",
		"missing secret":     "sites:\n  - key: \"k\"\n",
		"secret equals key":  "sites:\n  - key: \"k\"\n    secret: \"k\"\n",
		"duplicate site key": "sites:\n  - key: \"k\"\n    secret: \"s1\"\n  - key: \"k\"\n    secret: \"s2\"\n",
		"unknown difficulty": "sites:\n  - key: \"k\"\n    secret: \"s\"\n    difficulty: impossible\n",
		"tab indentation":    "sites:\n\t- key: \"k\"\n",
		"unterminated list":  "sites: [a, b\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Error("the configuration was accepted")
			}
		})
	}
}

func TestEnvironmentOnlyConfiguration(t *testing.T) {
	t.Setenv("PCAPTCHA_SITE_KEY", "env_key")
	t.Setenv("PCAPTCHA_SITE_SECRET", "env_secret")
	t.Setenv("PCAPTCHA_SITE_ORIGINS", "https://a.example, https://b.example")
	t.Setenv("PCAPTCHA_LISTEN", ":7777")
	t.Setenv("PCAPTCHA_REDIS_ADDR", "cache:6379")

	// A missing file is not an error when the environment supplies a site.
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":7777" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.Store.Driver != "redis" {
		t.Errorf("setting a Redis address should select the Redis driver, got %q", cfg.Store.Driver)
	}
	site, ok := cfg.SiteByKey("env_key")
	if !ok {
		t.Fatal("the environment-declared site is missing")
	}
	if len(site.AllowedOrigins) != 2 || site.AllowedOrigins[1] != "https://b.example" {
		t.Errorf("origins = %v", site.AllowedOrigins)
	}
}

func TestEnvironmentOverridesAFileSite(t *testing.T) {
	path := writeConfig(t, sample)
	t.Setenv("PCAPTCHA_SITE_KEY", "pc_site_a")
	t.Setenv("PCAPTCHA_SITE_SECRET", "rotated_secret")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 2 {
		t.Errorf("the override added a site instead of replacing one: %d sites", len(cfg.Sites))
	}
	if _, ok := cfg.SiteBySecret("rotated_secret"); !ok {
		t.Error("the rotated secret is not in effect")
	}
	if _, ok := cfg.SiteBySecret("pc_secret_a"); ok {
		t.Error("the replaced secret still works")
	}
}

func TestOriginAllowed(t *testing.T) {
	site := &Site{AllowedOrigins: []string{"https://example.com", "*.example.org", "HTTP://Mixed.Case/"}}
	allowed := []string{
		"https://example.com",
		"https://example.com/",
		"https://sub.example.org",
		"https://deep.sub.example.org",
		"http://mixed.case",
	}
	for _, origin := range allowed {
		if !site.OriginAllowed(origin) {
			t.Errorf("%q should be allowed", origin)
		}
	}
	denied := []string{
		"https://evil.com",
		"http://example.com",  // a different scheme is a different origin
		"https://example.org", // the bare host does not match "*.example.org"
		"https://notexample.com",
		"",
	}
	for _, origin := range denied {
		if site.OriginAllowed(origin) {
			t.Errorf("%q should be denied", origin)
		}
	}

	// An empty list means "anything", which is the development default.
	open := &Site{}
	if !open.OriginAllowed("https://anywhere.example") {
		t.Error("an empty allow-list should permit any origin")
	}
}

func TestSampleRateIsClamped(t *testing.T) {
	high, low := 5.0, -2.0
	if got := (&Site{ChallengeRate: &high}).SampleRate(); got != 1 {
		t.Errorf("SampleRate(5) = %v, want 1", got)
	}
	if got := (&Site{ChallengeRate: &low}).SampleRate(); got != 0 {
		t.Errorf("SampleRate(-2) = %v, want 0", got)
	}
	zero := 0.0
	if got := (&Site{ChallengeRate: &zero}).SampleRate(); got != 0 {
		t.Errorf("an explicit zero became %v", got)
	}
}

func TestWatcherReloads(t *testing.T) {
	path := writeConfig(t, sample)
	initial, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var reloadErr error
	w := NewWatcher(path, initial, func(err error) { reloadErr = err })

	// A valid change is picked up.
	updated := sample + "\n  - name: \"Third\"\n    key: \"pc_site_c\"\n    secret: \"pc_secret_c\"\n"
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, path)
	w.reloadIfChanged()
	if _, ok := w.Current().SiteByKey("pc_site_c"); !ok {
		t.Error("the added site was not picked up")
	}

	// A broken file must not take the service down with it.
	if err := os.WriteFile(path, []byte("sites: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, path)
	w.reloadIfChanged()
	if reloadErr == nil {
		t.Error("the reload error was not reported")
	}
	if _, ok := w.Current().SiteByKey("pc_site_c"); !ok {
		t.Error("a broken file replaced the last good configuration")
	}
}

// touch moves the modification time forward, since a test can rewrite a file
// within the filesystem's timestamp resolution.
func touch(t *testing.T, path string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func TestYAMLSubset(t *testing.T) {
	cases := map[string]string{
		"flow sequence":     "a: [1, 2, 3]",
		"flow mapping":      "a: {x: 1, y: two}",
		"nested maps":       "a:\n  b:\n    c: 1",
		"sequence of maps":  "a:\n  - k: 1\n    j: 2\n  - k: 3\n    j: 4",
		"sequence at key":   "a:\n- 1\n- 2",
		"quoted colon":      `a: "http://example.com:8080/x"`,
		"single quotes":     "a: 'it''s here'",
		"comment only line": "# nothing\na: 1",
		"booleans":          "a: true\nb: no\nc: null",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := yamlToJSON(body)
			if err != nil {
				t.Fatalf("%v", err)
			}
			var generic map[string]any
			if err := json.Unmarshal(out, &generic); err != nil {
				t.Fatalf("produced invalid JSON %s: %v", out, err)
			}
		})
	}
}

func TestYAMLValues(t *testing.T) {
	out, err := yamlToJSON("a: 1\nb: \"2\"\nc: true\nd: [x, 'y']\ne:\n  - k: v\n")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["a"] != float64(1) {
		t.Errorf("a = %#v, want the number 1", got["a"])
	}
	if got["b"] != "2" {
		t.Errorf("b = %#v, want the string \"2\"", got["b"])
	}
	if got["c"] != true {
		t.Errorf("c = %#v, want true", got["c"])
	}
	if list, ok := got["d"].([]any); !ok || len(list) != 2 || list[1] != "y" {
		t.Errorf("d = %#v", got["d"])
	}
	if list, ok := got["e"].([]any); !ok || len(list) != 1 {
		t.Errorf("e = %#v", got["e"])
	} else if m, ok := list[0].(map[string]any); !ok || m["k"] != "v" {
		t.Errorf("e[0] = %#v", list[0])
	}
}

func TestYAMLPassesJSONThrough(t *testing.T) {
	out, err := yamlToJSON(`{"listen": ":1", "sites": []}`)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":1" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
}
