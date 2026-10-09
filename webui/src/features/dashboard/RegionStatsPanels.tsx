import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, Globe2 } from "lucide-react";
import { useMemo, useState } from "react";
import { Card } from "../../components/ui/Card";
import { useI18n } from "../../i18n";
import { formatApiErrorMessage } from "../../lib/error-message";
import { getRegionName } from "../nodes/regions";
import { getNodeRegionStats } from "./api";
import type { NodeRegionStat } from "./types";

const VISIBLE_ROWS = 5;
const EMPTY_REGION_ROWS: NodeRegionStat[] = [];

function flagFor(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return "";
  return String.fromCodePoint(...Array.from(code, (letter) => 127397 + letter.charCodeAt(0)));
}

function ratio(numerator: number, denominator: number): number {
  return denominator > 0 ? numerator / denominator : 0;
}

function percent(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
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

type RegionRowsProps = {
  rows: NodeRegionStat[];
  kind: "availability" | "latency";
};

function RegionRows({ rows, kind }: RegionRowsProps) {
  const { t } = useI18n();
  return (
    <div className="region-stats-rows">
      {rows.map((row) => {
        const value = kind === "availability"
          ? ratio(row.available_ips, row.total_ips)
          : ratio(row.low_latency_nodes, row.sampled_nodes);
        const noSamples = kind === "latency" && row.sampled_nodes === 0;
        return (
          <div className="region-stats-row" key={row.region}>
            <Country code={row.region} />
            <span className="region-stats-count">
              {kind === "availability"
                ? `${row.available_ips} / ${row.total_ips} IP`
                : noSamples ? t("暂无延迟样本") : `${row.low_latency_nodes} / ${row.sampled_nodes}`}
            </span>
            <span className="region-stats-percent">{noSamples ? "-" : percent(value)}</span>
            <div className="region-stats-track" role={noSamples ? undefined : "meter"} aria-label={`${row.region || t("未知地区")} ${kind === "availability" ? t("可用占比") : t("低延迟占比")}`} aria-valuemin={noSamples ? undefined : 0} aria-valuemax={noSamples ? undefined : 100} aria-valuenow={noSamples ? undefined : Math.round(value * 100)}>
              <span className={kind === "latency" ? "region-stats-fill latency" : "region-stats-fill"} style={{ width: `${value * 100}%` }} />
            </div>
          </div>
        );
      })}
    </div>
  );
}

type RegionPanelProps = {
  title: string;
  subtitle: string;
  rows: NodeRegionStat[];
  kind: RegionRowsProps["kind"];
  pending: boolean;
  error: Error | null;
  emptyText: string;
};

function RegionPanel({ title, subtitle, rows, kind, pending, error, emptyText }: RegionPanelProps) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const rest = rows.slice(VISIBLE_ROWS);
  return (
    <Card className="dashboard-panel region-stats-panel">
      <div className="region-stats-heading">
        <h3>{title}</h3>
        <span>{subtitle}</span>
      </div>
      {error ? <p className="region-stats-state" role="alert">{formatApiErrorMessage(error, t)}</p> : pending ? (
        <p className="region-stats-state">{t("正在加载国家统计...")}</p>
      ) : rows.length === 0 ? (
        <p className="region-stats-state">{emptyText}</p>
      ) : (
        <>
          <div className="region-stats-labels" aria-hidden="true">
            <span>{t("国家/地区")}</span>
            <span>{kind === "availability" ? t("可用 / 总 IP") : t("低延迟 / 样本")}</span>
            <span>{kind === "availability" ? t("可用占比") : t("低延迟占比")}</span>
          </div>
          <RegionRows rows={rows.slice(0, VISIBLE_ROWS)} kind={kind} />
          {rest.length > 0 ? (
            <>
              <button type="button" className="region-stats-toggle" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
                {expanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
                {expanded ? t("收起其它") : t("其它 {{count}} 项", { count: rest.length })}
              </button>
              {expanded ? <div className="region-stats-remainder"><RegionRows rows={rest} kind={kind} /></div> : null}
            </>
          ) : null}
        </>
      )}
    </Card>
  );
}

export function RegionStatsPanels() {
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["dashboard-node-region-stats"],
    queryFn: getNodeRegionStats,
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  });
  const items = query.data?.items ?? EMPTY_REGION_ROWS;
  const byAvailability = useMemo(() => [
    ...items.filter((item) => item.region !== ""),
    ...items.filter((item) => item.region === ""),
  ], [items]);
  const byLatency = useMemo(() => [...byAvailability].sort((a, b) => {
    if (!a.region) return b.region ? 1 : 0;
    if (!b.region) return -1;
    if (a.sampled_nodes === 0) return b.sampled_nodes === 0 ? a.region.localeCompare(b.region) : 1;
    if (b.sampled_nodes === 0) return -1;
    const difference = ratio(a.low_latency_nodes, a.sampled_nodes) - ratio(b.low_latency_nodes, b.sampled_nodes);
    return difference || a.region.localeCompare(b.region);
  }), [byAvailability]);
  const shared = {
    pending: query.isPending,
    error: query.error,
    emptyText: t("暂无国家统计数据"),
  };

  return (
    <div className="dashboard-region-grid">
      <RegionPanel {...shared} title={t("节点国家分布")} subtitle={t("共 {{count}} 个国家/地区", { count: query.data?.country_count ?? 0 })} rows={byAvailability} kind="availability" />
      <RegionPanel {...shared} title={t("延迟占比")} subtitle={t("健康节点 · 参考延迟 ≤ 400 ms")} rows={byLatency} kind="latency" />
    </div>
  );
}
