package api

import (
	"errors"
	"net/http"

	"github.com/Resinat/Resin/internal/ipquality"
	"github.com/Resinat/Resin/internal/service"
)

func HandleGetNodeIPQuality(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := cp.GetNodeIPQuality(r.PathValue("hash"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, result)
	}
}

func HandleCheckNodeIPQuality(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := cp.CheckNodeIPQuality(r.Context(), r.PathValue("hash"))
		if err != nil {
			var svcErr *service.ServiceError
			switch {
			case errors.As(err, &svcErr) && svcErr.Code == "IP_QUALITY_DISABLED":
				WriteError(w, http.StatusServiceUnavailable, "IP_QUALITY_DISABLED", "IP quality is disabled")
			case errors.As(err, &svcErr) && svcErr.Code == "NO_EGRESS_IP":
				WriteError(w, http.StatusConflict, "NO_EGRESS_IP", "node has no public egress IP")
			case errors.As(err, &svcErr) && svcErr.Code == "EGRESS_IP_CHANGED":
				WriteError(w, http.StatusConflict, "EGRESS_IP_CHANGED", "node egress IP changed during check")
			case errors.Is(err, ipquality.ErrRateLimited):
				WriteError(w, http.StatusTooManyRequests, "IP_QUALITY_RATE_LIMITED", "IP quality request limit reached")
			case errors.Is(err, ipquality.ErrInvalidResponse):
				WriteError(w, http.StatusBadGateway, "IP_QUALITY_INVALID_RESPONSE", "IP quality provider returned an invalid response")
			case errors.Is(err, ipquality.ErrProviderUnavailable):
				WriteError(w, http.StatusServiceUnavailable, "IP_QUALITY_UNAVAILABLE", "IP quality provider unavailable")
			default:
				writeServiceError(w, err)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, result)
	}
}
