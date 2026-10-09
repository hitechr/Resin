export type IPQualityResult = {
  ip: string;
  score: number | null;
  status: "good" | "moderate" | "poor" | "unknown";
  isp: string;
  asn: string;
  flags: Record<"residential" | "datacenter" | "vpn" | "proxy" | "tor" | "abuser", boolean | null>;
  source: string;
  observed_at: string;
  expires_at: string;
};

export type NodeIPQuality = {
  state: "disabled" | "no_egress_ip" | "not_checked" | "fresh" | "stale";
  egress_ip?: string;
  quality?: IPQualityResult;
};

export type NodeIPGroup = {
  key: string;
  ip: string;
  region: string;
  matched_node_count: number;
  score: number | null;
  risk: number | null;
  risk_version: string;
  residential: boolean | null;
  asn: string;
  state: "fresh" | "stale" | "unknown" | "unresolved";
  observed_at: string | null;
};

export type NodeIPGroupPage = {
  items: NodeIPGroup[];
  total: number;
  public_ip_count: number;
  pending_quality_count: number;
  queue_pending: number;
  background_deferred: number;
  limit: number;
  offset: number;
};

export type NodeIPGroupDetail = {
  group: NodeIPGroup;
  quality: IPQualityResult | null;
  nodes: NodeSummary[];
  total: number;
  limit: number;
  offset: number;
};

export type NodeIPBatchJob = {
  id: string;
  source: "manual" | "automatic";
  total: number;
  fresh_skipped: number;
  completed: number;
  failed: number;
  deferred: number;
  canceled: boolean;
  started_at: string;
  ended_at?: string;
  error_summary?: string;
};

export type NodeIPGroupSortBy = "ip" | "risk" | "score" | "matched_nodes" | "region";

export type IPGroupListQuery = NodeListFilters & {
  sort_by?: NodeIPGroupSortBy;
  sort_order?: SortOrder;
  limit?: number;
  offset?: number;
};

export type NodeTag = {
  subscription_id: string;
  subscription_name: string;
  tag: string;
};

export type NodeSummary = {
  node_hash: string;
  created_at: string;
  enabled: boolean;
  display_tag?: string;
  has_outbound: boolean;
  last_error?: string;
  circuit_open_since?: string;
  failure_count: number;
  egress_ip?: string;
  reference_latency_ms?: number;
  region?: string;
  last_egress_update?: string;
  last_latency_probe_attempt?: string;
  last_authority_latency_probe_attempt?: string;
  last_egress_update_attempt?: string;
  tags: NodeTag[];
};

export type PageResponse<T> = {
  items: T[];
  total: number;
  limit: number;
  offset: number;
  unique_egress_ips: number;
  unique_healthy_egress_ips: number;
};

export type NodeSortBy = "tag" | "created_at" | "failure_count" | "region";
export type SortOrder = "asc" | "desc";

export type NodeListFilters = {
  platform_id?: string;
  subscription_id?: string;
  tag_keyword?: string;
  region?: string;
  egress_ip?: string;
  probed_since?: string;
  enabled?: boolean;
  circuit_open?: boolean;
  has_outbound?: boolean;
};

export type NodeListQuery = NodeListFilters & {
  sort_by?: NodeSortBy;
  sort_order?: SortOrder;
  limit?: number;
  offset?: number;
};

export type EgressProbeResult = {
  egress_ip: string;
  region?: string;
  latency_ewma_ms: number;
};

export type LatencyProbeResult = {
  latency_ewma_ms: number;
};
