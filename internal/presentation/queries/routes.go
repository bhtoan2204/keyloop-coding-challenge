// Package queries serves the read endpoints over the inventory read model: list with
// filters and keyset pagination, vehicle detail, action history and the summary.
package queries

import (
	"log/slog"
	"net/http"
)

type handlers struct {
	service Service
	log     *slog.Logger
}

// Register adds the four query endpoints to mux.
func Register(mux *http.ServeMux, service Service, log *slog.Logger) {
	h := &handlers{
		service: service,
		log:     log,
	}
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles", h.listInventory)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}", h.getVehicle)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/vehicles/{vehicleId}/actions", h.getActionHistory)
	mux.HandleFunc("GET /v1/dealerships/{dealershipId}/inventory/summary", h.getInventorySummary)
}
