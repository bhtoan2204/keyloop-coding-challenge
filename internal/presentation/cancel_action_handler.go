package presentation

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
)

func (a *api) cancelAction(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req cancelActionRequest
	if err := decodeJSON(w, r, &req, true); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	cmd := command.CancelAction{
		Target:   t,
		ActionID: r.PathValue("actionId"),
		Reason:   req.Reason,
	}
	res, err := a.commands.CancelAction(r.Context(), cmd, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeVehicle(w, http.StatusOK, res)
}
