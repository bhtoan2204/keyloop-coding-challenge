package commands

import (
	"net/http"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/presentation/httpkit"
)

func (h *handlers) proposeAction(w http.ResponseWriter, r *http.Request) {
	meta, t, err := commandInputs(r)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	var req actionRequest
	if err := httpkit.DecodeJSON(w, r, &req, false); err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	cmd := command.ProposeAction{
		Target:     t,
		ActionType: domain.ActionType(req.ActionType),
		Note:       req.Note,
	}
	res, err := h.service.ProposeAction(r.Context(), cmd, meta)
	if err != nil {
		httpkit.WriteError(w, r, h.log, err)
		return
	}
	w.Header().Set("Location", r.URL.Path+"/"+res.ActionID)
	writeVehicle(w, http.StatusCreated, res)
}
