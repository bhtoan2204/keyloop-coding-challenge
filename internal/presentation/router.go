// Package presentation composes the HTTP handlers of the API and projector processes.
// The endpoints live in the commands, queries and probes subpackages; httpkit holds
// the shared transport (middleware, request parsing, problem responses).
package presentation

import (
	"log/slog"
	"net/http"

	"keyloop-coding-challenge/internal/presentation/commands"
	"keyloop-coding-challenge/internal/presentation/httpkit"
	"keyloop-coding-challenge/internal/presentation/probes"
	"keyloop-coding-challenge/internal/presentation/queries"
)

// NewHandler returns the REST API with its probes and middleware. The query routes
// are registered only when qs is not nil.
func NewHandler(cmds CommandService, qs QueryService, ready ReadinessCheck, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	probes.Register(mux, ready, log)
	commands.Register(mux, cmds, log)
	if qs != nil {
		queries.Register(mux, qs, log)
	}
	return withFallbackAndMiddleware(mux, log)
}

// NewProbeHandler serves the probes of the projection consumer process: /healthz
// (process alive), /readyz (503 while bootstrapping, while the broker is unreachable
// or while a partition is halted on a failing record) and /status (progress detail).
func NewProbeHandler(ready ReadinessCheck, status ConsumerStatus, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	probes.Register(mux, ready, log)
	probes.RegisterStatus(mux, status)
	return withFallbackAndMiddleware(mux, log)
}

// withFallbackAndMiddleware answers unknown routes with a 404 problem and wraps the
// mux in recovery (inner) and request context (outer), so a panic is still logged
// with its request ID.
func withFallbackAndMiddleware(mux *http.ServeMux, log *slog.Logger) http.Handler {
	mux.Handle("/", httpkit.NotFound(log))
	return httpkit.WithRequestContext(log, httpkit.WithRecovery(log, mux))
}
