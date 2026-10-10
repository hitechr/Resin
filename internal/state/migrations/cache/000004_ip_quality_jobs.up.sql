CREATE TABLE IF NOT EXISTS ip_quality_jobs (
    id TEXT PRIMARY KEY,
    payload BLOB NOT NULL,
    started_at_ns INTEGER NOT NULL
);
