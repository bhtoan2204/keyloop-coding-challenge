package aggregates

import (
	"fmt"
	"strings"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
)

type ProposeAction struct {
	ActionID   string
	ActionType domain.ActionType
	Note       string
}

// ProposeAction records a new action. Only an in-stock, aging vehicle without an
// active action qualifies.
func (a *VehicleInventoryAggregate) ProposeAction(cmd ProposeAction, businessDate domain.Date) error {
	if err := a.requireInStock(); err != nil {
		return err
	}
	if strings.TrimSpace(cmd.ActionID) == "" {
		return domain.Invalid("action_id", "is required")
	}
	note, err := validateAction(cmd.ActionType, cmd.Note)
	if err != nil {
		return err
	}
	if a.activeAction != nil {
		return fmt.Errorf("%w: action %s", domain.ErrActionAlreadyActive, a.activeAction.ID)
	}
	if !a.IsAging(businessDate) {
		return fmt.Errorf("%w: %d days in stock at %s, more than %d required",
			domain.ErrVehicleNotAging, a.AgeInDays(businessDate), businessDate, domain.AgingThresholdDays)
	}
	return a.ApplyChange(a, &events.VehicleActionProposedEvent{
		ActionID:   cmd.ActionID,
		ActionType: cmd.ActionType,
		Note:       note,
	})
}

func (a *VehicleInventoryAggregate) applyVehicleActionProposedEvent(e *events.VehicleActionProposedEvent) error {
	if err := a.inStockForEvent("VehicleActionProposedEvent"); err != nil {
		return err
	}
	if e.ActionID == "" {
		return a.invalidSequence("VehicleActionProposedEvent", "action_id is required")
	}
	if a.activeAction != nil {
		return a.invalidSequence("VehicleActionProposedEvent", "action "+a.activeAction.ID+" is already active")
	}
	a.activeAction = &Action{ID: e.ActionID, Type: e.ActionType, Note: e.Note}
	return nil
}
