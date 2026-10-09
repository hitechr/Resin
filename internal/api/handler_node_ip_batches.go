package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/service"
)

var nodeIPBatchQueryKeys = map[string]bool{
	"platform_id": true, "subscription_id": true, "enabled": true,
	"region": true, "circuit_open": true, "has_outbound": true,
	"egress_ip": true, "probed_since": true, "tag_keyword": true,
	"limit": true, "offset": true, "sort_by": true, "sort_order": true,
}

func writeIPBatchError(w http.ResponseWriter, err error) {
	var svcErr *service.ServiceError
	switch {
	case errors.Is(err, ipquality.ErrBatchTooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, "IP_QUALITY_BATCH_TOO_LARGE", "filtered pool exceeds 1000 distinct public IPs")
	case errors.Is(err, ipquality.ErrQueueFull):
		WriteError(w, http.StatusTooManyRequests, "IP_QUALITY_QUEUE_FULL", "IP quality queue is full; retry later")
	case errors.Is(err, ipquality.ErrCoordinatorStopped):
		WriteError(w, http.StatusServiceUnavailable, "IP_QUALITY_STOPPED", "IP quality coordinator is stopping")
	case errors.As(err, &svcErr) && svcErr.Code == "IP_QUALITY_DISABLED":
		WriteError(w, http.StatusServiceUnavailable, svcErr.Code, svcErr.Message)
	case errors.As(err, &svcErr) && svcErr.Code == "IP_QUALITY_JOB_NOT_FOUND":
		WriteError(w, http.StatusNotFound, svcErr.Code, svcErr.Message)
	default:
		writeServiceError(w, err)
	}
}

func HandleStartNodeIPBatch(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for key := range r.URL.Query() {
			if !nodeIPBatchQueryKeys[key] {
				writeInvalidArgument(w, "unknown batch filter: "+key)
				return
			}
		}
		// Targets are always derived from server-side filters, never from a request body.
		if r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(body) != 0 {
				writeInvalidArgument(w, "batch request body is not allowed")
				return
			}
		}
		filters, ok := parseNodeFilters(w, r)
		if !ok {
			return
		}
		job, err := cp.StartNodeIPBatch(filters)
		if err != nil {
			writeIPBatchError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusAccepted, job)
	}
}

func HandleGetNodeIPBatch(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, err := cp.GetNodeIPBatch(r.PathValue("id"))
		if err != nil {
			writeIPBatchError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, job)
	}
}

func HandleCancelNodeIPBatch(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, err := cp.CancelNodeIPBatch(r.PathValue("id"))
		if err != nil {
			writeIPBatchError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, job)
	}
}
