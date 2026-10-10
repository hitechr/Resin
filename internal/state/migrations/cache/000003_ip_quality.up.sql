CREATE TABLE IF NOT EXISTS ip_quality_cache (
	ip             TEXT PRIMARY KEY,
	payload        BLOB NOT NULL,
	expires_at_ns  INTEGER NOT NULL
);
