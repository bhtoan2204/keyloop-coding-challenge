package presentation

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain"
)

func (a *api) reviseAction(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req actionRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	cmd := command.ReviseAction{
		Target:     t,
		ActionID:   r.PathValue("actionId"),
		ActionType: domain.ActionType(req.ActionType),
		Note:       req.Note,
	}
	res, err := a.commands.ReviseAction(r.Context(), cmd, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeVehicle(w, http.StatusOK, res)
}
