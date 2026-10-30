package aggregates

import (
	"testing"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

// Event registration and serialization are checked separately from domain
// transitions: an empty payload is valid for the serializer but, since business
// invariants exist, not a valid event for the aggregate.
func TestVehicleEventsUsePointerPayloads(t *testing.T) {
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	if err := serializer.RegisterAggregate(&VehicleInventoryAggregate{}); err == nil {
		t.Fatal("expected duplicate registration")
	}

	// Stored event type names are part of the persisted data and must never change.
	wantTypes := []string{
		"VehicleStockedEvent",
		"VehicleActionProposedEvent",
		"VehicleActionRevisedEvent",
		"VehicleActionCancelledEvent",
		"VehicleSoldEvent",
	}
	for _, name := range wantTypes {
		factory, ok := serializer.Type("VehicleInventoryAggregate", name)
		if !ok {
			t.Fatalf("event %s is not registered", name)
		}
		raw, err := serializer.Marshal(factory())
		if err != nil {
			t.Fatal(err)
		}
		if err := serializer.Unmarshal(raw, factory()); err != nil {
			t.Fatalf("%s round trip: %v", name, err)
		}
	}

	agg := newVehicle(t)
	if err := agg.ApplyChange(agg, events.VehicleStockedEvent{}); err == nil {
		t.Fatal("expected value payload to be rejected")
	}

	// Drive every event type through valid commands and check the pending events.
	mustDo(t, agg.Stock(validStock(bizDate.AddDays(-91)), bizDate))
	mustDo(t, agg.ProposeAction(ProposeAction{ActionID: "a1", ActionType: domain.ActionHold}, bizDate))
	mustDo(t, agg.ReviseAction(ReviseAction{ActionID: "a1", ActionType: domain.ActionAuction}))
	mustDo(t, agg.CancelAction(CancelAction{ActionID: "a1"}))
	mustDo(t, agg.Sell(SellVehicle{SoldOn: bizDate}, bizDate))

	got := agg.Events()
	if len(got) != len(wantTypes) {
		t.Fatalf("pending events: got %d", len(got))
	}
	for i, event := range got {
		if event.Version != i+1 || event.EventType != wantTypes[i] {
			t.Fatalf("event %d: version=%d type=%s", i, event.Version, event.EventType)
		}
	}
}
