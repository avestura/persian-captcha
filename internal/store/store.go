// Package store holds the short-lived state the captcha service needs:
// pending challenge sessions, issued verification tokens and rate-limit
// counters.
//
// Everything stored here expires within minutes, so the interface is a small
// key/value contract with TTLs rather than anything resembling a database.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a key is absent or has expired.
var ErrNotFound = errors.New("store: not found")

// Store is the backend contract. Implementations must be safe for concurrent
// use by multiple goroutines.
type Store interface {
	// Set writes value under key with the given time to live.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Get reads a key, returning ErrNotFound when it is absent or expired.
	Get(ctx context.Context, key string) ([]byte, error)

	// Take atomically reads and deletes a key. It is how single-use
	// verification tokens are redeemed: two concurrent redemptions of the same
	// token must not both succeed.
	Take(ctx context.Context, key string) ([]byte, error)

	// Delete removes a key, whether or not it exists.
	Delete(ctx context.Context, key string) error

	// Incr increments a counter and returns its new value. The TTL is applied
	// only when the counter is created, so a fixed window is not extended by
	// traffic arriving inside it.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)

	// Ping checks that the backend is reachable.
	Ping(ctx context.Context) error

	// Close releases any resources held by the backend.
	Close() error
}
