package aggregates

import (
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
)

type ReviseAction struct {
	ActionID   string
	ActionType domain.ActionType
	Note       string
}

// ReviseAction replaces the type and note of the active action. A revision that
// changes nothing records no event.
func (a *VehicleInventoryAggregate) ReviseAction(cmd ReviseAction) error {
	if err := a.requireInStock(); err != nil {
		return err
	}
	note, err := validateAction(cmd.ActionType, cmd.Note)
	if err != nil {
		return err
	}
	if err := a.requireActiveAction(cmd.ActionID); err != nil {
		return err
	}
	if a.activeAction.Type == cmd.ActionType && a.activeAction.Note == note {
		return nil
	}
	return a.ApplyChange(a, &events.VehicleActionRevisedEvent{
		ActionID:   cmd.ActionID,
		ActionType: cmd.ActionType,
		Note:       note,
	})
}

func (a *VehicleInventoryAggregate) applyVehicleActionRevisedEvent(e *events.VehicleActionRevisedEvent) error {
	if err := a.inStockForEvent("VehicleActionRevisedEvent"); err != nil {
		return err
	}
	if a.activeActionID() == "" || e.ActionID != a.activeActionID() {
		return a.invalidSequence("VehicleActionRevisedEvent", "action "+e.ActionID+" is not active")
	}
	a.activeAction = &Action{ID: e.ActionID, Type: e.ActionType, Note: e.Note}
	return nil
}
