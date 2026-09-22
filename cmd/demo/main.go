// Command demo runs a small site that embeds the captcha and verifies the
// token server-side, so the whole round trip can be exercised locally.
//
//	go run ./cmd/captchad -config captcha.demo.yaml     # terminal one
//	go run ./cmd/demo                                   # terminal two
//	open http://localhost:5173
//
// Or, without a Go toolchain:
//
//	docker compose -f docker-compose.demo.yml up --build
//
// The page is a gallery: one widget per challenge kind, then one per
// difficulty, then one configured the way a real site would be. Every card is
// its own form with its own site key, so a token minted for one is useless to
// the next.
//
// It is a development tool. The verification step it performs is, however,
// exactly what a real backend has to do, so it doubles as a worked example.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed assets
var assets embed.FS

// variant is one card on the page. Key and Secret must match a site declared
// in captcha.demo.yaml; the rest is prose for the card.
type variant struct {
	ID     string
	Key    string
	Secret string
	Title  string
	Note   string
}

// section groups cards under a heading.
type section struct {
	Title    string
	Blurb    string
	Variants []variant
}

// sections mirrors the sites in captcha.demo.yaml. Nothing checks the two
// against each other, so a key changed in one place has to be changed in the
// other; the symptom is a card whose widget reports an origin error, because
// the service does not recognise the key at all.
var sections = []section{
	{
		Title: "The five challenge types",
		Blurb: "Each of these site keys is pinned to one kind and set to challenge every visitor, which is the only reason they are predictable. A site that did this in production would be throwing away the cheapest half of the defence.",
		Variants: []variant{
			{
				ID: "slider", Key: "pc_site_demo_slider", Secret: "pc_secret_demo_slider_change_me",
				Title: "Slider jigsaw",
				Note:  "Drag the piece until it sits in the notch. The arrow keys work too, and are graded on the same scale.",
			},
			{
				ID: "rotate", Key: "pc_site_demo_rotate", Secret: "pc_secret_demo_rotate_change_me",
				Title: "Rotate",
				Note:  "Turn the disc back upright. The artwork is drawn from the session seed, so no two discs repeat.",
			},
			{
				ID: "click", Key: "pc_site_demo_click", Secret: "pc_secret_demo_click_change_me",
				Title: "Click in order",
				Note:  "Select the named shapes in the order asked for. A keyboard crosshair covers the same board.",
			},
			{
				ID: "drag", Key: "pc_site_demo_drag", Secret: "pc_secret_demo_drag_change_me",
				Title: "Drag and drop",
				Note:  "Put each shape into the outline that matches it. Tab picks a shape up, the arrow keys move it, Enter drops it.",
			},
			{
				ID: "accessible", Key: "pc_site_demo_accessible", Secret: "pc_secret_demo_accessible_change_me",
				Title: "Accessible question",
				Note:  "The non-visual fallback, and the one kind no site key can pin: it is offered, never imposed. Start the check, then follow the link under the puzzle that offers a question instead.",
			},
		},
	},
	{
		Title: "The same four puzzles, at each difficulty",
		Blurb: "Difficulty widens or narrows the tolerances, changes how many items a puzzle holds, and moves the proof of work a couple of bits either way.",
		Variants: []variant{
			{
				ID: "easy", Key: "pc_site_demo_level_easy", Secret: "pc_secret_demo_level_easy_change_me",
				Title: "Easy",
				Note:  "Generous tolerances and the smallest proof of work. Worth choosing where a wrongly refused visitor costs more than a wrongly admitted one.",
			},
			{
				ID: "normal", Key: "pc_site_demo_level_normal", Secret: "pc_secret_demo_level_normal_change_me",
				Title: "Normal",
				Note:  "The default, and the setting the tolerances were tuned against.",
			},
			{
				ID: "hard", Key: "pc_site_demo_level_hard", Secret: "pc_secret_demo_level_hard_change_me",
				Title: "Hard",
				Note:  "Tight tolerances, more items to place, and a proof of work a phone will notice.",
			},
		},
	},
	{
		Title: "What a real site would use",
		Blurb: "The cards above force a puzzle so that there is something to look at. This one does not.",
		Variants: []variant{
			{
				ID: "adaptive", Key: "pc_site_demo_adaptive", Secret: "pc_secret_demo_adaptive_change_me",
				Title: "Adaptive",
				Note:  "Passive signals decide, and a quarter of the visitors who look clean are shown a puzzle regardless. Most clicks here pass straight through; click a few times to land in the sampled fraction.",
			},
		},
	},
}

// variants indexes every card by its ID, for the verification handler.
var variants = func() map[string]variant {
	m := make(map[string]variant)
	for _, s := range sections {
		for _, v := range s.Variants {
			m[v.ID] = v
		}
	}
	return m
}()

// localeDir gives the writing direction for each language the page offers.
// The captcha frame translates itself; this only decides which tag the widget
// is told to ask for, and which way the demo page itself reads.
var localeDir = map[string]string{"en": "ltr", "fa": "rtl"}

func main() {
	var (
		listen   = flag.String("listen", ":5173", "address to serve the demo on")
		captcha  = flag.String("captcha", "http://localhost:8080", "base URL of the captcha service, as the browser reaches it")
		verifyAt = flag.String("verify", "", "base URL used for server-side verification (default: the value of -captcha)")
	)
	flag.Parse()

	// The two URLs are the same when everything runs on one machine, and
	// differ the moment the service is in a container: -captcha is written
	// into a script tag and an iframe src, so it must be a URL the *visitor's
	// browser* can resolve, while -verify is dialled by this process and can
	// use a name that only exists on the internal network.
	if *verifyAt == "" {
		*verifyAt = *captcha
	}

	page, err := template.ParseFS(assets, "assets/index.html")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		locale := r.URL.Query().Get("locale")
		if _, ok := localeDir[locale]; !ok {
			locale = "en"
		}
		theme := r.URL.Query().Get("theme")
		if theme != "light" && theme != "dark" {
			theme = ""
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, map[string]any{
			"CaptchaBase": *captcha,
			"Sections":    sections,
			"Locale":      locale,
			"Dir":         localeDir[locale],
			"Theme":       theme,
		})
	})

	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		// FormValue rather than ParseForm: the page posts a FormData object,
		// which the browser encodes as multipart, and ParseForm reads only
		// urlencoded bodies. Reaching for ParseForm here is an easy mistake
		// that presents as a captcha token that never arrives.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		v, ok := variants[r.FormValue("site")]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "unknown demo card"})
			return
		}

		// This is the part a real backend implements: take the token the
		// browser submitted, exchange it for a verdict, and refuse the request
		// if it does not come back successful. Never trust the browser's word
		// that the captcha was solved.
		//
		// The secret used here is the one belonging to *this* card. That is
		// what stops a token minted by an easier site key being spent on a
		// harder card's form: the service checks that the token was issued to
		// the site the secret identifies.
		result, err := verify(r.Context(), *verifyAt, v.Secret, r.FormValue("pcaptcha-response"), clientIP(r))
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !result.Success {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "codes": result.ErrorCodes})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"card": v.ID,
			// "solved" names the challenge that produced the token, or
			// "passive" when no puzzle was drawn at all.
			"solved":   result.Solved,
			"score":    result.Score,
			"hostname": result.Hostname,
			"issued":   result.ChallengeTS,
			"message":  r.FormValue("message"),
		})
	})

	log.Printf("demo site on http://localhost%s (captcha service at %s, verified via %s)",
		*listen, *captcha, *verifyAt)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

type verifyResult struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	Score       float64  `json:"score"`
	Solved      string   `json:"solved"`
	ErrorCodes  []string `json:"error-codes"`
}

// verify exchanges a captcha response token for a verdict.
func verify(ctx context.Context, base, secret, response, remoteIP string) (*verifyResult, error) {
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", response)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(base, "/")+"/v1/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("captcha service unreachable: %w", err)
	}
	defer res.Body.Close()

	var out verifyResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("captcha service returned an unreadable reply: %w", err)
	}
	return &out, nil
}

func clientIP(r *http.Request) string {
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}
