# IP Quality Risk Pool Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Read `docs/plans/2026-10-09-ip-quality-risk-pool-design.md` and the existing `2026-10-09-optional-ip-quality-design.md` first. This document is a new-session handoff, not permission to commit, push, deploy, run local compilation or use a public provider for bulk requests.

**Goal:** Present a cache-only, risk-sortable IP-deduplicated node pool with per-IP node details, a filtered manual batch and bounded midnight/subscription/first-egress background quality checks.

**Architecture:** Preserve the node-hash API and routing model. Add a separate IP-group projection over the filtered node pool, plus a versioned account-risk calculation over the existing IP-keyed quality cache. A single-instance bounded coordinator owns manual/automatic lookup admission, deduplication, cancellation and progress while sharing the existing provider rate budget. No persistence migration or automatic routing policy.

**Tech Stack:** Go `net/netip`, `net/http`, existing `golang.org/x/sync/singleflight`, `golang.org/x/time/rate`, groupcache LRU, existing subscription/probe callbacks, React Query and the node table/dialog components.

---

## Preflight (blocking decisions and verification policy)

1. Confirm the open decisions in the design: risk floors and `score:null` rule; midnight's container-local timezone/DST policy; operator-approved provider terms for automated inventory checks; manual-batch/queue caps. Record actual distinct-IP count and provider budget. Do not bake illustrative floor values into production before owner approval.
2. Confirm enabled mode: keep `RESIN_IP_QUALITY_ENABLED` as the prerequisite; add a separately opt-in automatic mode defaulting to off until bulk disclosure is approved. Check One IP HTTP response schema and license from its upstream repository before relying on the public endpoint; AGPL source cannot be copied into Resin.
3. Inspect `git status`, the two design docs, `internal/ipquality/{client,service}.go`, `internal/topology/{subscription_scheduler,pool}.go`, `cmd/resin/{main,app_runtime}.go`, `internal/{api,service}/control_plane*`, `webui/src/features/nodes/*`, current tests and workflow. Preserve unrelated work. Pick an isolated branch/workspace with the owner; do not silently commit on master.
4. Owner policy: **no local `go test`, `go build`, `npm run build`, `tsc -b`, or decompilation without explicit permission**. These compile project code. Write tests before implementation; when compilation is not authorized, run `gofmt`, `git diff --check`, focused ESLint, Go module/YAML parsing and source-contract inspections only. Do not claim tests pass. Run approved remote CI only after separate push/workflow authorization and check the whole workflow, not only WebUI.

## Task 1: Risk contract and configuration

**Files:** create `internal/ipquality/risk.go`, `risk_test.go`; modify `internal/config/env.go`, `env_test.go`, `internal/api/handler_system.go`, `webui/src/features/systemConfig/types.ts`, `.env.example`, `docs/IP_QUALITY.md`.

1. Write table tests for `risk = max(100-score, floors for explicitly true flags)` using the owner-approved floor table: 0, 44.9, 45, 75 and 100 score boundaries; null score; false/null flags; multiple true flags; decimals; clamp; status/ASN not double-counted. Name the risk formula version. Verify the tests fail when compilation is authorized; otherwise retain as unrun tests.
2. Implement pure `ComputeRisk(result) -> (nullable value, version)` without depending on node hash or routing. Never treat `risk:null` as zero. Recompute from cached provider data on read so a risk-version change does not require cache mutation or a provider call.
3. Add a separate startup opt-in for automatic checks (name/boolean chosen in config tests), requiring base quality enablement and nonempty admin token. Expose only safe enabled flags in env snapshot, not endpoint or credentials. Confirm the 0:00 timezone contract and document the restart/cache behavior.
4. Targeted verification when authorized: `go test ./internal/ipquality ./internal/config ./internal/api`; otherwise `gofmt -l` and `git diff --check`. Keep config disabled path zero-request compatible.

## Task 2: Bounded shared lookup coordinator

**Files:** create `internal/ipquality/coordinator.go`, `coordinator_test.go`; modify `internal/ipquality/service.go` and focused tests; wire lifecycle through `cmd/resin/app_runtime.go`.

1. First write fake-clock/fake-provider tests: 1 request per normalized public IP despite node fan-out and concurrent manual/auto submissions; 1 provider start/second; <=2 in-flight; no unbounded goroutines; <=approved queued-IP cap; manual checks are admitted ahead of queued background work; graceful cancel/stop; failed/429/timeout responses preserve last success and retry with bounded backoff. Include queue-full and canceled-job behavior.
2. Implement one coordinator with bounded deduped pending IPs and bounded job progress counters. Reuse the existing client, cache, `singleflight` and rate limiter rather than a second independent provider budget. Snapshot filtered IPs for manual jobs, skip fresh entries by default. Background work should revalidate pool membership and current IP before each start, not hold topology locks across HTTP requests. No repeated retry per page view.
3. Make lookup cancellation explicit: a canceled caller must not leak a provider request or poison unrelated same-IP waiters. Define retry caps for errors, queue overflow and shutdown. Keep provider errors sanitized.
4. Verify focused concurrency/race tests with permission (`go test -race ./internal/ipquality`), otherwise document the unverified concurrency risk for CI. Do not change node health or routing.

## Task 3: Midnight and node-refresh triggers

**Files:** modify `internal/topology/subscription_scheduler.go` and focused tests, `internal/topology/pool.go` or its wiring, `cmd/resin/{main,app_runtime}.go`; add targeted integration tests in `cmd/resin`/`internal/topology`.

1. Test that a *successfully applied* automatic subscription refresh schedules known public IPs lacking fresh quality, while fetch/parse/stale-success failures and disabled nodes do not enqueue. Existing `OnSubUpdated` fires on failure too: add a success-specific notification or equivalent narrow bridge rather than misusing the persistence callback. No HTTP or blocking wait inside the scheduler lock/callback.
2. Test a newly added node without egress IP: subscription refresh schedules nothing; first successful egress observation then schedules exactly that public IP. A changed IP schedules only the new IP; a circuit/failure/region-only dynamic event schedules nothing. Preserve the existing state-engine `OnNodeDynamicChanged` callback while adding the narrow IP-change event. Never trigger a new egress probe solely to obtain quality data.
3. Test daily local-midnight scan, no overlap with an in-progress run, DST/restart behavior as owner-approved, no traffic when automatic checking disabled, and one budget shared with manual jobs. Use an injectable clock/ticker; do not rely on sleeping until midnight in tests. Define shutdown ordering so callbacks cannot enqueue into a stopped worker.
4. Verify targeted packages when authorized (`go test ./internal/topology ./internal/ipquality ./cmd/resin`); otherwise format/static inspection and schedule CI verification.

## Task 4: IP-group read API and global risk sorting

**Files:** create `internal/service/control_plane_ip_groups.go`, `internal/api/handler_node_ip_groups.go` and corresponding `_test.go`; modify `internal/api/server.go`; leave `GET /api/v1/nodes` and `NodeSummary` compatible.

1. Write service/API tests using nodes A+B sharing an IP, node C with another IP and no-IP node D; filtered tag/subscription/platform/region/health combinations must group only matching members. Count groups separately from nodes. Support one labelled unresolved-IP bucket, never conflate unrelated nodes into a real IP.
2. Build a cache-only group projection over `ControlPlaneService.ListNodes(filters)`, grouping by normalized public IP. Each row includes current IP, matched-node count, nullable score/risk, risk version, residential tri-state, ASN, stale/fresh/unknown, observed time. No third-party request on GET. Use existing `groupcache/lru` cache reads under its mutex.
3. Sort **before pagination**. `sort_by=risk`, `sort_order=asc|desc`, unknown/unresolved last in both orders, stable tie-breaker normalized IP. Test page boundary and grouping after filtering; label stale results. Validate query sort/filter inputs with existing API helpers.
4. Add admin-only `GET /api/v1/node-ip-groups` and a detail endpoint for a validated current public IP, returning paginated associated `NodeSummary` data and quality. Use an explicit unresolved detail route. Do not accept arbitrary provider lookup IP; no result for an IP absent from the filtered pool. Test auth, IPv6, missing/changed IP and no node-health mutation. Keep node-specific actions by node hash.
5. With permission run `go test ./internal/service ./internal/api`; otherwise inspect diff, run `gofmt -l`/`git diff --check` and note tests as unrun.

## Task 5: Filtered manual batch API and status

**Files:** add handlers in `internal/api/handler_node_ip_groups.go` or a neighboring batch handler; extend `internal/service/control_plane_ip_groups.go`; focused API/service tests.

1. Specify admin-only start/status/cancel routes with bounded response schema: job ID, source, unique-IP total, fresh skipped, completed, failed, deferred, canceled, started/ended timestamps, and sanitized error summary. New requests carry existing node filters, not a caller-supplied list of arbitrary IPs/provider URLs. Snapshot the filtered pool server-side and enforce the approved distinct-IP cap with a clear rejection rather than silent truncation.
2. Write tests: page size does not limit job scope; duplicate IPs count once; unknown/non-public IPs skipped; two concurrent starts cannot produce duplicate provider work; cancel stops queued work without losing successful cache; auth/disabled/over-limit/queue-full safe errors; automatic and manual work share one budget.
3. Implement coordinator integration, no durable job history in the single-instance phase. On restart job status can be lost; expose a precise response for unknown/expired job IDs and bound retained terminal jobs.
4. Run focused API/coordinator tests with permission or record remote-CI verification requirement.

## Task 6: Node-pool IP table and detail dialog

**Files:** modify `webui/src/features/nodes/{NodesPage.tsx,api.ts,types.ts}`, add focused `NodeIPGroupDialog.tsx`/`NodeIPBatchStatus.tsx` if useful, update `webui/src/i18n/translations.ts` and only necessary CSS. Use existing icons, buttons, table and modal conventions.

1. Define TS types/API helpers for group page, detail, risk and batch job. Keep existing node-specific API functions for details/actions. The top-level list key is IP (or explicit unresolved key), never an arbitrary representative node hash.
2. Replace the node-name column with IP, matched-node count, risk (higher is worse), reputation score, residential `Yes/No/Unknown`, ASN and freshness. Keep existing filters and pagination; risk sort must request the server with asc/desc, not reorder current-page rows. Unknown risk last and stale labelled.
3. On row click open a dialog with the IP's quality details and a paginated list of associated nodes, including their names, status and per-node probe/check actions. A no-IP group shows nodes but no fake risk or quality. Changing an IP invalidates the group/detail queries. Avoid cards nested in cards; ensure desktop/mobile width, long IP/ASN, loading/empty/error states and keyboard close remain usable.
4. Provide one clearly labelled manual filtered-batch command with estimated distinct-IP work, progress and cancel; opening a list/dialog never starts provider work. Poll only active job status, not provider; stop polling on completion/unmount. Handle 429/503, disabled automation, stale cache and restart-lost jobs.
5. Static verification: focused ESLint **and** type validation when explicitly authorized (`cd webui && npm run build`, which runs `tsc -b && vite build`). ESLint alone cannot detect all TypeScript errors; do not claim WebUI compiles without `tsc -b` evidence. With permission, inspect real desktop/mobile UI against a backend; otherwise mark visual acceptance pending.

## Task 7: Delivery and acceptance

1. Expand `.github/workflows/build-fork-image.yml` Test API step to cover `./internal/service ./internal/topology` as well as `./internal/api ./internal/ipquality ./internal/config ./cmd/resin`. Confirm real CI results only after owner authorizes push/dispatch. Do not change deployment or call the public provider in tests.
2. Update `docs/IP_QUALITY.md` and `.env.example`: automatic opt-in, local-midnight TZ, distinct-IP capacity/ETA, queue/retry/cancel limits, provider privacy/terms, uncertainty and risk version, in-memory loss on restart, no routing/health side effects. Note the accepted single-instance limitation.
3. Acceptance matrix: >1,000 nodes with shared IPs collapse correctly; filters apply before grouping; sort works across page boundaries; null score/flags and stale data remain distinguishable; subscription success and first successful egress enqueue only missing/stale public IPs; failed subscription updates/list reads do not contact provider; midnight and manual work share the budget; cancel and provider outage leave prior cache/node health intact; IP changes do not display old results.
4. Finish with changed files, executed checks and exact outputs, tests not run, open provider/algorithm decisions, CI and image/deployment status. Do not commit or push without explicit authorization, and do not claim compilation passed from ESLint/static checks.

## New-session start

Ask the owner to settle the four preflight decisions before Task 1. Then execute tasks in small reviewable batches, test-first within the allowed verification policy. No project code, git commit or deployment was authorized by this plan-only request.
