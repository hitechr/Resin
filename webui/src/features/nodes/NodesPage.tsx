import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { AlertTriangle, Eraser, RefreshCw, Sparkles, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { useLocation } from "react-router-dom";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { DataTable } from "../../components/ui/DataTable";
import { Card } from "../../components/ui/Card";
import { Input } from "../../components/ui/Input";
import { OffsetPagination } from "../../components/ui/OffsetPagination";
import { Select } from "../../components/ui/Select";
import { ToastContainer } from "../../components/ui/Toast";
import { useToast } from "../../hooks/useToast";
import { useI18n } from "../../i18n";
import { formatApiErrorMessage } from "../../lib/error-message";
import { formatDateTime, formatRelativeTime } from "../../lib/time";
import { listPlatforms } from "../platforms/api";
import type { Platform } from "../platforms/types";
import { listSubscriptions } from "../subscriptions/api";
import { getEnvConfig } from "../systemConfig/api";
import { NodeIPBatchStatus } from "./NodeIPBatchStatus";
import { NodeIPGroupDialog } from "./NodeIPGroupDialog";
import { NodeIPQualityPanel } from "./NodeIPQualityPanel";
import { checkNodeIPQuality, getNode, listNodeIPGroups, probeEgress, probeLatency } from "./api";
import type { NodeIPGroup, NodeSummary } from "./types";
import { getAllRegions, getRegionName } from "./regions";
import type { NodeIPGroupSortBy, NodeListFilters, SortOrder } from "./types";

type NodeStatusFilter = "all" | "healthy" | "circuit_open" | "error" | "disabled";
type NodeDisplayStatus = "healthy" | "circuit_open" | "pending_test" | "error" | "disabled";
type ProbeAction = "egress" | "latency";

type NodeFilterDraft = {
  platform_id: string;
  subscription_id: string;
  tag_keyword: string;
  region: string;
  egress_ip: string;
  status: NodeStatusFilter;
};

const defaultFilterDraft: NodeFilterDraft = {
  platform_id: "",
  subscription_id: "",
  tag_keyword: "",
  region: "",
  egress_ip: "",
  status: "all",
};

const PAGE_SIZE_OPTIONS = [20, 50, 100, 200, 500, 1000, 2000, 5000] as const;
const EMPTY_PLATFORMS: Platform[] = [];
const NODE_FILTER_ITEM_STYLE: CSSProperties = {
  flex: "1 1 120px",
  minWidth: "80px",
  display: "flex",
  flexDirection: "column",
  gap: "0.25rem",
};
const NODE_FILTER_CONTROL_STYLE: CSSProperties = {
  width: "100%",
  padding: "4px 8px",
  fontSize: "0.875rem",
  minHeight: "32px",
  height: "32px",
};

function parseBoolParam(value: string | null): boolean | undefined {
  if (value === null) {
    return undefined;
  }

  const normalized = value.trim().toLowerCase();
  if (normalized === "true" || normalized === "1") {
    return true;
  }
  if (normalized === "false" || normalized === "0") {
    return false;
  }

  return undefined;
}

function parseStatusParam(value: string | null): NodeStatusFilter | undefined {
  if (value === null) {
    return undefined;
  }

  const normalized = value.trim().toLowerCase();
  if (normalized === "all" || normalized === "healthy" || normalized === "circuit_open" || normalized === "error" || normalized === "disabled") {
    return normalized;
  }

  return undefined;
}

function statusFromQuery(params: URLSearchParams): NodeStatusFilter {
  const explicitStatus = parseStatusParam(params.get("status"));
  if (explicitStatus) {
    return explicitStatus;
  }

  const hasOutbound = parseBoolParam(params.get("has_outbound"));
  const circuitOpen = parseBoolParam(params.get("circuit_open"));
  const enabled = parseBoolParam(params.get("enabled"));

  if (enabled === false) {
    return "disabled";
  }

  if (hasOutbound === false) {
    return "error";
  }
  if (hasOutbound === true && circuitOpen === true) {
    return "circuit_open";
  }
  if (hasOutbound === true && circuitOpen === false) {
    return "healthy";
  }

  return "all";
}

function trimQueryValue(params: URLSearchParams, key: string): string {
  return params.get(key)?.trim() ?? "";
}

function draftFromQuery(search: string): NodeFilterDraft {
  const params = new URLSearchParams(search);
  const tagKeyword = trimQueryValue(params, "tag_keyword") || trimQueryValue(params, "tag");

  return {
    platform_id: trimQueryValue(params, "platform_id"),
    subscription_id: trimQueryValue(params, "subscription_id"),
    tag_keyword: tagKeyword,
    region: trimQueryValue(params, "region").toUpperCase(),
    egress_ip: trimQueryValue(params, "egress_ip"),
    status: statusFromQuery(params),
  };
}



function draftToActiveFilters(draft: NodeFilterDraft): NodeListFilters {
  let circuit_open: boolean | undefined = undefined;
  let has_outbound: boolean | undefined = undefined;
  let enabled: boolean | undefined = undefined;

  switch (draft.status) {
    case "healthy":
      enabled = true;
      has_outbound = true;
      circuit_open = false;
      break;
    case "circuit_open":
      enabled = true;
      has_outbound = true;
      circuit_open = true;
      break;
    case "error":
      enabled = true;
      has_outbound = false;
      break;
    case "disabled":
      enabled = false;
      break;
    case "all":
    default:
      break;
  }

  return {
    platform_id: draft.platform_id,
    subscription_id: draft.subscription_id,
    tag_keyword: draft.tag_keyword,
    region: draft.region,
    egress_ip: draft.egress_ip,
    enabled,
    circuit_open,
    has_outbound,
  };
}

function firstTag(node: { display_tag?: string; tags: { tag: string }[] }): string {
  if (node.display_tag && node.display_tag.trim()) {
    return node.display_tag;
  }
  if (!node.tags.length) {
    return "-";
  }
  return node.tags[0].tag;
}

function hasReferenceLatency(node: NodeSummary): node is NodeSummary & { reference_latency_ms: number } {
  return typeof node.reference_latency_ms === "number";
}

function isPendingTestNode(node: NodeSummary): boolean {
  return Boolean(node.circuit_open_since) && node.failure_count === 0;
}

function getNodeDisplayStatus(node: NodeSummary): NodeDisplayStatus {
  if (!node.enabled) {
    return "disabled";
  }
  if (!node.has_outbound) {
    return "error";
  }
  if (isPendingTestNode(node)) {
    return "pending_test";
  }
  if (node.circuit_open_since) {
    return "circuit_open";
  }
  return "healthy";
}

function referenceLatencyColor(latencyMs: number): string {
  if (!Number.isFinite(latencyMs)) {
    return "var(--text-secondary)";
  }
  if (latencyMs <= 400) {
    return "var(--success)";
  }
  if (latencyMs <= 1000) {
    return "var(--warning)";
  }
  return "var(--danger)";
}

function displayableReferenceLatencyMs(node: NodeSummary): number | null {
  if (getNodeDisplayStatus(node) !== "healthy") {
    return null;
  }
  if (!hasReferenceLatency(node)) {
    return null;
  }
  return node.reference_latency_ms;
}


function formatLatency(value: number): string {
  if (!Number.isFinite(value)) {
    return "-";
  }
  return `${value.toFixed(0)} ms`;
}

function sortIndicator(active: boolean, order: SortOrder): string {
  if (!active) {
    return "↕";
  }
  return order === "asc" ? "▲" : "▼";
}

function regionToFlag(region: string | undefined): string {
  if (!region || region.length !== 2) {
    return region || "-";
  }
  const code = region.toUpperCase();
  const flag = String.fromCodePoint(...[...code].map((c) => c.charCodeAt(0) + 127397));
  const name = getRegionName(code);
  return name ? `${flag} ${code} (${name})` : `${flag} ${code}`;
}

export function NodesPage() {
  const { locale, t } = useI18n();
  const location = useLocation();
  const [draftFilters, setDraftFilters] = useState<NodeFilterDraft>(() => draftFromQuery(location.search));
  const [activeFilters, setActiveFilters] = useState<NodeListFilters>(() =>
    draftToActiveFilters(draftFromQuery(location.search))
  );
  const [sortBy, setSortBy] = useState<NodeIPGroupSortBy>("risk");
  const [sortOrder, setSortOrder] = useState<SortOrder>("desc");
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState<number>(200);
  const [selectedGroupKey, setSelectedGroupKey] = useState("");
  const [groupDetailPage, setGroupDetailPage] = useState(0);
  const [selectedNodeHash, setSelectedNodeHash] = useState("");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [pendingEgressHashes, setPendingEgressHashes] = useState<Set<string>>(() => new Set());
  const [pendingLatencyHashes, setPendingLatencyHashes] = useState<Set<string>>(() => new Set());
  const { toasts, showToast, dismissToast } = useToast();
  const pendingEgressHashesRef = useRef<Set<string>>(new Set());
  const pendingLatencyHashesRef = useRef<Set<string>>(new Set());

  const queryClient = useQueryClient();

  const allRegions = useMemo(() => getAllRegions(), [locale]);

  const platformsQuery = useQuery({
    queryKey: ["platforms", "all"],
    queryFn: async () => {
      const data = await listPlatforms({
        limit: 100000,
        offset: 0,
      });
      return data.items;
    },
    staleTime: 60_000,
  });
  const platforms = platformsQuery.data ?? EMPTY_PLATFORMS;

  const subscriptionsQuery = useQuery({
    queryKey: ["subscriptions", "all"],
    queryFn: async () => {
      const data = await listSubscriptions({
        limit: 100000,
        offset: 0,
      });
      return data.items;
    },
    staleTime: 60_000,
  });
  const subscriptions = subscriptionsQuery.data ?? [];

  const groupsQuery = useQuery({
    queryKey: ["node-ip-groups", activeFilters, sortBy, sortOrder, page, pageSize],
    queryFn: () =>
      listNodeIPGroups({
        ...activeFilters,
        sort_by: sortBy,
        sort_order: sortOrder,
        limit: pageSize,
        offset: page * pageSize,
      }),
    refetchInterval: 30_000,
    placeholderData: (prev) => prev,
  });

  const groupsPage = groupsQuery.data ?? {
    items: [], total: 0, public_ip_count: 0, pending_quality_count: 0,
    queue_pending: 0, background_deferred: 0,
    limit: pageSize, offset: page * pageSize,
  };
  const groups = groupsPage.items;
  const totalPages = Math.max(1, Math.ceil(groupsPage.total / pageSize));
  const selectedHash = selectedNodeHash;

  const ipQualityConfigQuery = useQuery({
    queryKey: ["system", "config", "env"],
    queryFn: getEnvConfig,
    staleTime: Infinity,
  });

  const nodeDetailQuery = useQuery({
    queryKey: ["node", selectedHash],
    queryFn: () => getNode(selectedHash),
    enabled: Boolean(selectedHash) && drawerOpen,
    refetchInterval: 30_000,
  });

  const detailNode = nodeDetailQuery.data;
  const drawerVisible = drawerOpen && Boolean(detailNode);

  useEffect(() => {
    if (!drawerVisible) {
      return;
    }

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") {
        return;
      }
      setDrawerOpen(false);
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [drawerVisible]);

  const openGroup = (key: string) => {
    setGroupDetailPage(0);
    setSelectedGroupKey(key);
  };

  const openDrawer = (hash: string) => {
    setSelectedNodeHash(hash);
    setDrawerOpen(true);
  };

  const refreshNodes = async () => {
    await queryClient.invalidateQueries({ queryKey: ["node-ip-groups"] });
    await queryClient.invalidateQueries({ queryKey: ["node-ip-group"] });
    if (selectedHash) {
      await queryClient.invalidateQueries({ queryKey: ["node", selectedHash] });
    }
  };

  const probeEgressMutation = useMutation({
    mutationFn: async (hash: string) => probeEgress(hash),
    onSuccess: async (result) => {
      await refreshNodes();
      showToast(
        "success",
        t("出口探测完成：出口 IP={{ip}}，区域={{region}}，延迟={{latency}}", {
          ip: result.egress_ip || "-",
          region: result.region || "-",
          latency: formatLatency(result.latency_ewma_ms),
        })
      );
    },
    onError: async (error) => {
      await refreshNodes();
      showToast("error", formatApiErrorMessage(error, t));
    },
  });

  const probeLatencyMutation = useMutation({
    mutationFn: async (hash: string) => probeLatency(hash),
    onSuccess: async (result) => {
      await refreshNodes();
      showToast("success", t("延迟探测完成：延迟={{latency}}", { latency: formatLatency(result.latency_ewma_ms) }));
    },
    onError: async (error) => {
      await refreshNodes();
      showToast("error", formatApiErrorMessage(error, t));
    },
  });

  const checkIPQualityMutation = useMutation({
    mutationFn: (hash: string) => checkNodeIPQuality(hash),
    onSuccess: async (_result, hash) => {
      await refreshNodes();
      await queryClient.invalidateQueries({ queryKey: ["node-ip-quality", hash] });
    },
    onError: (error) => showToast("error", formatApiErrorMessage(error, t)),
  });

  const markProbePending = (hash: string, action: ProbeAction): boolean => {
    if (action === "egress") {
      if (pendingEgressHashesRef.current.has(hash)) {
        return false;
      }
      const next = new Set(pendingEgressHashesRef.current);
      next.add(hash);
      pendingEgressHashesRef.current = next;
      setPendingEgressHashes(next);
      return true;
    }

    if (pendingLatencyHashesRef.current.has(hash)) {
      return false;
    }
    const next = new Set(pendingLatencyHashesRef.current);
    next.add(hash);
    pendingLatencyHashesRef.current = next;
    setPendingLatencyHashes(next);
    return true;
  };

  const clearProbePending = (hash: string, action: ProbeAction) => {
    if (action === "egress") {
      if (!pendingEgressHashesRef.current.has(hash)) {
        return;
      }
      const next = new Set(pendingEgressHashesRef.current);
      next.delete(hash);
      pendingEgressHashesRef.current = next;
      setPendingEgressHashes(next);
      return;
    }

    if (!pendingLatencyHashesRef.current.has(hash)) {
      return;
    }
    const next = new Set(pendingLatencyHashesRef.current);
    next.delete(hash);
    pendingLatencyHashesRef.current = next;
    setPendingLatencyHashes(next);
  };

  const isProbePending = (hash: string, action: ProbeAction): boolean =>
    action === "egress" ? pendingEgressHashes.has(hash) : pendingLatencyHashes.has(hash);

  const runProbeEgress = async (hash: string) => {
    if (!markProbePending(hash, "egress")) {
      return;
    }
    try {
      await probeEgressMutation.mutateAsync(hash);
    } catch {
      // Mutation callbacks already surface the failure to the user.
    } finally {
      clearProbePending(hash, "egress");
    }
  };

  const runProbeLatency = async (hash: string) => {
    if (!markProbePending(hash, "latency")) {
      return;
    }
    try {
      await probeLatencyMutation.mutateAsync(hash);
    } catch {
      // Mutation callbacks already surface the failure to the user.
    } finally {
      clearProbePending(hash, "latency");
    }
  };

  const handleFilterChange = (key: keyof NodeFilterDraft, value: string) => {
    setDraftFilters((prev) => {
      const next = { ...prev, [key]: value };
      setActiveFilters(draftToActiveFilters(next));
      setSelectedGroupKey("");
      setGroupDetailPage(0);
      setSelectedNodeHash("");
      setDrawerOpen(false);
      setPage(0);
      return next;
    });
  };

  const resetFilters = () => {
    setDraftFilters(defaultFilterDraft);
    setActiveFilters(draftToActiveFilters(defaultFilterDraft));
    setSelectedGroupKey("");
    setGroupDetailPage(0);
    setSelectedNodeHash("");
    setDrawerOpen(false);
    setPage(0);
  };

  const changeSort = (target: NodeIPGroupSortBy) => {
    if (sortBy === target) {
      setSortOrder((prev) => (prev === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(target);
      setSortOrder(target === "risk" ? "desc" : "asc");
    }
    setPage(0);
  };

  const changePageSize = (next: number) => {
    setPageSize(next);
    setPage(0);
  };

  const col = createColumnHelper<NodeIPGroup>();

  const groupColumns = [
    col.accessor("ip", {
      header: () => <button type="button" className="table-sort-btn" onClick={() => changeSort("ip")}>{t("出口 IP")} <span>{sortIndicator(sortBy === "ip", sortOrder)}</span></button>,
      cell: (info) => <button type="button" className="ip-group-address-btn" onClick={(event) => { event.stopPropagation(); openGroup(info.row.original.key); }}>{info.getValue() || t("未解析出口 IP")}</button>,
    }),
    col.accessor("region", {
      header: () => <button type="button" className="table-sort-btn" onClick={() => changeSort("region")}>{t("区域")} <span>{sortIndicator(sortBy === "region", sortOrder)}</span></button>,
      cell: (info) => {
        const val = regionToFlag(info.getValue());
        return (
          <div style={{ maxWidth: "100px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={val}>
            {val}
          </div>
        );
      },
    }),
    col.accessor("matched_node_count", {
      header: () => <button type="button" className="table-sort-btn" onClick={() => changeSort("matched_nodes")}>{t("匹配节点数")} <span>{sortIndicator(sortBy === "matched_nodes", sortOrder)}</span></button>,
    }),
    col.accessor("risk", {
      header: () => <button type="button" className="table-sort-btn" onClick={() => changeSort("risk")}>{t("账户风险（越高风险越大）")} <span>{sortIndicator(sortBy === "risk", sortOrder)}</span></button>,
      cell: (info) => info.getValue() ?? t("未知"),
    }),
    col.accessor("score", {
      header: () => <button type="button" className="table-sort-btn" onClick={() => changeSort("score")}>{t("信誉分（越高越好）")} <span>{sortIndicator(sortBy === "score", sortOrder)}</span></button>,
      cell: (info) => info.getValue() ?? t("未知"),
    }),
    col.accessor("residential", {
      header: t("住宅网络"),
      cell: (info) => info.getValue() == null ? t("未知") : info.getValue() ? t("是") : t("否"),
    }),
    col.accessor("asn", {
      header: "ASN",
      cell: (info) => <span className="ip-group-asn" title={info.getValue()}>{info.getValue() || t("未知")}</span>,
    }),
    col.accessor("state", {
      header: t("数据状态"),
      cell: (info) => {
        const state = info.getValue();
        if (state === "fresh") return <Badge variant="success">{t("新鲜")}</Badge>;
        if (state === "stale") return <Badge variant="warning">{t("已过期")}</Badge>;
        return <Badge variant="muted">{t("未知")}</Badge>;
      },
    }),
  ];

  return (
    <section className="nodes-page">
      <header className="module-header">
        <div>
          <h2>{t("节点池")}</h2>
        </div>
      </header>

      <ToastContainer toasts={toasts} onDismiss={dismissToast} />

      <Card className="filter-card platform-list-card platform-directory-card">
        <div className="list-card-header">
          <div>
            <h3>{t("IP 列表")}</h3>
            <p>{t("共 {{total}} 个 IP 分组，{{public}} 个公网 IP", { total: groupsPage.total, public: groupsPage.public_ip_count })}</p>
            {groupsPage.background_deferred > 0 ? <p className="ip-batch-error">{t("后台检查未排队 {{count}} 次", { count: groupsPage.background_deferred })}</p> : null}
          </div>

          <div
            className="nodes-inline-filters"
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: "0.5rem",
              alignItems: "flex-end",
            }}
          >
            <div style={NODE_FILTER_ITEM_STYLE}>
              <label htmlFor="node-tag-keyword" style={{ fontSize: "0.75rem", color: "var(--text-secondary)" }}>
                {t("节点名")}
              </label>
              <Input
                id="node-tag-keyword"
                value={draftFilters.tag_keyword}
                onChange={(event) => handleFilterChange("tag_keyword", event.target.value)}
                placeholder={t("模糊搜索")}
                style={NODE_FILTER_CONTROL_STYLE}
              />
            </div>

            <div style={NODE_FILTER_ITEM_STYLE}>
              <label htmlFor="node-platform-id" style={{ fontSize: "0.75rem", color: "var(--text-secondary)" }}>
                {t("被此平台路由")}
              </label>
              <Select
                id="node-platform-id"
                value={draftFilters.platform_id}
                onChange={(event) => handleFilterChange("platform_id", event.target.value)}
                style={NODE_FILTER_CONTROL_STYLE}
              >
                <option value="">{t("无限制")}</option>
                {platforms.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </div>

            <div style={NODE_FILTER_ITEM_STYLE}>
              <label htmlFor="node-subscription-id" style={{ fontSize: "0.75rem", color: "var(--text-secondary)" }}>
                {t("来自此订阅")}
              </label>
              <Select
                id="node-subscription-id"
                value={draftFilters.subscription_id}
                onChange={(event) => handleFilterChange("subscription_id", event.target.value)}
                style={NODE_FILTER_CONTROL_STYLE}
              >
                <option value="">{t("全部")}</option>
                {subscriptions.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </div>

            <div style={NODE_FILTER_ITEM_STYLE}>
              <label htmlFor="node-region" style={{ fontSize: "0.75rem", color: "var(--text-secondary)" }}>
                {t("区域")}
              </label>
              <Select
                id="node-region"
                value={draftFilters.region}
                onChange={(event) => handleFilterChange("region", event.target.value)}
                style={NODE_FILTER_CONTROL_STYLE}
              >
                <option value="">{t("全部")}</option>
                {allRegions.map((r) => (
                  <option key={r.code} value={r.code}>
                    {r.name}
                  </option>
                ))}
              </Select>
            </div>

            <div style={NODE_FILTER_ITEM_STYLE}>
              <label htmlFor="node-egress-ip" style={{ fontSize: "0.75rem", color: "var(--text-secondary)" }}>
                {t("出口 IP")}
              </label>
              <Input
                id="node-egress-ip"
                value={draftFilters.egress_ip}
                onChange={(event) => handleFilterChange("egress_ip", event.target.value)}
                placeholder="IP / CIDR"
                style={NODE_FILTER_CONTROL_STYLE}
              />
            </div>

            <div style={NODE_FILTER_ITEM_STYLE}>
              <label htmlFor="node-status" style={{ fontSize: "0.75rem", color: "var(--text-secondary)" }}>
                {t("状态")}
              </label>
              <Select
                id="node-status"
                value={draftFilters.status}
                onChange={(event) => handleFilterChange("status", event.target.value)}
                style={NODE_FILTER_CONTROL_STYLE}
              >
                <option value="all">{t("全部")}</option>
                <option value="healthy">{t("健康")}</option>
                <option value="circuit_open">{t("熔断 / 待测")}</option>
                <option value="error">{t("错误")}</option>
                <option value="disabled">{t("禁用")}</option>
              </Select>
            </div>

            <div style={{ display: "flex", gap: "0.5rem", marginBottom: "0.125rem", marginLeft: "auto", alignItems: "center", flexWrap: "wrap" }}>
              <NodeIPBatchStatus filters={activeFilters} enabled={Boolean(ipQualityConfigQuery.data?.ip_quality_enabled) && !groupsQuery.isPlaceholderData && !groupsQuery.isLoading} estimated={groupsPage.pending_quality_count} />
              <Button size="sm" variant="secondary" onClick={refreshNodes} disabled={groupsQuery.isFetching} style={{ minHeight: "32px", height: "32px", padding: "0 0.75rem", display: "flex", alignItems: "center", gap: "0.25rem" }}>
                <RefreshCw size={16} className={groupsQuery.isFetching ? "spin" : undefined} />
                {t("刷新")}
              </Button>
              <Button size="sm" variant="secondary" onClick={resetFilters} style={{ minHeight: "32px", height: "32px", padding: "0 0.75rem", display: "flex", alignItems: "center", gap: "0.25rem" }}>
                <Eraser size={16} />
                {t("重置")}
              </Button>
            </div>
          </div>
        </div>
      </Card>

      <Card className="nodes-table-card platform-cards-container subscriptions-table-card">
        {groupsQuery.isLoading ? <p className="muted">{t("正在加载节点数据...")}</p> : null}

        {groupsQuery.isError ? (
          <div className="callout callout-error">
            <AlertTriangle size={14} />
            <span>{formatApiErrorMessage(groupsQuery.error, t)}</span>
          </div>
        ) : null}

        {!groupsQuery.isLoading && !groups.length ? (
          <div className="empty-box">
            <Sparkles size={16} />
            <p>{t("没有匹配的节点")}</p>
          </div>
        ) : null}

        {groups.length ? (
          <DataTable
            data={groups}
            columns={groupColumns}
            onRowClick={(group) => openGroup(group.key)}
            getRowId={(group) => group.key}
          />
        ) : null}

        <OffsetPagination
          page={page}
          totalPages={totalPages}
          totalItems={groupsPage.total}
          pageSize={pageSize}
          pageSizeOptions={PAGE_SIZE_OPTIONS}
          onPageChange={setPage}
          onPageSizeChange={changePageSize}
        />
      </Card>

      {selectedGroupKey && !drawerOpen ? (
        <NodeIPGroupDialog
          groupKey={selectedGroupKey}
          filters={activeFilters}
          qualityEnabled={Boolean(ipQualityConfigQuery.data?.ip_quality_enabled)}
          page={groupDetailPage}
          onPageChange={setGroupDetailPage}
          onClose={() => setSelectedGroupKey("")}
          onNode={openDrawer}
          onEgress={(hash) => void runProbeEgress(hash)}
          onLatency={(hash) => void runProbeLatency(hash)}
          onCheckQuality={(hash) => checkIPQualityMutation.mutate(hash)}
        />
      ) : null}

      {drawerOpen && !drawerVisible ? (
        <div className="modal-overlay" role="dialog" aria-modal="true" aria-label={t("节点详情")} onClick={() => setDrawerOpen(false)}>
          <Card className="modal-card" onClick={(event) => event.stopPropagation()}>
            <div className="drawer-header"><h3>{t("节点详情")}</h3><Button variant="ghost" size="sm" aria-label={t("关闭详情面板")} onClick={() => setDrawerOpen(false)}><X size={16} /></Button></div>
            {nodeDetailQuery.isError ? <div className="callout callout-error">{formatApiErrorMessage(nodeDetailQuery.error, t)}</div> : <p className="muted">{t("加载中...")}</p>}
          </Card>
        </div>
      ) : null}

      {drawerVisible && detailNode ? (
        <div
          className="drawer-overlay"
          role="dialog"
          aria-modal="true"
          aria-label={t("节点详情 {{name}}", { name: firstTag(detailNode) })}
          onClick={() => setDrawerOpen(false)}
        >
          <Card className="drawer-panel" onClick={(event) => event.stopPropagation()}>
            <div className="drawer-header">
              <div>
                <h3>{firstTag(detailNode)}</h3>
                <p>{detailNode.node_hash}</p>
              </div>
              <div className="drawer-header-actions">
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("关闭详情面板")}
                  onClick={() => setDrawerOpen(false)}
                >
                  <X size={16} />
                </Button>
              </div>
            </div>

            <div className="platform-drawer-layout">
              <section className="platform-drawer-section">
                <div className="platform-drawer-section-head">
                  <h4>{t("节点状态")}</h4>
                  <p>{t("节点的网络出口、探测状态以及失败历史。")}</p>
                </div>

                <div className="stats-grid">
                  <div>
                    <span>{t("创建时间")}</span>
                    <p>{formatDateTime(detailNode.created_at)}</p>
                  </div>
                  <div>
                    <span>{t("连续失败")}</span>
                    <p>{!detailNode.has_outbound ? "-" : detailNode.failure_count}</p>
                  </div>
                  <div>
                    <span>{t("状态")}</span>
                    <div>
                      {(() => {
                        const status = getNodeDisplayStatus(detailNode);
                        return (
                          <div style={{ display: "flex", alignItems: "baseline", gap: "4px", flexWrap: "wrap" }}>
                            {status === "error" ? (
                              <Badge variant="danger">{t("错误")}</Badge>
                            ) : status === "disabled" ? (
                              <Badge variant="neutral">{t("禁用")}</Badge>
                            ) : status === "pending_test" ? (
                              <Badge variant="muted">{t("待测")}</Badge>
                            ) : status === "circuit_open" ? (
                              <Badge variant="warning">{t("熔断")}</Badge>
                            ) : (
                              <Badge variant="success">{t("健康")}</Badge>
                            )}
                            {(status === "circuit_open" || status === "pending_test") && detailNode.circuit_open_since ? (
                              <span
                                style={{
                                  fontSize: "11px",
                                  color: "var(--text-muted)",
                                  fontWeight: "normal",
                                }}
                              >
                                ({formatRelativeTime(detailNode.circuit_open_since)})
                              </span>
                            ) : null}
                          </div>
                        );
                      })()}
                    </div>
                  </div>
                  <div>
                    <span>{t("出口 / 区域")}</span>
                    <p>
                      {detailNode.egress_ip || "-"} / {regionToFlag(detailNode.region)}
                    </p>
                  </div>
                  <div>
                    <span>{t("参考延迟")}</span>
                    {(() => {
                      const latencyMs = displayableReferenceLatencyMs(detailNode);
                      if (latencyMs === null) {
                        return <p>-</p>;
                      }
                      return <p style={{ color: referenceLatencyColor(latencyMs) }}>{formatLatency(latencyMs)}</p>;
                    })()}
                  </div>
                  <div>
                    <span>{t("上次探测")}</span>
                    <p>{formatDateTime(detailNode.last_latency_probe_attempt || "")}</p>
                  </div>
                </div>

                {detailNode.last_error ? (
                  <div className="callout callout-error">{t("最近错误：{{message}}", { message: detailNode.last_error })}</div>
                ) : null}
              </section>

              {ipQualityConfigQuery.data?.ip_quality_enabled ? <NodeIPQualityPanel key={`${detailNode.node_hash}:${detailNode.egress_ip}`} node={detailNode} /> : null}

              <section className="platform-drawer-section">
                <div className="platform-drawer-section-head">
                  <h4>{t("节点别名")}</h4>
                </div>
                {!detailNode.tags.length ? (
                  <p className="muted">{t("无节点名信息")}</p>
                ) : (
                  <div className="tag-list">
                    {detailNode.tags.map((tag) => (
                      <div key={`${tag.subscription_id}:${tag.tag}`} className="tag-item">
                        <p>{tag.tag}</p>
                        <span>{tag.subscription_name}</span>
                        <code>{tag.subscription_id}</code>
                      </div>
                    ))}
                  </div>
                )}
              </section>

              <section className="platform-drawer-section platform-ops-section">
                <div className="platform-drawer-section-head">
                  <h4>{t("运维操作")}</h4>
                </div>
                <div className="platform-ops-list">
                  <div className="platform-op-item">
                    <div className="platform-op-copy">
                      <h5>{t("出口探测")}</h5>
                      <p className="platform-op-hint">{t("检查节点当前出口 IP。")}</p>
                    </div>
                    <Button
                      variant="secondary"
                      onClick={() => void runProbeEgress(detailNode.node_hash)}
                      disabled={isProbePending(detailNode.node_hash, "egress")}
                    >
                      {isProbePending(detailNode.node_hash, "egress") ? t("探测中...") : t("触发出口探测")}
                    </Button>
                  </div>
                  <div className="platform-op-item">
                    <div className="platform-op-copy">
                      <h5>{t("延迟探测")}</h5>
                      <p className="platform-op-hint">{t("检测节点网络延迟。")}</p>
                    </div>
                    <Button
                      variant="secondary"
                      onClick={() => void runProbeLatency(detailNode.node_hash)}
                      disabled={isProbePending(detailNode.node_hash, "latency")}
                    >
                      {isProbePending(detailNode.node_hash, "latency") ? t("探测中...") : t("触发延迟探测")}
                    </Button>
                  </div>
                </div>
              </section>
            </div>
          </Card>
        </div>
      ) : null}
    </section>
  );
}
