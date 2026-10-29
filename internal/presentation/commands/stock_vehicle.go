package commands

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/presentation/httpkit"
)

func (h *handlers) stockVehicle(w http.ResponseWriter, r *http.Request) {
	meta, err := commandMeta(r)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	var req stockVehicleRequest
	if err := httpkit.DecodeJSON(w, r, &req, false); err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	if req.VehicleID != "" && !httpkit.IsSafeID(req.VehicleID) {
		httpkit.WriteError(w, r, h.log, httpkit.InvalidField("vehicle_id", "must be 1-128 characters of A-Z a-z 0-9 . _ : @ -"))
		return
	}
	stockedOn, err := httpkit.ParseDate("stocked_on", req.StockedOn)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	cmd := command.StockVehicle{
		DealershipID: r.PathValue("dealershipId"),
		VehicleID:    req.VehicleID,
		VIN:          req.VIN,
		Make:         req.Make,
		Model:        req.Model,
		ModelYear:    req.ModelYear,
		StockedOn:    stockedOn,
	}
	res, err := h.service.StockVehicle(r.Context(), cmd, meta)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	location := "/v1/dealerships/" + res.Vehicle.DealershipID() + "/vehicles/" + res.Vehicle.VehicleID()
	w.Header().Set("Location", location)
	writeVehicle(w, http.StatusCreated, res)
}
