package presentation

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
)

func (a *api) stockVehicle(w http.ResponseWriter, r *http.Request) {
	meta, err := commandMeta(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req stockVehicleRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if req.VehicleID != "" && !safeID.MatchString(req.VehicleID) {
		writeError(w, r, a.log, invalidField("vehicle_id", "must be 1-128 characters of A-Z a-z 0-9 . _ : @ -"))
		return
	}
	stockedOn, err := parseDate("stocked_on", req.StockedOn)
	if err != nil {
		writeError(w, r, a.log, err)
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
	res, err := a.commands.StockVehicle(r.Context(), cmd, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	location := "/v1/dealerships/" + res.Vehicle.DealershipID() + "/vehicles/" + res.Vehicle.VehicleID()
	w.Header().Set("Location", location)
	writeVehicle(w, http.StatusCreated, res)
}
