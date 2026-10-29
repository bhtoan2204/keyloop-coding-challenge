package presentation

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
)

func (a *api) sellVehicle(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req sellVehicleRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	soldOn, err := parseDate("sold_on", req.SoldOn)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	cmd := command.SellVehicle{
		Target: t,
		SoldOn: soldOn,
	}
	res, err := a.commands.SellVehicle(r.Context(), cmd, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeVehicle(w, http.StatusOK, res)
}
