package queries

import (
	"net/http"

	"keyloop-coding-challenge/internal/presentation/httpkit"
)

func (h *handlers) getActionHistory(w http.ResponseWriter, r *http.Request) {
	params, err := httpkit.QueryParams(r, "limit", "cursor")
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	limit, err := httpkit.IntOrZero(params, "limit")
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	page, err := h.service.GetActionHistory(
		r.Context(),
		r.PathValue("dealershipId"),
		r.PathValue("vehicleId"),
		limit,
		params.Get("cursor"),
	)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, toHistoryResponse(page))
}
