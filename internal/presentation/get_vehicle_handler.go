package presentation

import (
	"net/http"
	"strconv"
)

func (a *api) getVehicle(w http.ResponseWriter, r *http.Request) {
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
	res, err := a.queries.GetVehicle(r.Context(), r.PathValue("dealershipId"), r.PathValue("vehicleId"), asOf)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	// The ETag is the read-model version, which can lag the write side until the next sync.
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(res.Vehicle.Version)))
	writeJSON(w, http.StatusOK, "application/json", toVehicleDetailResponse(res))
}
