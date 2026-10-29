package aggregates

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
)

type CancelAction struct {
	ActionID string
	Reason   string
}

// CancelAction withdraws the active action.
func (a *VehicleInventoryAggregate) CancelAction(cmd CancelAction) error {
	if err := a.requireInStock(); err != nil {
		return err
	}
	reason := strings.TrimSpace(cmd.Reason)
	if utf8.RuneCountInString(reason) > maxNoteLen {
		return domain.Invalid("reason", fmt.Sprintf("must be at most %d characters", maxNoteLen))
	}
	if err := a.requireActiveAction(cmd.ActionID); err != nil {
		return err
	}
	return a.ApplyChange(a, &events.VehicleActionCancelledEvent{ActionID: cmd.ActionID, Reason: reason})
}

func (a *VehicleInventoryAggregate) applyVehicleActionCancelledEvent(e *events.VehicleActionCancelledEvent) error {
	if err := a.inStockForEvent("VehicleActionCancelledEvent"); err != nil {
		return err
	}
	if a.activeActionID() == "" || e.ActionID != a.activeActionID() {
		return a.invalidSequence("VehicleActionCancelledEvent", "action "+e.ActionID+" is not active")
	}
	a.activeAction = nil
	return nil
}
