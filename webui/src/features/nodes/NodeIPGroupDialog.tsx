import { useQuery } from "@tanstack/react-query";
import { Globe, RefreshCw, ShieldCheck, X, Zap } from "lucide-react";
import { useEffect } from "react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { OffsetPagination } from "../../components/ui/OffsetPagination";
import { useI18n } from "../../i18n";
import { formatApiErrorMessage } from "../../lib/error-message";
import { formatDateTime } from "../../lib/time";
import { getNodeIPGroup } from "./api";
import type { NodeListFilters, NodeSummary } from "./types";

const flags = ["residential", "datacenter", "vpn", "proxy", "tor", "abuser"] as const;

export function NodeIPGroupDialog({
  groupKey, filters, qualityEnabled, page, onPageChange, onClose, onNode, onEgress, onLatency, onCheckQuality,
}: {
  groupKey: string;
  filters: NodeListFilters;
  qualityEnabled: boolean;
  page: number;
  onPageChange: (page: number) => void;
  onClose: () => void;
  onNode: (hash: string) => void;
  onEgress: (hash: string) => void;
  onLatency: (hash: string) => void;
  onCheckQuality: (hash: string) => void;
}) {
  const { t } = useI18n();
  const pageSize = 20;
  const detail = useQuery({
    queryKey: ["node-ip-group", groupKey, filters, page],
    queryFn: () => getNodeIPGroup(groupKey, filters, pageSize, page * pageSize),
    gcTime: 0,
    retry: false,
    refetchInterval: 30_000,
  });
  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [onClose]);

  const data = detail.isError ? undefined : detail.data;
  const group = data?.group;
  const quality = data?.quality;
  const unresolved = groupKey === "unresolved";
  const title = unresolved ? t("未解析出口 IP") : groupKey;
  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / pageSize));
  const status = (node: NodeSummary) => !node.enabled ? t("禁用") : !node.has_outbound ? t("错误") : node.circuit_open_since ? t("熔断") : t("健康");

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true" aria-label={t("IP 详情 {{ip}}", { ip: title })} onClick={onClose}>
      <Card className="modal-card ip-group-dialog" onClick={(event) => event.stopPropagation()}>
        <div className="drawer-header">
          <div><h3 className="ip-group-address">{title}</h3><p>{t("匹配节点 {{count}}", { count: group?.matched_node_count ?? 0 })}</p></div>
          <Button variant="ghost" size="sm" aria-label={t("关闭详情面板")} onClick={onClose}><X size={16} /></Button>
        </div>
        {detail.isLoading ? <p className="muted">{t("加载中...")}</p> : null}
        {detail.isError ? <div className="callout callout-error">{formatApiErrorMessage(detail.error, t)}</div> : null}
        {group ? (
          <>
            {!unresolved ? (
              <section className="ip-group-quality">
                <div className="ip-group-metrics">
                  <div><span>{t("账户风险（越高风险越大）")}</span><strong>{group.risk ?? t("未知")}</strong></div>
                  <div><span>{t("信誉分（越高越好）")}</span><strong>{group.score ?? t("未知")}</strong></div>
                  <div><span>{t("住宅网络")}</span><strong>{group.residential == null ? t("未知") : group.residential ? t("是") : t("否")}</strong></div>
                  <div><span>ASN</span><strong>{group.asn || t("未知")}</strong></div>
                  <div><span>{t("数据状态")}</span><strong>{group.state === "fresh" ? t("新鲜") : group.state === "stale" ? t("已过期") : t("未知")}</strong></div>
                  <div><span>{t("观测时间")}</span><strong>{group.observed_at ? formatDateTime(group.observed_at) : t("未知")}</strong></div>
                </div>
                {quality ? (
                  <div className="ip-group-evidence">
                    <div><span>{t("信誉等级")}</span><strong>{({ good: t("良好"), moderate: t("一般"), poor: t("较差"), unknown: t("未知") })[quality.status]}</strong></div>
                    <div><span>ISP</span><strong>{quality.isp || t("未知")}</strong></div>
                    <div><span>{t("数据来源")}</span><strong>{quality.source}</strong></div>
                    {flags.map((flag) => <div key={flag}><span>{({ residential: t("住宅网络"), datacenter: t("数据中心"), vpn: "VPN", proxy: t("代理"), tor: "Tor", abuser: t("滥用记录") })[flag]}</span><strong>{quality.flags[flag] == null ? t("未知") : quality.flags[flag] ? t("是") : t("否")}</strong></div>)}
                  </div>
                ) : null}
              </section>
            ) : null}
            <div className="ip-group-nodes-heading"><h4>{t("匹配节点 {{count}}", { count: data?.total ?? 0 })}</h4><Button variant="ghost" size="sm" title={t("刷新")} aria-label={t("刷新")} onClick={() => void detail.refetch()}><RefreshCw size={15} /></Button></div>
            <div className="data-table-wrap">
              <table className="data-table ip-group-node-table">
                <thead><tr><th>{t("节点名")}</th><th>{t("状态")}</th><th>{t("出口 IP")}</th><th>{t("操作")}</th></tr></thead>
                <tbody>{data?.nodes.map((node) => (
                  <tr key={node.node_hash}>
                    <td><button type="button" className="ip-group-node-link" onClick={() => onNode(node.node_hash)}>{node.display_tag || node.tags[0]?.tag || node.node_hash}</button></td>
                    <td><Badge variant={node.enabled && node.has_outbound && !node.circuit_open_since ? "success" : "muted"}>{status(node)}</Badge></td>
                    <td className="ip-group-address">{node.egress_ip || "-"}</td>
                    <td><div className="subscriptions-row-actions">
                      <Button variant="ghost" size="sm" title={t("触发出口探测")} aria-label={t("触发出口探测")} onClick={() => onEgress(node.node_hash)}><Globe size={15} /></Button>
                      <Button variant="ghost" size="sm" title={t("触发延迟探测")} aria-label={t("触发延迟探测")} onClick={() => onLatency(node.node_hash)}><Zap size={15} /></Button>
                      {qualityEnabled && !unresolved && node.egress_ip ? <Button variant="ghost" size="sm" title={t("检查 IP 质量")} aria-label={t("检查 IP 质量")} onClick={() => onCheckQuality(node.node_hash)}><ShieldCheck size={15} /></Button> : null}
                    </div></td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
            <OffsetPagination page={page} totalPages={totalPages} totalItems={data?.total ?? 0} pageSize={pageSize} pageSizeOptions={[pageSize]} onPageChange={onPageChange} onPageSizeChange={() => {}} />
          </>
        ) : null}
      </Card>
    </div>
  );
}
