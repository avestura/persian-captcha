// Package ratelimit provides fixed-window counters backed by the shared
// store, so limits hold across every instance of the service rather than per
// process.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"avestura.dev/persian-captcha/internal/store"
)

// Limiter counts events in fixed windows.
type Limiter struct {
	store store.Store
	now   func() time.Time
}

// New builds a limiter over a store.
func New(s store.Store) *Limiter {
	return &Limiter{store: s, now: time.Now}
}

// Result describes one limit check.
type Result struct {
	// Allowed is false once the limit is exceeded.
	Allowed bool
	// Count is the number of events seen in the current window, including
	// this one.
	Count int64
	// Limit is the configured cap.
	Limit int64
	// RetryAfter is how long until the window rolls over.
	RetryAfter time.Duration
}

// Allow records one event against a bucket and reports whether it fits.
//
// The window key includes the window index, so counters expire naturally and
// there is no sweep to run. A fixed window lets a burst straddle a boundary
// and briefly reach twice the limit; that is an acceptable trade for a
// counter that costs a single Redis round trip and needs no coordination.
func (l *Limiter) Allow(ctx context.Context, bucket string, limit int, window time.Duration) (Result, error) {
	if limit <= 0 {
		return Result{Allowed: true, Limit: 0}, nil
	}
	if window <= 0 {
		window = time.Minute
	}
	now := l.now()
	idx := now.UnixNano() / int64(window)
	key := fmt.Sprintf("rl:%s:%d", bucket, idx)

	// The counter is kept a little beyond the window so a request arriving at
	// the very end of it cannot be undercounted by early expiry.
	n, err := l.store.Incr(ctx, key, window+5*time.Second)
	if err != nil {
		return Result{}, err
	}
	windowEnd := time.Unix(0, (idx+1)*int64(window))
	return Result{
		Allowed:    n <= int64(limit),
		Count:      n,
		Limit:      int64(limit),
		RetryAfter: windowEnd.Sub(now),
	}, nil
}

// Peek reports the current count without recording an event.
func (l *Limiter) Peek(ctx context.Context, bucket string, window time.Duration) (int64, error) {
	if window <= 0 {
		window = time.Minute
	}
	idx := l.now().UnixNano() / int64(window)
	v, err := l.store.Get(ctx, fmt.Sprintf("rl:%s:%d", bucket, idx))
	if err != nil {
		if err == store.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
