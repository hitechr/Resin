import { apiRequest } from "../../lib/api-client";
import type {
  EgressProbeResult,
  LatencyProbeResult,
  NodeListQuery,
  NodeIPQuality,
  NodeIPGroupPage,
  NodeIPGroupDetail,
  NodeIPBatchJob,
  NodeListFilters,
  IPGroupListQuery,
  NodeSummary,
  PageResponse,
} from "./types";

const basePath = "/api/v1/nodes";

type ApiNodeSummary = Omit<NodeSummary, "tags"> & {
  tags?: NodeSummary["tags"] | null;
  enabled?: boolean | null;
  display_tag?: string | null;
  last_error?: string | null;
  circuit_open_since?: string | null;
  egress_ip?: string | null;
  reference_latency_ms?: number | null;
  region?: string | null;
  last_egress_update?: string | null;
  last_latency_probe_attempt?: string | null;
  last_authority_latency_probe_attempt?: string | null;
  last_egress_update_attempt?: string | null;
};

function normalizeNode(raw: ApiNodeSummary): NodeSummary {
  const { reference_latency_ms, ...rest } = raw;
  const normalized: NodeSummary = {
    ...rest,
    enabled: raw.enabled !== false,
    display_tag: raw.display_tag || "",
    tags: Array.isArray(raw.tags) ? raw.tags : [],
    last_error: raw.last_error || "",
    circuit_open_since: raw.circuit_open_since || "",
    egress_ip: raw.egress_ip || "",
    region: raw.region || "",
    last_egress_update: raw.last_egress_update || "",
    last_latency_probe_attempt: raw.last_latency_probe_attempt || "",
    last_authority_latency_probe_attempt: raw.last_authority_latency_probe_attempt || "",
    last_egress_update_attempt: raw.last_egress_update_attempt || "",
  };

  // Backend uses `omitempty`; field missing means "no reference latency".
  if (typeof reference_latency_ms === "number") {
    normalized.reference_latency_ms = reference_latency_ms;
  }

  return normalized;
}

function nodeFiltersQuery(filters: NodeListFilters): URLSearchParams {
  const query = new URLSearchParams();
  const appendIfNotEmpty = (key: string, value?: string) => {
    if (value?.trim()) {
      query.set(key, value.trim());
    }
  };
  appendIfNotEmpty("platform_id", filters.platform_id);
  appendIfNotEmpty("subscription_id", filters.subscription_id);
  appendIfNotEmpty("tag_keyword", filters.tag_keyword);
  appendIfNotEmpty("region", filters.region?.toLowerCase());
  appendIfNotEmpty("egress_ip", filters.egress_ip);
  appendIfNotEmpty("probed_since", filters.probed_since);
  if (filters.circuit_open !== undefined) query.set("circuit_open", String(filters.circuit_open));
  if (filters.has_outbound !== undefined) query.set("has_outbound", String(filters.has_outbound));
  if (filters.enabled !== undefined) query.set("enabled", String(filters.enabled));
  return query;
}

export async function listNodes(filters: NodeListQuery): Promise<PageResponse<NodeSummary>> {
  const query = nodeFiltersQuery(filters);
  query.set("limit", String(filters.limit ?? 50));
  query.set("offset", String(filters.offset ?? 0));
  query.set("sort_by", filters.sort_by || "tag");
  query.set("sort_order", filters.sort_order || "asc");

  const data = await apiRequest<PageResponse<ApiNodeSummary>>(`${basePath}?${query.toString()}`);
  return {
    ...data,
    items: data.items.map(normalizeNode),
  };
}

export async function listNodeIPGroups(filters: IPGroupListQuery): Promise<NodeIPGroupPage> {
  const query = nodeFiltersQuery(filters);
  query.set("sort_by", filters.sort_by ?? "risk");
  query.set("sort_order", filters.sort_order ?? "desc");
  query.set("limit", String(filters.limit ?? 50));
  query.set("offset", String(filters.offset ?? 0));
  return apiRequest<NodeIPGroupPage>(`/api/v1/node-ip-groups?${query.toString()}`);
}

export async function getNodeIPGroup(key: string, filters: NodeListFilters, limit: number, offset: number): Promise<NodeIPGroupDetail> {
  const query = nodeFiltersQuery(filters);
  query.set("limit", String(limit));
  query.set("offset", String(offset));
  const data = await apiRequest<Omit<NodeIPGroupDetail, "nodes"> & { nodes: ApiNodeSummary[] }>(
    `/api/v1/node-ip-groups/${encodeURIComponent(key)}?${query.toString()}`
  );
  return { ...data, nodes: data.nodes.map(normalizeNode) };
}

export async function startNodeIPBatch(filters: NodeListFilters): Promise<NodeIPBatchJob> {
  return apiRequest<NodeIPBatchJob>(`/api/v1/node-ip-batches?${nodeFiltersQuery(filters).toString()}`, { method: "POST" });
}

export async function getNodeIPBatch(id: string): Promise<NodeIPBatchJob> {
  return apiRequest<NodeIPBatchJob>(`/api/v1/node-ip-batches/${encodeURIComponent(id)}`);
}

export async function cancelNodeIPBatch(id: string): Promise<NodeIPBatchJob> {
  return apiRequest<NodeIPBatchJob>(`/api/v1/node-ip-batches/${encodeURIComponent(id)}/cancel`, { method: "POST" });
}

export async function getNode(hash: string): Promise<NodeSummary> {
  const data = await apiRequest<ApiNodeSummary>(`${basePath}/${hash}`);
  return normalizeNode(data);
}

export async function getNodeIPQuality(hash: string): Promise<NodeIPQuality> {
  return apiRequest<NodeIPQuality>(`${basePath}/${hash}/ip-quality`);
}

export async function checkNodeIPQuality(hash: string): Promise<NodeIPQuality> {
  return apiRequest<NodeIPQuality>(`${basePath}/${hash}/actions/check-ip-quality`, { method: "POST" });
}

export async function probeEgress(hash: string): Promise<EgressProbeResult> {
  return apiRequest<EgressProbeResult>(`${basePath}/${hash}/actions/probe-egress`, {
    method: "POST",
  });
}

export async function probeLatency(hash: string): Promise<LatencyProbeResult> {
  return apiRequest<LatencyProbeResult>(`${basePath}/${hash}/actions/probe-latency`, {
    method: "POST",
  });
}
