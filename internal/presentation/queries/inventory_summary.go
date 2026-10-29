package queries

import (
	"net/http"

	"keyloop-coding-challenge/internal/presentation/httpkit"
)

func (h *handlers) getInventorySummary(w http.ResponseWriter, r *http.Request) {
	params, err := httpkit.QueryParams(r, "as_of")
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	asOf, err := httpkit.OptionalDate(params, "as_of")
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	res, err := h.service.GetInventorySummary(r.Context(), r.PathValue("dealershipId"), asOf)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, toSummaryResponse(res))
}
