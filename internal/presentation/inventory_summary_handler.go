package presentation

import "net/http"

func (a *api) getInventorySummary(w http.ResponseWriter, r *http.Request) {
	params, err := queryParams(r, "as_of")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	asOf, err := optionalDate(params, "as_of")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.queries.GetInventorySummary(r.Context(), r.PathValue("dealershipId"), asOf)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, "application/json", toSummaryResponse(res))
}
