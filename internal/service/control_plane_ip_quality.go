package service

import (
	"context"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/node"
)

type NodeIPQualityResponse struct {
	State    string            `json:"state"`
	EgressIP string            `json:"egress_ip,omitempty"`
	Quality  *ipquality.Result `json:"quality,omitempty"`
}

func (s *ControlPlaneService) qualityNode(hash string) (*node.NodeEntry, error) {
	h, err := node.ParseHex(hash)
	if err != nil {
		return nil, invalidArg("node_hash: invalid format")
	}
	if s.Pool == nil {
		return nil, notFound("node not found")
	}
	entry, ok := s.Pool.GetEntry(h)
	if !ok {
		return nil, notFound("node not found")
	}
	return entry, nil
}

func (s *ControlPlaneService) qualityEnabled() bool {
	return s.EnvCfg != nil && s.EnvCfg.IPQualityEnabled
}

func (s *ControlPlaneService) GetNodeIPQuality(hash string) (NodeIPQualityResponse, error) {
	entry, err := s.qualityNode(hash)
	if err != nil {
		return NodeIPQualityResponse{}, err
	}
	if !s.qualityEnabled() {
		return NodeIPQualityResponse{State: "disabled"}, nil
	}
	ip, err := ipquality.PublicIP(entry.GetEgressIP().String())
	if err != nil {
		return NodeIPQualityResponse{State: "no_egress_ip"}, nil
	}
	resp := NodeIPQualityResponse{State: "not_checked", EgressIP: ip}
	if s.IPQuality != nil {
		if result, ok, fresh := s.IPQuality.Cached(ip); ok {
			resp.Quality = &result
			if fresh {
				resp.State = "fresh"
			} else {
				resp.State = "stale"
			}
		}
	}
	return resp, nil
}

func (s *ControlPlaneService) CheckNodeIPQuality(ctx context.Context, hash string) (NodeIPQualityResponse, error) {
	entry, err := s.qualityNode(hash)
	if err != nil {
		return NodeIPQualityResponse{}, err
	}
	if !s.qualityEnabled() || s.IPQuality == nil {
		return NodeIPQualityResponse{}, &ServiceError{Code: "IP_QUALITY_DISABLED", Message: "IP quality is disabled"}
	}
	ip, err := ipquality.PublicIP(entry.GetEgressIP().String())
	if err != nil {
		return NodeIPQualityResponse{}, &ServiceError{Code: "NO_EGRESS_IP", Message: "node has no public egress IP"}
	}
	result, err := s.IPQuality.Check(ctx, ip)
	if err != nil {
		return NodeIPQualityResponse{}, err
	}
	current, err := s.qualityNode(hash)
	if err != nil {
		return NodeIPQualityResponse{}, err
	}
	currentIP, err := ipquality.PublicIP(current.GetEgressIP().String())
	if err != nil || currentIP != ip {
		return NodeIPQualityResponse{}, &ServiceError{Code: "EGRESS_IP_CHANGED", Message: "node egress IP changed during check"}
	}
	return NodeIPQualityResponse{State: "fresh", EgressIP: ip, Quality: &result}, nil
}
