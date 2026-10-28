package aggregates

import (
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/package/eventsourcing"
)

var _ eventsourcing.Aggregate = (*VehicleInventoryAggregate)(nil)

type VehicleInventoryAggregate struct {
	eventsourcing.AggregateRoot
}

func (a *VehicleInventoryAggregate) RegisterEvents(register eventsourcing.RegisterEventsFunc) error {
	return register(
		&events.VehicleStockedEvent{},
		&events.VehicleSoldEvent{},
		&events.VehicleActionProposedEvent{},
		&events.VehicleActionRevisedEvent{},
		&events.VehicleActionCancelledEvent{},
	)
}

func (a *VehicleInventoryAggregate) Transition(e eventsourcing.Event) error {
	switch event := e.Data.(type) {
	case *events.VehicleStockedEvent:
		return a.applyVehicleStockedEvent(event)
	case *events.VehicleSoldEvent:
		return a.applyVehicleSoldEvent(event)
	case *events.VehicleActionProposedEvent:
		return a.applyVehicleActionProposedEvent(event)
	case *events.VehicleActionRevisedEvent:
		return a.applyVehicleActionRevisedEvent(event)
	case *events.VehicleActionCancelledEvent:
		return a.applyVehicleActionCancelledEvent(event)
	default:
		return domain.ErrUnknownEventType
	}
}

func (a *VehicleInventoryAggregate) applyVehicleStockedEvent(_ *events.VehicleStockedEvent) error {
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleSoldEvent(_ *events.VehicleSoldEvent) error {
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionProposedEvent(_ *events.VehicleActionProposedEvent) error {
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionRevisedEvent(_ *events.VehicleActionRevisedEvent) error {
	return nil
}

func (a *VehicleInventoryAggregate) applyVehicleActionCancelledEvent(_ *events.VehicleActionCancelledEvent) error {
	return nil
}
