# Optional IP Quality Implementation Plan

> New-session handoff. Read `docs/plans/2026-10-09-optional-ip-quality-design.md` first. This is a plan, not implementation authorization to push, deploy, or run local compilation. Owner preference: do not compile the project or decompile binaries/dependencies without explicit permission.

**Goal:** Optional admin-initiated, read-only IP reputation display for the current egress IP in Resin's node detail drawer.

**Architecture:** A standalone One IP-compatible HTTPS client and bounded, in-memory IP-keyed cache live outside the topology and router. Admin-only node routes read cached results or request one manual lookup. No scheduler, state DB migration, node health mutation, or bulk scanning.

**Tech stack:** Go `net/http`, `net/netip`, existing `golang.org/x/sync/singleflight`, `golang.org/x/time/rate`, bounded `github.com/golang/groupcache/lru` (already in go.mod as indirect); React Query and the current node drawer UI.

---

## Before Code

1. Read the design and verify the current One IP `/api/ip/health` schema, data-source terms and rate limit. Public endpoint usage at production scale is **not** assumed approved. If the operator does not approve disclosure of egress IPs to the public service, use an authorized self-hosted HTTPS endpoint. Never copy AGPL code into Resin. Keep a short source/contract link in docs.
2. Inspect the current dirty worktree, `internal/config/env.go`, `internal/api/handler_system.go`, `internal/service/control_plane_system.go`, `cmd/resin/app_runtime.go:buildNetworkServers`, `internal/api/server.go`, `webui/src/features/nodes/{NodesPage,api,types}.ts(x)` and neighboring tests. Preserve unrelated changes.
3. Determine verification authorization: local `go test`, `npm run build` and `go build` compile the project and require explicit owner permission. If not authorized, still write the regression tests first; run formatting/static checks and arrange CI execution only after separate push authorization. Do not report tests as passing without evidence.

## Task 1: Opt-In Config and Contract

**Files:** `internal/config/env.go`, `internal/config/env_test.go`, `internal/api/handler_system.go`, `webui/src/features/systemConfig/types.ts`, `.env.example`, relevant config tests.

- Add `RESIN_IP_QUALITY_ENABLED` (false by default) and `RESIN_IP_QUALITY_ENDPOINT` (default public One IP URL). Enabled mode requires a nonempty admin token and a valid HTTPS URL with no credentials or fragment. Endpoint is startup-only; not user-request input. Keep disabled mode backward-compatible.
- Add `ip_quality_enabled` to the existing system env response; UI hides the feature when disabled. Write tests for default-off, invalid endpoint, missing admin token, and enabled config before implementation.

## Task 2: Provider Client and Cache

**Files:** create `internal/ipquality/client.go`, `client_test.go`, `service.go`, `service_test.go`; adjust `go.mod` only if the existing indirect LRU module becomes a direct import.

- Define a Resin-owned typed result: normalized IP, nullable floating-point score (no integer rounding), `good|moderate|poor|unknown`, ISP, ASN, nullable residential/datacenter/VPN/proxy/Tor/abuser flags, source, observed time and expiry. For `score:0`, `score:null`, `flag:false` and `flag:null`, preserve each distinct value. Derive status locally from validated score boundaries.
- Add fake-HTTP tests first: correct URL/query, 0/45/75/100 boundaries, IPv4/IPv6 normalized IP match, invalid/private IP, response IP mismatch, malformed or >64KiB body, null flags, redirect, timeout, upstream 429/5xx. No raw upstream error body should reach the caller.
- Implement client with a 12s bounded request, HTTPS, no cross-host redirect and explicit input validation. Server-side HTTP requests to the quality provider are direct, not through the selected node's outbound.
- Add fake-clock/concurrency tests first: 24h fresh/stale boundary, max 10k LRU entries, same-IP `singleflight`, max two concurrent provider calls, one start per second, cancellation, errors do not overwrite successful data. The service must not have a periodic scan or background refresh. Use existing mature LRU/rate/singleflight structures rather than inventing a custom queue.

## Task 3: Node API and Wiring

**Files:** `internal/service/control_plane_system.go`, new `internal/service/control_plane_ip_quality.go`, `internal/api/handler_node.go` or a neighboring quality handler, `internal/api/server.go`, `cmd/resin/app_runtime.go`, focused API/service tests.

- Inject the optional quality service through `buildNetworkServers` and `ControlPlaneService`; do not add it to node entry, topology health, routing or probe manager.
- `GET /api/v1/nodes/{hash}/ip-quality`: admin-authenticated, cache-only; typed `disabled|no_egress_ip|not_checked|fresh|stale` state. No provider call. For a missing hash return 404; for missing/unusable public IP return `no_egress_ip`.
- `POST /api/v1/nodes/{hash}/actions/check-ip-quality`: admin-authenticated, manual only. Resolve current IP server-side, coalesce by IP, then re-read the node's IP before returning success. If it changed, cache under the old IP but return 409 rather than showing it on the changed node. Rate/concurrency exhaustion returns 429; provider failures return a safe 502/503 code. Preserve previously cached success on failures. Define error mapping in this handler rather than broadening unrelated service errors without need.
- Tests: auth on both routes, disabled/no-IP, shared IP nodes one provider request, changed IP response, score/null fidelity, no mutation to failure count/circuit status, provider error/429. Keep result/response schema backward-compatible with existing node endpoints.

## Task 4: Node Drawer UI

**Files:** `webui/src/features/nodes/types.ts`, `api.ts`, `NodesPage.tsx` or a focused `NodeIPQualityPanel.tsx`, `webui/src/i18n/translations.ts`, `webui/src/styles/theme.css` if necessary.

- Reuse existing node detail drawer. Conditionally show an unframed IP Quality section with a Check command, score/status, ISP/ASN, six tri-state flags, source/time and fresh/stale indication. Label egress IP on disabled/unroutable nodes as last observed. No list-column requests or sorting. Distinguish unknown from false and show provider errors without changing the node status badge.
- Query key includes node hash and current egress IP. Opening the drawer calls only the cache-only GET; clicking Check calls POST and then invalidates GET. Handle disabled, no IP, loading, changed IP, 429, timeout and stale result. Use existing i18n, icons and error display conventions; avoid explanatory feature copy inside the page.

## Task 5: Verification and Delivery

- Run non-compiling checks (`gofmt -l`, `git diff --check`, targeted ESLint, YAML parse) locally. With explicit compile permission, run focused `go test ./internal/ipquality ./internal/api ./internal/config ./cmd/resin` and `npm run build`; otherwise do not run them locally.
- Update `.github/workflows/build-fork-image.yml` Test API step to include `./internal/ipquality`, `./internal/config` and `./cmd/resin` alongside `./internal/api`, so approved remote CI builds actually exercise the new client/wiring tests. Only push and trigger CI with owner authorization. Verify the full workflow result, not just the WebUI job.
- Manual acceptance on a deployment with an authorized endpoint: disabled mode shows no UI or outbound calls; one manual check displays source/time and nullable fields; opening the drawer again makes no upstream request; two nodes sharing an IP reuse the result; changing IP hides the old result; failing provider leaves routing unchanged. Check browser layout on desktop/mobile with a real backend. Document provider opt-in, privacy/terms, rate budget, 24h in-memory TTL, unknown semantics and restart behavior.
- Finish with changed files, measured checks, unrun tests, known limits and the image/deployment status. Do not claim the provider score measures node reachability or that public endpoint bulk usage has been approved.
