package ipquality

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientNullableAndValidation(t *testing.T) {
	client := NewClient("https://example.com/api/ip/health", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("ip") != "8.8.8.8" {
			t.Errorf("query IP: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ip":"8.8.8.8","score":0,"isp":"Test","asn":"AS123","flags":{"proxy":false,"tor":null}}`)), Header: make(http.Header)}, nil
	})})
	result, err := client.Lookup(context.Background(), "8.8.8.8")
	if err != nil || result.Score == nil || *result.Score != 0 || result.Status != "poor" || result.Flags.Proxy == nil || *result.Flags.Proxy || result.Flags.Tor != nil {
		t.Fatalf("nullable result: %+v, %v", result, err)
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "203.0.113.1", "::1", "not-an-ip"} {
		if _, err := client.Lookup(context.Background(), ip); err != ErrInvalidIP {
			t.Errorf("accepted %q: %v", ip, err)
		}
	}
}

func TestClientRejectsInvalidProviderResponses(t *testing.T) {
	for _, body := range []string{
		`{"ip":"1.1.1.1","score":75}`,
		`{"ip":"8.8.8.8","score":101}`,
		`{"ip":"8.8.8.8","score":null,"flags":{"proxy":"false"}}`,
		`broken`, strings.Repeat("x", 65537),
	} {
		t.Run(body[:min(len(body), 30)], func(t *testing.T) {
			client := NewClient("https://example.com/check", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})})
			if _, err := client.Lookup(context.Background(), "8.8.8.8"); err != ErrInvalidResponse {
				t.Fatalf("expected invalid response, got %v", err)
			}
		})
	}
}

func TestClientAcceptsNumericASNAndUnknownISP(t *testing.T) {
	client := NewClient("https://example.com/check", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ip":"8.8.8.8","score":null,"isp":null,"asn":15169,"flags":{}}`)), Header: make(http.Header)}, nil
	})})
	result, err := client.Lookup(context.Background(), "8.8.8.8")
	if err != nil || result.ASN != "15169" || result.ISP != "" {
		t.Fatalf("ASN/ISP: %+v %v", result, err)
	}
}

func TestClientNormalizesIPv6(t *testing.T) {
	client := NewClient("https://example.com/check", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("ip") != "2001:4860:4860::8888" {
			t.Errorf("query IP: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ip":"2001:4860:4860:0:0:0:0:8888","score":null,"isp":"","asn":"","flags":{}}`)), Header: make(http.Header)}, nil
	})})
	result, err := client.Lookup(context.Background(), "2001:4860:4860::8888")
	if err != nil || result.IP != "2001:4860:4860::8888" || result.Score != nil || result.Status != "unknown" {
		t.Fatalf("IPv6: %+v %v", result, err)
	}
}

func TestClientStatusBoundaries(t *testing.T) {
	for _, tc := range []struct {
		score *float64
		want  string
	}{
		{nil, "unknown"}, {scorePtr(0), "poor"}, {scorePtr(44.9), "poor"},
		{scorePtr(45), "moderate"}, {scorePtr(74.9), "moderate"},
		{scorePtr(75), "good"}, {scorePtr(100), "good"},
	} {
		if got := statusFor(tc.score); got != tc.want {
			t.Errorf("score %v: %s, want %s", tc.score, got, tc.want)
		}
	}
}

func scorePtr(v float64) *float64 { return &v }

func TestClientRejectsRedirectAndTimeout(t *testing.T) {
	client := NewClient("https://example.com/check", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/check"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	if _, err := client.Lookup(context.Background(), "8.8.8.8"); err != ErrProviderUnavailable {
		t.Fatalf("redirect: %v", err)
	}
	client = NewClient("https://example.com/check", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := client.Lookup(ctx, "8.8.8.8"); err != ErrProviderUnavailable {
		t.Fatalf("timeout: %v", err)
	}
}

func TestClientUpstreamFailures(t *testing.T) {
	for _, status := range []int{429, 503} {
		client := NewClient("https://example.com/check", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("secret upstream body")), Header: make(http.Header)}, nil
		})})
		_, err := client.Lookup(context.Background(), "8.8.8.8")
		if err != ErrProviderUnavailable {
			t.Fatalf("status %d leaked error: %v", status, err)
		}
	}
}
