package commands

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/presentation/httpkit"
)

func (h *handlers) sellVehicle(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	var req sellVehicleRequest
	if err := httpkit.DecodeJSON(w, r, &req, false); err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	soldOn, err := httpkit.ParseDate("sold_on", req.SoldOn)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	cmd := command.SellVehicle{
		Target: t,
		SoldOn: soldOn,
	}
	res, err := h.service.SellVehicle(r.Context(), cmd, meta)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	writeVehicle(w, http.StatusOK, res)
}
