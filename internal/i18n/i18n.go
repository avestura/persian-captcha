// Package i18n loads the widget's locale bundles and negotiates which one a
// visitor should see.
//
// Locales are plain JSON files embedded at build time. Adding a language means
// adding one file: nothing in the Go code enumerates the supported set, and
// nothing in the rendering path draws text server-side, so a new script works
// without any font or shaping work on the server.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed locales/*.json
var localeFS embed.FS

// Direction is a locale's writing direction.
type Direction string

// The writing directions.
const (
	LTR Direction = "ltr"
	RTL Direction = "rtl"
)

// Meta describes how a locale should be presented.
type Meta struct {
	// Name is the language's own name, for a language picker.
	Name string `json:"name"`
	// Dir is "ltr" or "rtl". The widget mirrors its chrome for RTL: the
	// slider track, the progress fill, the arrows and the panel layout.
	Dir Direction `json:"dir"`
	// Digits selects the numeral system: "latn" for 0-9 or "arabext" for the
	// Persian digits. Numbers are formatted in the browser, not here.
	Digits string `json:"digits"`
	// Font is an optional CSS font stack for this locale, used ahead of the
	// widget default.
	Font string `json:"font,omitempty"`
}

// Locale is one loaded language bundle.
type Locale struct {
	// Tag is the BCP 47 language tag, such as "en" or "fa".
	Tag  string            `json:"tag"`
	Meta Meta              `json:"meta"`
	Msg  map[string]string `json:"messages"`

	// json is the marshalled bundle, cached so every frame request does not
	// re-serialise it.
	json []byte
}

// T looks up a message, falling back to the key itself so a missing string is
// visible in testing rather than silently blank.
func (l *Locale) T(key string) string {
	if v, ok := l.Msg[key]; ok {
		return v
	}
	return key
}

// JSON returns the bundle as it is sent to the browser.
func (l *Locale) JSON() []byte { return l.json }

// Bundle is the set of available locales.
type Bundle struct {
	locales map[string]*Locale
	tags    []string
	def     string
}

// Load reads every embedded locale and validates it against the default one,
// which acts as the reference for the key set.
func Load(defaultTag string) (*Bundle, error) {
	entries, err := fs.Glob(localeFS, "locales/*.json")
	if err != nil {
		return nil, err
	}
	b := &Bundle{locales: make(map[string]*Locale, len(entries))}
	for _, name := range entries {
		raw, err := localeFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var l Locale
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", name, err)
		}
		tag := strings.TrimSuffix(path.Base(name), ".json")
		l.Tag = tag
		if l.Meta.Dir == "" {
			l.Meta.Dir = LTR
		}
		if l.Meta.Digits == "" {
			l.Meta.Digits = "latn"
		}
		encoded, err := json.Marshal(&l)
		if err != nil {
			return nil, err
		}
		l.json = encoded
		b.locales[tag] = &l
		b.tags = append(b.tags, tag)
	}
	if len(b.locales) == 0 {
		return nil, fmt.Errorf("i18n: no locale files embedded")
	}
	sort.Strings(b.tags)

	if _, ok := b.locales[defaultTag]; !ok {
		if _, ok := b.locales["en"]; ok {
			defaultTag = "en"
		} else {
			defaultTag = b.tags[0]
		}
	}
	b.def = defaultTag

	if err := b.checkCoverage(); err != nil {
		return nil, err
	}
	return b, nil
}

// checkCoverage reports keys present in the default locale but missing
// elsewhere. Catching this at startup beats discovering it as a raw key on a
// visitor's screen.
func (b *Bundle) checkCoverage() error {
	ref := b.locales[b.def]
	var problems []string
	for _, tag := range b.tags {
		if tag == b.def {
			continue
		}
		var missing []string
		for k := range ref.Msg {
			if _, ok := b.locales[tag].Msg[k]; !ok {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			problems = append(problems, fmt.Sprintf("%s is missing %d keys: %s",
				tag, len(missing), strings.Join(missing[:min(len(missing), 8)], ", ")))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("i18n: incomplete locales: %s", strings.Join(problems, "; "))
	}
	return nil
}

// Tags lists the available language tags.
func (b *Bundle) Tags() []string {
	out := make([]string, len(b.tags))
	copy(out, b.tags)
	return out
}

// Default returns the fallback locale.
func (b *Bundle) Default() *Locale { return b.locales[b.def] }

// Get returns a locale by exact tag.
func (b *Bundle) Get(tag string) (*Locale, bool) {
	l, ok := b.locales[strings.ToLower(tag)]
	return l, ok
}

// Negotiate picks a locale, in order of preference: an explicit request from
// the embedding page, the site's configured default, then the browser's
// Accept-Language header, then the service default.
//
// The site default outranks Accept-Language deliberately. An operator running
// a Persian-language site wants the widget in Persian even for a visitor
// whose browser is configured in English, because the widget sits inside
// their page and should match it.
func (b *Bundle) Negotiate(explicit, siteDefault, acceptLanguage string) *Locale {
	if l := b.match(explicit); l != nil {
		return l
	}
	if l := b.match(siteDefault); l != nil {
		return l
	}
	for _, tag := range parseAcceptLanguage(acceptLanguage) {
		if l := b.match(tag); l != nil {
			return l
		}
	}
	return b.Default()
}

// match resolves a tag, accepting region subtags such as "fa-IR" for "fa".
func (b *Bundle) match(tag string) *Locale {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return nil
	}
	if l, ok := b.locales[tag]; ok {
		return l
	}
	if base, _, found := strings.Cut(tag, "-"); found {
		if l, ok := b.locales[base]; ok {
			return l
		}
	}
	return nil
}

// parseAcceptLanguage returns the tags of an Accept-Language header in
// descending order of quality.
func parseAcceptLanguage(header string) []string {
	if header == "" {
		return nil
	}
	type pref struct {
		tag string
		q   float64
	}
	var prefs []pref
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, params, _ := strings.Cut(part, ";")
		tag = strings.TrimSpace(tag)
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		if _, qv, ok := strings.Cut(params, "q="); ok {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(qv), 64); err == nil {
				q = parsed
			}
		}
		if q <= 0 {
			continue
		}
		prefs = append(prefs, pref{tag: tag, q: q})
	}
	// A stable sort keeps equal-quality tags in the order the browser listed
	// them, which is itself a preference ordering.
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })

	out := make([]string, 0, len(prefs))
	for _, p := range prefs {
		out = append(out, p.tag)
	}
	return out
}
