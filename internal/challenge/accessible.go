package challenge

import (
	"encoding/json"
	"math/rand/v2"
	"strconv"
)

// The accessible challenge is the fallback for visitors who cannot use a
// pointer puzzle: screen-reader users, people navigating by keyboard only, and
// anyone for whom fine dragging is difficult.
//
// It is a short question with four choices, rendered entirely as HTML by the
// browser. Nothing here produces an image, and no text is drawn server-side:
// the server sends translation keys and the widget resolves them against its
// locale bundle. That is what lets the same question work in Persian without
// any server-side text shaping.
//
// The answer never leaves the server. Options carry opaque positional ids, so
// the correct choice cannot be inferred from the payload.

// AccessibleState is the private state of the fallback challenge.
type AccessibleState struct {
	// PromptKey is a translation key such as "access.arith.add".
	PromptKey string `json:"promptKey"`
	// Args are numeric substitutions for the prompt, for example a and b.
	Args map[string]int `json:"args,omitempty"`
	// Options are the choices in display order.
	Options []AccessibleOption `json:"options"`
	// AnswerIdx is the index of the correct option. Never serialised to the
	// browser: only State is stored, and only Spec is sent.
	AnswerIdx int `json:"answerIdx"`
}

// AccessibleOption is one selectable choice. Exactly one of LabelKey or
// Number is set.
type AccessibleOption struct {
	// ID is what the browser submits.
	ID string `json:"id"`
	// LabelKey is a translation key such as "word.pomegranate".
	LabelKey string `json:"labelKey,omitempty"`
	// Number is a bare number, which the browser formats using the locale's
	// digits, so Persian visitors see Persian numerals.
	Number *int `json:"number,omitempty"`
}

// AccessibleSpec is the public form: the same question, minus the answer.
type AccessibleSpec struct {
	PromptKey string             `json:"promptKey"`
	Args      map[string]int     `json:"args,omitempty"`
	Options   []AccessibleOption `json:"options"`
}

// wordCategories backs the "which of these is a ..." questions. Every word
// here has a matching "word.<id>" entry in each locale file; the category
// prompts are "access.category.<category>".
//
// Categories are kept concrete and culturally neutral so the translations are
// unambiguous in both English and Persian.
var wordCategories = map[string][]string{
	"color":     {"red", "blue", "green", "yellow", "black", "white"},
	"animal":    {"cat", "dog", "horse", "bird", "fish", "sheep"},
	"fruit":     {"apple", "peach", "grape", "melon", "fig", "pomegranate"},
	"vehicle":   {"car", "bus", "train", "bicycle", "ship", "airplane"},
	"furniture": {"chair", "table", "door", "window", "lamp", "carpet"},
}

// categoryNames is the stable iteration order of wordCategories, since map
// order in Go is randomised and challenge generation must be reproducible
// from a seed.
var categoryNames = []string{"color", "animal", "fruit", "vehicle", "furniture"}

type accessibleGen struct{}

func (g accessibleGen) generate(rng *rand.Rand, level Level) *State {
	var st *AccessibleState
	if rng.IntN(2) == 0 {
		st = generateArithmetic(rng, level)
	} else {
		st = generateCategory(rng)
	}
	for i := range st.Options {
		st.Options[i].ID = strconv.Itoa(i)
	}
	return &State{Accessible: st}
}

// generateArithmetic builds a small sum or difference with plausible wrong
// answers nearby, so guessing is no easier than one in four.
func generateArithmetic(rng *rand.Rand, level Level) *AccessibleState {
	hi := pick(level, 6, 9, 12)
	a := 1 + rng.IntN(hi)
	b := 1 + rng.IntN(hi)
	key, want := "access.arith.add", a+b
	if rng.IntN(2) == 0 {
		if a < b {
			a, b = b, a
		}
		key, want = "access.arith.sub", a-b
	}

	used := map[int]bool{want: true}
	values := []int{want}
	for len(values) < 4 {
		// Distractors sit within three of the answer, which keeps them
		// believable without ever colliding with it.
		d := rng.IntN(7) - 3
		v := want + d
		if v < 0 || used[v] {
			continue
		}
		used[v] = true
		values = append(values, v)
	}
	rng.Shuffle(len(values), func(i, j int) { values[i], values[j] = values[j], values[i] })

	opts := make([]AccessibleOption, len(values))
	answer := 0
	for i, v := range values {
		n := v
		opts[i] = AccessibleOption{Number: &n}
		if v == want {
			answer = i
		}
	}
	return &AccessibleState{
		PromptKey: key,
		Args:      map[string]int{"a": a, "b": b},
		Options:   opts,
		AnswerIdx: answer,
	}
}

// generateCategory builds a "which of these is a colour?" style question.
func generateCategory(rng *rand.Rand) *AccessibleState {
	target := categoryNames[rng.IntN(len(categoryNames))]
	words := wordCategories[target]
	correct := words[rng.IntN(len(words))]

	// Three distractors, each from a different category than the target.
	others := make([]string, 0, len(categoryNames)-1)
	for _, c := range categoryNames {
		if c != target {
			others = append(others, c)
		}
	}
	rng.Shuffle(len(others), func(i, j int) { others[i], others[j] = others[j], others[i] })

	picks := []string{correct}
	for _, c := range others[:3] {
		pool := wordCategories[c]
		picks = append(picks, pool[rng.IntN(len(pool))])
	}
	rng.Shuffle(len(picks), func(i, j int) { picks[i], picks[j] = picks[j], picks[i] })

	opts := make([]AccessibleOption, len(picks))
	answer := 0
	for i, w := range picks {
		opts[i] = AccessibleOption{LabelKey: "word." + w}
		if w == correct {
			answer = i
		}
	}
	return &AccessibleState{
		PromptKey: "access.category." + target,
		Options:   opts,
		AnswerIdx: answer,
	}
}

func (accessibleGen) spec(st *State) *Spec {
	a := st.Accessible
	opts := make([]AccessibleOption, len(a.Options))
	copy(opts, a.Options)
	return &Spec{Accessible: &AccessibleSpec{
		PromptKey: a.PromptKey,
		Args:      a.Args,
		Options:   opts,
	}}
}

func (accessibleGen) assets(*State) []string { return nil }

func (accessibleGen) render(*State, string) ([]byte, error) { return nil, ErrUnknownAsset }

type accessibleAnswer struct {
	Choice *string `json:"choice"`
}

func (accessibleGen) check(st *State, raw json.RawMessage) (Outcome, error) {
	var a accessibleAnswer
	if err := json.Unmarshal(raw, &a); err != nil || a.Choice == nil {
		return Outcome{}, ErrBadAnswer
	}
	want := st.Accessible.Options[st.Accessible.AnswerIdx].ID
	ok := *a.Choice == want
	c := 0.0
	if ok {
		c = 1
	}
	return Outcome{Correct: ok, Closeness: c, Reason: "choice"}, nil
}
