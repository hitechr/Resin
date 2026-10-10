package ipquality

import (
	"context"
	"encoding/json"
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

func TestServiceCancelOneWaiterKeepsSharedLookup(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return Result{IP: ip}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := s.Check(ctx, "8.8.8.8"); first <- err }()
	<-started
	second := make(chan error, 1)
	go func() { _, err := s.Check(context.Background(), "8.8.8.8"); second <- err }()
	// Ensure the second caller is registered before canceling the first.
	for {
		s.flightsMu.Lock()
		joined := s.flights["8.8.8.8"].waiters == 2
		s.flightsMu.Unlock()
		if joined {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-first; err != ErrProviderUnavailable {
		t.Fatalf("first canceled waiter: %v", err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("unrelated waiter: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider requests: %d", calls.Load())
	}
}

func TestServiceCancelLastWaiterStopsProvider(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return Result{}, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := s.Check(ctx, "8.8.8.8"); finished <- err }()
	<-started
	cancel()
	if err := <-finished; err != ErrProviderUnavailable {
		t.Fatalf("canceled waiter: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("provider request was not canceled")
	}
}

type mapStore map[string][]byte

func (m mapStore) SaveQuality(ip string, payload []byte, _ time.Time) error {
	m[ip] = payload
	return nil
}

func (m mapStore) LoadQuality() (map[string][]byte, error) { return m, nil }

func (m mapStore) ReadQuality(ip string) ([]byte, bool, error) {
	payload, ok := m[ip]
	return payload, ok, nil
}

func TestServicePersistFailureDoesNotFailLookup(t *testing.T) {
	store := &failingQualityStore{mapStore: mapStore{}, fail: true}
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		return Result{IP: ip}, nil
	}))
	if err := s.AttachStore(store); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(context.Background(), "8.8.8.8"); err != nil {
		t.Fatalf("write failure failed lookup: %v", err)
	}
	if _, found, _ := s.Cached("8.8.8.8"); !found {
		t.Fatal("write failure removed in-memory value")
	}
	store.fail = false
	s.limiter = rate.NewLimiter(1000, 1)
	if _, err := s.Check(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	if len(store.mapStore) != 1 {
		t.Fatal("next successful check did not retry persistence")
	}
}

type failingQualityStore struct {
	mapStore
	fail bool
}

func (s *failingQualityStore) SaveQuality(ip string, payload []byte, expiresAt time.Time) error {
	if s.fail {
		return errors.New("disk unavailable")
	}
	return s.mapStore.SaveQuality(ip, payload, expiresAt)
}

func TestServiceReadThroughAfterEviction(t *testing.T) {
	now := time.Now()
	store := mapStore{}
	var calls atomic.Int32
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		calls.Add(1)
		return Result{IP: ip}, nil
	}))
	s.now = func() time.Time { return now }
	if err := s.AttachStore(store); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= MaxEntries; i++ {
		s.cache.Add(fmt.Sprintf("9.%d.%d.%d", i/65536, i/256%256, i%256), Result{})
	}
	result, found, fresh := s.Cached("8.8.8.8")
	if !found || !fresh || result.IP != "8.8.8.8" || calls.Load() != 1 {
		t.Fatalf("read-through: %+v found=%v fresh=%v calls=%d", result, found, fresh, calls.Load())
	}
	if s.cache.Len() != MaxEntries {
		t.Fatalf("LRU size: %d", s.cache.Len())
	}
	now = now.Add(FreshFor)
	s.cache.Remove("8.8.8.8")
	if _, found, _ := s.Cached("8.8.8.8"); found {
		t.Fatal("expired disk row was revived")
	}
}

func TestServiceReadThroughRejectsInvalidRows(t *testing.T) {
	for _, payload := range [][]byte{[]byte("bad"), []byte(`{"ip":"1.1.1.1"}`), []byte(`{"ip":"8.8.8.8","expires_at":"2000-01-01T00:00:00Z"}`)} {
		store := mapStore{"8.8.8.8": payload}
		s := NewService(nil)
		if err := s.AttachStore(store); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := s.Cached("8.8.8.8"); found {
			t.Fatalf("invalid row accepted: %s", payload)
		}
	}
}

type blockedReadStore struct {
	mapStore
	entered chan struct{}
	release chan struct{}
}

func (b *blockedReadStore) ReadQuality(ip string) ([]byte, bool, error) {
	close(b.entered)
	<-b.release
	return b.mapStore.ReadQuality(ip)
}

func TestServiceReadThroughDoesNotOverwriteLookup(t *testing.T) {
	store := &blockedReadStore{mapStore: mapStore{}, entered: make(chan struct{}), release: make(chan struct{})}
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		score := 99.0
		return Result{IP: ip, Score: &score}, nil
	}))
	if err := s.AttachStore(store); err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(Result{IP: "8.8.8.8", ExpiresAt: time.Now().Add(time.Hour)})
	store.mapStore["8.8.8.8"] = old
	read := make(chan struct{})
	go func() { s.Cached("8.8.8.8"); close(read) }()
	<-store.entered
	check := make(chan error, 1)
	go func() { _, err := s.Check(context.Background(), "8.8.8.8"); check <- err }()
	close(store.release)
	<-read
	if err := <-check; err != nil {
		t.Fatal(err)
	}
	result, found, _ := s.Cached("8.8.8.8")
	if !found || result.Score == nil || *result.Score != 99 {
		t.Fatalf("newer lookup lost: %+v", result)
	}
}

func TestServicePersistsAndRestoresCacheThroughStore(t *testing.T) {
	store := mapStore{}
	score := 42.0
	first := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		return Result{IP: ip, Score: &score}, nil
	}))
	if err := first.AttachStore(store); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Check(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	if len(store) != 1 {
		t.Fatalf("persisted rows: %d", len(store))
	}

	// A restarted process restores the persisted rows into its LRU.
	second := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		return Result{}, errors.New("provider must not be called")
	}))
	if err := second.AttachStore(store); err != nil {
		t.Fatal(err)
	}
	result, ok, fresh := second.Cached("8.8.8.8")
	if !ok || !fresh || result.Score == nil || *result.Score != 42 {
		t.Fatalf("restored entry: %+v ok=%v fresh=%v", result, ok, fresh)
	}
}

func TestServiceRestoreSkipsExpiredEntries(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	payload, err := json.Marshal(Result{IP: "8.8.8.8", ExpiresAt: expired})
	if err != nil {
		t.Fatal(err)
	}
	store := mapStore{"8.8.8.8": payload}
	s := NewService(nil)
	if err := s.AttachStore(store); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Cached("8.8.8.8"); ok {
		t.Fatal("expired persisted entry must not be restored")
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
