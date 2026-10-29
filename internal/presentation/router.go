// Package presentation is the REST API. It translates HTTP into application commands
// and application errors into RFC 9457 problem responses; it holds no business rules.
package presentation

import (
	"log/slog"
	"net/http"
)

type api struct {
	commands CommandService
	queries  QueryService
	ready    ReadinessCheck
	log      *slog.Logger
}

// NewHandler returns the HTTP handler with all routes and middleware. The query routes
// are registered only when queries is not nil.
func NewHandler(commands CommandService, queries QueryService, ready ReadinessCheck, log *slog.Logger) http.Handler {
	a := &api{
		commands: commands,
		queries:  queries,
		ready:    ready,
		log:      log,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	a.registerCommands(mux)
	if queries != nil {
		a.registerQueries(mux)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, a.log, &apiError{
			status: http.StatusNotFound,
			code:   "NOT_FOUND",
			detail: "no such endpoint",
		})
	})
	return a.withRequestContext(a.withRecovery(mux))
}

func (a *api) registerCommands(mux *http.ServeMux) {
	const vehicle = "/v1/dealerships/{dealershipId}/vehicles/{vehicleId}"
	mux.HandleFunc("POST /v1/dealerships/{dealershipId}/vehicles", a.stockVehicle)
	mux.HandleFunc("POST "+vehicle+"/actions", a.proposeAction)
	mux.HandleFunc("PUT "+vehicle+"/actions/{actionId}", a.reviseAction)
	mux.HandleFunc("POST "+vehicle+"/actions/{actionId}/cancel", a.cancelAction)
	mux.HandleFunc("POST "+vehicle+"/sale", a.sellVehicle)
}

func (a *api) registerQueries(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles", a.listInventory)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}", a.getVehicle)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}/actions", a.getActionHistory)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/inventory/summary", a.getInventorySummary)
}
