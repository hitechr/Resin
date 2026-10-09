package ipquality

import "math"

// RiskVersion identifies the product policy used to calculate risk from cached reputation data.
const RiskVersion = "v1"

// ComputeRisk returns an unknown value if the provider did not supply a score.
// The floors are product assumptions, not provider coefficients or routing policy.
func ComputeRisk(result Result) (*float64, string) {
	if result.Score == nil || math.IsNaN(*result.Score) {
		return nil, RiskVersion
	}
	risk := math.Max(0, math.Min(100, 100-*result.Score))
	for _, evidence := range []struct {
		flag  *bool
		floor float64
	}{
		{result.Flags.Tor, 90},
		{result.Flags.Abuser, 85},
		{result.Flags.Proxy, 70},
		{result.Flags.VPN, 60},
		{result.Flags.Datacenter, 45},
	} {
		if evidence.flag != nil && *evidence.flag {
			risk = math.Max(risk, evidence.floor)
		}
	}
	return &risk, RiskVersion
}
