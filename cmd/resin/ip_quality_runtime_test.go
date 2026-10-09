package main

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/service"
	"github.com/Resinat/Resin/internal/subscription"
	"github.com/Resinat/Resin/internal/topology"
)

type autoLookup func(context.Context, string) (ipquality.Result, error)

func (f autoLookup) Lookup(ctx context.Context, ip string) (ipquality.Result, error) {
	return f(ctx, ip)
}

func TestAutomaticQualityWaitsForFirstPublicEgressObservation(t *testing.T) {
	subs := topology.NewSubscriptionManager()
	sub := subscription.NewSubscription("s1", "A", "https://example.com", true, false)
	subs.Register(sub)
	pool := topology.NewGlobalNodePool(topology.PoolConfig{
		SubLookup: subs.Lookup, MaxLatencyTableEntries: 16,
		MaxConsecutiveFailures: func() int { return 3 },
	})
	raw := []byte(`{"type":"ss","server":"a"}`)
	hash := node.HashFromRawOptions(raw)
	pool.AddNodeFromSub(hash, raw, sub.ID)
	sub.ManagedNodes().StoreNode(hash, subscription.ManagedNode{Tags: []string{"a"}})
	cp := &service.ControlPlaneService{Pool: pool, SubMgr: subs}
	calls := make(chan string, 2)
	quality := ipquality.NewService(autoLookup(func(_ context.Context, ip string) (ipquality.Result, error) {
		calls <- ip
		return ipquality.Result{IP: ip}, nil
	}))
	coordinator := ipquality.NewCoordinator(quality, func(ip string) bool { return allowedIP(cp, ip) })
	defer coordinator.Stop()
	r := newIPQualityAutoRuntime(cp, coordinator)
	defer r.Stop()
	pool.SetOnNodeEgressIPChanged(r.OnNodeEgressIPChanged)
	if _, ok, _ := quality.Cached("8.8.8.8"); ok {
		t.Fatal("new node has no IP or quality")
	}
	r.scanNodes(service.NodeFilters{})
	ip := netip.MustParseAddr("8.8.8.8")
	pool.UpdateNodeEgressIP(hash, &ip, nil)
	select {
	case got := <-calls:
		if got != "8.8.8.8" {
			t.Fatalf("first observed IP: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first public IP was not checked")
	}
	pool.UpdateNodeEgressIP(hash, &ip, nil)
	select {
	case got := <-calls:
		t.Fatalf("same-IP observation retriggered: %s", got)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestNextLocalMidnightAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("timezone unavailable: %v", err)
	}
	for _, tc := range []struct {
		month    time.Month
		day      int
		interval time.Duration
	}{
		{time.March, 7, 24 * time.Hour},
		{time.March, 8, 23 * time.Hour},
		{time.November, 1, 25 * time.Hour},
	} {
		start := time.Date(2026, tc.month, tc.day, 0, 0, 0, 0, loc)
		next := nextLocalMidnight(start)
		if next.Day() != tc.day+1 || next.Hour() != 0 || next.Sub(start) != tc.interval {
			t.Fatalf("next midnight from %v = %v (%v)", start, next, next.Sub(start))
		}
	}
}

func TestDailyQualityScanDoesNotCatchUpMissedDates(t *testing.T) {
	loc := time.FixedZone("local", 8*3600)
	now := time.Date(2026, time.October, 9, 10, 0, 0, 0, loc)
	var scans int
	r := &ipQualityAutoRuntime{
		now: func() time.Time { return now },
		waitUntil: func(_ context.Context, target time.Time) bool {
			if scans == 0 {
				now = time.Date(2026, time.October, 12, 0, 0, 0, 0, loc)
				return true
			}
			return false
		},
		scanAll: func() { scans++ },
	}
	r.runDaily(context.Background())
	if scans != 1 {
		t.Fatalf("missed dates should yield one scan, got %d", scans)
	}
}
