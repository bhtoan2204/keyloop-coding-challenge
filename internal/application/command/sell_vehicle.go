package command

import (
	"context"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
)

type SellVehicle struct {
	Target
	SoldOn domain.Date
}

func (s *Service) SellVehicle(ctx context.Context, cmd SellVehicle, meta Meta) (*Result, error) {
	return s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, bd domain.Date) error {
		return v.Sell(aggregates.SellVehicle{SoldOn: cmd.SoldOn}, bd)
	})
}
