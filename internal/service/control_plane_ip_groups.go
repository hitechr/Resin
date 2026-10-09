package service

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/Resinat/Resin/internal/ipquality"
)

const UnresolvedIPGroupKey = "unresolved"

type NodeIPGroup struct {
	Key              string     `json:"key"`
	IP               string     `json:"ip"`
	MatchedNodeCount int        `json:"matched_node_count"`
	Score            *float64   `json:"score"`
	Risk             *float64   `json:"risk"`
	RiskVersion      string     `json:"risk_version"`
	Residential      *bool      `json:"residential"`
	ASN              string     `json:"asn"`
	State            string     `json:"state"`
	ObservedAt       *time.Time `json:"observed_at"`
}

func nodeIPGroupKey(raw string) string {
	ip, err := ipquality.PublicIP(raw)
	if err != nil {
		return UnresolvedIPGroupKey
	}
	return ip
}

func projectNodeIPGroups(nodes []NodeSummary, cached func(string) (ipquality.Result, bool, bool)) []NodeIPGroup {
	byIP := make(map[string]*NodeIPGroup)
	for _, n := range nodes {
		key := nodeIPGroupKey(n.EgressIP)
		group := byIP[key]
		if group == nil {
			group = &NodeIPGroup{Key: key, IP: key, State: "unknown", RiskVersion: ipquality.RiskVersion}
			if key == UnresolvedIPGroupKey {
				group.IP = ""
				group.State = "unresolved"
				group.RiskVersion = ""
			} else if cached != nil {
				if result, found, fresh := cached(key); found {
					group.Score = result.Score
					group.Risk, group.RiskVersion = ipquality.ComputeRisk(result)
					group.Residential = result.Flags.Residential
					group.ASN = result.ASN
					if !result.ObservedAt.IsZero() {
						observed := result.ObservedAt
						group.ObservedAt = &observed
					}
					group.State = "stale"
					if fresh {
						group.State = "fresh"
					}
				}
			}
			byIP[key] = group
		}
		group.MatchedNodeCount++
	}
	groups := make([]NodeIPGroup, 0, len(byIP))
	for _, group := range byIP {
		groups = append(groups, *group)
	}
	return groups
}

func (s *ControlPlaneService) ListNodeIPGroups(filters NodeFilters) ([]NodeIPGroup, error) {
	nodes, err := s.ListNodes(filters)
	if err != nil {
		return nil, err
	}
	var cached func(string) (ipquality.Result, bool, bool)
	if s.IPQuality != nil {
		cached = s.IPQuality.Cached
	}
	return projectNodeIPGroups(nodes, cached), nil
}

func (s *ControlPlaneService) GetNodeIPGroup(filters NodeFilters, raw string) (NodeIPGroup, []NodeSummary, error) {
	key := raw
	if key != UnresolvedIPGroupKey {
		var err error
		key, err = ipquality.PublicIP(raw)
		if err != nil {
			return NodeIPGroup{}, nil, invalidArg("ip: invalid public IP")
		}
	}
	nodes, err := s.ListNodes(filters)
	if err != nil {
		return NodeIPGroup{}, nil, err
	}
	members := make([]NodeSummary, 0)
	for _, n := range nodes {
		if nodeIPGroupKey(n.EgressIP) == key {
			members = append(members, n)
		}
	}
	if len(members) == 0 {
		return NodeIPGroup{}, nil, notFound("IP group not found in filtered pool")
	}
	slices.SortFunc(members, func(a, b NodeSummary) int { return strings.Compare(a.NodeHash, b.NodeHash) })
	var cached func(string) (ipquality.Result, bool, bool)
	if s.IPQuality != nil {
		cached = s.IPQuality.Cached
	}
	return projectNodeIPGroups(members, cached)[0], members, nil
}

func (s *ControlPlaneService) StartNodeIPBatch(filters NodeFilters) (ipquality.Job, error) {
	if !s.qualityEnabled() || s.IPCoordinator == nil {
		return ipquality.Job{}, &ServiceError{Code: "IP_QUALITY_DISABLED", Message: "IP quality batch is disabled"}
	}
	nodes, err := s.ListNodes(filters)
	if err != nil {
		return ipquality.Job{}, err
	}
	ips := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ips = append(ips, n.EgressIP)
	}
	return s.IPCoordinator.StartManual(ips)
}

func (s *ControlPlaneService) GetNodeIPBatch(id string) (ipquality.Job, error) {
	if !s.qualityEnabled() || s.IPCoordinator == nil {
		return ipquality.Job{}, &ServiceError{Code: "IP_QUALITY_DISABLED", Message: "IP quality batch is disabled"}
	}
	job, ok := s.IPCoordinator.Status(id)
	if !ok {
		return ipquality.Job{}, &ServiceError{Code: "IP_QUALITY_JOB_NOT_FOUND", Message: "IP quality batch not found or expired"}
	}
	return job, nil
}

func (s *ControlPlaneService) CancelNodeIPBatch(id string) (ipquality.Job, error) {
	job, err := s.GetNodeIPBatch(id)
	if err != nil {
		return ipquality.Job{}, err
	}
	if job.EndedAt != nil || !s.IPCoordinator.Cancel(id) {
		return ipquality.Job{}, conflict("IP quality batch has already ended")
	}
	return s.GetNodeIPBatch(id)
}

func compareKnownFloat(a, b *float64, order string) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return 1
	}
	if b == nil {
		return -1
	}
	comparison := cmp.Compare(*a, *b)
	if order == "desc" {
		return -comparison
	}
	return comparison
}

// SortNodeIPGroups sorts the entire filtered projection before API pagination.
func SortNodeIPGroups(groups []NodeIPGroup, sortBy, order string) {
	slices.SortStableFunc(groups, func(a, b NodeIPGroup) int {
		if a.Key == UnresolvedIPGroupKey {
			if b.Key == UnresolvedIPGroupKey {
				return 0
			}
			return 1
		}
		if b.Key == UnresolvedIPGroupKey {
			return -1
		}
		var comparison int
		switch sortBy {
		case "risk":
			comparison = compareKnownFloat(a.Risk, b.Risk, order)
		case "score":
			comparison = compareKnownFloat(a.Score, b.Score, order)
		case "matched_nodes":
			comparison = cmp.Compare(a.MatchedNodeCount, b.MatchedNodeCount)
		case "ip":
			comparison = strings.Compare(a.IP, b.IP)
		}
		if sortBy != "risk" && sortBy != "score" && order == "desc" {
			comparison = -comparison
		}
		if comparison != 0 {
			return comparison
		}
		return strings.Compare(a.IP, b.IP)
	})
}
