package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// RedisOptions configures the Redis backend.
type RedisOptions struct {
	Addr     string
	Password string
	DB       int
	Prefix   string

	// PoolSize caps the number of pooled connections. Zero means 8.
	PoolSize int
	// DialTimeout and Timeout default to 3 and 2 seconds.
	DialTimeout time.Duration
	Timeout     time.Duration
}

// Redis is a Store backed by a Redis server, for deployments that run more
// than one instance of the service.
//
// It speaks RESP directly (see resp.go) and needs Redis 6.2 or newer for the
// GETDEL command, which makes token redemption atomic.
type Redis struct {
	opt   RedisOptions
	mu    sync.Mutex
	idle  []*respConn
	nOpen int
	dial  func() (net.Conn, error)
}

// NewRedis connects to Redis and verifies the connection with a PING.
func NewRedis(ctx context.Context, opt RedisOptions) (*Redis, error) {
	if opt.PoolSize <= 0 {
		opt.PoolSize = 8
	}
	if opt.DialTimeout <= 0 {
		opt.DialTimeout = 3 * time.Second
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 2 * time.Second
	}
	r := &Redis{opt: opt}
	r.dial = func() (net.Conn, error) {
		d := net.Dialer{Timeout: opt.DialTimeout}
		return d.Dial("tcp", opt.Addr)
	}
	if err := r.Ping(ctx); err != nil {
		return nil, fmt.Errorf("redis %s: %w", opt.Addr, err)
	}
	return r, nil
}

func (r *Redis) key(k string) string { return r.opt.Prefix + k }

// acquire returns a pooled connection, dialing and authenticating a new one
// when the pool is empty.
func (r *Redis) acquire() (*respConn, error) {
	r.mu.Lock()
	if n := len(r.idle); n > 0 {
		c := r.idle[n-1]
		r.idle = r.idle[:n-1]
		r.mu.Unlock()
		return c, nil
	}
	r.nOpen++
	r.mu.Unlock()

	conn, err := r.dial()
	if err != nil {
		r.mu.Lock()
		r.nOpen--
		r.mu.Unlock()
		return nil, err
	}
	c := newRESPConn(conn)
	if r.opt.Password != "" {
		if _, err := c.do(r.opt.Timeout, "AUTH", r.opt.Password); err != nil {
			c.close()
			r.discard()
			return nil, err
		}
	}
	if r.opt.DB != 0 {
		if _, err := c.do(r.opt.Timeout, "SELECT", r.opt.DB); err != nil {
			c.close()
			r.discard()
			return nil, err
		}
	}
	return c, nil
}

// release returns a healthy connection to the pool.
func (r *Redis) release(c *respConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.idle) >= r.opt.PoolSize {
		r.nOpen--
		c.close()
		return
	}
	r.idle = append(r.idle, c)
}

// drop closes a connection whose state is unknown after a protocol error.
func (r *Redis) drop(c *respConn) {
	c.close()
	r.discard()
}

func (r *Redis) discard() {
	r.mu.Lock()
	r.nOpen--
	r.mu.Unlock()
}

// command runs one command, returning the parsed reply.
func (r *Redis) command(ctx context.Context, args ...any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := r.acquire()
	if err != nil {
		return nil, err
	}
	timeout := r.opt.Timeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d < timeout {
			timeout = d
		}
	}
	reply, err := c.do(timeout, args...)
	switch {
	case err == nil, errors.Is(err, errNilReply):
		r.release(c)
		return reply, err
	default:
		var rerr respError
		if errors.As(err, &rerr) {
			// A server-side error leaves the connection usable.
			r.release(c)
			return nil, err
		}
		r.drop(c)
		return nil, err
	}
}

// Set implements Store.
func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	ms := ttl.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	_, err := r.command(ctx, "SET", r.key(key), value, "PX", ms)
	return err
}

// Get implements Store.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, error) {
	return r.bulk(r.command(ctx, "GET", r.key(key)))
}

// Take implements Store.
func (r *Redis) Take(ctx context.Context, key string) ([]byte, error) {
	return r.bulk(r.command(ctx, "GETDEL", r.key(key)))
}

func (r *Redis) bulk(reply any, err error) ([]byte, error) {
	switch {
	case errors.Is(err, errNilReply):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	b, ok := reply.([]byte)
	if !ok {
		if reply == nil {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("redis: expected bulk string, got %T", reply)
	}
	return b, nil
}

// Delete implements Store.
func (r *Redis) Delete(ctx context.Context, key string) error {
	_, err := r.command(ctx, "DEL", r.key(key))
	if errors.Is(err, errNilReply) {
		return nil
	}
	return err
}

// Incr implements Store. The expiry is set only when the counter is created,
// so the rate-limit window stays fixed rather than sliding forward with every
// request inside it.
func (r *Redis) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	reply, err := r.command(ctx, "INCR", r.key(key))
	if err != nil {
		return 0, err
	}
	n, ok := reply.(int64)
	if !ok {
		return 0, fmt.Errorf("redis: expected integer from INCR, got %T", reply)
	}
	if n == 1 {
		ms := ttl.Milliseconds()
		if ms <= 0 {
			ms = 1
		}
		if _, err := r.command(ctx, "PEXPIRE", r.key(key), ms); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Ping implements Store.
func (r *Redis) Ping(ctx context.Context) error {
	reply, err := r.command(ctx, "PING")
	if err != nil {
		return err
	}
	if s, ok := reply.(string); ok && s == "PONG" {
		return nil
	}
	return fmt.Errorf("redis: unexpected PING reply %v", reply)
}

// Close shuts every pooled connection.
func (r *Redis) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.idle {
		c.close()
	}
	r.idle = nil
	r.nOpen = 0
	return nil
}
