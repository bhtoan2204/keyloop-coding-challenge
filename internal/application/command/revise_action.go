package command

import (
	"context"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
)

type ReviseAction struct {
	Target
	ActionID   string
	ActionType domain.ActionType
	Note       string
}

func (s *Service) ReviseAction(ctx context.Context, cmd ReviseAction, meta Meta) (*Result, error) {
	return s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, _ domain.Date) error {
		return v.ReviseAction(aggregates.ReviseAction{
			ActionID:   cmd.ActionID,
			ActionType: cmd.ActionType,
			Note:       cmd.Note,
		})
	})
}
