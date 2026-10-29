package aggregates

import (
	"fmt"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
)

type SellVehicle struct {
	SoldOn domain.Date
}

// Sell removes the vehicle from active inventory and closes the active action, if any.
func (a *VehicleInventoryAggregate) Sell(cmd SellVehicle, businessDate domain.Date) error {
	if !a.IsStocked() {
		return fmt.Errorf("%w: vehicle %s", domain.ErrVehicleNotStocked, a.AggregateID())
	}
	if a.status == domain.StatusSold {
		return fmt.Errorf("%w: sold on %s", domain.ErrVehicleAlreadySold, a.soldOn)
	}
	switch {
	case cmd.SoldOn.IsZero():
		return domain.Invalid("sold_on", "is required")
	case cmd.SoldOn.Before(a.stockedOn):
		return domain.Invalid("sold_on", fmt.Sprintf("must not be before stocked_on %s", a.stockedOn))
	case cmd.SoldOn.After(businessDate):
		return domain.Invalid("sold_on", fmt.Sprintf("must not be after the business date %s", businessDate))
	}
	event := &events.VehicleSoldEvent{SoldOn: cmd.SoldOn}
	if a.activeAction != nil {
		event.ClosedActionID = a.activeAction.ID
	}
	return a.ApplyChange(a, event)
}

func (a *VehicleInventoryAggregate) applyVehicleSoldEvent(e *events.VehicleSoldEvent) error {
	if err := a.inStockForEvent("VehicleSoldEvent"); err != nil {
		return err
	}
	if e.SoldOn.IsZero() {
		return a.invalidSequence("VehicleSoldEvent", "sold_on is required")
	}
	if active := a.activeActionID(); e.ClosedActionID != active {
		return a.invalidSequence("VehicleSoldEvent",
			fmt.Sprintf("closed_action_id %q does not match active action %q", e.ClosedActionID, active))
	}
	a.status = domain.StatusSold
	a.soldOn = e.SoldOn
	a.activeAction = nil
	return nil
}
