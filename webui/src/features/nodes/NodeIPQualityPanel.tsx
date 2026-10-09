import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { useI18n } from "../../i18n";
import { ApiError } from "../../lib/api-client";
import { formatApiErrorMessage } from "../../lib/error-message";
import { formatDateTime } from "../../lib/time";
import { checkNodeIPQuality, getNodeIPQuality } from "./api";
import type { NodeSummary } from "./types";

const flags = ["residential", "datacenter", "vpn", "proxy", "tor", "abuser"] as const;

export function NodeIPQualityPanel({ node }: { node: NodeSummary }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const key = ["node-ip-quality", node.node_hash, node.egress_ip ?? ""];
  const query = useQuery({
    queryKey: key,
    queryFn: () => getNodeIPQuality(node.node_hash),
    refetchOnWindowFocus: false,
    retry: false,
  });
  const check = useMutation({
    mutationFn: () => checkNodeIPQuality(node.node_hash),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["node", node.node_hash] });
      await queryClient.invalidateQueries({ queryKey: ["node-ip-quality", node.node_hash] });
    },
    onError: async (error) => {
      if (error instanceof ApiError && error.code === "EGRESS_IP_CHANGED") {
        await queryClient.invalidateQueries({ queryKey: ["node", node.node_hash] });
      }
    },
  });
  const data = query.data;
  const changedIP = error instanceof ApiError && error.code === "EGRESS_IP_CHANGED";
  const quality = !changedIP && data?.egress_ip === node.egress_ip ? data.quality : undefined;
  const lastObserved = !node.enabled || !node.has_outbound;
  const state = data?.state;
  const error = check.error;
  const errorLabel = error instanceof ApiError ? ({
    EGRESS_IP_CHANGED: t("出口 IP 已变化，请刷新节点详情"),
    NO_EGRESS_IP: t("没有可检查的公共出口 IP"),
    IP_QUALITY_RATE_LIMITED: t("检查过于频繁，请稍后重试"),
    IP_QUALITY_INVALID_RESPONSE: t("质量服务返回无效数据"),
    IP_QUALITY_UNAVAILABLE: t("质量服务暂时不可用"),
    IP_QUALITY_DISABLED: t("IP 质量检查已关闭"),
  } as Record<string, string>)[error.code] : undefined;

  return (
    <section className="platform-drawer-section">
      <div className="platform-drawer-section-head" style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "12px", flexWrap: "wrap" }}>
        <h4>{t("IP 质量")}</h4>
        <Button variant="secondary" size="sm" onClick={() => check.mutate()} disabled={check.isPending || query.isLoading || state === "disabled" || state === "no_egress_ip"}>
          <RefreshCw size={14} className={check.isPending ? "spin" : undefined} />
          {check.isPending ? t("检查中...") : t("检查 IP 质量")}
        </Button>
      </div>
      {query.isLoading ? <p className="muted">{t("加载中...")}</p> : null}
      {query.error ? <p className="callout callout-error">{formatApiErrorMessage(query.error, t)}</p> : null}
      {error ? <p className="callout callout-error">{errorLabel ?? formatApiErrorMessage(error, t)}</p> : null}
      {data ? (
        <div className="stats-grid">
          <div><span>{lastObserved ? t("上次观测出口 IP") : t("出口 IP")}</span><p>{changedIP ? t("未知") : data.egress_ip || node.egress_ip || t("未知")}</p></div>
          <div><span>{t("数据状态")}</span><p>{state === "fresh" ? t("新鲜") : state === "stale" ? t("已过期") : t("未知")}</p></div>
          {quality ? <>
            <div><span>{t("信誉分（越高越好）")}</span><p>{quality.score == null ? t("未知") : quality.score}</p></div>
            <div><span>{t("信誉等级")}</span><p>{({ good: t("良好"), moderate: t("一般"), poor: t("较差"), unknown: t("未知") })[quality.status]}</p></div>
            <div><span>ISP</span><p>{quality.isp || t("未知")}</p></div>
            <div><span>ASN</span><p>{quality.asn || t("未知")}</p></div>
            {flags.map((flag) => <div key={flag}><span>{({ residential: t("住宅网络"), datacenter: t("数据中心"), vpn: "VPN", proxy: t("代理"), tor: "Tor", abuser: t("滥用记录") })[flag]}</span><p>{quality.flags[flag] == null ? t("未知") : quality.flags[flag] ? t("是") : t("否")}</p></div>)}
            <div><span>{t("数据来源")}</span><p>{quality.source}</p></div>
            <div><span>{t("观测时间")}</span><p>{formatDateTime(quality.observed_at)}</p></div>
          </> : null}
        </div>
      ) : null}
    </section>
  );
}
