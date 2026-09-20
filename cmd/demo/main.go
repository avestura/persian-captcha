// Command demo runs a small site that embeds the captcha and verifies the
// token server-side, so the whole round trip can be exercised locally.
//
//	go run ./cmd/captchad -config captcha.example.yaml     # terminal one
//	go run ./cmd/demo                                      # terminal two
//	open http://localhost:5173
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

// siteKeys maps the demo's language buttons onto the site keys declared in
// captcha.example.yaml.
var siteKeys = map[string]struct{ Key, Secret, Locale string }{
	"en":   {"pc_site_demo_en", "pc_secret_demo_en_change_me", "en"},
	"fa":   {"pc_site_demo_fa", "pc_secret_demo_fa_change_me", "fa"},
	"hard": {"pc_site_demo_hard", "pc_secret_demo_hard_change_me", "en"},
}

func main() {
	var (
		listen  = flag.String("listen", ":5173", "address to serve the demo on")
		captcha = flag.String("captcha", "http://localhost:8080", "base URL of the captcha service")
	)
	flag.Parse()

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
		variant := r.URL.Query().Get("site")
		site, ok := siteKeys[variant]
		if !ok {
			variant, site = "en", siteKeys["en"]
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, map[string]any{
			"CaptchaBase": *captcha,
			"SiteKey":     site.Key,
			"Locale":      site.Locale,
			"Variant":     variant,
			"Theme":       r.URL.Query().Get("theme"),
		})
	})

	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		// FormValue rather than ParseForm: the page posts a FormData object,
		// which the browser encodes as multipart, and ParseForm reads only
		// urlencoded bodies. Reaching for ParseForm here is an easy mistake
		// that presents as a captcha token that never arrives.
		variant := r.FormValue("site")
		site, ok := siteKeys[variant]
		if !ok {
			site = siteKeys["en"]
		}

		// This is the part a real backend implements: take the token the
		// browser submitted, exchange it for a verdict, and refuse the request
		// if it does not come back successful. Never trust the browser's word
		// that the captcha was solved.
		result, err := verify(r.Context(), *captcha, site.Secret, r.FormValue("pcaptcha-response"), clientIP(r))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
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
			"ok":       true,
			"score":    result.Score,
			"solved":   result.Solved,
			"hostname": result.Hostname,
			"issued":   result.ChallengeTS,
			"message":  r.FormValue("message"),
		})
	})

	log.Printf("demo site on http://localhost%s (captcha service at %s)", *listen, *captcha)
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
