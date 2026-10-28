package aggregates

import (
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/package/eventsourcing"
)

type VehicleInventoryAggregate struct {
	eventsourcing.AggregateRoot
}

func (a *VehicleInventoryAggregate) Transition(e eventsourcing.Event) error {
	switch event := e.Data.(type) {
	case events.VehicleStockedEvent:
		return a.applyVehicleStockedEvent(event)
	case events.VehicleSoldEvent:
		return a.applyVehicleSoldEvent(event)
	case events.VehicleActionProposedEvent:
		return a.applyVehicleActionProposedEvent(event)
	case events.VehicleActionRevisedEvent:
		return a.applyVehicleActionRevisedEvent(event)
	default:
		return domain.ErrUnknownEventType
	}
}

func (a *VehicleInventoryAggregate) applyVehicleStockedEvent(event events.VehicleStockedEvent) error {
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleSoldEvent(event events.VehicleSoldEvent) error {
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionProposedEvent(event events.VehicleActionProposedEvent) error {
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionRevisedEvent(event events.VehicleActionRevisedEvent) error {
	return nil
}
