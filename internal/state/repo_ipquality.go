package state

import (
	"database/sql"
	"errors"
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

// ReadQuality returns one persisted payload without contacting the provider.
func (r *IPQualityRepo) ReadQuality(ip string) ([]byte, bool, error) {
	var payload []byte
	err := r.db.QueryRow("SELECT payload FROM ip_quality_cache WHERE ip = ?", ip).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return payload, err == nil, err
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

// SaveJob writes a snapshot and keeps only the newest 128 job records.
func (r *IPQualityRepo) SaveJob(id string, payload []byte, startedAt time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO ip_quality_jobs (id, payload, started_at_ns) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, started_at_ns = excluded.started_at_ns", id, payload, startedAt.UnixNano()); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM ip_quality_jobs WHERE id NOT IN (SELECT id FROM ip_quality_jobs ORDER BY started_at_ns DESC, id DESC LIMIT 128)"); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *IPQualityRepo) DeleteJob(id string) error {
	_, err := r.db.Exec("DELETE FROM ip_quality_jobs WHERE id = ?", id)
	return err
}

func (r *IPQualityRepo) LoadJobs() ([][]byte, error) {
	rows, err := r.db.Query("SELECT payload FROM ip_quality_jobs ORDER BY started_at_ns, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs [][]byte
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		jobs = append(jobs, payload)
	}
	return jobs, rows.Err()
}

// DeleteExpiredIPQuality removes rows whose freshness window has lapsed.
func (r *IPQualityRepo) DeleteExpiredIPQuality(nowNs int64) error {
	_, err := r.db.Exec("DELETE FROM ip_quality_cache WHERE expires_at_ns < ?", nowNs)
	return err
}
