package command

import (
	"log/slog"

	"github.com/google/uuid"
)

type Service struct {
	store       VehicleStore
	dealerships DealershipDirectory
	clock       Clock
	newID       func() string
	log         *slog.Logger
}

func NewService(store VehicleStore, dealerships DealershipDirectory, clock Clock, log *slog.Logger) *Service {
	return &Service{
		store:       store,
		dealerships: dealerships,
		clock:       clock,
		newID:       newUUIDv7,
		log:         log,
	}
}

func newUUIDv7() string {
	return uuid.Must(uuid.NewV7()).String()
}
