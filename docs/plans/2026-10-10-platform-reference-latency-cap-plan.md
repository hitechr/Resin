# Platform Reference Latency Cap Implementation Plan

> **For agentic workers:** Implement task-by-task with focused tests first. This is a new-session handoff, not authorization to edit product code, compile locally, commit, push or deploy in this session.

**Goal:** Let a platform exclude nodes whose displayed reference latency exceeds an optional maximum, e.g. 400 ms, before request routing or sticky-lease reuse.

**Architecture:** Add one platform-owned numeric limit to state.db, API and the platform form. Reuse `node.AverageEWMAForDomainsMs(entry, cfg.LatencyAuthorities)`, the same value used by `NodeSummary.ReferenceLatencyMs`, in the platform routable-view predicate and filter preview. Update membership on relevant probe results and on authority-list changes; do not add a per-request latency check or a second selection algorithm.

**Tech Stack:** Go platform/topology/routing, existing SQLite migrations and state repo, React/TypeScript platform form, existing CI matrix.

**Spec:** The behavior and boundaries below record the owner's 2026-10-10 choice: the threshold uses the *node list's displayed reference latency*, not target-site latency or IP-group latency. No separate design document was requested.

## Behavior And Boundaries

- `max_reference_latency_ms` is a non-negative integer. `0` (default, including migrated platforms) disables the filter; empty UI input maps to `0`. Reject negative, fractional, non-numeric and out-of-range inputs. Existing clients omitting the field retain current behavior; PATCH omission preserves the old value and PATCH `0` clears it. Follow current PATCH convention: `null` is rejected.
- With a positive limit, compare each node's `node.AverageEWMAForDomainsMs` against the limit: `400` passes, `>400` fails. A node without any configured authority-domain samples fails the filter, even if it has latency for another domain. This is a per-node rule: an IP remains eligible through another qualifying node sharing that IP.
- Reuse the displayed reference value *as-is*: it averages existing authority-domain EWMA values and has no freshness check. V1 does not change probe cadence, define a new staleness window, inspect target-domain latency, calculate an IP-group aggregate, or contact the provider on a request. Old samples remain effective until replaced or the authority set changes; record this limitation in delivery.
- The threshold is a hard platform filter, not a P2C score preference. An empty routable view stays empty; do not silently fall back to an over-limit node. Existing sticky leases follow the current view-membership check and same-IP rotation before new allocation. Default/unconfigured platforms behave exactly as before.
- Owner confirmation at new-session preflight: explicitly accept the proposed **unknown=exclude, no freshness window** v1 behavior above. If not accepted, revise this plan before implementation; do not silently choose a new staleness policy.
- Owner policy: no local `go test`, `go build`, `npm run build`, `tsc -b`, or decompilation without explicit permission. Write tests first; if permission is absent, use format/static checks and mark tests unrun. Push or trigger CI only after separate authorization; do not claim CI passes without the complete workflow result.

## Review Focus

- Existing state.db migration: older platforms load with limit `0`, including the built-in Default platform (Task 1 test).
- API PATCH `0` clears, omission preserves, and invalid values are rejected without changing stored/runtime state (Task 1 test).
- At the boundary, displayed 400 ms passes, 401 ms fails; unknown authority latency excludes only when enabled (Task 2 test).
- An authority probe crosses the limit in either direction *after* the first sample, so the platform view and sticky lease reflect the change; unrelated-domain probes do not force unnecessary rebuilds (Task 2 test).
- Changing `latency_authorities` at runtime recomputes filtered views; preview and saved platform use the same calculation (Tasks 2-3 tests).

---

### Task 1: Persist And Expose The Platform Limit

**Files:** `internal/model/models.go`, `internal/state/repo_state.go`, `internal/state/migrate.go`, new `internal/state/migrations/state/000010_platform_max_reference_latency.{up,down}.sql`, `internal/service/control_plane_platform.go`, `internal/service/control_plane_system.go` (platform PATCH allowlist), `internal/state/repo_state_test.go`, `internal/service/control_plane_test.go`.

**Interface:** `model.Platform.MaxReferenceLatencyMs int`; `PlatformResponse`, create request, platform config and update PATCH expose `max_reference_latency_ms` as an integer. Schema: `INTEGER NOT NULL DEFAULT 0`; update the state migration version marker. Reuse `mergePatch.optionalInt("max_reference_latency_ms")` for PATCH, then validate `>=0` in service *and* repository; malformed PATCH values fail before persistence. API field omission on create uses `0`.

- [ ] Write migration/repo tests: upgrading a pre-000010 platform keeps it at `0`; save/reload `400`; reject negatives and preserve prior data. Write service/API tests for create, read, update, clear with `0`, omitted PATCH and invalid number/null.
- [ ] Run focused tests to see expected failures **only if local compilation is authorized**; otherwise document that the red phase could not be executed.
- [ ] Add migration, model/repo encoding and API config/validation. Keep existing platform create/update atomicity and copy-on-write replacement; wire runtime `Platform` construction to the new field in Task 2.
- [ ] Re-run focused tests if authorized: `go test ./internal/state ./internal/service ./internal/api`; otherwise run `gofmt -l` and `git diff --check` without claiming test success.

### Task 2: Enforce And Refresh Routable Membership

**Files:** `internal/platform/{platform.go,model_codec.go,platform_test.go}`, `internal/topology/{pool.go,health_test.go}`, `internal/service/control_plane_system.go`, `internal/routing/routing_test.go` (or a focused sticky-lease test), `cmd/resin/app_runtime.go` only if runtime-config wiring requires it.

**Interface:** `platform.Platform.MaxReferenceLatencyMs int`. Extend `FullRebuild`/`NotifyDirty` evaluation with the current authority-domain list supplied by `GlobalNodePool.latencyAuthorities`; apply `node.AverageEWMAForDomainsMs` only when the limit is positive. Keep model/bootstrap construction and service `toRuntime` consistent. Prefer re-evaluating only latency-limited platforms on successful authority-domain samples; preserve the existing all-platform notification on the first latency sample. Do not hold a latency-table lock while notifying platforms. After an actual `latency_authorities` change in `PatchRuntimeConfig`, rebuild affected platform views using the new snapshot.

- [ ] Write platform tests for disabled/unknown/400/401, multiple authorities using exactly the displayed average, and two nodes sharing an IP with one over the limit.
- [ ] Write topology tests for later successful authority samples crossing `399 -> 401 -> 399`, ordinary-domain samples, boot rebuild, and authority-list changes; write routing tests for an existing over-limit sticky node rotating to a qualified same-IP node or leaving the now-empty view without a bypass.
- [ ] Run focused tests to observe expected failure if authorized; implement the platform predicate and targeted view refresh; keep P2C scoring, probe scheduler and IP-quality cache untouched.
- [ ] Verify `go test ./internal/platform ./internal/topology ./internal/routing ./internal/service ./cmd/resin` and `go test -race ./internal/topology ./internal/routing` if authorized; otherwise mark both unrun and perform static checks.

### Task 3: Platform Form And Preview Parity

**Files:** `internal/service/control_plane_platform.go` and `control_plane_platform_preview_test.go`, `webui/src/features/platforms/{types.ts,api.ts,formModel.ts,PlatformPage.tsx,PlatformDetailPage.tsx}`, any existing focused form tests and translation keys if needed.

**Interface:** `PlatformSpecFilter.MaxReferenceLatencyMs int` with `0` as default, and the same field on stored-platform preview. The form has one optional non-negative integer input labelled "最大参考延迟 (ms)" beside region filtering; empty means off and submitting an empty field sends `0`. Do not model it as region-filter text or add a global setting.

- [ ] Write preview tests proving draft and stored-platform previews agree on 400/401/unknown and that preview remains a filter preview (it does not add health/routing checks). Add focused form tests for empty, 400 and invalid input if the current UI test harness supports them.
- [ ] Run tests to observe failure if authorized, then implement preview and form/API mapping; ensure create/edit reload round-trip and clearing the input work.
- [ ] Verify focused Go tests and the WebUI build only with explicit compilation permission; otherwise run focused ESLint, formatting and `git diff --check`, and record missing TypeScript/browser verification. Confirm mobile/desktop input and error layout in a browser when available.

## New-Session Delivery

Start on a fresh branch after confirming the proposed unknown/stale behavior and checking `git status`; leave unrelated untracked files intact. Finish with an acceptance matrix covering disabled mode, 400 boundary, unknown samples, live threshold crossings, same-IP sticky fallback, authority-list update and upgraded state.db. Use the full `.github/workflows/build-fork-image.yml` matrix (including `internal/state`) only after owner authorization. Record exact verification evidence and remaining stale-data limitation; do not commit, push, dispatch CI or deploy merely because this plan exists.
