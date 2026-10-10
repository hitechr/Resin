package service

import (
	"net/netip"
	"testing"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/subscription"
	"github.com/Resinat/Resin/internal/topology"
)

func TestNodeIPGroupsFilterBeforeGroupingAndKeepUnresolved(t *testing.T) {
	subs := topology.NewSubscriptionManager()
	pool := newNodeListTestPool(subs)
	first := subscription.NewSubscription("sub-a", "A", "https://example.com/a", true, false)
	second := subscription.NewSubscription("sub-b", "B", "https://example.com/b", true, false)
	subs.Register(first)
	subs.Register(second)
	for _, tc := range []struct {
		sub *subscription.Subscription
		raw string
		ip  string
	}{
		{first, `{"type":"ss","server":"a"}`, "8.8.8.8"},
		{first, `{"type":"ss","server":"b"}`, "8.8.8.8"},
		{second, `{"type":"ss","server":"c"}`, "1.1.1.1"},
		{first, `{"type":"ss","server":"d"}`, "192.0.2.1"},
	} {
		addRoutableNodeForSubscription(t, pool, tc.sub, []byte(tc.raw), tc.ip)
	}
	cp := &ControlPlaneService{Pool: pool, SubMgr: subs}
	groups, err := cp.ListNodeIPGroups(NodeFilters{SubscriptionID: &first.ID})
	if err != nil || len(groups) != 2 {
		t.Fatalf("filtered groups: %+v, %v", groups, err)
	}
	for _, group := range groups {
		switch group.Key {
		case "8.8.8.8":
			if group.MatchedNodeCount != 2 || group.Risk != nil {
				t.Fatalf("shared group: %+v", group)
			}
		case UnresolvedIPGroupKey:
			if group.MatchedNodeCount != 1 || group.IP != "" || group.State != "unresolved" {
				t.Fatalf("unresolved group: %+v", group)
			}
		default:
			t.Fatalf("unexpected group: %+v", group)
		}
	}
	group, members, err := cp.GetNodeIPGroup(NodeFilters{SubscriptionID: &first.ID}, "8.8.8.8")
	if err != nil || group.MatchedNodeCount != 2 || len(members) != 2 {
		t.Fatalf("detail: %+v %d %v", group, len(members), err)
	}
	if _, _, err := cp.GetNodeIPGroup(NodeFilters{SubscriptionID: &first.ID}, "1.1.1.1"); err == nil {
		t.Fatal("IP outside filtered pool must not have a detail")
	}
	if _, _, err := cp.GetNodeIPGroup(NodeFilters{}, netip.MustParseAddr("2001:db8::1").String()); err == nil {
		t.Fatal("non-public IP must be rejected")
	}
}

func TestNodeIPGroupDetailTracksChangedIP(t *testing.T) {
	subs := topology.NewSubscriptionManager()
	pool := newNodeListTestPool(subs)
	sub := subscription.NewSubscription("sub-a", "A", "https://example.com/a", true, false)
	subs.Register(sub)
	hash := addRoutableNodeForSubscription(t, pool, sub, []byte(`{"type":"ss","server":"a"}`), "8.8.8.8")
	cp := &ControlPlaneService{Pool: pool, SubMgr: subs}
	if _, _, err := cp.GetNodeIPGroup(NodeFilters{}, "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	changed := netip.MustParseAddr("1.1.1.1")
	pool.UpdateNodeEgressIP(hash, &changed, nil)
	if _, _, err := cp.GetNodeIPGroup(NodeFilters{}, "8.8.8.8"); err == nil {
		t.Fatal("old IP should no longer resolve")
	}
	if group, _, err := cp.GetNodeIPGroup(NodeFilters{}, "1.1.1.1"); err != nil || group.IP != "1.1.1.1" {
		t.Fatalf("new IP detail: %+v %v", group, err)
	}
}

func TestNodeIPGroupsAggregateBestLatencyHealthAndProbeTime(t *testing.T) {
	latency := func(ms float64) *float64 { return &ms }
	nodes := []NodeSummary{
		{EgressIP: "8.8.8.8", Enabled: true, HasOutbound: true, ReferenceLatencyMs: latency(120), LastLatencyProbeAttempt: "2026-10-09T01:00:00Z"},
		{EgressIP: "8.8.8.8", Enabled: true, HasOutbound: true, ReferenceLatencyMs: latency(80), LastLatencyProbeAttempt: "2026-10-09T02:00:00Z"},
		{EgressIP: "8.8.8.8", Enabled: true, ReferenceLatencyMs: latency(30), LastLatencyProbeAttempt: "2026-10-09T03:00:00Z"},
		{EgressIP: "1.1.1.1", Enabled: true, ReferenceLatencyMs: latency(50)},
	}
	byKey := make(map[string]NodeIPGroup)
	for _, group := range projectNodeIPGroups(nodes, nil) {
		byKey[group.Key] = group
	}
	shared := byKey["8.8.8.8"]
	if shared.ReferenceLatencyMs == nil || *shared.ReferenceLatencyMs != 80 {
		t.Fatalf("latency must be the minimum over healthy members: %+v", shared)
	}
	if !shared.Healthy {
		t.Fatalf("group with a healthy member must be healthy: %+v", shared)
	}
	if shared.LastLatencyProbeAttempt != "2026-10-09T03:00:00Z" {
		t.Fatalf("probe time must be the most recent member attempt: %+v", shared)
	}
	unhealthy := byKey["1.1.1.1"]
	if unhealthy.Healthy || unhealthy.ReferenceLatencyMs != nil || unhealthy.LastLatencyProbeAttempt != "" {
		t.Fatalf("unhealthy-only group must not expose latency: %+v", unhealthy)
	}
}

func TestNodeIPGroupsRegionUsesMajorityMemberRegion(t *testing.T) {
	nodes := []NodeSummary{
		{EgressIP: "8.8.8.8", Region: "us"},
		{EgressIP: "8.8.8.8", Region: "US"},
		{EgressIP: "8.8.8.8", Region: "ca"},
		{EgressIP: "1.1.1.1", Region: "jp"},
		{EgressIP: "9.9.9.9"},
		{},
	}
	byKey := make(map[string]NodeIPGroup)
	for _, group := range projectNodeIPGroups(nodes, nil) {
		byKey[group.Key] = group
	}
	if byKey["8.8.8.8"].Region != "US" {
		t.Fatalf("majority region: %+v", byKey["8.8.8.8"])
	}
	if byKey["1.1.1.1"].Region != "JP" {
		t.Fatalf("single member region: %+v", byKey["1.1.1.1"])
	}
	if byKey["9.9.9.9"].Region != "" || byKey[UnresolvedIPGroupKey].Region != "" {
		t.Fatalf("unknown regions must stay empty: %+v %+v", byKey["9.9.9.9"], byKey[UnresolvedIPGroupKey])
	}
}

func TestNodeIPGroupsLargeSharedIPCollapsesBeforePagination(t *testing.T) {
	nodes := make([]NodeSummary, 1201)
	for i := range nodes[:1200] {
		nodes[i].EgressIP = "8.8.8.8"
	}
	groups := projectNodeIPGroups(nodes, nil)
	if len(groups) != 2 {
		t.Fatalf("1200 shared nodes should form one public group and one unresolved group, got %d", len(groups))
	}
	for _, group := range groups {
		if group.IP == "8.8.8.8" && group.MatchedNodeCount != 1200 {
			t.Fatalf("shared IP group: %+v", group)
		}
	}
}

func TestNodeIPGroupsRiskFromCachedDataAndNullLast(t *testing.T) {
	ptr := func(n float64) *float64 { return &n }
	nodes := []NodeSummary{{EgressIP: "8.8.8.8"}, {EgressIP: "1.1.1.1"}, {EgressIP: "9.9.9.9"}, {}}
	cache := func(ip string) (ipquality.Result, bool, bool) {
		switch ip {
		case "8.8.8.8":
			return ipquality.Result{Score: ptr(90), Flags: ipquality.Flags{Proxy: func() *bool { b := true; return &b }()}}, true, true
		case "1.1.1.1":
			return ipquality.Result{Score: ptr(40)}, true, false
		case "9.9.9.9":
			flag := true
			return ipquality.Result{Flags: ipquality.Flags{Tor: &flag}}, true, true
		default:
			return ipquality.Result{}, false, false
		}
	}
	groups := projectNodeIPGroups(nodes, cache)
	SortNodeIPGroups(groups, "risk", "desc")
	if len(groups) != 4 || groups[0].Key != "8.8.8.8" || groups[1].Key != "1.1.1.1" || groups[2].Key != "9.9.9.9" || groups[3].Key != UnresolvedIPGroupKey {
		t.Fatalf("risk descending: %+v", groups)
	}
	if groups[0].Risk == nil || *groups[0].Risk != 70 || groups[0].RiskVersion != "v1" || groups[1].State != "stale" || groups[2].Risk != nil || groups[2].State != "fresh" {
		t.Fatalf("risk/freshness: %+v", groups)
	}
	SortNodeIPGroups(groups, "risk", "asc")
	if groups[0].Key != "1.1.1.1" || groups[1].Key != "8.8.8.8" || groups[2].Key != "9.9.9.9" || groups[3].Key != UnresolvedIPGroupKey {
		t.Fatalf("risk ascending: %+v", groups)
	}
}
