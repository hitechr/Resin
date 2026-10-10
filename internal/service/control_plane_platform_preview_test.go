package service

import (
	"errors"
	"net/netip"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/config"
	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/platform"
	"github.com/Resinat/Resin/internal/subscription"
	"github.com/Resinat/Resin/internal/testutil"
	"github.com/Resinat/Resin/internal/topology"
)

type previewFilterFixture struct {
	cp          *ControlPlaneService
	hkHash      string
	usHash      string
	unknownHash string
}

func buildPreviewFilterFixture(t *testing.T) previewFilterFixture {
	t.Helper()

	subMgr := topology.NewSubscriptionManager()
	sub := subscription.NewSubscription("sub-1", "sub-1", "https://example.com/sub", true, false)
	subMgr.Register(sub)

	pool := topology.NewGlobalNodePool(topology.PoolConfig{
		SubLookup:              subMgr.Lookup,
		GeoLookup:              func(netip.Addr) string { return "" },
		MaxLatencyTableEntries: 16,
		MaxConsecutiveFailures: func() int { return 3 },
		LatencyDecayWindow:     func() time.Duration { return 10 * time.Minute },
	})

	hkRaw := []byte(`{"type":"ss","server":"1.1.1.1","port":443}`)
	hkHash := node.HashFromRawOptions(hkRaw)
	pool.AddNodeFromSub(hkHash, hkRaw, sub.ID)
	sub.ManagedNodes().StoreNode(hkHash, subscription.ManagedNode{Tags: []string{"all", "hk"}})

	usRaw := []byte(`{"type":"ss","server":"2.2.2.2","port":443}`)
	usHash := node.HashFromRawOptions(usRaw)
	pool.AddNodeFromSub(usHash, usRaw, sub.ID)
	sub.ManagedNodes().StoreNode(usHash, subscription.ManagedNode{Tags: []string{"all", "us"}})

	unknownRaw := []byte(`{"type":"ss","server":"3.3.3.3","port":443}`)
	unknownHash := node.HashFromRawOptions(unknownRaw)
	pool.AddNodeFromSub(unknownHash, unknownRaw, sub.ID)
	sub.ManagedNodes().StoreNode(unknownHash, subscription.ManagedNode{Tags: []string{"all", "unknown"}})

	hkEntry, ok := pool.GetEntry(hkHash)
	if !ok {
		t.Fatal("hk entry missing")
	}
	hkOutbound := testutil.NewNoopOutbound()
	hkEntry.Outbound.Store(&hkOutbound)
	hkEntry.SetEgressIP(netip.MustParseAddr("1.1.1.1"))
	hkEntry.SetEgressRegion("hk")

	usEntry, ok := pool.GetEntry(usHash)
	if !ok {
		t.Fatal("us entry missing")
	}
	usOutbound := testutil.NewNoopOutbound()
	usEntry.Outbound.Store(&usOutbound)
	usEntry.SetEgressIP(netip.MustParseAddr("2.2.2.2"))
	usEntry.SetEgressRegion("us")

	unknownEntry, ok := pool.GetEntry(unknownHash)
	if !ok {
		t.Fatal("unknown entry missing")
	}
	unknownOutbound := testutil.NewNoopOutbound()
	unknownEntry.Outbound.Store(&unknownOutbound)
	unknownEntry.SetEgressIP(netip.MustParseAddr("3.3.3.3"))

	cp := &ControlPlaneService{
		Pool:   pool,
		SubMgr: subMgr,
	}
	return previewFilterFixture{
		cp:          cp,
		hkHash:      hkHash.Hex(),
		usHash:      usHash.Hex(),
		unknownHash: unknownHash.Hex(),
	}
}

func TestPreviewFilter_RegionNegation(t *testing.T) {
	fixture := buildPreviewFilterFixture(t)

	nodes, err := fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters:  []string{".*"},
			RegionFilters: []string{"!hk"},
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
	if nodes[0].NodeHash != fixture.usHash {
		t.Fatalf("matched node = %s, want %s", nodes[0].NodeHash, fixture.usHash)
	}
	if nodes[0].NodeHash == fixture.hkHash {
		t.Fatalf("hk node %s should have been excluded", fixture.hkHash)
	}
}

func TestPreviewFilter_RegexRulesAnyMustAndMustNot(t *testing.T) {
	fixture := buildPreviewFilterFixture(t)

	nodes, err := fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters: []string{"hk", "us"},
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter ANY: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("ANY nodes len = %d, want 2", len(nodes))
	}

	nodes, err = fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters: []string{"hk", "us", `*^sub-1/`},
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter MUST: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("MUST nodes len = %d, want 2", len(nodes))
	}

	nodes, err = fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters: []string{"all", "!hk", "!unknown"},
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter MUST_NOT: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeHash != fixture.usHash {
		t.Fatalf("MUST_NOT nodes = %+v, want only %s", nodes, fixture.usHash)
	}
}

func TestPreviewFilter_RegionMixedIncludeExclude(t *testing.T) {
	fixture := buildPreviewFilterFixture(t)

	nodes, err := fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters:  []string{".*"},
			RegionFilters: []string{"hk", "!us"},
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
	if nodes[0].NodeHash != fixture.hkHash {
		t.Fatalf("matched node = %s, want %s", nodes[0].NodeHash, fixture.hkHash)
	}

	nodes, err = fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters:  []string{".*"},
			RegionFilters: []string{"hk", "!hk"},
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("nodes len = %d, want 0", len(nodes))
	}
}

func TestPreviewFilter_RegionNegation_UnknownRegionExcluded(t *testing.T) {
	fixture := buildPreviewFilterFixture(t)

	nodes, err := fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters:  []string{".*"},
			RegionFilters: []string{"!hk"},
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter: %v", err)
	}

	for _, node := range nodes {
		if node.NodeHash == fixture.unknownHash {
			t.Fatalf("node with unknown region %s should not match region filters", fixture.unknownHash)
		}
	}
}

// --- max reference latency preview tests ---

type previewLatencyFixture struct {
	cp          *ControlPlaneService
	fastHash    string
	slowHash    string
	unknownHash string
	openHash    string
	limited400  string
	limited401  string
}

func buildPreviewLatencyFixture(t *testing.T) previewLatencyFixture {
	t.Helper()

	subMgr := topology.NewSubscriptionManager()
	sub := subscription.NewSubscription("sub-1", "sub-1", "https://example.com/sub", true, false)
	subMgr.Register(sub)

	pool := topology.NewGlobalNodePool(topology.PoolConfig{
		SubLookup:              subMgr.Lookup,
		GeoLookup:              func(netip.Addr) string { return "" },
		MaxLatencyTableEntries: 16,
		MaxConsecutiveFailures: func() int { return 3 },
		LatencyDecayWindow:     func() time.Duration { return 10 * time.Minute },
	})

	runtimeCfg := &atomic.Pointer[config.RuntimeConfig]{}
	runtimeCfg.Store(config.NewDefaultRuntimeConfig()) // authorities include gstatic.com

	addNode := func(raw, ip string, authorityMs, regularMs int, circuitOpen bool) node.Hash {
		rawOpts := []byte(raw)
		h := node.HashFromRawOptions(rawOpts)
		pool.AddNodeFromSub(h, rawOpts, sub.ID)
		sub.ManagedNodes().StoreNode(h, subscription.ManagedNode{Tags: []string{"all"}})
		entry, ok := pool.GetEntry(h)
		if !ok {
			t.Fatalf("node %s missing", h.Hex())
		}
		ob := testutil.NewNoopOutbound()
		entry.Outbound.Store(&ob)
		entry.SetEgressIP(netip.MustParseAddr(ip))
		if authorityMs > 0 {
			entry.LatencyTable.LoadEntry("gstatic.com", node.DomainLatencyStats{
				Ewma:        time.Duration(authorityMs) * time.Millisecond,
				LastUpdated: time.Now(),
			})
		}
		if regularMs > 0 {
			entry.LatencyTable.LoadEntry("other.example", node.DomainLatencyStats{
				Ewma:        time.Duration(regularMs) * time.Millisecond,
				LastUpdated: time.Now(),
			})
		}
		if circuitOpen {
			entry.CircuitOpenSince.Store(time.Now().UnixNano())
		} else {
			pool.RecordResult(h, true)
		}
		return h
	}

	fastHash := addNode(`{"type":"ss","server":"1.1.1.1","port":443}`, "1.1.1.1", 400, 0, false)
	slowHash := addNode(`{"type":"ss","server":"2.2.2.2","port":443}`, "2.2.2.2", 401, 0, false)
	unknownHash := addNode(`{"type":"ss","server":"3.3.3.3","port":443}`, "3.3.3.3", 0, 100, false)
	openHash := addNode(`{"type":"ss","server":"4.4.4.4","port":443}`, "4.4.4.4", 400, 0, true)

	plat400 := platform.NewPlatform("preview-400", "preview-400", nil, nil)
	plat400.MaxReferenceLatencyMs = 400
	pool.RegisterPlatform(plat400)
	plat401 := platform.NewPlatform("preview-401", "preview-401", nil, nil)
	plat401.MaxReferenceLatencyMs = 401
	pool.RegisterPlatform(plat401)

	cp := &ControlPlaneService{
		Pool:       pool,
		SubMgr:     subMgr,
		RuntimeCfg: runtimeCfg,
	}
	return previewLatencyFixture{
		cp:          cp,
		fastHash:    fastHash.Hex(),
		slowHash:    slowHash.Hex(),
		unknownHash: unknownHash.Hex(),
		openHash:    openHash.Hex(),
		limited400:  plat400.ID,
		limited401:  plat401.ID,
	}
}

func previewNodeHashes(nodes []NodeSummary) []string {
	hashes := make([]string, 0, len(nodes))
	for _, n := range nodes {
		hashes = append(hashes, n.NodeHash)
	}
	sort.Strings(hashes)
	return hashes
}

func TestPreviewFilter_MaxReferenceLatencyDraftMatchesStoredPlatform(t *testing.T) {
	fixture := buildPreviewLatencyFixture(t)

	want400 := []string{fixture.fastHash, fixture.openHash}
	sort.Strings(want400)
	want401 := []string{fixture.fastHash, fixture.openHash, fixture.slowHash}
	sort.Strings(want401)
	want0 := []string{fixture.fastHash, fixture.openHash, fixture.slowHash, fixture.unknownHash}
	sort.Strings(want0)

	cases := []struct {
		name  string
		limit int
		want  []string
	}{
		{name: "limit 400", limit: 400, want: want400},
		{name: "limit 401", limit: 401, want: want401},
		{name: "limit 0", limit: 0, want: want0},
	}
	for _, tc := range cases {
		draft, err := fixture.cp.PreviewFilter(PreviewFilterRequest{
			PlatformSpec: &PlatformSpecFilter{
				RegexFilters:          []string{".*"},
				MaxReferenceLatencyMs: tc.limit,
			},
		})
		if err != nil {
			t.Fatalf("draft %s preview: %v", tc.name, err)
		}
		if got := previewNodeHashes(draft); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("draft %s hashes = %v, want %v", tc.name, got, tc.want)
		}
	}

	stored400, err := fixture.cp.PreviewFilter(PreviewFilterRequest{PlatformID: &fixture.limited400})
	if err != nil {
		t.Fatalf("stored 400 preview: %v", err)
	}
	if got := previewNodeHashes(stored400); !reflect.DeepEqual(got, want400) {
		t.Fatalf("stored 400 hashes = %v, want %v", got, want400)
	}

	stored401, err := fixture.cp.PreviewFilter(PreviewFilterRequest{PlatformID: &fixture.limited401})
	if err != nil {
		t.Fatalf("stored 401 preview: %v", err)
	}
	if got := previewNodeHashes(stored401); !reflect.DeepEqual(got, want401) {
		t.Fatalf("stored 401 hashes = %v, want %v", got, want401)
	}
}

func TestPreviewFilter_MaxReferenceLatencyUnknownAndFilterOnly(t *testing.T) {
	fixture := buildPreviewLatencyFixture(t)

	nodes, err := fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters:          []string{".*"},
			MaxReferenceLatencyMs: 400,
		},
	})
	if err != nil {
		t.Fatalf("PreviewFilter: %v", err)
	}

	// Nodes without any authority-domain sample are excluded while the cap
	// is active, even when they have latency for other domains.
	for _, n := range nodes {
		if n.NodeHash == fixture.unknownHash {
			t.Fatal("node without authority-domain latency must be excluded")
		}
	}

	// A circuit-open node with qualifying latency stays in the preview: this
	// is a filter preview, not a health/routing view.
	foundOpen := false
	for _, n := range nodes {
		if n.NodeHash == fixture.openHash {
			foundOpen = true
		}
	}
	if !foundOpen {
		t.Fatal("filter preview must not add health/routing checks")
	}

	// Negative limits are rejected.
	_, err = fixture.cp.PreviewFilter(PreviewFilterRequest{
		PlatformSpec: &PlatformSpecFilter{
			RegexFilters:          []string{".*"},
			MaxReferenceLatencyMs: -1,
		},
	})
	var svcErr *ServiceError
	if !errors.As(err, &svcErr) || svcErr.Code != "INVALID_ARGUMENT" {
		t.Fatalf("expected INVALID_ARGUMENT for negative limit, got %v", err)
	}
}
