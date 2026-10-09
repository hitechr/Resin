import assert from "node:assert/strict";
import test from "node:test";
import { sortRegionStats, toggleRegionSort } from "./regionSorting.ts";

const makeRow = (region, available_ips, total_ips, low_latency_nodes, sampled_nodes) => ({
  region, available_ips, total_ips, low_latency_nodes, sampled_nodes,
});

test("header click replaces primary sort while Shift click adds a secondary criterion", () => {
  const initial = [{ field: "available_ips", descending: true }];
  assert.deepEqual(toggleRegionSort(initial, "total_ips", false), [{ field: "total_ips", descending: true }]);
  const added = toggleRegionSort(initial, "low_latency_ratio", true);
  assert.deepEqual(added, [
    { field: "available_ips", descending: true },
    { field: "low_latency_ratio", descending: true },
  ]);
  assert.deepEqual(toggleRegionSort(added, "low_latency_ratio", true)[1], { field: "low_latency_ratio", descending: false });
  assert.deepEqual(toggleRegionSort(added, "region", false), [{ field: "region", descending: false }]);
});

test("secondary fields break ties without changing the primary ordering", () => {
  const rows = [
    makeRow("US", 8, 10, 2, 4),
    makeRow("JP", 8, 9, 3, 4),
    makeRow("DE", 5, 5, 1, 2),
  ];
  assert.deepEqual(
    sortRegionStats(rows, [
      { field: "available_ips", descending: true },
      { field: "low_latency_ratio", descending: false },
    ]).map((row) => row.region),
    ["US", "JP", "DE"],
  );
  assert.deepEqual(rows.map((row) => row.region), ["US", "JP", "DE"]);
});

test("unknown regions and missing latency samples remain last in either direction", () => {
  const rows = [
    makeRow("", 30, 30, 0, 0),
    makeRow("US", 5, 5, 0, 0),
    makeRow("JP", 5, 5, 1, 2),
  ];
  for (const descending of [true, false]) {
    assert.deepEqual(
      sortRegionStats(rows, [{ field: "low_latency_ratio", descending }]).map((row) => row.region),
      ["JP", "US", ""],
    );
  }
});
