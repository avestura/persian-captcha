package i18n

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func load(t *testing.T) *Bundle {
	t.Helper()
	b, err := Load("en")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return b
}

// Load validates that every locale covers the reference key set, so a missing
// translation fails the build rather than showing a raw key to a visitor.
func TestLoadRequiresCompleteLocales(t *testing.T) {
	b := load(t)
	tags := b.Tags()
	if len(tags) < 2 {
		t.Fatalf("expected at least English and Persian, got %v", tags)
	}
	for _, want := range []string{"en", "fa"} {
		if _, ok := b.Get(want); !ok {
			t.Errorf("locale %q is missing", want)
		}
	}
}

func TestPersianIsRightToLeftWithPersianDigits(t *testing.T) {
	b := load(t)
	fa, ok := b.Get("fa")
	if !ok {
		t.Fatal("no Persian locale")
	}
	if fa.Meta.Dir != RTL {
		t.Errorf("fa direction = %q, want rtl", fa.Meta.Dir)
	}
	if fa.Meta.Digits != "arabext" {
		t.Errorf("fa digits = %q, want arabext", fa.Meta.Digits)
	}
	en, _ := b.Get("en")
	if en.Meta.Dir != LTR {
		t.Errorf("en direction = %q, want ltr", en.Meta.Dir)
	}
}

// Translated strings keep the placeholders the widget substitutes into. A
// dropped {n} silently loses information for everyone reading that language.
func TestPlaceholdersMatchAcrossLocales(t *testing.T) {
	b := load(t)
	reference, _ := b.Get("en")
	placeholder := regexp.MustCompile(`\{(\w+)\}`)

	for _, tag := range b.Tags() {
		if tag == "en" {
			continue
		}
		other, _ := b.Get(tag)
		for key, want := range reference.Msg {
			got := other.Msg[key]
			wantNames := placeholder.FindAllString(want, -1)
			gotNames := placeholder.FindAllString(got, -1)
			if len(wantNames) != len(gotNames) {
				t.Errorf("%s/%s: placeholders %v do not match the English %v",
					tag, key, gotNames, wantNames)
				continue
			}
			for _, name := range wantNames {
				if !strings.Contains(got, name) {
					t.Errorf("%s/%s: missing placeholder %s", tag, key, name)
				}
			}
		}
	}
}

// No translated string may be left as a copy of the English, and none may be
// blank: both are the usual symptoms of a half-finished locale file.
func TestTranslationsAreActuallyTranslated(t *testing.T) {
	b := load(t)
	reference, _ := b.Get("en")
	fa, _ := b.Get("fa")

	var untranslated []string
	for key, english := range reference.Msg {
		persian := fa.Msg[key]
		if strings.TrimSpace(persian) == "" {
			t.Errorf("fa/%s is empty", key)
			continue
		}
		if persian == english {
			untranslated = append(untranslated, key)
		}
	}
	if len(untranslated) > 0 {
		t.Errorf("%d Persian strings are identical to the English: %v",
			len(untranslated), untranslated)
	}
}

// The challenge sends shape identifiers and expects the browser to have a
// name for each one.
func TestShapeAndWordKeysArePresent(t *testing.T) {
	b := load(t)
	shapes := []string{
		"star", "crescent", "triangle", "square", "pentagon", "hexagon",
		"diamond", "circle", "heart", "droplet", "arrow", "flower",
	}
	words := []string{
		"red", "blue", "green", "yellow", "black", "white",
		"cat", "dog", "horse", "bird", "fish", "sheep",
		"apple", "peach", "grape", "melon", "fig", "pomegranate",
		"car", "bus", "train", "bicycle", "ship", "airplane",
		"chair", "table", "door", "window", "lamp", "carpet",
	}
	for _, tag := range b.Tags() {
		locale, _ := b.Get(tag)
		for _, shape := range shapes {
			if key := "shape." + shape; locale.T(key) == key {
				t.Errorf("%s is missing %s", tag, key)
			}
		}
		for _, word := range words {
			if key := "word." + word; locale.T(key) == key {
				t.Errorf("%s is missing %s", tag, key)
			}
		}
	}
}

func TestNegotiate(t *testing.T) {
	b := load(t)
	cases := []struct {
		name                            string
		explicit, siteDefault, accepted string
		want                            string
	}{
		{"explicit wins", "fa", "en", "en-GB,en;q=0.9", "fa"},
		{"region subtag resolves to its base", "fa-IR", "", "", "fa"},
		{"site default beats the browser", "", "fa", "en-US,en;q=0.9", "fa"},
		{"browser is used when nothing else applies", "", "", "fa-IR,fa;q=0.9,en;q=0.8", "fa"},
		{"quality ordering is honoured", "", "", "de;q=0.2,fa;q=0.9", "fa"},
		{"unknown explicit falls through", "klingon", "", "fa", "fa"},
		{"nothing at all gives the default", "", "", "", "en"},
		{"wildcard is ignored", "", "", "*", "en"},
		{"zero quality is ignored", "", "", "fa;q=0", "en"},
		{"unknown languages fall back", "", "", "de-DE,fr;q=0.8", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := b.Negotiate(tc.explicit, tc.siteDefault, tc.accepted)
			if got.Tag != tc.want {
				t.Errorf("Negotiate(%q, %q, %q) = %q, want %q",
					tc.explicit, tc.siteDefault, tc.accepted, got.Tag, tc.want)
			}
		})
	}
}

func TestNegotiateHandlesJunkHeaders(t *testing.T) {
	b := load(t)
	junk := []string{
		",,,", ";q=", "fa;q=abc", strings.Repeat("x,", 500), "\x00\x01",
	}
	for _, header := range junk {
		if got := b.Negotiate("", "", header); got == nil {
			t.Errorf("Negotiate returned nil for %q", header)
		}
	}
}

func TestTranslatorFallsBackToTheKey(t *testing.T) {
	b := load(t)
	en, _ := b.Get("en")
	if got := en.T("no.such.key"); got != "no.such.key" {
		t.Errorf("T on a missing key = %q, want the key itself", got)
	}
}

// The bundle is inlined into the frame document, so it has to be valid JSON
// with the shape the browser expects.
func TestJSONIsServable(t *testing.T) {
	b := load(t)
	for _, tag := range b.Tags() {
		locale, _ := b.Get(tag)
		var decoded struct {
			Tag  string            `json:"tag"`
			Meta Meta              `json:"meta"`
			Msg  map[string]string `json:"messages"`
		}
		if err := json.Unmarshal(locale.JSON(), &decoded); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		if decoded.Tag != tag {
			t.Errorf("%s: serialised tag is %q", tag, decoded.Tag)
		}
		if len(decoded.Msg) == 0 {
			t.Errorf("%s: no messages in the serialised bundle", tag)
		}
		if decoded.Meta.Name == "" {
			t.Errorf("%s: no display name", tag)
		}
	}
}

func TestLoadFallsBackForAnUnknownDefault(t *testing.T) {
	b, err := Load("klingon")
	if err != nil {
		t.Fatal(err)
	}
	if b.Default().Tag != "en" {
		t.Errorf("default = %q, want en", b.Default().Tag)
	}
}
