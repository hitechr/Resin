package api

import (
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/subscription"
)

type qualityTransport func(*http.Request) (*http.Response, error)

func (f qualityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNodeIPQualityAuthCacheAndChangedIP(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	cp.EnvCfg.IPQualityEnabled = true
	sub := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "quality", "https://example.com", true, false)
	cp.SubMgr.Register(sub)
	raw := `{"type":"ss","server":"1.1.1.1","port":443}`
	addNodeForNodeListTest(t, cp, sub, raw, "8.8.8.8")
	hash := node.HashFromRawOptions([]byte(raw)).Hex()
	path := "/api/v1/nodes/" + hash + "/ip-quality"
	check := "/api/v1/nodes/" + hash + "/actions/check-ip-quality"
	calls := 0
	cp.IPQuality = ipquality.NewService(ipquality.NewClient("https://example.com/check", &http.Client{Transport: qualityTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ip":"8.8.8.8","score":0,"isp":"Test","asn":"AS1","flags":{"proxy":false,"tor":null}}`)), Header: make(http.Header)}, nil
	})}))
	if rec := doJSONRequest(t, srv, "GET", path, nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET auth: %d", rec.Code)
	}
	if rec := doJSONRequest(t, srv, "POST", check, nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST auth: %d", rec.Code)
	}
	if body := decodeJSONMap(t, doJSONRequest(t, srv, "GET", path, nil, true)); body["state"] != "not_checked" || calls != 0 {
		t.Fatalf("cache-only: %v, calls=%d", body, calls)
	}
	body := decodeJSONMap(t, doJSONRequest(t, srv, "POST", check, nil, true))
	quality, ok := body["quality"].(map[string]any)
	if !ok || quality["score"] != float64(0) || quality["status"] != "poor" || calls != 1 {
		t.Fatalf("check: %v calls=%d", body, calls)
	}
	flags := quality["flags"].(map[string]any)
	if flags["proxy"] != false || flags["tor"] != nil {
		t.Fatalf("flags: %v", flags)
	}
	if body := decodeJSONMap(t, doJSONRequest(t, srv, "GET", path, nil, true)); body["state"] != "fresh" || calls != 1 {
		t.Fatalf("cached GET: %v calls=%d", body, calls)
	}
	entry, _ := cp.Pool.GetEntry(node.HashFromRawOptions([]byte(raw)))
	entry.SetEgressIP(netip.MustParseAddr("1.1.1.1"))
	if body := decodeJSONMap(t, doJSONRequest(t, srv, "GET", path, nil, true)); body["state"] != "not_checked" || body["quality"] != nil {
		t.Fatalf("old IP leaked: %v", body)
	}
}

func TestNodeIPQualityChangedDuringCheckIsConflict(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	cp.EnvCfg.IPQualityEnabled = true
	sub := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "quality", "https://example.com", true, false)
	cp.SubMgr.Register(sub)
	raw := `{"type":"ss","server":"1.1.1.1","port":443}`
	addNodeForNodeListTest(t, cp, sub, raw, "8.8.8.8")
	hash := node.HashFromRawOptions([]byte(raw))
	entry, _ := cp.Pool.GetEntry(hash)
	failureCount := entry.FailureCount.Load()
	cp.IPQuality = ipquality.NewService(ipquality.NewClient("https://example.com/check", &http.Client{Transport: qualityTransport(func(*http.Request) (*http.Response, error) {
		entry.SetEgressIP(netip.MustParseAddr("1.1.1.1"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ip":"8.8.8.8","score":null,"isp":"","asn":"","flags":{}}`)), Header: make(http.Header)}, nil
	})}))
	path := "/api/v1/nodes/" + hash.Hex()
	rec := doJSONRequest(t, srv, "POST", path+"/actions/check-ip-quality", nil, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "EGRESS_IP_CHANGED") {
		t.Fatalf("changed IP: %d %s", rec.Code, rec.Body.String())
	}
	if entry.FailureCount.Load() != failureCount {
		t.Fatal("quality check mutated node health")
	}
	if body := decodeJSONMap(t, doJSONRequest(t, srv, "GET", path+"/ip-quality", nil, true)); body["quality"] != nil {
		t.Fatalf("old IP leaked: %v", body)
	}
	if _, ok, _ := cp.IPQuality.Cached("8.8.8.8"); !ok {
		t.Fatal("old IP result not cached")
	}
}

func TestNodeIPQualityDisabledAndMissingIP(t *testing.T) {
	srv, cp, _ := newControlPlaneTestServer(t)
	sub := subscription.NewSubscription("11111111-1111-1111-1111-111111111111", "quality", "https://example.com", true, false)
	cp.SubMgr.Register(sub)
	raw := `{"type":"ss","server":"1.1.1.1","port":443}`
	addNodeForNodeListTest(t, cp, sub, raw, "")
	path := "/api/v1/nodes/" + node.HashFromRawOptions([]byte(raw)).Hex() + "/ip-quality"
	if body := decodeJSONMap(t, doJSONRequest(t, srv, "GET", path, nil, true)); body["state"] != "disabled" {
		t.Fatalf("disabled: %v", body)
	}
	cp.EnvCfg.IPQualityEnabled = true
	if body := decodeJSONMap(t, doJSONRequest(t, srv, "GET", path, nil, true)); body["state"] != "no_egress_ip" {
		t.Fatalf("no IP: %v", body)
	}
}
