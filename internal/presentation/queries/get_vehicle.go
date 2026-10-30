package queries

import (
	"net/http"
	"strconv"

	"keyloop-coding-challenge/internal/presentation/httpkit"
)

func (h *handlers) getVehicle(w http.ResponseWriter, r *http.Request) {
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
	res, err := h.service.GetVehicle(r.Context(), r.PathValue("dealershipId"), r.PathValue("vehicleId"), asOf)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	// The ETag is the projected version, committed together with the events, so it is
	// usable for If-Match immediately.
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(res.Vehicle.Version)))
	httpkit.WriteJSON(w, http.StatusOK, toVehicleDetailResponse(res))
}
