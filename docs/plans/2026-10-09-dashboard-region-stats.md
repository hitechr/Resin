# Dashboard Region Statistics Implementation Plan

> Implement within this session; do not commit or run compile-based tests without explicit authorization.

**Goal:** Show Top 5 expandable country IP availability and worst-first low-latency ratios on the existing dashboard.

**Architecture:** Add an aggregate endpoint to the control-plane node API, reusing NodeSummary health and reference latency. Fetch its compact snapshot separately with React Query and render two responsive panels.

**Tech Stack:** Go net/http, existing service, React, TypeScript, React Query, CSS.

---

### Task 1: Aggregate contract

**Files:** `internal/api/handler_node_test.go`, `internal/api/handler_node.go`, `internal/api/server.go`.

1. Add a handler regression test exercising duplicate IPs, unhealthy nodes, latency thresholds and missing samples.
2. Inspect the test and new endpoint contract statically (Go tests require compilation and are not authorized).
3. Implement the aggregation endpoint using the existing `ListNodes` and `IsHealthyAndEnabled` contract; sort deterministically and register the route.

### Task 2: Dashboard panels

**Files:** `webui/src/features/dashboard/DashboardPage.tsx`, `webui/src/features/dashboard/api.ts`, `webui/src/features/dashboard/types.ts`, `webui/src/i18n/translations.ts`, `webui/src/styles/theme.css`.

1. Add typed API call and independent 30-second query.
2. Render Top 5 rows with a collapsible remainder in both panels and independent loading/empty/error states.
3. Add responsive, compact bars and labels plus Chinese/English translations.

### Task 3: Verify

1. Run `gofmt` and `git diff --check`.
2. Run ESLint if dependencies are installed; inspect changed files and endpoint/test contract.
3. Report explicitly which compile-based tests/builds were not run.
