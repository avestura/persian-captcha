// Command captchad runs the captcha service.
//
//	captchad -config captcha.yaml
//
// Every setting can also come from the environment, so a container deployment
// needs no configuration file at all:
//
//	PCAPTCHA_SITE_KEY=... PCAPTCHA_SITE_SECRET=... captchad
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"avestura.dev/persian-captcha/internal/api"
	"avestura.dev/persian-captcha/internal/config"
	"avestura.dev/persian-captcha/internal/i18n"
	"avestura.dev/persian-captcha/internal/session"
	"avestura.dev/persian-captcha/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "captchad:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", envOr("PCAPTCHA_CONFIG", "captcha.yaml"), "path to the configuration file")
		showVersion = flag.Bool("version", false, "print the version and exit")
		checkOnly   = flag.Bool("check", false, "validate the configuration and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("captchad", version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)

	if *checkOnly {
		logger.Info("configuration is valid",
			"sites", len(cfg.Sites), "store", cfg.Store.Driver, "listen", cfg.Listen)
		return nil
	}

	locales, err := i18n.Load(cfg.DefaultLocale)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backing, err := store.New(ctx, cfg.Store)
	if err != nil {
		return err
	}
	defer backing.Close()

	ipSecret, err := session.NewSecret()
	if err != nil {
		return err
	}

	// The configuration is re-read when the file changes, so site keys and
	// origins can be rotated without dropping in-flight sessions.
	watcher := config.NewWatcher(*configPath, cfg, func(err error) {
		logger.Error("configuration reload failed, keeping the previous one", "err", err)
	})
	reloadDone := make(chan struct{})
	go func() {
		watcher.Run(ctx.Done(), 10*time.Second)
		close(reloadDone)
	}()

	srv, err := api.New(api.Options{
		Config:   watcher.Current,
		Store:    backing,
		Locales:  locales,
		Logger:   logger,
		IPSecret: ipSecret,
	})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:    cfg.Listen,
		Handler: withLogging(logger, srv),
		// Generous enough for a phone on a slow link solving a proof of work,
		// tight enough that a stalled connection is not held open for free.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("captcha service listening",
			"addr", cfg.Listen, "version", version,
			"store", cfg.Store.Driver, "sites", len(cfg.Sites),
			"locales", locales.Tags())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	<-reloadDone
	return <-serveErr
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// withLogging records one line per request at debug level, and surfaces
// server errors at warn. Request logging is deliberately quiet by default:
// this service sees a request for every page view on every tenant site.
func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		level := slog.LevelDebug
		if rec.status >= 500 {
			level = slog.LevelWarn
		}
		logger.Log(r.Context(), level, "request",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush lets the recorder sit in front of handlers that stream.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
