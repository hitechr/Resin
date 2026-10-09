import type { NodeRegionStat } from "./types";

export type RegionSortField = "region" | "available_ratio" | "available_ips" | "total_ips" | "low_latency_nodes" | "low_latency_ratio";
export type RegionSort = { field: RegionSortField; descending: boolean };

function ratio(numerator: number, denominator: number): number {
  return denominator > 0 ? numerator / denominator : 0;
}

function value(row: NodeRegionStat, field: RegionSortField): number {
  if (field === "available_ratio") return ratio(row.available_ips, row.total_ips);
  if (field === "low_latency_ratio") return ratio(row.low_latency_nodes, row.sampled_nodes);
  if (field === "region") return 0;
  return row[field];
}

export function toggleRegionSort(current: RegionSort[], field: RegionSortField, append: boolean): RegionSort[] {
  const index = current.findIndex((sort) => sort.field === field);
  if (!append) {
    return [{ field, descending: index === 0 ? !current[0].descending : field !== "region" }];
  }
  if (index < 0) return [...current, { field, descending: field !== "region" }];
  return current.map((sort, position) => position === index ? { ...sort, descending: !sort.descending } : sort);
}

export function sortRegionStats(rows: NodeRegionStat[], sorting: RegionSort[]): NodeRegionStat[] {
  return [...rows].sort((a, b) => {
    if (!a.region) return b.region ? 1 : 0;
    if (!b.region) return -1;
    for (const { field, descending } of sorting) {
      if (field === "low_latency_ratio") {
        if (!a.sampled_nodes && b.sampled_nodes) return 1;
        if (!b.sampled_nodes && a.sampled_nodes) return -1;
        if (!a.sampled_nodes) continue;
      }
      const diff = field === "region"
        ? a.region.localeCompare(b.region)
        : value(a, field) - value(b, field);
      if (diff !== 0) return descending ? -diff : diff;
    }
    return a.region.localeCompare(b.region);
  });
}
