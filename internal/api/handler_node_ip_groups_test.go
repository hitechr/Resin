package api

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/subscription"
)

type groupTestLookup func(context.Context, string) (ipquality.Result, error)

func (f groupTestLookup) Lookup(ctx context.Context, ip string) (ipquality.Result, error) {
	return f(ctx, ip)
}

func TestNodeIPGroupsFilteredListSortBeforePaginationAndDetails(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	subA := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "A", "https://example.com/a", true, false)
	subB := subscription.NewSubscription("22222222-2222-2222-2222-222222222222", "B", "https://example.com/b", true, false)
	cp.SubMgr.Register(subA)
	cp.SubMgr.Register(subB)
	addNodeForNodeListTest(t, cp, subA, `{"type":"ss","server":"a"}`, "8.8.8.8")
	addNodeForNodeListTest(t, cp, subA, `{"type":"ss","server":"b"}`, "8.8.8.8")
	addNodeForNodeListTest(t, cp, subA, `{"type":"ss","server":"c"}`, "1.1.1.1")
	addNodeForNodeListTest(t, cp, subB, `{"type":"ss","server":"d"}`, "4.4.4.4")
	addNodeForNodeListTest(t, cp, subA, `{"type":"ss","server":"e"}`, "")
	// The test GeoIP service is a no-op, so set explicit egress regions.
	for _, tc := range []struct{ raw, region string }{
		{`{"type":"ss","server":"a"}`, "us"},
		{`{"type":"ss","server":"b"}`, "us"},
		{`{"type":"ss","server":"c"}`, "jp"},
	} {
		if entry, ok := cp.Pool.GetEntry(node.HashFromRawOptions([]byte(tc.raw))); ok {
			entry.SetEgressRegion(tc.region)
		}
	}
	path := "/api/v1/node-ip-groups?subscription_id=" + subA.ID
	rec := doJSONRequest(t, srv, http.MethodGet, path+"&sort_by=ip&sort_order=desc&limit=1&offset=1", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeJSONMap(t, rec)
	items := body["items"].([]any)
	if body["total"] != float64(3) || body["public_ip_count"] != float64(2) || len(items) != 1 || items[0].(map[string]any)["ip"] != "1.1.1.1" {
		t.Fatalf("filtered sorted page: %s", rec.Body.String())
	}
	rec = doJSONRequest(t, srv, http.MethodGet, path+"&sort_by=region&sort_order=desc&limit=1", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("region sort: %d %s", rec.Code, rec.Body.String())
	}
	items = decodeJSONMap(t, rec)["items"].([]any)
	if items[0].(map[string]any)["region"] != "US" {
		t.Fatalf("group rows must expose aggregated region: %s", rec.Body.String())
	}
	rec = doJSONRequest(t, srv, http.MethodGet, "/api/v1/node-ip-groups/8.8.8.8?subscription_id="+subA.ID+"&limit=1", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	body = decodeJSONMap(t, rec)
	if body["total"] != float64(2) || len(body["nodes"].([]any)) != 1 {
		t.Fatalf("detail pagination: %s", rec.Body.String())
	}
	rec = doJSONRequest(t, srv, http.MethodGet, "/api/v1/node-ip-groups/unresolved?subscription_id="+subA.ID, nil, true)
	if rec.Code != http.StatusOK || decodeJSONMap(t, rec)["total"] != float64(1) {
		t.Fatalf("unresolved detail: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSONRequest(t, srv, http.MethodGet, "/api/v1/node-ip-groups/4.4.4.4?subscription_id="+subA.ID, nil, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("IP absent from filtered pool: %d", rec.Code)
	}
}

func TestNodeIPBatchUsesAllFilteredIPsAndRejectsTargetBody(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	cp.EnvCfg.IPQualityEnabled = true
	cp.IPQuality = ipquality.NewService(groupTestLookup(func(_ context.Context, ip string) (ipquality.Result, error) {
		return ipquality.Result{IP: ip}, nil
	}))
	cp.IPCoordinator = ipquality.NewCoordinator(cp.IPQuality, nil)
	t.Cleanup(cp.IPCoordinator.Stop)
	sub := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "A", "https://example.com/a", true, false)
	cp.SubMgr.Register(sub)
	for _, tc := range []struct{ raw, ip string }{
		{`{"type":"ss","server":"a"}`, "8.8.8.8"},
		{`{"type":"ss","server":"b"}`, "8.8.8.8"},
		{`{"type":"ss","server":"c"}`, "1.1.1.1"},
		{`{"type":"ss","server":"d"}`, ""},
	} {
		addNodeForNodeListTest(t, cp, sub, tc.raw, tc.ip)
	}
	path := "/api/v1/node-ip-batches?subscription_id=" + sub.ID + "&limit=1&offset=1"
	if rec := doJSONRequest(t, srv, http.MethodPost, path, map[string]any{"ips": []string{"4.4.4.4"}}, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("caller-supplied targets should be rejected: %d %s", rec.Code, rec.Body.String())
	}
	rec := doJSONRequest(t, srv, http.MethodPost, path, nil, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	job := decodeJSONMap(t, rec)
	if job["total"] != float64(2) {
		t.Fatalf("batch should include both distinct filtered IPs: %s", rec.Body.String())
	}
	id := job["id"].(string)
	rec = doJSONRequest(t, srv, http.MethodGet, "/api/v1/node-ip-batches/"+id, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSONRequest(t, srv, http.MethodGet, "/api/v1/node-ip-batches/missing", nil, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("lost job must return precise 404: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeIPBatchDisabledAndAuth(t *testing.T) {
	srv, _, _ := newControlPlaneTestServer(t)
	if rec := doJSONRequest(t, srv, http.MethodPost, "/api/v1/node-ip-batches", nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated batch: %d", rec.Code)
	}
	if rec := doJSONRequest(t, srv, http.MethodPost, "/api/v1/node-ip-batches", nil, true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled batch: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeIPGroupsRiskSortAcrossPages(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	sub := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "A", "https://example.com/a", true, false)
	cp.SubMgr.Register(sub)
	for _, tc := range []struct{ raw, ip string }{
		{`{"type":"ss","server":"a"}`, "8.8.8.8"},
		{`{"type":"ss","server":"b"}`, "1.1.1.1"},
		{`{"type":"ss","server":"c"}`, "9.9.9.9"},
	} {
		addNodeForNodeListTest(t, cp, sub, tc.raw, tc.ip)
	}
	cp.IPQuality = ipquality.NewService(groupTestLookup(func(_ context.Context, ip string) (ipquality.Result, error) {
		score := 80.0
		if ip == "8.8.8.8" {
			score = 20
		}
		return ipquality.Result{IP: ip, Score: &score}, nil
	}))
	for _, ip := range []string{"8.8.8.8", "1.1.1.1"} {
		if _, err := cp.IPQuality.CheckQueued(context.Background(), ip); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ order, want string }{{"desc", "1.1.1.1"}, {"asc", "8.8.8.8"}} {
		rec := doJSONRequest(t, srv, http.MethodGet, "/api/v1/node-ip-groups?sort_by=risk&sort_order="+tc.order+"&limit=1&offset=1", nil, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("risk page: %d %s", rec.Code, rec.Body.String())
		}
		items := decodeJSONMap(t, rec)["items"].([]any)
		if got := items[0].(map[string]any)["ip"]; got != tc.want {
			t.Fatalf("risk %s page 2: got %v, want %s", tc.order, got, tc.want)
		}
	}
}

func TestNodeIPGroupReadsNeverCallProvider(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	var calls atomic.Int32
	cp.IPQuality = ipquality.NewService(groupTestLookup(func(_ context.Context, ip string) (ipquality.Result, error) {
		calls.Add(1)
		return ipquality.Result{IP: ip}, nil
	}))
	sub := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "A", "https://example.com/a", true, false)
	cp.SubMgr.Register(sub)
	addNodeForNodeListTest(t, cp, sub, `{"type":"ss","server":"a"}`, "8.8.8.8")
	for _, path := range []string{"/api/v1/node-ip-groups", "/api/v1/node-ip-groups/8.8.8.8"} {
		if rec := doJSONRequest(t, srv, http.MethodGet, path, nil, true); rec.Code != http.StatusOK {
			t.Fatalf("cache-only read: %d %s", rec.Code, rec.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("read made %d external lookups", calls.Load())
	}
}

func TestNodeIPGroupsIPv6Detail(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	sub := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "A", "https://example.com/a", true, false)
	cp.SubMgr.Register(sub)
	addNodeForNodeListTest(t, cp, sub, `{"type":"ss","server":"v6"}`, "2606:4700:4700::1111")
	rec := doJSONRequest(t, srv, http.MethodGet, "/api/v1/node-ip-groups/2606:4700:4700::1111", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("IPv6 detail: %d %s", rec.Code, rec.Body.String())
	}
	if decodeJSONMap(t, rec)["total"] != float64(1) {
		t.Fatalf("IPv6 detail count: %s", rec.Body.String())
	}
}

func TestNodeIPGroupsAuthAndValidation(t *testing.T) {
	srv, _, _ := newControlPlaneTestServer(t)
	for _, path := range []string{"/api/v1/node-ip-groups", "/api/v1/node-ip-groups/8.8.8.8"} {
		if rec := doJSONRequest(t, srv, http.MethodGet, path, nil, false); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without auth: %d", path, rec.Code)
		}
	}
	for _, path := range []string{
		"/api/v1/node-ip-groups?sort_by=unknown",
		"/api/v1/node-ip-groups?sort_order=invalid",
		"/api/v1/node-ip-groups?enabled=maybe",
		"/api/v1/node-ip-groups/192.0.2.1",
	} {
		if rec := doJSONRequest(t, srv, http.MethodGet, path, nil, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
