package main

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/service"
	"github.com/Resinat/Resin/internal/subscription"
)

// ipQualityAutoRuntime has no durable schedule or job history. All enqueue paths
// converge on the same coordinator used by manual filtered batches.
type ipQualityAutoRuntime struct {
	cp          *service.ControlPlaneService
	coordinator *ipquality.Coordinator
	updates     chan string
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	now         func() time.Time
	waitUntil   func(context.Context, time.Time) bool
	scanAll     func()
}

func newIPQualityAutoRuntime(cp *service.ControlPlaneService, coordinator *ipquality.Coordinator) *ipQualityAutoRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	r := &ipQualityAutoRuntime{
		cp: cp, coordinator: coordinator, updates: make(chan string, 256),
		ctx: ctx, cancel: cancel, now: func() time.Time { return time.Now().In(time.Local) },
		waitUntil: waitForLocalMidnight,
	}
	r.scanAll = func() { r.scanNodes(service.NodeFilters{}) }
	return r
}

func nextLocalMidnight(now time.Time) time.Time {
	year, month, day := now.Date()
	return time.Date(year, month, day+1, 0, 0, 0, 0, now.Location())
}

func waitForLocalMidnight(ctx context.Context, target time.Time) bool {
	delay := time.Until(target)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *ipQualityAutoRuntime) runDaily(ctx context.Context) {
	lastDay := r.now().Format("2006-01-02")
	for {
		target := nextLocalMidnight(r.now())
		if !r.waitUntil(ctx, target) {
			return
		}
		day := r.now().Format("2006-01-02")
		if day != lastDay && !r.now().Before(target) {
			lastDay = day
			r.scanAll()
		}
	}
}

func (r *ipQualityAutoRuntime) Start() {
	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		r.runDaily(r.ctx)
	}()
	go func() {
		defer r.wg.Done()
		for {
			select {
			case <-r.ctx.Done():
				return
			case id := <-r.updates:
				enabled := true
				r.scanNodes(service.NodeFilters{SubscriptionID: &id, Enabled: &enabled})
			}
		}
	}()
}

func (r *ipQualityAutoRuntime) Stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *ipQualityAutoRuntime) OnSubApplied(sub *subscription.Subscription) {
	if sub == nil || !sub.Enabled() {
		return
	}
	select {
	case r.updates <- sub.ID:
	default: // A later successful refresh or midnight scan rediscovers dropped work.
	}
}

func (r *ipQualityAutoRuntime) OnNodeEgressIPChanged(hash node.Hash, ip netip.Addr) {
	entry, ok := r.cp.Pool.GetEntry(hash)
	if !ok || !entry.HasEnabledSubscription(r.cp.Pool.MakeSubLookup()) {
		return
	}
	r.coordinator.EnqueueBackground(ip.String())
}

func (r *ipQualityAutoRuntime) scanNodes(filters service.NodeFilters) {
	if r.ctx.Err() != nil {
		return
	}
	enabled := true
	filters.Enabled = &enabled
	nodes, err := r.cp.ListNodes(filters)
	if err != nil {
		return
	}
	for _, n := range nodes {
		if r.ctx.Err() != nil {
			return
		}
		r.coordinator.EnqueueBackground(n.EgressIP)
	}
}

// allowedIP revalidates membership and the current egress IP before a background start.
func allowedIP(cp *service.ControlPlaneService, ip string) bool {
	if cp == nil || cp.Pool == nil {
		return false
	}
	lookup := cp.Pool.MakeSubLookup()
	found := false
	cp.Pool.Range(func(_ node.Hash, entry *node.NodeEntry) bool {
		if !entry.HasEnabledSubscription(lookup) {
			return true
		}
		current, err := ipquality.PublicIP(entry.GetEgressIP().String())
		if err == nil && current == ip {
			found = true
			return false
		}
		return true
	})
	return found
}
