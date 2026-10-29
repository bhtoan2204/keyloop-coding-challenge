// Package commands serves the write endpoints: it decodes and validates each request,
// runs the application command and answers with the committed vehicle state.
package commands

import (
	"log/slog"
	"net/http"
)

type handlers struct {
	service Service
	log     *slog.Logger
}

// Register adds the five command endpoints to mux.
func Register(mux *http.ServeMux, service Service, log *slog.Logger) {
	h := &handlers{
		service: service,
		log:     log,
	}
	const vehicle = "/v1/dealerships/{dealershipId}/vehicles/{vehicleId}"
	mux.HandleFunc("POST /v1/dealerships/{dealershipId}/vehicles", h.stockVehicle)
	mux.HandleFunc("POST "+vehicle+"/actions", h.proposeAction)
	mux.HandleFunc("PUT "+vehicle+"/actions/{actionId}", h.reviseAction)
	mux.HandleFunc("POST "+vehicle+"/actions/{actionId}/cancel", h.cancelAction)
	mux.HandleFunc("POST "+vehicle+"/sale", h.sellVehicle)
}
