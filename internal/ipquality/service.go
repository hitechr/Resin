package ipquality

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/golang/groupcache/lru"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

const FreshFor = 24 * time.Hour
const MaxEntries = 10000

type Service struct {
	client  Lookup
	cache   *lru.Cache
	mu      sync.Mutex
	group   singleflight.Group
	limiter *rate.Limiter
	slots   chan struct{}
	now     func() time.Time
}

func NewService(client Lookup) *Service {
	return &Service{client: client, cache: lru.New(MaxEntries), limiter: rate.NewLimiter(rate.Every(time.Second), 1), slots: make(chan struct{}, 2), now: time.Now}
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

// Check is manual-only. Repeated concurrent checks of the same IP share one request.
func (s *Service) Check(ctx context.Context, raw string) (Result, error) {
	ip, err := PublicIP(raw)
	if err != nil {
		return Result{}, err
	}
	ch := s.group.DoChan(ip, func() (any, error) {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			return nil, ErrRateLimited
		}
		if !s.limiter.Allow() {
			return nil, ErrRateLimited
		}
		result, err := s.client.Lookup(ctx, ip)
		if err != nil {
			if errors.Is(err, ErrInvalidResponse) {
				return nil, ErrInvalidResponse
			}
			return nil, ErrProviderUnavailable
		}
		if result.IP != ip {
			return nil, ErrInvalidResponse
		}
		now := s.now().UTC()
		result.Source = Source
		result.ObservedAt = now
		result.ExpiresAt = now.Add(FreshFor)
		s.mu.Lock()
		s.cache.Add(ip, result)
		s.mu.Unlock()
		return result, nil
	})
	select {
	case <-ctx.Done():
		return Result{}, ErrProviderUnavailable
	case result := <-ch:
		if result.Err != nil {
			return Result{}, result.Err
		}
		return result.Val.(Result), nil
	}
}
