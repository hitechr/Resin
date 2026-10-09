package main

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/config"
	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/subscription"
	"github.com/Resinat/Resin/internal/testutil"
)

func TestNodePoolStatsAdapter_CollectNodeEWMAsByRegion(t *testing.T) {
	_, pool := newBootstrapTestRuntime(config.NewDefaultRuntimeConfig())
	adapter := &runtimeStatsAdapter{
		pool:        pool,
		authorities: func() []string { return []string{"example.com"} },
		regionLookup: func(ip netip.Addr) string {
			if ip.String() == "203.0.113.2" {
				return "jp"
			}
			return "us"
		},
	}
	for i, tc := range []struct {
		ip     string
		region string
		ms     int
	}{
		{"203.0.113.1", "jp", 100},
		{"203.0.113.2", "", 200},
		{"203.0.113.3", "us", 300},
		{"", "jp", 400},
	} {
		raw := []byte{byte(i)}
		entry := node.NewNodeEntry(node.HashFromRawOptions(raw), raw, time.Now(), 16)
		if tc.ip != "" {
			entry.SetEgressIP(netip.MustParseAddr(tc.ip))
		}
		entry.SetEgressRegion(tc.region)
		entry.LatencyTable.LoadEntry("example.com", node.DomainLatencyStats{Ewma: time.Duration(tc.ms) * time.Millisecond, LastUpdated: time.Now()})
		pool.LoadNodeFromBootstrap(entry)
	}
	if got := adapter.CollectNodeEWMAs("", "jp"); len(got) != 2 || got[0]+got[1] != 300 {
		t.Fatalf("JP EWMAs = %v, want 100 and 200", got)
	}
	if got := adapter.CollectNodeEWMAs("", "us"); len(got) != 1 || got[0] != 300 {
		t.Fatalf("US EWMAs = %v, want 300", got)
	}
	if got := adapter.CollectNodeEWMAs("", ""); len(got) != 4 {
		t.Fatalf("global EWMAs = %v, want all 4", got)
	}
}

func TestNodePoolStatsAdapter_HealthyNodesRequiresOutbound(t *testing.T) {
	subMgr, pool := newBootstrapTestRuntime(config.NewDefaultRuntimeConfig())
	adapter := &runtimeStatsAdapter{pool: pool}

	enabledSub := subscription.NewSubscription("sub-enabled", "enabled", "https://example.com/enabled", true, false)
	disabledSub := subscription.NewSubscription("sub-disabled", "disabled", "https://example.com/disabled", false, false)
	subMgr.Register(enabledSub)
	subMgr.Register(disabledSub)

	healthyHash := node.HashFromRawOptions([]byte(`{"type":"direct","server":"1.1.1.1","port":443}`))
	healthy := node.NewNodeEntry(healthyHash, nil, time.Now(), 0)
	healthy.AddSubscriptionID(enabledSub.ID)
	enabledSub.ManagedNodes().StoreNode(healthyHash, subscription.ManagedNode{Tags: []string{"healthy"}})
	healthyOb := testutil.NewNoopOutbound()
	healthy.Outbound.Store(&healthyOb)
	healthy.SetEgressIP(netip.MustParseAddr("203.0.113.10"))
	pool.LoadNodeFromBootstrap(healthy)

	noOutboundHash := node.HashFromRawOptions([]byte(`{"type":"direct","server":"2.2.2.2","port":443}`))
	noOutbound := node.NewNodeEntry(noOutboundHash, nil, time.Now(), 0)
	noOutbound.AddSubscriptionID(enabledSub.ID)
	enabledSub.ManagedNodes().StoreNode(noOutboundHash, subscription.ManagedNode{Tags: []string{"no-outbound"}})
	noOutbound.SetEgressIP(netip.MustParseAddr("203.0.113.10"))
	pool.LoadNodeFromBootstrap(noOutbound)

	circuitOpenHash := node.HashFromRawOptions([]byte(`{"type":"direct","server":"3.3.3.3","port":443}`))
	circuitOpen := node.NewNodeEntry(circuitOpenHash, nil, time.Now(), 0)
	circuitOpen.AddSubscriptionID(enabledSub.ID)
	enabledSub.ManagedNodes().StoreNode(circuitOpenHash, subscription.ManagedNode{Tags: []string{"circuit-open"}})
	circuitOpenOb := testutil.NewNoopOutbound()
	circuitOpen.Outbound.Store(&circuitOpenOb)
	circuitOpen.SetEgressIP(netip.MustParseAddr("203.0.113.11"))
	circuitOpen.CircuitOpenSince.Store(time.Now().UnixNano())
	pool.LoadNodeFromBootstrap(circuitOpen)

	disabledHash := node.HashFromRawOptions([]byte(`{"type":"direct","server":"4.4.4.4","port":443}`))
	disabled := node.NewNodeEntry(disabledHash, nil, time.Now(), 0)
	disabled.AddSubscriptionID(disabledSub.ID)
	disabledSub.ManagedNodes().StoreNode(disabledHash, subscription.ManagedNode{Tags: []string{"disabled"}})
	disabledOb := testutil.NewNoopOutbound()
	disabled.Outbound.Store(&disabledOb)
	disabled.SetEgressIP(netip.MustParseAddr("203.0.113.12"))
	pool.LoadNodeFromBootstrap(disabled)

	if got, want := adapter.HealthyNodes(), 1; got != want {
		t.Fatalf("healthy_nodes: got %d, want %d", got, want)
	}
	if got, want := adapter.UniqueHealthyEgressIPCount(), 1; got != want {
		t.Fatalf("unique_healthy_egress_ips: got %d, want %d", got, want)
	}
}
