package command

import (
	"context"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
)

type CancelAction struct {
	Target
	ActionID string
	Reason   string
}

func (s *Service) CancelAction(ctx context.Context, cmd CancelAction, meta Meta) (*Result, error) {
	return s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, _ domain.Date) error {
		return v.CancelAction(aggregates.CancelAction{ActionID: cmd.ActionID, Reason: cmd.Reason})
	})
}
