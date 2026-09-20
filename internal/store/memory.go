package store

import (
	"context"
	"sync"
	"time"
)

type memEntry struct {
	value   []byte
	expires time.Time
}

// Memory is an in-process Store. It is the default backend and is the right
// choice for a single instance; horizontal deployments want Redis so that a
// session started on one node can be solved on another.
type Memory struct {
	mu     sync.Mutex
	items  map[string]memEntry
	now    func() time.Time // injectable for tests
	stop   chan struct{}
	closed bool
}

// NewMemory returns a Memory store that sweeps expired keys in the background.
func NewMemory() *Memory {
	m := &Memory{
		items: make(map[string]memEntry),
		now:   time.Now,
		stop:  make(chan struct{}),
	}
	go m.sweep(30 * time.Second)
	return m
}

func (m *Memory) sweep(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.mu.Lock()
			now := m.now()
			for k, e := range m.items {
				if now.After(e.expires) {
					delete(m.items, k)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Set implements Store.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	cp := make([]byte, len(value))
	copy(cp, value)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = memEntry{value: cp, expires: m.now().Add(ttl)}
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.get(key)
}

// Take implements Store.
func (m *Memory) Take(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.get(key)
	if err != nil {
		return nil, err
	}
	delete(m.items, key)
	return v, nil
}

// get requires m.mu to be held.
func (m *Memory) get(key string) ([]byte, error) {
	e, ok := m.items[key]
	if !ok {
		return nil, ErrNotFound
	}
	if m.now().After(e.expires) {
		delete(m.items, key)
		return nil, ErrNotFound
	}
	cp := make([]byte, len(e.value))
	copy(cp, e.value)
	return cp, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}

// Incr implements Store.
func (m *Memory) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	e, ok := m.items[key]
	if !ok || now.After(e.expires) {
		m.items[key] = memEntry{value: []byte("1"), expires: now.Add(ttl)}
		return 1, nil
	}
	n := decodeCounter(e.value) + 1
	// The expiry is left untouched so the window stays fixed.
	m.items[key] = memEntry{value: encodeCounter(n), expires: e.expires}
	return n, nil
}

// Ping implements Store.
func (m *Memory) Ping(context.Context) error { return nil }

// Close implements Store.
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.stop)
	}
	return nil
}

// Len reports how many live keys the store holds. Used by tests and /healthz.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	n := 0
	for _, e := range m.items {
		if !now.After(e.expires) {
			n++
		}
	}
	return n
}

func decodeCounter(b []byte) int64 {
	var n int64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func encodeCounter(n int64) []byte {
	if n == 0 {
		return []byte("0")
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return append([]byte(nil), buf[i:]...)
}
