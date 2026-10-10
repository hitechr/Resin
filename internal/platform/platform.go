package platform

import (
	"net/netip"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/Resinat/Resin/internal/node"
)

// DefaultPlatformID is the well-known UUID of the built-in Default platform.
const DefaultPlatformID = "00000000-0000-0000-0000-000000000000"

// DefaultPlatformName is the built-in platform name.
const DefaultPlatformName = "Default"

// GeoLookupFunc resolves an IP address to a lowercase ISO country code.
type GeoLookupFunc func(netip.Addr) string

// PoolRangeFunc iterates all nodes in the global pool.
type PoolRangeFunc func(fn func(node.Hash, *node.NodeEntry) bool)

// GetEntryFunc retrieves a node entry from the global pool by hash.
type GetEntryFunc func(node.Hash) (*node.NodeEntry, bool)

// Platform represents a routing platform with its filtered routable view.
type Platform struct {
	ID   string
	Name string

	// Filter configuration.
	RegexFilters  node.TagFilter
	RegionFilters []string // lowercase ISO codes, supports negation "!xx"

	// Other config fields.
	StickyTTLNs                      int64
	ReverseProxyMissAction           string
	ReverseProxyEmptyAccountBehavior string
	ReverseProxyFixedAccountHeader   string
	ReverseProxyFixedAccountHeaders  []string
	AllocationPolicy                 AllocationPolicy
	PassiveCircuitBreakerDisabled    bool
	// MaxReferenceLatencyMs excludes nodes whose displayed reference latency
	// (authority-domain EWMA average) exceeds the value; 0 disables the cap.
	MaxReferenceLatencyMs int

	// Routable view & its lock.
	// viewMu serializes FullRebuild and NotifyDirty. Readers load the view
	// pointer atomically: FullRebuild builds a new view offline and swaps it
	// in, so a reader never observes an empty or partially rebuilt set.
	view   atomic.Pointer[RoutableView]
	viewMu sync.Mutex
}

// NewPlatform creates a Platform with an empty routable view.
// The regex slice is treated as MUST rules for compatibility with internal callers.
func NewPlatform(id, name string, regexFilters []*regexp.Regexp, regionFilters []string) *Platform {
	return NewPlatformWithTagFilter(id, name, node.TagFilter{Must: regexFilters}, regionFilters)
}

// NewPlatformWithTagFilter creates a Platform with compiled line-oriented tag rules.
func NewPlatformWithTagFilter(id, name string, regexFilters node.TagFilter, regionFilters []string) *Platform {
	plat := &Platform{
		ID:            id,
		Name:          name,
		RegexFilters:  regexFilters,
		RegionFilters: regionFilters,
	}
	plat.view.Store(NewRoutableView())
	return plat
}

// View returns the platform's routable view as a read-only interface.
// External callers cannot Add/Remove — only FullRebuild and NotifyDirty can mutate.
func (p *Platform) View() ReadOnlyView {
	return p.view.Load()
}

// FullRebuild re-evaluates all nodes from the pool into a fresh view and
// atomically swaps it in, so readers never observe a partially rebuilt set.
// Acquires viewMu — any concurrent NotifyDirty calls block until the swap
// completes and then apply to the new view.
func (p *Platform) FullRebuild(
	poolRange PoolRangeFunc,
	subLookup node.SubLookupFunc,
	geoLookup GeoLookupFunc,
	latencyAuthorities []string,
) {
	p.viewMu.Lock()
	defer p.viewMu.Unlock()

	next := NewRoutableView()
	poolRange(func(h node.Hash, entry *node.NodeEntry) bool {
		if p.evaluateNode(entry, subLookup, geoLookup, latencyAuthorities) {
			next.Add(h)
		}
		return true
	})
	p.view.Store(next)
}

// NotifyDirty re-evaluates a single node and adds/removes it from the view.
// Acquires viewMu — serialized with FullRebuild.
func (p *Platform) NotifyDirty(
	h node.Hash,
	getEntry GetEntryFunc,
	subLookup node.SubLookupFunc,
	geoLookup GeoLookupFunc,
	latencyAuthorities []string,
) {
	p.viewMu.Lock()
	defer p.viewMu.Unlock()

	view := p.view.Load()
	entry, ok := getEntry(h)
	if !ok {
		// Node was deleted from pool.
		view.Remove(h)
		return
	}

	if p.evaluateNode(entry, subLookup, geoLookup, latencyAuthorities) {
		view.Add(h)
	} else {
		view.Remove(h)
	}
}

// evaluateNode checks all filter conditions for platform routability.
func (p *Platform) evaluateNode(
	entry *node.NodeEntry,
	subLookup node.SubLookupFunc,
	geoLookup GeoLookupFunc,
	latencyAuthorities []string,
) bool {
	// 0. Disabled nodes are never routable.
	if entry.IsDisabledBySubscriptions(subLookup) {
		return false
	}

	// 1. Healthy for routing (outbound ready + circuit not open).
	if !entry.IsHealthy() {
		return false
	}

	// 2. Tag regex match.
	if !entry.MatchTagFilter(p.RegexFilters, subLookup) {
		return false
	}

	// 3. Egress IP must be known.
	egressIP := entry.GetEgressIP()
	if !egressIP.IsValid() {
		return false
	}

	// 4. Region filter (when configured).
	if len(p.RegionFilters) > 0 {
		region := entry.GetRegion(geoLookup)
		if !MatchRegionFilter(region, p.RegionFilters) {
			return false
		}
	}

	// 5. Has at least one latency record.
	if !entry.HasLatency() {
		return false
	}

	// 6. Reference-latency cap (when configured): compare the same
	// authority-domain average the node list displays; nodes without any
	// authority sample are excluded while the cap is active.
	if p.MaxReferenceLatencyMs > 0 {
		avgMs, ok := node.AverageEWMAForDomainsMs(entry, latencyAuthorities)
		if !ok || avgMs > float64(p.MaxReferenceLatencyMs) {
			return false
		}
	}

	return true
}

// MatchRegionFilter applies include/exclude region filters.
// Positive entries (xx) build an include set; negative entries (!xx) build an exclude set.
// Unknown regions never match when region filters are configured.
// Final result is: region known AND (include empty OR region in include) AND (region not in exclude).
func MatchRegionFilter(region string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	if region == "" {
		return false
	}

	included := false
	hasInclude := false

	for _, filter := range filters {
		if len(filter) > 0 && filter[0] == '!' {
			if region == filter[1:] {
				return false
			}
			continue
		}
		hasInclude = true
		if region == filter {
			included = true
		}
	}

	if hasInclude && !included {
		return false
	}
	return true
}
