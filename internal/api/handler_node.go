package api

import (
	"cmp"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Resinat/Resin/internal/service"
)

func nodeTagSortKey(n service.NodeSummary) string {
	if n.DisplayTag != "" {
		return n.DisplayTag
	}
	if len(n.Tags) == 0 {
		return ""
	}
	bestCreated := int64(math.MaxInt64)
	bestTag := ""
	for _, t := range n.Tags {
		if t.SubscriptionCreatedAtNs < bestCreated {
			bestCreated = t.SubscriptionCreatedAtNs
			bestTag = t.Tag
			continue
		}
		if t.SubscriptionCreatedAtNs == bestCreated && (bestTag == "" || t.Tag < bestTag) {
			bestTag = t.Tag
		}
	}
	return bestTag
}

func compareNodeSummaries(sortBy string, a, b service.NodeSummary) int {
	order := 0
	switch sortBy {
	case "created_at":
		order = strings.Compare(a.CreatedAt, b.CreatedAt)
	case "failure_count":
		order = cmp.Compare(a.FailureCount, b.FailureCount)
	case "region":
		order = strings.Compare(a.Region, b.Region)
	default:
		order = strings.Compare(nodeTagSortKey(a), nodeTagSortKey(b))
	}
	if order != 0 {
		return order
	}
	return strings.Compare(a.NodeHash, b.NodeHash)
}

func sortNodeSummaries(nodes []service.NodeSummary, sorting Sorting) {
	slices.SortStableFunc(nodes, func(a, b service.NodeSummary) int {
		return applySortOrder(compareNodeSummaries(sorting.SortBy, a, b), sorting.SortOrder)
	})
}

type nodeListPageResponse struct {
	Items                  []service.NodeSummary `json:"items"`
	Total                  int                   `json:"total"`
	Limit                  int                   `json:"limit"`
	Offset                 int                   `json:"offset"`
	UniqueEgressIPs        int                   `json:"unique_egress_ips"`
	UniqueHealthyEgressIPs int                   `json:"unique_healthy_egress_ips"`
}

func countUniqueEgressIPs(nodes []service.NodeSummary) int {
	seen := make(map[string]struct{})
	for _, n := range nodes {
		if n.EgressIP == "" {
			continue
		}
		seen[n.EgressIP] = struct{}{}
	}
	return len(seen)
}

func countUniqueHealthyAndEnabledEgressIPs(nodes []service.NodeSummary) int {
	seen := make(map[string]struct{})
	for _, n := range nodes {
		if n.EgressIP == "" {
			continue
		}
		if !n.IsHealthyAndEnabled() {
			continue
		}
		seen[n.EgressIP] = struct{}{}
	}
	return len(seen)
}

// parseNodeFilters is shared by the node list, IP groups and filtered batch.
func parseNodeFilters(w http.ResponseWriter, r *http.Request) (service.NodeFilters, bool) {
	q := r.URL.Query()
	filters := service.NodeFilters{}

	platformID, ok := parseOptionalUUIDQuery(w, r, "platform_id", "platform_id")
	if !ok {
		return filters, false
	}
	filters.PlatformID = platformID

	subscriptionID, ok := parseOptionalUUIDQuery(w, r, "subscription_id", "subscription_id")
	if !ok {
		return filters, false
	}
	filters.SubscriptionID = subscriptionID

	if v := q.Get("region"); v != "" {
		filters.Region = &v
	}
	if v := q.Get("egress_ip"); v != "" {
		filters.EgressIP = &v
	}
	if v := strings.TrimSpace(q.Get("tag_keyword")); v != "" {
		filters.TagKeyword = &v
	}

	circuitOpen, ok := parseBoolQueryOrWriteInvalid(w, r, "circuit_open")
	if !ok {
		return filters, false
	}
	filters.CircuitOpen = circuitOpen

	hasOutbound, ok := parseBoolQueryOrWriteInvalid(w, r, "has_outbound")
	if !ok {
		return filters, false
	}
	filters.HasOutbound = hasOutbound

	enabled, ok := parseBoolQueryOrWriteInvalid(w, r, "enabled")
	if !ok {
		return filters, false
	}
	filters.Enabled = enabled

	if v := q.Get("probed_since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeInvalidArgument(w, "probed_since: invalid RFC3339 timestamp")
			return filters, false
		}
		filters.ProbedSince = &t
	}
	return filters, true
}

// HandleListNodes returns a handler for GET /api/v1/nodes.
func HandleListNodes(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filters, ok := parseNodeFilters(w, r)
		if !ok {
			return
		}

		nodes, err := cp.ListNodes(filters)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		sorting, ok := parseSortingOrWriteInvalid(w, r, []string{"tag", "created_at", "failure_count", "region"}, "tag", "asc")
		if !ok {
			return
		}
		sortNodeSummaries(nodes, sorting)

		pg, ok := parsePaginationOrWriteInvalid(w, r)
		if !ok {
			return
		}
		WriteJSON(w, http.StatusOK, nodeListPageResponse{
			Items:                  PaginateSlice(nodes, pg),
			Total:                  len(nodes),
			Limit:                  pg.Limit,
			Offset:                 pg.Offset,
			UniqueEgressIPs:        countUniqueEgressIPs(nodes),
			UniqueHealthyEgressIPs: countUniqueHealthyAndEnabledEgressIPs(nodes),
		})
	}
}

type nodeRegionStats struct {
	Region          string `json:"region"`
	TotalIPs        int    `json:"total_ips"`
	AvailableIPs    int    `json:"available_ips"`
	SampledNodes    int    `json:"sampled_nodes"`
	LowLatencyNodes int    `json:"low_latency_nodes"`
}

type regionIPCounts struct {
	stats     nodeRegionStats
	all       map[string]struct{}
	available map[string]struct{}
}

func summarizeNodeRegions(nodes []service.NodeSummary) []nodeRegionStats {
	byRegion := make(map[string]*regionIPCounts)
	for _, n := range nodes {
		region := strings.ToUpper(strings.TrimSpace(n.Region))
		if len(region) != 2 || region[0] < 'A' || region[0] > 'Z' || region[1] < 'A' || region[1] > 'Z' {
			region = ""
		}
		if region == "" && n.EgressIP == "" {
			continue
		}
		bucket := byRegion[region]
		if bucket == nil {
			bucket = &regionIPCounts{
				stats:     nodeRegionStats{Region: region},
				all:       make(map[string]struct{}),
				available: make(map[string]struct{}),
			}
			byRegion[region] = bucket
		}
		if n.EgressIP != "" {
			bucket.all[n.EgressIP] = struct{}{}
		}
		if !n.IsHealthyAndEnabled() {
			continue
		}
		if n.EgressIP != "" {
			bucket.available[n.EgressIP] = struct{}{}
		}
		if n.ReferenceLatencyMs != nil && !math.IsNaN(*n.ReferenceLatencyMs) && !math.IsInf(*n.ReferenceLatencyMs, 0) {
			bucket.stats.SampledNodes++
			if *n.ReferenceLatencyMs <= 400 {
				bucket.stats.LowLatencyNodes++
			}
		}
	}

	result := make([]nodeRegionStats, 0, len(byRegion))
	for _, bucket := range byRegion {
		bucket.stats.TotalIPs = len(bucket.all)
		bucket.stats.AvailableIPs = len(bucket.available)
		result = append(result, bucket.stats)
	}
	slices.SortFunc(result, func(a, b nodeRegionStats) int {
		if n := cmp.Compare(b.AvailableIPs, a.AvailableIPs); n != 0 {
			return n
		}
		if n := cmp.Compare(b.TotalIPs, a.TotalIPs); n != 0 {
			return n
		}
		return strings.Compare(a.Region, b.Region)
	})
	return result
}

// HandleNodeRegionStats returns a compact global node-region snapshot.
func HandleNodeRegionStats(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes, err := cp.ListNodes(service.NodeFilters{})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		items := summarizeNodeRegions(nodes)
		countryCount := 0
		for _, item := range items {
			if item.Region != "" {
				countryCount++
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"generated_at":  formatTimestamp(time.Now()),
			"country_count": countryCount,
			"items":         items,
		})
	}
}

// HandleGetNode returns a handler for GET /api/v1/nodes/{hash}.
func HandleGetNode(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := PathParam(r, "hash")
		n, err := cp.GetNode(hash)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, n)
	}
}

// HandleProbeEgress returns a handler for POST /api/v1/nodes/{hash}/actions/probe-egress.
func HandleProbeEgress(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := PathParam(r, "hash")
		result, err := cp.ProbeEgress(hash)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

// HandleProbeLatency returns a handler for POST /api/v1/nodes/{hash}/actions/probe-latency.
func HandleProbeLatency(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := PathParam(r, "hash")
		result, err := cp.ProbeLatency(hash)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
