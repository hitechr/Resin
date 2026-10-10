# IP Quality Cache Durability: Design

Date: 2026-10-10. Status: proposed design for a new implementation session on a fresh branch (suggested: `feature/ip-quality-cache-durability`). Builds on the merged IP quality risk pool (master `9cc8d17`). No product code is changed by this document.

## Current state and evidence

- `internal/ipquality/service.go`: bounded 10,000-entry in-memory LRU keyed by normalized public IP. `Cached()` reads the LRU only; `lookup()` writes through to the store on every success via `persistQuality`.
- `internal/state/repo_ipquality.go` + cache migration `000003_ip_quality`: `ip_quality_cache (ip PRIMARY KEY, payload BLOB, expires_at_ns INTEGER)` in cache.db. On startup `AttachStore` garbage-collects expired rows and restores still-fresh rows into the LRU.
- `internal/ipquality/coordinator.go`: batch job records (≤128) live only in memory; a restart loses them and the UI falls back to the "job status was lost" copy.

Remaining gaps this design closes:

1. **No read-through.** Once the LRU evicts a still-fresh entry at runtime, `Cached()` cannot see it until the next restart, although the row is present in cache.db.
2. **Job records are volatile.** Terminal and in-flight job state disappears on restart.
3. **Retention is fresh-window only.** DB rows are deleted once expired (boot GC); "stale but visible" exists only inside the LRU lifetime.

## Scope

1. **Read-through cache.** On an LRU miss in `Cached()`, read the row from the store (single-row SELECT by primary key), validate it (IP matches key, payload parses, public IP), insert it into the LRU, and return it with freshness computed exactly as today. Read paths never contact the provider. Negative results are not cached; a miss on an unknown IP costs one local SQLite SELECT.
2. **Job record persistence (separable phase 2).** Persist the bounded job set write-through into a new `ip_quality_jobs` table in cache.db. On restore, terminal jobs reappear as-is; jobs that were still running are marked canceled with their remaining work counted as deferred. Job IDs stay UUIDs. The 128-record bound stays; the table is truncated to the newest 128 on restore.
3. **Retention stays fresh-window.** Rejected alternative: keeping stale rows in the DB for display. It would diverge from the LRU capacity bound and revive the stale-data-guarantee question this feature explicitly avoids. Stale display remains an LRU-only state.

## Contract invariants

- No provider request can originate from any read path (`Cached`, list/detail APIs, UI polling).
- All lookups share the single coordinator budget; no new budget or worker pool.
- Write-through failures never fail a lookup; they are logged and retried on the next success.
- cache.db stays weak-persist: no cross-database foreign keys; `RepairConsistency` does not touch these tables; rows are disposable.
- `Store` grows a single-row read (`ReadQuality(ip) ([]byte, bool, error)`); SQLite access stays concurrency-safe via the existing WAL/busy-timeout pragmas.

## Acceptance

- Evict a still-fresh entry from the LRU at runtime → `Cached()` returns it via read-through with zero provider calls.
- Restart mid-batch → fresh rows visible immediately; expired rows garbage-collected; restored job list shows terminal jobs and marks interrupted jobs canceled with deferred counts.
- Concurrent `Cached()` + successful lookups remain race-free; a read-through populate never duplicates or overwrites a fresher concurrent write.
- CI matrix (now including `internal/state`) stays green; no routing or node-health behavior changes.

## Out of scope

Multi-instance cache sharing, provider aggregation or credentials, changing risk floors or `risk_version`, stale-row retention beyond the LRU, and any UI change beyond existing job-lost copy. The trimmed node-row enrichment from the superseded `2026-10-09-node-ip-quality-batch` plan (per-node `ip_quality` on `NodeSummary`, node-level `sort_by=risk`, drawer formula text) is a separate future plan if requested.
