package state

import (
	"database/sql"
	"time"
)

// IPQualityRepo persists the third-party IP quality cache in cache.db.
// Rows are written through on every successful provider lookup and restored
// into the in-memory LRU at startup; expired rows are garbage-collected.
type IPQualityRepo struct {
	db *sql.DB
}

func newIPQualityRepo(db *sql.DB) *IPQualityRepo {
	return &IPQualityRepo{db: db}
}

// SaveQuality upserts one quality result keyed by its normalized public IP.
func (r *IPQualityRepo) SaveQuality(ip string, payload []byte, expiresAt time.Time) error {
	_, err := r.db.Exec(
		"INSERT INTO ip_quality_cache (ip, payload, expires_at_ns) VALUES (?, ?, ?) "+
			"ON CONFLICT(ip) DO UPDATE SET payload = excluded.payload, expires_at_ns = excluded.expires_at_ns",
		ip, payload, expiresAt.UnixNano(),
	)
	return err
}

// LoadQuality returns all persisted payloads keyed by IP.
func (r *IPQualityRepo) LoadQuality() (map[string][]byte, error) {
	rows, err := r.db.Query("SELECT ip, payload FROM ip_quality_cache")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]byte)
	for rows.Next() {
		var ip string
		var payload []byte
		if err := rows.Scan(&ip, &payload); err != nil {
			return nil, err
		}
		result[ip] = payload
	}
	return result, rows.Err()
}

// DeleteExpiredIPQuality removes rows whose freshness window has lapsed.
func (r *IPQualityRepo) DeleteExpiredIPQuality(nowNs int64) error {
	_, err := r.db.Exec("DELETE FROM ip_quality_cache WHERE expires_at_ns < ?", nowNs)
	return err
}
