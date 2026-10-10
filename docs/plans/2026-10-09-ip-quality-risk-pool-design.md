# IP Quality Risk Pool: Design Handoff

Date: 2026-10-09. Status: proposed design for a new implementation session; no product code changed here. Read `2026-10-09-optional-ip-quality-design.md` for the existing phase-1 contract.

## Confirmed scope

- One Resin instance. In the node-pool UI, show one row per normalized public egress IP instead of one row per node or a node-name column. Clicking an IP row opens a detail dialog containing all associated node information and the existing per-node operations; do not expand child rows inline. Retain access to nodes without a public egress IP via a clearly separate unresolved-IP group/dialog, never represent them as one shared IP.
- Display reputation score, residential flag, ASN, computed account-risk value, cache freshness and last observation on the IP row. Sort risk ascending/descending across all filtered groups on the server before pagination. Unknown risk goes last in either direction; stale is visibly distinct from fresh. Other flags, provider status, ISP and source remain accessible in the IP detail.
- A manual batch check acts on the complete current filtered result, not just the visible page. Snapshot the filtered set, deduplicate public IPs, skip fresh cache entries by default, show estimated work/progress and allow cancellation.
- At local midnight each day scan the pool for missing/expired quality. After a successful automatic subscription refresh, enqueue known public IPs with no fresh result. Newly added nodes with no IP are skipped until their first successful egress probe publishes a public IP, then queued. Neither merely viewing the list nor a failed subscription refresh triggers external work. All three sources and manual checks share a bounded budget; no automatic routing or health mutation.

## Evidence and boundaries

- `internal/api/handler_node.go` sorts `ListNodes` before `PaginateSlice`; existing `/api/v1/nodes` returns node-hash-keyed responses used by tests and other consumers. Preserve it and add new IP-group endpoints for the UI.
- `internal/service/control_plane_nodes.go` returns one `NodeSummary` per node; filters are applied before list sorting. Group AFTER applying the same node filters. A tag/subscription/platform filter limits the group's displayed members to matched nodes, with a clearly labelled matched-node count.
- `internal/ipquality` currently caches by public IP for 24h in a 10,000-entry in-memory LRU. `Check` rejects excess traffic immediately; it is not a batch worker. Budget: at most two in-flight, one new provider request per second per process, 12s timeout. More than 1,000 *distinct* IPs needs at least ~17 minutes even without provider latency. Cache and in-memory jobs are lost on restart.
- `internal/topology/subscription_scheduler.go` invokes `onSubUpdated` on both success and failure. `cmd/resin/main.go` queues an immediate egress probe for a newly added node; `internal/topology/pool.go:UpdateNodeEgressIP` is called later and also fires a generic dynamic callback. Do not block scheduler/pool callbacks or replace existing persistence callbacks; use a successful-apply signal and an IP-change-specific signal, or a narrowly scoped bridge that preserves existing callbacks.
- `One IP / Net.Coffee` supplies `score` (higher = better); the good/moderate/poor status is derived from that score, not independent evidence. Null score and null flags remain unknown. ASN identifies a network; ASN value alone is not a risk verdict. One IP code is AGPL-3.0; do not copy its implementation into MIT Resin. Public endpoint bulk-use terms, current response shape and availability have NOT been reverified; explicit operator authorization for automated lookups is required.

## Recommended architecture and alternatives

1. **Recommended:** keep the existing node API; add an admin-only cache-enriched IP-group list/detail service and a bounded single-instance coordinator. A background worker consumes deduplicated IP work from midnight, successful subscription refresh, first IP observation and manual filtered-batch tasks. Give interactive manual checks priority over queued background work, share rate/concurrency limits and singleflight, use backoff for 429/outages, and retain successful cache entries. Expose job progress without unbounded per-IP history. This preserves node actions and existing API consumers.
2. Grouping and sorting only in the browser is smaller but wrong across pages and cannot safely drive a large batch. Do not use it.
3. Persisting quality and jobs in SQLite can survive restart and support multiple instances, but introduces migrations and consistency ownership not requested for the confirmed single-instance setup. Defer it.

## Risk contract (owner approval required before implementation)

- Propose a versioned, server-owned `account_risk` in [0,100], higher = more risk: when score is known, `base = 100 - score`, `risk = max(base, floor of each explicitly true high-risk flag)`, clamped to [0,100]. Status is not added again. False/null flags do not lower risk. Residential true does not grant a discount. ASN/ISP are display-only until validated policy exists. Preserve decimals until display formatting; store `risk_version` with each response, and recompute from cached quality after a version change.
- Candidate floors for review, NOT approved policy: Tor 90, abuser 85, proxy 70, VPN 60, datacenter 45. These are product assumptions, not proven GitHub/One IP coefficients. Obtain representative labelled account-restriction samples or owner sign-off before fixing values in tests or production. With `score:null`, return `risk:null` and show known high-risk evidence separately; do not fabricate a precise number. A stale result may show its last calculated value marked stale, never as a current guarantee.
- Risk is third-party egress reputation and account-risk proxy, not a measured probability of bans, node reachability, or permission to auto-exclude a route. No routing changes.

## Data/API/UI behavior

- Add `GET /api/v1/node-ip-groups` with existing node filters, sorting and pagination; return `total` as IP-group count, plus matched-node counts and cache-only score/risk/flags/ASN/freshness. New `sort_by=risk` handles null-last in both directions, breaks ties by normalized IP. Include an unresolved-IP bucket at the end (non-public/absent IP, no risk), never call the provider for it. If filtering removes a member, it disappears from that filtered group's detail.
- Add `GET /api/v1/node-ip-groups/{ip}` for a cache-only group detail with paginated member nodes; validate/normalize the IP and ensure it belongs to the filtered pool. Provide an explicit unresolved-group route. Preserve `GET /api/v1/nodes/{hash}` and node-specific probe/check actions. POST manual filtered-batch start, GET job status and POST cancel are admin-only. Neither GET list nor GET detail contacts the provider. Never accept an arbitrary target IP or provider URL from clients.
- Replace the node table with a dense IP table; row click opens a modal listing associated nodes and their per-node actions alongside the IP quality details. Keep filters, scanning, pagination and responsive layout. Show one clear bulk command, a cancel control and compact progress; no marketing copy or node names in top-level columns. Unknown, stale, failed, disabled and no-IP states remain distinguishable. No implicit background refetch of third-party data.

## Automatic-work semantics

- Scan only when the feature is explicitly enabled and an approved provider is configured. Derive the daily schedule from server `time.Local` (typically container `TZ`), with a testable clock; avoid overlapping midnight runs. Treat subscription callback failures as non-events. For newly observed or changed IPs, enqueue that IP only if public and not fresh. Deduplicate queued/in-flight work and refresh against current pool membership before starting each lookup; a node's changed IP must not receive a prior IP's result.
- Bound queued distinct IPs and active job metadata, expose dropped/deferred counts, and rescan later instead of starting an unbounded goroutine per IP. Provider failures leave stale cache intact, use bounded exponential backoff and a shared provider budget. On stop, cancel/drain cleanly; on restart, in-memory progress is lost and the next refresh/midnight pass can re-discover work. Ensure manual actions remain responsive during a long background scan. Never hold topology locks during network I/O.

## Decisions to close before code

1. Approve or change the five risk floors and the `score:null -> risk:null` rule, including what evidence will calibrate them. No externally sourced algorithm has been verified; do not claim GitHub coefficients are evidence.
2. Confirm that midnight uses container-local timezone (`TZ`) and the desired behavior across DST/restart; proposed behavior is one scan per local calendar day, no missed-day catch-up storm.
3. Confirm operator permission to send the full distinct-IP inventory to the configured HTTPS provider, especially if using the public endpoint. The current phase-1 opt-in alone is not evidence of bulk-use authorization. Make automatic scanning a separately enabled startup setting until confirmed.
4. Decide maximum distinct IPs per manual filtered batch and queue bound after measuring actual distinct-IP count (not node count); propose 1,000 per job and 10,000 queued, with an explicit refusal/progress message rather than silently truncating.

No changes to node routing, existing node endpoint contracts, persistence schema or third-party license boundary are part of this feature.
