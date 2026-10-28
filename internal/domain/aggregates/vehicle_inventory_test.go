package aggregates

import (
	"testing"

	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/package/eventsourcing"
)

func TestVehicleEventsUsePointerPayloads(t *testing.T) {
	agg := &VehicleInventoryAggregate{}
	if err := agg.SetID("vehicle-1"); err != nil {
		t.Fatal(err)
	}
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(agg); err != nil {
		t.Fatal(err)
	}
	if err := serializer.RegisterAggregate(&VehicleInventoryAggregate{}); err == nil {
		t.Fatal("expected duplicate registration")
	}

	payloads := []interface{}{
		&events.VehicleStockedEvent{},
		&events.VehicleSoldEvent{},
		&events.VehicleActionProposedEvent{},
		&events.VehicleActionRevisedEvent{},
		&events.VehicleActionCancelledEvent{},
	}
	for _, payload := range payloads {
		if err := agg.ApplyChange(agg, payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := agg.ApplyChange(agg, events.VehicleStockedEvent{}); err == nil {
		t.Fatal("expected value payload to be rejected")
	}
	got := agg.Events()
	if len(got) != len(payloads) {
		t.Fatalf("pending events: got %d", len(got))
	}
	wantTypes := []string{
		"VehicleStockedEvent",
		"VehicleSoldEvent",
		"VehicleActionProposedEvent",
		"VehicleActionRevisedEvent",
		"VehicleActionCancelledEvent",
	}
	for i, event := range got {
		if event.Version != i+1 || event.EventType != wantTypes[i] {
			t.Fatalf("event %d: version=%d type=%s", i, event.Version, event.EventType)
		}
	}
}
