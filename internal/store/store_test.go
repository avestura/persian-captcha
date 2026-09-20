package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	defer m.Close()

	if err := m.Set(ctx, "k", []byte("value"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "value" {
		t.Errorf("Get = %q, want %q", got, "value")
	}

	// The store must hand back a copy: a caller mutating what it read must
	// not corrupt what the next caller sees.
	got[0] = 'X'
	again, err := m.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != "value" {
		t.Errorf("stored value was mutated through a returned slice: %q", again)
	}
}

func TestMemoryMissingAndExpired(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	defer m.Close()

	if _, err := m.Get(ctx, "absent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on a missing key = %v, want ErrNotFound", err)
	}

	now := time.Now()
	m.now = func() time.Time { return now }
	if err := m.Set(ctx, "short", []byte("v"), time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := m.Get(ctx, "short"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on an expired key = %v, want ErrNotFound", err)
	}
}

// A verification token is single use. Two concurrent redemptions must not both
// succeed, or a captcha response could be spent twice.
func TestMemoryTakeIsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	defer m.Close()

	const racers = 32
	for round := range 50 {
		key := "token"
		if err := m.Set(ctx, key, []byte("once"), time.Minute); err != nil {
			t.Fatal(err)
		}

		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			successes int
		)
		start := make(chan struct{})
		for range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := m.Take(ctx, key); err == nil {
					mu.Lock()
					successes++
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()

		if successes != 1 {
			t.Fatalf("round %d: %d goroutines redeemed the same token, want exactly 1", round, successes)
		}
	}
}

// A fixed window must not slide: traffic arriving inside the window may not
// push its expiry further out, or a steady stream would never reset.
func TestMemoryIncrKeepsTheWindowFixed(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	defer m.Close()

	now := time.Now()
	m.now = func() time.Time { return now }

	for i := range 5 {
		n, err := m.Incr(ctx, "c", 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if want := int64(i + 1); n != want {
			t.Fatalf("Incr = %d, want %d", n, want)
		}
		now = now.Add(2 * time.Second)
	}

	// Ten seconds after the first increment the counter must be gone, even
	// though it was touched throughout.
	now = now.Add(time.Second)
	n, err := m.Incr(ctx, "c", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("counter did not reset at the window boundary: got %d, want 1", n)
	}
}

func TestMemoryDeleteAndLen(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	defer m.Close()

	for _, k := range []string{"a", "b", "c"} {
		if err := m.Set(ctx, k, []byte("v"), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}
	if err := m.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "not-there"); err != nil {
		t.Errorf("deleting a missing key should succeed, got %v", err)
	}
	if got := m.Len(); got != 2 {
		t.Errorf("Len after delete = %d, want 2", got)
	}
}

func TestCounterCodec(t *testing.T) {
	for _, n := range []int64{0, 1, 9, 10, 4095, 1 << 40} {
		if got := decodeCounter(encodeCounter(n)); got != n {
			t.Errorf("round trip of %d gave %d", n, got)
		}
	}
	if got := decodeCounter([]byte("not a number")); got != 0 {
		t.Errorf("decodeCounter on junk = %d, want 0", got)
	}
}

// Memory must satisfy the interface the rest of the service programs against.
var _ Store = (*Memory)(nil)
var _ Store = (*Redis)(nil)
