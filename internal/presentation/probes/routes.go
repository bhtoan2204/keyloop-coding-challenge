// Package probes serves the operational endpoints of the API process: liveness,
// readiness and Prometheus metrics.
package probes

import (
	"context"
	"log/slog"
	"net/http"
)

// ReadinessCheck reports whether the process can serve: nil when ready.
type ReadinessCheck func(ctx context.Context) error

// Register adds GET /healthz and GET /readyz. A nil ready makes /readyz always 503.
func Register(mux *http.ServeMux, ready ReadinessCheck, log *slog.Logger) {
	h := &health{
		ready: ready,
		log:   log,
	}
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
}
