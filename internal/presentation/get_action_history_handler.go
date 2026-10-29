package presentation

import "net/http"

func (a *api) getActionHistory(w http.ResponseWriter, r *http.Request) {
	params, err := queryParams(r, "limit", "cursor")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	limit, err := intOrZero(params, "limit")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	page, err := a.queries.GetActionHistory(
		r.Context(),
		r.PathValue("dealershipId"),
		r.PathValue("vehicleId"),
		limit,
		params.Get("cursor"),
	)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, "application/json", toHistoryResponse(page))
}
