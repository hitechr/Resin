import { useQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowDownUp, ArrowUp, Globe2 } from "lucide-react";
import { useMemo, useState } from "react";
import { Card } from "../../components/ui/Card";
import { useI18n } from "../../i18n";
import { formatApiErrorMessage } from "../../lib/error-message";
import { getRegionName } from "../nodes/regions";
import { getNodeRegionStats } from "./api";
import type { NodeRegionStat } from "./types";
import { sortRegionStats, toggleRegionSort, type RegionSort, type RegionSortField } from "./regionSorting";

const EMPTY_REGION_ROWS: NodeRegionStat[] = [];
const COLUMNS: { field: RegionSortField; label: string }[] = [
  { field: "region", label: "国家/地区" },
  { field: "available_ips", label: "可用 IP" },
  { field: "total_ips", label: "总 IP" },
  { field: "low_latency_nodes", label: "低延迟 / 样本" },
  { field: "available_ratio", label: "可用占比" },
  { field: "low_latency_ratio", label: "低延迟占比" },
];

function flagFor(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return "";
  return String.fromCodePoint(...Array.from(code, (letter) => 127397 + letter.charCodeAt(0)));
}

function ratio(numerator: number, denominator: number): number {
  return denominator > 0 ? numerator / denominator : 0;
}

function Country({ code }: { code: string }) {
  const { t } = useI18n();
  const name = code ? getRegionName(code) : undefined;
  return (
    <span className="region-stats-country" title={name ?? t("未知地区")}>
      {code ? <span className="region-stats-flag" aria-hidden="true">{flagFor(code)}</span> : <Globe2 size={16} aria-hidden="true" />}
      <strong>{code || t("未知地区")}</strong>
    </span>
  );
}

function RatioCell({ value, label, variant, missing = false }: { value: number; label: string; variant: "availability" | "latency"; missing?: boolean }) {
  const { t } = useI18n();
  return (
    <span className={`region-stats-ratio ${variant}`}>
      <span className="region-stats-mobile-label">{label}</span>
      <strong>{missing ? t("暂无延迟样本") : `${(value * 100).toFixed(1)}%`}</strong>
      <span className="region-stats-track" role={missing ? undefined : "meter"} aria-label={label} aria-valuemin={missing ? undefined : 0} aria-valuemax={missing ? undefined : 100} aria-valuenow={missing ? undefined : Math.round(value * 100)}>
        <span className="region-stats-fill" style={{ width: `${value * 100}%` }} />
      </span>
    </span>
  );
}

function RegionRow({ row }: { row: NodeRegionStat }) {
  const { t } = useI18n();
  return (
    <div className="region-stats-row">
      <Country code={row.region} />
      <span className="region-stats-value available">
        <span className="region-stats-mobile-label">{t("可用 IP")}</span>
        {row.available_ips}
      </span>
      <span className="region-stats-value total">
        <span className="region-stats-mobile-label">{t("总 IP")}</span>
        {row.total_ips}
      </span>
      <span className="region-stats-value low">
        <span className="region-stats-mobile-label">{t("低延迟 / 样本")}</span>
        {row.low_latency_nodes} / {row.sampled_nodes}
      </span>
      <RatioCell value={ratio(row.available_ips, row.total_ips)} label={t("可用占比")} variant="availability" />
      <RatioCell value={ratio(row.low_latency_nodes, row.sampled_nodes)} label={t("低延迟占比")} variant="latency" missing={row.sampled_nodes === 0} />
    </div>
  );
}

export function RegionStatsPanels() {
  const { t } = useI18n();
  const [sorting, setSorting] = useState<RegionSort[]>([{ field: "available_ips", descending: true }]);
  const query = useQuery({
    queryKey: ["dashboard-node-region-stats"],
    queryFn: getNodeRegionStats,
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  });
  const rows = query.data?.items ?? EMPTY_REGION_ROWS;
  const sorted = useMemo(() => sortRegionStats(rows, sorting), [rows, sorting]);

  return (
    <Card className="dashboard-panel region-stats-panel">
      <div className="region-stats-heading">
        <div>
          <h3>{t("节点国家分布")}</h3>
          <span>{t("健康节点 · 参考延迟 ≤ 400 ms")}</span>
        </div>
        <span>{t("共 {{count}} 个国家/地区", { count: query.data?.country_count ?? 0 })}</span>
      </div>
      <div className="region-stats-labels">
        {COLUMNS.map(({ field, label }) => {
          const priority = sorting.findIndex((item) => item.field === field);
          const active = sorting[priority];
          return (
            <button
              key={field}
              type="button"
              className={active ? "region-stats-sort-active" : ""}
              onClick={(event) => {
                const append = event.shiftKey;
                setSorting((current) => toggleRegionSort(current, field, append));
              }}
              aria-pressed={Boolean(active)}
              aria-label={active
                ? `${t(label)}，${t("第 {{count}} 优先级", { count: priority + 1 })}，${t(active.descending ? "降序" : "升序")}`
                : t(label)}
              title={t("单击排序，Shift 点击追加排序")}
            >
              <span className="region-stats-sort-label">{t(label)}</span>
              <span className="region-stats-sort-indicator" aria-hidden="true">
                {active ? (active.descending ? <ArrowDown size={14} /> : <ArrowUp size={14} />) : <ArrowDownUp size={14} />}
                {active && sorting.length > 1 ? <small>{priority + 1}</small> : null}
              </span>
            </button>
          );
        })}
      </div>
      <div className="region-stats-rows" role="region" aria-label={t("节点国家分布")} tabIndex={0}>
        {query.error ? <p className="region-stats-state" role="alert">{formatApiErrorMessage(query.error, t)}</p> : query.isPending ? (
          <p className="region-stats-state">{t("正在加载国家统计...")}</p>
        ) : rows.length === 0 ? (
          <p className="region-stats-state">{t("暂无国家统计数据")}</p>
        ) : sorted.map((row) => <RegionRow key={row.region} row={row} />)}
      </div>
    </Card>
  );
}
