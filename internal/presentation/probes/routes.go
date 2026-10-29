// Package probes serves the operational endpoints of the API and projector processes:
// liveness, readiness and the projector's progress status.
package probes

import (
	"context"
	"log/slog"
	"net/http"

	"keyloop-coding-challenge/internal/application/messaging"
)

// ReadinessCheck reports whether the process can serve: nil when ready.
type ReadinessCheck func(ctx context.Context) error

// ConsumerStatus is implemented by (*messaging.Consumer).Status.
type ConsumerStatus func() messaging.Status

// Register adds GET /healthz and GET /readyz. A nil ready makes /readyz always 503.
func Register(mux *http.ServeMux, ready ReadinessCheck, log *slog.Logger) {
	h := &health{
		ready: ready,
		log:   log,
	}
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
}

// RegisterStatus adds GET /status with the consumer's progress detail.
func RegisterStatus(mux *http.ServeMux, status ConsumerStatus) {
	mux.HandleFunc("GET /status", statusHandler(status))
}
