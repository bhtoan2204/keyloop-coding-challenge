package query

import (
	"context"

	"keyloop-coding-challenge/internal/domain"
)

type VehicleResult struct {
	Vehicle Vehicle
	AsOf    domain.Date
}

func (s *Service) GetVehicle(ctx context.Context, dealershipID, vehicleID string, asOf *domain.Date) (*VehicleResult, error) {
	businessDate, err := s.businessDate(ctx, dealershipID, asOf)
	if err != nil {
		return nil, err
	}
	v, err := s.rm.GetVehicle(ctx, dealershipID, vehicleID)
	if err != nil {
		return nil, err
	}
	return &VehicleResult{Vehicle: *v, AsOf: businessDate}, nil
}
