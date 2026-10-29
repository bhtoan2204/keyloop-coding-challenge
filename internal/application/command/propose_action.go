package command

import (
	"context"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
)

type ProposeAction struct {
	Target
	ActionType domain.ActionType
	Note       string
}

func (s *Service) ProposeAction(ctx context.Context, cmd ProposeAction, meta Meta) (*Result, error) {
	actionID := s.newID()
	propose := aggregates.ProposeAction{
		ActionID:   actionID,
		ActionType: cmd.ActionType,
		Note:       cmd.Note,
	}
	res, err := s.execute(ctx, cmd.Target, meta, false, func(v *aggregates.VehicleInventoryAggregate, bd domain.Date) error {
		return v.ProposeAction(propose, bd)
	})
	if err != nil {
		return nil, err
	}
	res.ActionID = actionID
	return res, nil
}
