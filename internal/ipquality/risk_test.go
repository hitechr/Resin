package ipquality

import (
	"math"
	"testing"
)

func TestComputeRisk(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	flag := func(v bool) *bool { return &v }
	tests := []struct {
		name   string
		result Result
		want   *float64
	}{
		{"perfect score", Result{Score: ptr(100)}, ptr(0)},
		{"score 75", Result{Score: ptr(75)}, ptr(25)},
		{"score 45", Result{Score: ptr(45)}, ptr(55)},
		{"score 44.9", Result{Score: ptr(44.9)}, ptr(55.1)},
		{"score zero", Result{Score: ptr(0)}, ptr(100)},
		{"fraction preserved", Result{Score: ptr(88.25)}, ptr(11.75)},
		{"tor floor", Result{Score: ptr(95), Flags: Flags{Tor: flag(true)}}, ptr(90)},
		{"abuser floor", Result{Score: ptr(95), Flags: Flags{Abuser: flag(true)}}, ptr(85)},
		{"proxy floor", Result{Score: ptr(95), Flags: Flags{Proxy: flag(true)}}, ptr(70)},
		{"vpn floor", Result{Score: ptr(95), Flags: Flags{VPN: flag(true)}}, ptr(60)},
		{"datacenter floor", Result{Score: ptr(95), Flags: Flags{Datacenter: flag(true)}}, ptr(45)},
		{"stronger base", Result{Score: ptr(10), Flags: Flags{Datacenter: flag(true)}}, ptr(90)},
		{"multiple flags use maximum", Result{Score: ptr(95), Flags: Flags{VPN: flag(true), Proxy: flag(true), Tor: flag(true)}}, ptr(90)},
		{"false and unknown do not contribute", Result{Score: ptr(95), Flags: Flags{Tor: flag(false)}}, ptr(5)},
		{"residential does not discount", Result{Score: ptr(50), Flags: Flags{Residential: flag(true)}}, ptr(50)},
		{"status and ASN are display only", Result{Score: ptr(95), Status: "poor", ASN: "12345"}, ptr(5)},
		{"clamp high score", Result{Score: ptr(125)}, ptr(0)},
		{"clamp low score", Result{Score: ptr(-20)}, ptr(100)},
		{"unknown score with flag stays unknown", Result{Flags: Flags{Tor: flag(true)}}, nil},
		{"unknown score without flags", Result{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, version := ComputeRisk(tt.result)
			if version != RiskVersion || version != "v1" {
				t.Fatalf("risk version: %q", version)
			}
			if (got == nil) != (tt.want == nil) || got != nil && math.Abs(*got-*tt.want) > 1e-9 {
				t.Fatalf("risk: got %v, want %v", got, tt.want)
			}
		})
	}
}
