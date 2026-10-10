package ipquality

import (
	"encoding/json"
	"log"
	"time"
)

// Store persists quality results across restarts. Implementations must be
// safe for concurrent use; SaveQuality is best-effort write-through.
type Store interface {
	SaveQuality(ip string, payload []byte, expiresAt time.Time) error
	ReadQuality(ip string) ([]byte, bool, error)
	LoadQuality() (map[string][]byte, error)
}

func (s *Service) decodeFresh(ip string, payload []byte) (Result, bool) {
	var result Result
	if json.Unmarshal(payload, &result) != nil || result.IP != ip {
		return Result{}, false
	}
	if normalized, err := PublicIP(ip); err != nil || normalized != ip {
		return Result{}, false
	}
	return result, s.now().Before(result.ExpiresAt)
}

// AttachStore restores previously persisted results into the in-memory cache
// and enables write-through persistence for later successful lookups.
// Only entries that are still fresh are restored; call any garbage collection
// on the store before attaching. Attach before background workers start.
func (s *Service) AttachStore(store Store) error {
	if store == nil {
		return nil
	}
	rows, err := store.LoadQuality()
	if err != nil {
		return err
	}
	s.mu.Lock()
	for ip, payload := range rows {
		result, fresh := s.decodeFresh(ip, payload)
		if !fresh {
			continue
		}
		s.cache.Add(ip, result)
	}
	s.mu.Unlock()
	s.store = store
	return nil
}

// persistQuality is a best-effort write-through; a failed write never fails
// the provider lookup itself and is retried on the next successful check.
func (s *Service) persistQuality(result Result) {
	if s.store == nil {
		return
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return
	}
	if err := s.store.SaveQuality(result.IP, payload, result.ExpiresAt); err != nil {
		log.Printf("[ipquality] persist cache entry %s: %v", result.IP, err)
	}
}
