package presentation

import (
	"context"
	"net/http"
	"time"
)

func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}

func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	if a.ready == nil {
		writeJSON(w, http.StatusServiceUnavailable, "application/json", map[string]string{"status": "unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.ready(ctx); err != nil {
		a.log.WarnContext(r.Context(), "not ready", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, "application/json", map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}
