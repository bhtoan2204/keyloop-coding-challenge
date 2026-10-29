package probes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"keyloop-coding-challenge/internal/presentation/httpkit"
)

// readinessTimeout keeps a slow dependency from holding the probe past the
// orchestrator's own probe timeout.
const readinessTimeout = 2 * time.Second

type health struct {
	ready ReadinessCheck
	log   *slog.Logger
}

func (h *health) healthz(w http.ResponseWriter, _ *http.Request) {
	httpkit.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *health) readyz(w http.ResponseWriter, r *http.Request) {
	if h.ready == nil {
		httpkit.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	if err := h.ready(ctx); err != nil {
		h.log.WarnContext(r.Context(), "not ready", "error", err.Error())
		httpkit.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
