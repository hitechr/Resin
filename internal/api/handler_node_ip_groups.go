package api

import (
	"net/http"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/service"
)

type nodeIPGroupPage struct {
	Items               []service.NodeIPGroup `json:"items"`
	Total               int                   `json:"total"`
	PublicIPCount       int                   `json:"public_ip_count"`
	PendingQualityCount int                   `json:"pending_quality_count"`
	QueuePending        int                   `json:"queue_pending"`
	BackgroundDeferred  int64                 `json:"background_deferred"`
	Limit               int                   `json:"limit"`
	Offset              int                   `json:"offset"`
}

type nodeIPGroupDetail struct {
	Group   service.NodeIPGroup   `json:"group"`
	Quality *ipquality.Result     `json:"quality"`
	Nodes   []service.NodeSummary `json:"nodes"`
	Total   int                   `json:"total"`
	Limit   int                   `json:"limit"`
	Offset  int                   `json:"offset"`
}

func HandleListNodeIPGroups(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filters, ok := parseNodeFilters(w, r)
		if !ok {
			return
		}
		sorting, ok := parseSortingOrWriteInvalid(w, r, []string{"ip", "risk", "score", "matched_nodes", "region"}, "risk", "desc")
		if !ok {
			return
		}
		pg, ok := parsePaginationOrWriteInvalid(w, r)
		if !ok {
			return
		}
		groups, err := cp.ListNodeIPGroups(filters)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		service.SortNodeIPGroups(groups, sorting.SortBy, sorting.SortOrder)
		publicCount, pendingCount := 0, 0
		for _, group := range groups {
			if group.Key == service.UnresolvedIPGroupKey {
				continue
			}
			publicCount++
			if group.State != "fresh" {
				pendingCount++
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		queue := ipquality.QueueSnapshot{}
		if cp.IPCoordinator != nil {
			queue = cp.IPCoordinator.QueueStatus()
		}
		WriteJSON(w, http.StatusOK, nodeIPGroupPage{
			Items: PaginateSlice(groups, pg), Total: len(groups), PublicIPCount: publicCount,
			PendingQualityCount: pendingCount, QueuePending: queue.Pending, BackgroundDeferred: queue.BackgroundDeferred,
			Limit: pg.Limit, Offset: pg.Offset,
		})
	}
}

func HandleGetNodeIPGroup(cp *service.ControlPlaneService, unresolved bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filters, ok := parseNodeFilters(w, r)
		if !ok {
			return
		}
		pg, ok := parsePaginationOrWriteInvalid(w, r)
		if !ok {
			return
		}
		key := r.PathValue("ip")
		if unresolved {
			key = service.UnresolvedIPGroupKey
		}
		group, members, err := cp.GetNodeIPGroup(filters, key)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		var quality *ipquality.Result
		if group.IP != "" && cp.IPQuality != nil {
			if result, found, _ := cp.IPQuality.Cached(group.IP); found {
				quality = &result
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, nodeIPGroupDetail{Group: group, Quality: quality, Nodes: PaginateSlice(members, pg), Total: len(members), Limit: pg.Limit, Offset: pg.Offset})
	}
}
