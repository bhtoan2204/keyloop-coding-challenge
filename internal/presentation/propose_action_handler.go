package presentation

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain"
)

func (a *api) proposeAction(w http.ResponseWriter, r *http.Request) {
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
	cmd := command.ProposeAction{
		Target:     t,
		ActionType: domain.ActionType(req.ActionType),
		Note:       req.Note,
	}
	res, err := a.commands.ProposeAction(r.Context(), cmd, meta)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	w.Header().Set("Location", r.URL.Path+"/"+res.ActionID)
	writeVehicle(w, http.StatusCreated, res)
}
