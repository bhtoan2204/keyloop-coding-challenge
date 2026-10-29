package command

import (
	"context"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
)

type StockVehicle struct {
	DealershipID string
	VehicleID    string // optional; generated when empty
	VIN          string
	Make         string
	Model        string
	ModelYear    int
	StockedOn    domain.Date
}

// StockVehicle creates a vehicle's stream. The dealership comes from the caller's
// route, never from the request body.
func (s *Service) StockVehicle(ctx context.Context, cmd StockVehicle, meta Meta) (*Result, error) {
	vehicleID := cmd.VehicleID
	if vehicleID == "" {
		vehicleID = s.newID()
	}
	stock := aggregates.StockVehicle{
		DealershipID: cmd.DealershipID,
		VIN:          cmd.VIN,
		Make:         cmd.Make,
		Model:        cmd.Model,
		ModelYear:    cmd.ModelYear,
		StockedOn:    cmd.StockedOn,
	}
	target := Target{
		DealershipID: cmd.DealershipID,
		VehicleID:    vehicleID,
	}
	return s.execute(ctx, target, meta, true, func(v *aggregates.VehicleInventoryAggregate, bd domain.Date) error {
		return v.Stock(stock, bd)
	})
}
