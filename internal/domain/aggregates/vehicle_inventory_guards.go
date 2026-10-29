package aggregates

import (
	"fmt"

	"keyloop-coding-challenge/internal/domain"
)

func (a *VehicleInventoryAggregate) inStockForEvent(eventType string) error {
	switch a.status {
	case domain.StatusInStock:
		return nil
	case domain.StatusSold:
		return a.invalidSequence(eventType, "vehicle is already sold")
	default:
		return a.invalidSequence(eventType, "vehicle is not stocked")
	}
}

func (a *VehicleInventoryAggregate) invalidSequence(eventType, reason string) error {
	return fmt.Errorf("%w: aggregate %s: %s: %s", domain.ErrInvalidEventSequence, a.AggregateID(), eventType, reason)
}

func (a *VehicleInventoryAggregate) activeActionID() string {
	if a.activeAction == nil {
		return ""
	}
	return a.activeAction.ID
}

func (a *VehicleInventoryAggregate) requireInStock() error {
	switch a.status {
	case domain.StatusInStock:
		return nil
	case domain.StatusSold:
		return fmt.Errorf("%w: vehicle %s was sold on %s", domain.ErrVehicleNotInStock, a.AggregateID(), a.soldOn)
	default:
		return fmt.Errorf("%w: vehicle %s", domain.ErrVehicleNotStocked, a.AggregateID())
	}
}

func (a *VehicleInventoryAggregate) requireActiveAction(actionID string) error {
	if a.activeAction == nil {
		return fmt.Errorf("%w: vehicle %s has no active action", domain.ErrActionNotActive, a.AggregateID())
	}
	if a.activeAction.ID != actionID {
		return fmt.Errorf("%w: action %s is not the active action", domain.ErrActionNotActive, actionID)
	}
	return nil
}
