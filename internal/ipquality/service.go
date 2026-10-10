package ipquality

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/golang/groupcache/lru"
	"golang.org/x/time/rate"
)

const FreshFor = 24 * time.Hour
const MaxEntries = 10000

type lookupFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  Result
	err     error
}

type Service struct {
	client    Lookup
	store     Store
	cache     *lru.Cache
	mu        sync.Mutex
	flightsMu sync.Mutex
	flights   map[string]*lookupFlight
	limiter   *rate.Limiter
	slots     chan struct{}
	now       func() time.Time
}

func NewService(client Lookup) *Service {
	return &Service{client: client, cache: lru.New(MaxEntries), flights: make(map[string]*lookupFlight), limiter: rate.NewLimiter(rate.Every(time.Second), 1), slots: make(chan struct{}, 2), now: time.Now}
}

// Cached never contacts the provider. A stale value remains available until LRU eviction.
func (s *Service) Cached(raw string) (Result, bool, bool) {
	ip, err := PublicIP(raw)
	if err != nil {
		return Result{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.cache.Get(ip)
	if !ok {
		return Result{}, false, false
	}
	result := value.(Result)
	return result, true, s.now().Before(result.ExpiresAt)
}

// Check retains the immediate admission behavior of the existing node action.
func (s *Service) Check(ctx context.Context, raw string) (Result, error) {
	return s.check(ctx, raw, false, nil)
}

// CheckQueued waits for the same provider budget used by interactive checks.
func (s *Service) CheckQueued(ctx context.Context, raw string) (Result, error) {
	return s.check(ctx, raw, true, nil)
}

// CheckQueuedWhen revalidates admission after the shared rate wait, just before HTTP.
func (s *Service) CheckQueuedWhen(ctx context.Context, raw string, canStart func() bool) (Result, error) {
	return s.check(ctx, raw, true, canStart)
}

func (s *Service) check(ctx context.Context, raw string, queued bool, canStart func() bool) (Result, error) {
	ip, err := PublicIP(raw)
	if err != nil {
		return Result{}, err
	}
	if ctx.Err() != nil {
		return Result{}, ErrProviderUnavailable
	}
	s.flightsMu.Lock()
	flight := s.flights[ip]
	if flight == nil {
		requestCtx, cancel := context.WithCancel(context.Background())
		flight = &lookupFlight{done: make(chan struct{}), cancel: cancel}
		s.flights[ip] = flight
		go s.runFlight(requestCtx, ip, flight, queued, canStart)
	}
	flight.waiters++
	s.flightsMu.Unlock()

	select {
	case <-ctx.Done():
		s.leaveFlight(ip, flight)
		return Result{}, ErrProviderUnavailable
	case <-flight.done:
		s.leaveFlight(ip, flight)
		return flight.result, flight.err
	}
}

func (s *Service) leaveFlight(ip string, flight *lookupFlight) {
	s.flightsMu.Lock()
	defer s.flightsMu.Unlock()
	flight.waiters--
	if flight.waiters == 0 {
		flight.cancel()
		if s.flights[ip] == flight {
			delete(s.flights, ip)
		}
	}
}

func (s *Service) runFlight(ctx context.Context, ip string, flight *lookupFlight, queued bool, canStart func() bool) {
	result, err := s.lookup(ctx, ip, queued, canStart)
	s.flightsMu.Lock()
	flight.result, flight.err = result, err
	close(flight.done)
	if s.flights[ip] == flight {
		delete(s.flights, ip)
	}
	s.flightsMu.Unlock()
	flight.cancel()
}

func (s *Service) lookup(ctx context.Context, ip string, queued bool, canStart func() bool) (Result, error) {
	if queued {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			return Result{}, ErrProviderUnavailable
		}
		if err := s.limiter.Wait(ctx); err != nil {
			return Result{}, ErrProviderUnavailable
		}
	} else {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			return Result{}, ErrRateLimited
		}
		if !s.limiter.Allow() {
			return Result{}, ErrRateLimited
		}
	}
	if ctx.Err() != nil {
		return Result{}, ErrProviderUnavailable
	}
	if canStart != nil && !canStart() {
		return Result{}, ErrNoLongerEligible
	}
	if ctx.Err() != nil {
		return Result{}, ErrProviderUnavailable
	}
	result, err := s.client.Lookup(ctx, ip)
	if err != nil {
		if errors.Is(err, ErrInvalidResponse) {
			return Result{}, ErrInvalidResponse
		}
		return Result{}, ErrProviderUnavailable
	}
	if ctx.Err() != nil {
		return Result{}, ErrProviderUnavailable
	}
	if result.IP != ip {
		return Result{}, ErrInvalidResponse
	}
	now := s.now().UTC()
	result.Source = Source
	result.ObservedAt = now
	result.ExpiresAt = now.Add(FreshFor)
	s.mu.Lock()
	s.cache.Add(ip, result)
	s.mu.Unlock()
	s.persistQuality(result)
	return result, nil
}
