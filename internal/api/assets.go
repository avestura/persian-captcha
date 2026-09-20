package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"avestura.dev/persian-captcha/internal/i18n"
	"avestura.dev/persian-captcha/web"
)

// assetSet holds the compiled browser assets and their cache validators.
type assetSet struct {
	widgetJS  []byte
	frameJS   []byte
	frameCSS  []byte
	powJS     []byte
	frameTmpl *template.Template

	// version is a short hash of the bundles, used to bust caches when the
	// service is upgraded.
	version string
	etags   map[string]string
	fonts   map[string][]byte
}

// loadAssets reads the embedded bundles at startup, so no file system access
// happens on the request path.
func loadAssets() (*assetSet, error) {
	a := &assetSet{etags: map[string]string{}, fonts: map[string][]byte{}}

	for name, dst := range map[string]*[]byte{
		"dist/widget.js": &a.widgetJS,
		"dist/frame.js":  &a.frameJS,
		"dist/frame.css": &a.frameCSS,
		"dist/pow.js":    &a.powJS,
	} {
		data, err := web.Dist.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("api: missing built asset %s (run `npm run build` in web/): %w", name, err)
		}
		*dst = data
		a.etags[path.Base(name)] = etagOf(data)
	}

	tmpl, err := template.New("frame").Parse(web.FrameHTML)
	if err != nil {
		return nil, fmt.Errorf("api: frame template: %w", err)
	}
	a.frameTmpl = tmpl

	// Any font dropped into web/assets/fonts is served; none is required.
	entries, err := fs.Glob(web.Assets, "assets/fonts/*.woff2")
	if err != nil {
		return nil, err
	}
	for _, name := range entries {
		data, err := web.Assets.ReadFile(name)
		if err != nil {
			return nil, err
		}
		base := path.Base(name)
		a.fonts[base] = data
		a.etags[base] = etagOf(data)
	}

	sum := sha256.New()
	sum.Write(a.widgetJS)
	sum.Write(a.frameJS)
	sum.Write(a.frameCSS)
	a.version = base64.RawURLEncoding.EncodeToString(sum.Sum(nil))[:10]
	return a, nil
}

func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:])[:20] + `"`
}

// serveStatic writes an embedded asset with revalidation support.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, name, contentType string, data []byte, maxAge time.Duration) {
	etag := s.assets.etags[name]
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(maxAge.Seconds())))
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// handleWidgetScript serves the loader that host pages include. It is cached
// only briefly: a host page pins no version, so an upgrade has to be able to
// reach every embedder reasonably quickly.
func (s *Server) handleWidgetScript(w http.ResponseWriter, r *http.Request) {
	s.serveStatic(w, r, "widget.js", "application/javascript; charset=utf-8", s.assets.widgetJS, 10*time.Minute)
}

func (s *Server) handleFrameScript(w http.ResponseWriter, r *http.Request) {
	s.serveStatic(w, r, "frame.js", "application/javascript; charset=utf-8", s.assets.frameJS, time.Hour)
}

func (s *Server) handleFrameStyle(w http.ResponseWriter, r *http.Request) {
	s.serveStatic(w, r, "frame.css", "text/css; charset=utf-8", s.assets.frameCSS, time.Hour)
}

// handlePowWorker serves the proof-of-work worker. It is a separate
// same-origin script rather than a blob URL so the frame's Content-Security-
// Policy can keep worker-src restricted to 'self'.
func (s *Server) handlePowWorker(w http.ResponseWriter, r *http.Request) {
	s.serveStatic(w, r, "pow.js", "application/javascript; charset=utf-8", s.assets.powJS, time.Hour)
}

func (s *Server) handleFont(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.PathValue("name"))
	data, ok := s.assets.fonts[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Fonts are content-addressed by name and never change in place, so they
	// can be cached for a long time.
	s.serveStatic(w, r, name, "font/woff2", data, 30*24*time.Hour)
}

// frameData is the template context for the iframe document.
type frameData struct {
	LocaleTag string
	Dir       string
	Title     string
	Theme     string
	Base      string
	Version   string
	Nonce     string
	FontURL   string
	Boot      template.JS
}

// bootData is the JSON handed to the frame script. Inlining it avoids a
// round trip before the widget can render its first frame, and keeps the
// locale bundle out of a separately cacheable URL.
type bootData struct {
	SiteKey      string          `json:"sitekey"`
	Base         string          `json:"base"`
	Theme        string          `json:"theme"`
	ParentOrigin string          `json:"parentOrigin"`
	Locale       json.RawMessage `json:"locale"`
	Locales      []localeChoice  `json:"locales"`
}

type localeChoice struct {
	Tag  string `json:"tag"`
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// handleFrame serves the challenge document that the widget embeds.
//
// This is the security boundary of the whole design. The page runs on the
// captcha service's origin, so the host page cannot read its DOM, cannot
// observe the challenge artwork and cannot synthesise the events that solve
// it. The Content-Security-Policy below carries frame-ancestors, which is
// what actually stops an unlisted site from embedding a tenant's widget:
// browsers enforce it, unlike any check the service could do on a header the
// client controls.
func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	siteKey := q.Get("sitekey")
	site, ok := s.site(siteKey)
	if !ok {
		s.frameError(w, "origin")
		return
	}

	parentOrigin := normaliseOrigin(q.Get("origin"))
	if parentOrigin == "" {
		parentOrigin = normaliseOrigin(r.Header.Get("Referer"))
	}
	if !site.OriginAllowed(parentOrigin) {
		s.frameError(w, "origin")
		return
	}

	locale := s.locales.Negotiate(q.Get("locale"), site.Locale, r.Header.Get("Accept-Language"))
	theme := q.Get("theme")
	switch theme {
	case "light", "dark", "auto":
	default:
		theme = firstNonEmpty(site.Theme, "auto")
	}

	nonce, err := randomNonce()
	if err != nil {
		s.fail(w, "nonce", err)
		return
	}

	tags := s.locales.Tags()
	choices := make([]localeChoice, 0, len(tags))
	for _, tag := range tags {
		l, _ := s.locales.Get(tag)
		choices = append(choices, localeChoice{Tag: tag, Name: l.Meta.Name, Dir: string(l.Meta.Dir)})
	}

	boot := bootData{
		SiteKey:      site.Key,
		Base:         "/v1",
		Theme:        theme,
		ParentOrigin: parentOrigin,
		Locale:       locale.JSON(),
		Locales:      choices,
	}
	bootJSON, err := json.Marshal(boot)
	if err != nil {
		s.fail(w, "boot json", err)
		return
	}

	data := frameData{
		LocaleTag: locale.Tag,
		Dir:       string(locale.Meta.Dir),
		Title:     locale.T("challenge.title"),
		Theme:     theme,
		Base:      "/v1",
		Version:   s.assets.version,
		Nonce:     nonce,
		FontURL:   s.fontURLFor(locale),
		Boot:      template.JS(bootJSON),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", framePolicy(nonce, site.AllowedOrigins))
	// The frame must stay framed only by its allow-list; X-Frame-Options is
	// the fallback for anything that predates frame-ancestors, and it cannot
	// express a list, so it is set only in the single-origin case.
	if len(site.AllowedOrigins) == 1 && !strings.Contains(site.AllowedOrigins[0], "*") {
		w.Header().Set("X-Frame-Options", "ALLOW-FROM "+site.AllowedOrigins[0])
	}
	if err := s.assets.frameTmpl.Execute(w, data); err != nil {
		s.log.Error("frame render failed", "err", err)
	}
}

// fontURLFor returns the bundled font for a locale, if one was installed.
func (s *Server) fontURLFor(l *i18n.Locale) string {
	if l.Meta.Dir != i18n.RTL {
		return ""
	}
	for name := range s.assets.fonts {
		if strings.HasPrefix(strings.ToLower(name), "vazirmatn") {
			return "/v1/font/" + name
		}
	}
	return ""
}

// framePolicy builds the Content-Security-Policy for the iframe document.
func framePolicy(nonce string, origins []string) string {
	ancestors := "*"
	if len(origins) > 0 {
		ancestors = strings.Join(origins, " ")
	}
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'nonce-" + nonce + "'",
		"style-src 'self' 'unsafe-inline'",
		// Challenge images and the optional webfont come from this origin
		// only; data: is allowed for the small inline cursors in the CSS.
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self'",
		// The proof-of-work worker is served from this origin, which is why it
		// is a real script rather than a blob: 'self' is a tighter policy than
		// allowing blob: workers would be.
		"worker-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors " + ancestors,
	}, "; ")
}

// frameError renders a minimal, unframeable error document. It deliberately
// carries no site information.
func (s *Server) frameError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>captcha</title>`+
		`<style>body{font:14px system-ui;margin:0;display:grid;place-items:center;height:100vh;`+
		`color:#b42318;background:#fff5f5}</style><p data-error="%s">This site key cannot be used here.</p>`,
		template.HTMLEscapeString(code))
}

func randomNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b[:]), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
