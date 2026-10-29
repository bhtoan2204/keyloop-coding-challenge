package presentation

import (
	"log/slog"
	"net/http"
)

// NewProbeHandler serves the probes of the projection consumer process: /healthz
// (process alive), /readyz (503 while bootstrapping, while the broker is unreachable
// or while a partition is halted on a failing record) and /status (progress detail).
func NewProbeHandler(ready ReadinessCheck, status ConsumerStatus, log *slog.Logger) http.Handler {
	a := &api{
		ready: ready,
		log:   log,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, "application/json", status())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, a.log, &apiError{
			status: http.StatusNotFound,
			code:   "NOT_FOUND",
			detail: "no such endpoint",
		})
	})
	return a.withRequestContext(a.withRecovery(mux))
}
