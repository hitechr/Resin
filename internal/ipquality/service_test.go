package ipquality

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

type lookupFunc func(context.Context, string) (Result, error)

func (f lookupFunc) Lookup(ctx context.Context, ip string) (Result, error) { return f(ctx, ip) }

func TestServiceFreshStaleAndFailureKeepsCache(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	fail := false
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		if fail {
			return Result{}, errors.New("offline")
		}
		return Result{IP: ip, Status: "unknown"}, nil
	}))
	s.now = func() time.Time { return now }
	if _, ok, _ := s.Cached("8.8.8.8"); ok {
		t.Fatal("uncached IP")
	}
	if _, err := s.Check(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	if _, ok, fresh := s.Cached("8.8.8.8"); !ok || !fresh {
		t.Fatal("expected fresh")
	}
	now = now.Add(24 * time.Hour)
	if _, ok, fresh := s.Cached("8.8.8.8"); !ok || fresh {
		t.Fatal("expected stale at 24h")
	}
	fail = true
	s.limiter = rate.NewLimiter(1000, 1)
	if _, err := s.Check(context.Background(), "8.8.8.8"); err != ErrProviderUnavailable {
		t.Fatalf("error: %v", err)
	}
	if _, ok, fresh := s.Cached("8.8.8.8"); !ok || fresh {
		t.Fatal("failure replaced stale entry")
	}
}

func TestServiceLRUBoundAndRateLimit(t *testing.T) {
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) { return Result{IP: ip}, nil }))
	for i := 1; i <= MaxEntries+1; i++ {
		ip := fmt.Sprintf("8.%d.%d.%d", i/65536, i/256%256, i%256)
		s.cache.Add(ip, Result{IP: ip})
	}
	if s.cache.Len() != MaxEntries {
		t.Fatalf("cache size: %d", s.cache.Len())
	}
	if _, ok, _ := s.Cached("8.0.0.1"); ok {
		t.Fatal("oldest entry should be evicted")
	}
	if _, err := s.Check(context.Background(), "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(context.Background(), "8.8.8.8"); err != ErrRateLimited {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestServiceConcurrencyAndCancellation(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		started <- ip
		select {
		case <-release:
			return Result{IP: ip}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	s.limiter = rate.NewLimiter(1000, 2) // Isolate concurrency from the per-second budget.
	results := make(chan error, 2)
	for _, ip := range []string{"8.8.8.8", "1.1.1.1"} {
		go func(ip string) { _, err := s.Check(context.Background(), ip); results <- err }(ip)
		<-started
	}
	if _, err := s.Check(context.Background(), "9.9.9.9"); err != ErrRateLimited {
		t.Fatalf("third request: %v", err)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Check(ctx, "4.4.4.4"); err != ErrProviderUnavailable {
		t.Fatalf("cancelled request: %v", err)
	}
}

func TestServiceCoalescesSameIP(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		calls.Add(1)
		close(started)
		<-release
		return Result{IP: ip}, nil
	}))
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			if _, err := s.Check(context.Background(), "8.8.8.8"); err != nil {
				t.Error(err)
			}
		}()
	}
	<-started
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls: %d", calls.Load())
	}
}
