# Dashboard region statistics design

Add two panels below the existing dashboard charts, side by side on desktop and stacked on narrow screens. Both show five countries initially, with an expandable remainder. No navigation or existing metrics change.

The control-plane node service owns the snapshot. A new authenticated GET `/api/v1/nodes/stats/regions` aggregates all nodes without pagination. A country is identified by its normalized two-letter egress region; unknown regions are displayed separately and not counted as countries. Total IPs are distinct nonempty egress addresses within each region; available IPs are those with at least one healthy and enabled node. Low latency is the number of healthy nodes with reference authority latency at most 400 ms, divided by healthy nodes with a reference latency. Regions without samples have no ratio and appear after sampled regions.

The distribution panel sorts by available IP count descending and shows flag, code, IP counts, ratio and bar. The latency panel sorts by low-latency ratio ascending and shows sampled node counts and ratio. Both have a collapsed remainder with an accessible expand button. Empty, loading and error states belong to these panels independently of the existing dashboard queries. Snapshot refresh is 30 seconds to avoid shipping full node lists to browsers.

Tests cover distinct IPs, mixed health, latency boundary, missing samples and empty snapshot. Build and test commands which compile code are excluded by workspace policy.
