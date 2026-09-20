package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"avestura.dev/persian-captcha/internal/store"
)

func newLimiter(t *testing.T) (*Limiter, *time.Time) {
	t.Helper()
	backing := store.NewMemory()
	t.Cleanup(func() { backing.Close() })

	now := time.Now()
	l := New(backing)
	l.now = func() time.Time { return now }
	return l, &now
}

func TestAllowUpToTheLimit(t *testing.T) {
	ctx := context.Background()
	l, _ := newLimiter(t)

	for i := 1; i <= 3; i++ {
		res, err := l.Allow(ctx, "ip:1", 3, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Allowed {
			t.Fatalf("request %d was refused within the limit", i)
		}
		if res.Count != int64(i) {
			t.Errorf("Count = %d, want %d", res.Count, i)
		}
	}

	res, err := l.Allow(ctx, "ip:1", 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if res.Allowed {
		t.Error("the fourth request was allowed past a limit of three")
	}
	if res.RetryAfter <= 0 || res.RetryAfter > time.Minute {
		t.Errorf("RetryAfter = %v, want somewhere inside the window", res.RetryAfter)
	}
}

func TestBucketsAreIndependent(t *testing.T) {
	ctx := context.Background()
	l, _ := newLimiter(t)

	for range 3 {
		if _, err := l.Allow(ctx, "ip:1", 3, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	res, err := l.Allow(ctx, "ip:2", 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Allowed || res.Count != 1 {
		t.Errorf("one client's traffic affected another: %+v", res)
	}
}

func TestWindowRollsOver(t *testing.T) {
	ctx := context.Background()
	l, now := newLimiter(t)

	for range 3 {
		if _, err := l.Allow(ctx, "ip:1", 3, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if res, _ := l.Allow(ctx, "ip:1", 3, time.Minute); res.Allowed {
		t.Fatal("the limit was not reached")
	}

	*now = now.Add(2 * time.Minute)
	res, err := l.Allow(ctx, "ip:1", 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Allowed || res.Count != 1 {
		t.Errorf("the counter did not reset in the next window: %+v", res)
	}
}

// A limit of zero means unlimited, which is how an operator switches a
// particular check off.
func TestZeroLimitMeansUnlimited(t *testing.T) {
	ctx := context.Background()
	l, _ := newLimiter(t)

	for i := range 50 {
		res, err := l.Allow(ctx, "ip:1", 0, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Allowed {
			t.Fatalf("request %d was refused under a zero limit", i)
		}
	}
}

func TestPeekDoesNotCount(t *testing.T) {
	ctx := context.Background()
	l, _ := newLimiter(t)

	if n, err := l.Peek(ctx, "ip:1", time.Minute); err != nil || n != 0 {
		t.Fatalf("Peek on an empty bucket = %d, %v", n, err)
	}
	for range 2 {
		if _, err := l.Allow(ctx, "ip:1", 5, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	n, err := l.Peek(ctx, "ip:1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Peek = %d, want 2", n)
	}
	// Peeking twice must not have incremented anything.
	if again, _ := l.Peek(ctx, "ip:1", time.Minute); again != 2 {
		t.Errorf("Peek changed the count to %d", again)
	}
}

// The counter is shared state, so concurrent traffic must not lose
// increments and let more requests through than the limit allows.
func TestConcurrentTrafficRespectsTheLimit(t *testing.T) {
	ctx := context.Background()
	backing := store.NewMemory()
	defer backing.Close()
	l := New(backing)

	const limit = 20
	const callers = 200

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := l.Allow(ctx, "shared", limit, time.Minute)
			if err != nil {
				return
			}
			if res.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if allowed != limit {
		t.Errorf("%d of %d concurrent requests were allowed, want exactly %d", allowed, callers, limit)
	}
}
