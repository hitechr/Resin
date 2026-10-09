package ipquality

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidIP           = errors.New("invalid public egress IP")
	ErrInvalidResponse     = errors.New("invalid provider response")
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrRateLimited         = errors.New("IP quality rate or concurrency limit")
)

const Source = "One IP / Net.Coffee"

// Result is Resin's contract, not a copy of the provider's response.
type Result struct {
	IP         string    `json:"ip"`
	Score      *float64  `json:"score"`
	Status     string    `json:"status"`
	ISP        string    `json:"isp"`
	ASN        string    `json:"asn"`
	Flags      Flags     `json:"flags"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type Flags struct {
	Residential *bool `json:"residential"`
	Datacenter  *bool `json:"datacenter"`
	VPN         *bool `json:"vpn"`
	Proxy       *bool `json:"proxy"`
	Tor         *bool `json:"tor"`
	Abuser      *bool `json:"abuser"`
}

// PublicIP excludes non-routable, special-use, and documentation prefixes.
func PublicIP(raw string) (string, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || addr.Zone() != "" {
		return "", ErrInvalidIP
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
		return "", ErrInvalidIP
	}
	for _, prefix := range excludedPrefixes {
		if prefix.Contains(addr) {
			return "", ErrInvalidIP
		}
	}
	return addr.String(), nil
}

var excludedPrefixes = func() []netip.Prefix {
	blocks := []string{"0.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "2001:db8::/32", "2001::/23", "fc00::/7", "fe80::/10"}
	out := make([]netip.Prefix, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, netip.MustParsePrefix(block))
	}
	return out
}()

type Lookup interface {
	Lookup(context.Context, string) (Result, error)
}

type Client struct {
	endpoint *url.URL
	http     *http.Client
}

func NewClient(endpoint string, transport *http.Client) *Client {
	u, _ := url.Parse(endpoint) // Validated by startup config.
	if transport == nil {
		transport = http.DefaultClient
	}
	copyClient := *transport
	copyClient.Timeout = 12 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{endpoint: u, http: &copyClient}
}

func statusFor(score *float64) string {
	if score == nil {
		return "unknown"
	}
	if *score >= 75 {
		return "good"
	}
	if *score >= 45 {
		return "moderate"
	}
	return "poor"
}

func (c *Client) Lookup(ctx context.Context, raw string) (Result, error) {
	ip, err := PublicIP(raw)
	if err != nil {
		return Result{}, err
	}
	if c.endpoint == nil || c.endpoint.Scheme != "https" {
		return Result{}, ErrProviderUnavailable
	}
	u := *c.endpoint
	q := u.Query()
	q.Set("ip", ip)
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, ErrProviderUnavailable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, ErrProviderUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, ErrProviderUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil {
		return Result{}, ErrProviderUnavailable
	}
	if len(body) > 64*1024 {
		return Result{}, ErrInvalidResponse
	}
	var payload struct {
		IP     string          `json:"ip"`
		Score  *float64        `json:"score"`
		Status *string         `json:"status"`
		ISP    *string         `json:"isp"`
		ASN    json.RawMessage `json:"asn"`
		Flags  json.RawMessage `json:"flags"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if dec.Decode(&payload) != nil || dec.Decode(new(any)) != io.EOF {
		return Result{}, ErrInvalidResponse
	}
	responseIP, err := PublicIP(payload.IP)
	if err != nil || responseIP != ip || payload.Score != nil && (*payload.Score < 0 || *payload.Score > 100) {
		return Result{}, ErrInvalidResponse
	}
	if payload.Status != nil && *payload.Status != "good" && *payload.Status != "moderate" && *payload.Status != "poor" && *payload.Status != "unknown" {
		return Result{}, ErrInvalidResponse
	}
	if len(payload.Flags) == 0 || string(payload.Flags) == "null" {
		return Result{}, ErrInvalidResponse
	}
	asn := ""
	if len(payload.ASN) != 0 && !bytes.Equal(bytes.TrimSpace(payload.ASN), []byte("null")) {
		if err := json.Unmarshal(payload.ASN, &asn); err != nil {
			var number json.Number
			if err := json.Unmarshal(payload.ASN, &number); err != nil {
				return Result{}, ErrInvalidResponse
			}
			if _, err := strconv.ParseUint(number.String(), 10, 64); err != nil {
				return Result{}, ErrInvalidResponse
			}
			asn = number.String()
		}
	}
	isp := ""
	if payload.ISP != nil {
		isp = *payload.ISP
	}
	var flags Flags
	if err := json.Unmarshal(payload.Flags, &flags); err != nil || payload.Flags[0] != '{' {
		return Result{}, ErrInvalidResponse
	}
	return Result{IP: ip, Score: payload.Score, Status: statusFor(payload.Score), ISP: isp, ASN: asn, Flags: flags, Source: Source}, nil
}
