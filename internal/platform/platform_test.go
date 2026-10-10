package platform

import (
	"net/netip"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/testutil"
)

// makeFullyRoutableEntry creates a NodeEntry that passes all 5 filter conditions.
func makeFullyRoutableEntry(hash node.Hash, subIDs ...string) *node.NodeEntry {
	e := node.NewNodeEntry(hash, nil, time.Now(), 16)
	for _, id := range subIDs {
		e.AddSubscriptionID(id)
	}
	// Set all conditions to pass.
	e.LatencyTable.LoadEntry("example.com", node.DomainLatencyStats{
		Ewma:        100 * time.Millisecond,
		LastUpdated: time.Now(),
	})
	ob := testutil.NewNoopOutbound()
	e.Outbound.Store(&ob)
	e.SetEgressIP(netip.MustParseAddr("1.2.3.4"))
	return e
}

func alwaysLookup(subID string, hash node.Hash) (string, bool, []string, bool) {
	return "TestSub", true, []string{"us-node", "fast"}, true
}

func usGeoLookup(addr netip.Addr) string { return "us" }

func TestPlatform_EvaluateNode_AllPass(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil) // no filters
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 1 {
		t.Fatalf("expected 1 routable node, got %d", p.View().Size())
	}
}

func TestPlatform_EvaluateNode_CircuitOpen(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	entry.CircuitOpenSince.Store(time.Now().UnixNano()) // circuit open

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 0 {
		t.Fatal("circuit-broken node should not be routable")
	}
}

func TestPlatform_EvaluateNode_NoLatency(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	// Create entry without latency table (maxLatencyTableEntries=0).
	entry := node.NewNodeEntry(h, nil, time.Now(), 0)
	entry.AddSubscriptionID("sub1")
	ob := testutil.NewNoopOutbound()
	entry.Outbound.Store(&ob)
	entry.SetEgressIP(netip.MustParseAddr("1.2.3.4"))

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 0 {
		t.Fatal("node without latency should not be routable")
	}
}

func TestPlatform_EvaluateNode_NoOutbound(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	entry.Outbound.Store(nil) // no outbound

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 0 {
		t.Fatal("node without outbound should not be routable")
	}
}

func TestPlatform_EvaluateNode_NoEgressIP(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil) // no region filters
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	entry.SetEgressIP(netip.Addr{}) // egress unknown

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 0 {
		t.Fatal("node without egress IP should not be routable")
	}
}

func TestPlatform_EvaluateNode_RegexFilter(t *testing.T) {
	regexes := []*regexp.Regexp{regexp.MustCompile("us")}
	p := NewPlatform("p1", "Test", regexes, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")

	// Lookup returns "TestSub/us-node" which matches "us".
	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 1 {
		t.Fatal("node matching regex should be routable")
	}

	// Now with a "jp" filter — should NOT match.
	p2 := NewPlatform("p2", "Test", []*regexp.Regexp{regexp.MustCompile("^jp")}, nil)
	p2.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p2.View().Size() != 0 {
		t.Fatal("node not matching regex should not be routable")
	}
}

func TestPlatform_EvaluateNode_RegionFilter(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, []string{"us"})
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 1 {
		t.Fatal("node in allowed region should be routable")
	}

	// Region filter "jp" — node has US egress, should fail.
	p2 := NewPlatform("p2", "Test", nil, []string{"jp"})
	p2.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p2.View().Size() != 0 {
		t.Fatal("node not in allowed region should not be routable")
	}
}

func TestPlatform_EvaluateNode_RegionFilter_NoEgressIP(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, []string{"us"})
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	// Don't set egress IP — clear it.
	entry.SetEgressIP(netip.Addr{})

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 0 {
		t.Fatal("node without egress IP should not be routable")
	}
}

func TestPlatform_EvaluateNode_RegionFilter_PrefersStoredRegion(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, []string{"jp"})
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	entry.SetEgressRegion("jp")

	geoCalled := false
	geoLookup := func(netip.Addr) string {
		geoCalled = true
		return "us"
	}

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, geoLookup, nil)

	if p.View().Size() != 1 {
		t.Fatal("stored region should be used before GeoIP fallback")
	}
	if geoCalled {
		t.Fatal("GeoIP lookup should be skipped when stored region exists")
	}
}

func TestPlatform_EvaluateNode_RegionFilter_ExcludeOnlyUnknownRegion(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, []string{"!hk"})
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")

	geoLookup := func(netip.Addr) string { return "" }
	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, geoLookup, nil)

	if p.View().Size() != 0 {
		t.Fatal("node with unknown region should not be routable when region filters are configured")
	}
}

func TestMatchRegionFilter(t *testing.T) {
	tests := []struct {
		name    string
		filters []string
		region  string
		want    bool
	}{
		{
			name:    "include only match",
			filters: []string{"hk", "us"},
			region:  "hk",
			want:    true,
		},
		{
			name:    "include only miss",
			filters: []string{"hk", "us"},
			region:  "jp",
			want:    false,
		},
		{
			name:    "exclude only",
			filters: []string{"!hk"},
			region:  "us",
			want:    true,
		},
		{
			name:    "exclude only blocked",
			filters: []string{"!hk"},
			region:  "hk",
			want:    false,
		},
		{
			name:    "exclude only unknown region",
			filters: []string{"!hk"},
			region:  "",
			want:    false,
		},
		{
			name:    "mixed include and exclude allows expected",
			filters: []string{"hk", "!us"},
			region:  "hk",
			want:    true,
		},
		{
			name:    "mixed include and exclude blocks excluded",
			filters: []string{"hk", "!us"},
			region:  "us",
			want:    false,
		},
		{
			name:    "mixed include and same exclude blocks",
			filters: []string{"hk", "!hk"},
			region:  "hk",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchRegionFilter(tt.region, tt.filters); got != tt.want {
				t.Fatalf("MatchRegionFilter(%q, %v) = %v, want %v", tt.region, tt.filters, got, tt.want)
			}
		})
	}
}

func TestPlatform_NotifyDirty_AddRemove(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")

	entryStore := map[node.Hash]*node.NodeEntry{h: entry}
	getEntry := func(hash node.Hash) (*node.NodeEntry, bool) {
		e, ok := entryStore[hash]
		return e, ok
	}

	// Initially empty — add via NotifyDirty.
	p.NotifyDirty(h, getEntry, alwaysLookup, usGeoLookup, nil)
	if p.View().Size() != 1 {
		t.Fatal("NotifyDirty should add passing node")
	}

	// Circuit-break → NotifyDirty removes.
	entry.CircuitOpenSince.Store(time.Now().UnixNano())
	p.NotifyDirty(h, getEntry, alwaysLookup, usGeoLookup, nil)
	if p.View().Size() != 0 {
		t.Fatal("NotifyDirty should remove circuit-broken node")
	}

	// Recover → NotifyDirty re-adds.
	entry.CircuitOpenSince.Store(0)
	p.NotifyDirty(h, getEntry, alwaysLookup, usGeoLookup, nil)
	if p.View().Size() != 1 {
		t.Fatal("NotifyDirty should re-add recovered node")
	}

	// Delete from pool → NotifyDirty removes.
	delete(entryStore, h)
	p.NotifyDirty(h, getEntry, alwaysLookup, usGeoLookup, nil)
	if p.View().Size() != 0 {
		t.Fatal("NotifyDirty should remove deleted node")
	}
}

func TestPlatform_FullRebuild_ClearsOld(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h1 := makeHash(`{"type":"ss","n":1}`)
	h2 := makeHash(`{"type":"ss","n":2}`)
	e1 := makeFullyRoutableEntry(h1, "sub1")
	e2 := makeFullyRoutableEntry(h2, "sub1")

	// First rebuild with 2 nodes.
	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h1, e1)
		fn(h2, e2)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 2 {
		t.Fatalf("expected 2, got %d", p.View().Size())
	}

	// Second rebuild with only 1 node — old entries cleared.
	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h1, e1)
	}, alwaysLookup, usGeoLookup, nil)

	if p.View().Size() != 1 {
		t.Fatalf("expected 1 after rebuild, got %d", p.View().Size())
	}
	if p.View().Contains(h2) {
		t.Fatal("h2 should have been removed by rebuild")
	}
}

// --- max reference latency tests ---

// makeAuthorityLatencyEntry builds a fully routable entry with one
// authority-domain latency sample on top of the regular example.com sample.
func makeAuthorityLatencyEntry(raw string, domain string, latencyMs int) (node.Hash, *node.NodeEntry) {
	h := makeHash(raw)
	e := makeFullyRoutableEntry(h, "sub1")
	e.LatencyTable.LoadEntry(domain, node.DomainLatencyStats{
		Ewma:        time.Duration(latencyMs) * time.Millisecond,
		LastUpdated: time.Now(),
	})
	return h, e
}

func TestPlatform_EvaluateNode_MaxReferenceLatencyDisabledAndUnknown(t *testing.T) {
	authorities := []string{"gstatic.com"}

	// Limit 0 (default) ignores reference latency entirely.
	unlimited := NewPlatform("p-unlimited", "Unlimited", nil, nil)
	hSlow, slow := makeAuthorityLatencyEntry(`{"type":"ss","n":"slow"}`, "gstatic.com", 5000)
	unlimited.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(hSlow, slow)
	}, alwaysLookup, usGeoLookup, authorities)
	if !unlimited.View().Contains(hSlow) {
		t.Fatal("limit 0 must not filter nodes by reference latency")
	}

	// A positive limit excludes nodes without any authority-domain sample,
	// even when they have latency for another domain.
	limited := NewPlatform("p-limited", "Limited", nil, nil)
	limited.MaxReferenceLatencyMs = 400
	hUnknown := makeHash(`{"type":"ss","n":"unknown"}`)
	unknown := makeFullyRoutableEntry(hUnknown, "sub1") // only example.com latency
	limited.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(hUnknown, unknown)
	}, alwaysLookup, usGeoLookup, authorities)
	if limited.View().Contains(hUnknown) {
		t.Fatal("node without authority-domain latency must be excluded while a limit is active")
	}

	// Without any configured authorities, no node can satisfy a positive limit.
	hFast, fast := makeAuthorityLatencyEntry(`{"type":"ss","n":"fast"}`, "gstatic.com", 100)
	limited.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(hFast, fast)
	}, alwaysLookup, usGeoLookup, nil)
	if limited.View().Contains(hFast) {
		t.Fatal("positive limit with no authority list must exclude every node")
	}
}

func TestPlatform_EvaluateNode_MaxReferenceLatencyBoundary(t *testing.T) {
	authorities := []string{"gstatic.com"}
	p := NewPlatform("p-limited", "Limited", nil, nil)
	p.MaxReferenceLatencyMs = 400

	hAt, at := makeAuthorityLatencyEntry(`{"type":"ss","n":"at-limit"}`, "gstatic.com", 400)
	hOver, over := makeAuthorityLatencyEntry(`{"type":"ss","n":"over-limit"}`, "gstatic.com", 401)
	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(hAt, at)
		fn(hOver, over)
	}, alwaysLookup, usGeoLookup, authorities)
	if !p.View().Contains(hAt) {
		t.Fatal("displayed 400ms must pass a 400ms limit")
	}
	if p.View().Contains(hOver) {
		t.Fatal("displayed 401ms must fail a 400ms limit")
	}

	// NotifyDirty applies the same rule when a node's average moves.
	entries := map[node.Hash]*node.NodeEntry{hAt: at, hOver: over}
	getEntry := func(h node.Hash) (*node.NodeEntry, bool) {
		e, ok := entries[h]
		return e, ok
	}
	at.LatencyTable.LoadEntry("gstatic.com", node.DomainLatencyStats{Ewma: 401 * time.Millisecond, LastUpdated: time.Now()})
	p.NotifyDirty(hAt, getEntry, alwaysLookup, usGeoLookup, authorities)
	if p.View().Contains(hAt) {
		t.Fatal("NotifyDirty must remove a node whose average crossed the limit")
	}
	over.LatencyTable.LoadEntry("gstatic.com", node.DomainLatencyStats{Ewma: 399 * time.Millisecond, LastUpdated: time.Now()})
	p.NotifyDirty(hOver, getEntry, alwaysLookup, usGeoLookup, authorities)
	if !p.View().Contains(hOver) {
		t.Fatal("NotifyDirty must add a node whose average dropped under the limit")
	}
}

func TestPlatform_EvaluateNode_MaxReferenceLatencyUsesDisplayedAverage(t *testing.T) {
	authorities := []string{"a.example", "b.example", "c.example"}
	p := NewPlatform("p-limited", "Limited", nil, nil)
	p.MaxReferenceLatencyMs = 400

	// (300 + 500) / 2 = 400 over the authorities that have samples; the
	// missing c.example is ignored exactly like the node list display.
	hAvg, avg := makeAuthorityLatencyEntry(`{"type":"ss","n":"avg"}`, "a.example", 300)
	avg.LatencyTable.LoadEntry("b.example", node.DomainLatencyStats{Ewma: 500 * time.Millisecond, LastUpdated: time.Now()})
	// (300 + 501) / 2 = 400.5 exceeds the limit.
	hHalf, half := makeAuthorityLatencyEntry(`{"type":"ss","n":"half"}`, "a.example", 300)
	half.LatencyTable.LoadEntry("b.example", node.DomainLatencyStats{Ewma: 501 * time.Millisecond, LastUpdated: time.Now()})

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(hAvg, avg)
		fn(hHalf, half)
	}, alwaysLookup, usGeoLookup, authorities)

	if displayed, ok := node.AverageEWMAForDomainsMs(avg, authorities); !ok || displayed != 400 {
		t.Fatalf("displayed average = %v/%v, want 400", displayed, ok)
	}
	if !p.View().Contains(hAvg) {
		t.Fatal("displayed average of exactly 400ms must pass")
	}
	if p.View().Contains(hHalf) {
		t.Fatal("displayed average of 400.5ms must fail")
	}
}

func TestPlatform_EvaluateNode_MaxReferenceLatencySharedIPIsPerNode(t *testing.T) {
	authorities := []string{"gstatic.com"}
	p := NewPlatform("p-limited", "Limited", nil, nil)
	p.MaxReferenceLatencyMs = 400

	// Both nodes share the same egress IP (makeFullyRoutableEntry uses 1.2.3.4).
	hFast, fast := makeAuthorityLatencyEntry(`{"type":"ss","n":"shared-fast"}`, "gstatic.com", 300)
	hSlow, slow := makeAuthorityLatencyEntry(`{"type":"ss","n":"shared-slow"}`, "gstatic.com", 500)
	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(hFast, fast)
		fn(hSlow, slow)
	}, alwaysLookup, usGeoLookup, authorities)

	if !p.View().Contains(hFast) {
		t.Fatal("qualifying node sharing the IP must stay routable")
	}
	if p.View().Contains(hSlow) {
		t.Fatal("over-limit node must be excluded even though a sibling shares its IP")
	}
}

func TestPlatform_FullRebuild_SwapsCompleteView(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	poolRange := func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}
	p.FullRebuild(poolRange, alwaysLookup, usGeoLookup, nil)

	// A concurrent reader must never observe an empty or partially rebuilt
	// view: FullRebuild builds the view offline and swaps it in atomically.
	stop := make(chan struct{})
	var sawIncomplete atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if view := p.View(); view.Size() != 1 || !view.Contains(h) {
				sawIncomplete.Store(true)
				return
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		p.FullRebuild(poolRange, alwaysLookup, usGeoLookup, nil)
	}
	close(stop)
	wg.Wait()

	if sawIncomplete.Load() {
		t.Fatal("FullRebuild exposed an empty or partial view to a concurrent reader")
	}
}
