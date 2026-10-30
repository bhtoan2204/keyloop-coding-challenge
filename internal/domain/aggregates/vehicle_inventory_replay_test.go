package aggregates

import (
	"errors"
	"reflect"
	"testing"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

// replay rebuilds a fresh aggregate from history, as AggregateStore.Get does.
func replay(t *testing.T, history []eventsourcing.Event) *VehicleInventoryAggregate {
	t.Helper()
	a := &VehicleInventoryAggregate{}
	if err := a.LoadFromHistory(a, history); err != nil {
		t.Fatalf("replay: %v", err)
	}
	return a
}

func TestReplayEquivalence(t *testing.T) {
	scenarios := map[string]func(*testing.T) *VehicleInventoryAggregate{
		"stocked":  func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 30) },
		"proposed": withAction,
		"sold":     sold,
		"revised then cancelled then proposed again": func(t *testing.T) *VehicleInventoryAggregate {
			a := withAction(t)
			mustDo(t, a.ReviseAction(ReviseAction{ActionID: testActionID, ActionType: domain.ActionWholesale, Note: "trade"}))
			mustDo(t, a.CancelAction(CancelAction{ActionID: testActionID, Reason: "price agreed"}))
			mustDo(t, a.ProposeAction(ProposeAction{ActionID: "a2", ActionType: domain.ActionOther, Note: "photo shoot"}, bizDate))
			return a
		},
	}
	for name, build := range scenarios {
		t.Run(name, func(t *testing.T) {
			commanded := build(t)
			replayed := replay(t, commanded.CloneEvents())

			assertSameState(t, replayed, commanded)
			if replayed.IsUnsaved() {
				t.Fatalf("replay produced %d pending events", len(replayed.Events()))
			}
			if replayed.Version() != commanded.Version() || replayed.BaseVersion() != commanded.Version() {
				t.Fatalf("replayed version=%d base=%d, want %d", replayed.Version(), replayed.BaseVersion(), commanded.Version())
			}
			if replayed.AggregateID() != testVehicleID {
				t.Fatalf("replayed id = %s", replayed.AggregateID())
			}
		})
	}
}

func TestReplayIgnoresRecordingTime(t *testing.T) {
	commanded := sold(t)
	history := commanded.CloneEvents()
	shifted := make([]eventsourcing.Event, len(history))
	for i, e := range history {
		e.CreatedAt = int64(i) * 86400 * 365 // decades away from "now"
		shifted[i] = e
	}
	assertSameState(t, replay(t, shifted), replay(t, history))
}

func TestReplayRejectsInvalidSequences(t *testing.T) {
	stocked := &events.VehicleStockedEvent{DealershipID: "d-001", VIN: testVIN, Make: "Toyota", Model: "Corolla", ModelYear: 2023, StockedOn: bizDate.AddDays(-100)}
	proposed := &events.VehicleActionProposedEvent{ActionID: testActionID, ActionType: domain.ActionHold}
	soldEvent := &events.VehicleSoldEvent{SoldOn: bizDate}

	cases := []struct {
		name string
		data []interface{}
	}{
		{"action before stocking", []interface{}{proposed}},
		{"sale before stocking", []interface{}{soldEvent}},
		{"stocked twice", []interface{}{stocked, stocked}},
		{"stocked event missing required fields", []interface{}{&events.VehicleStockedEvent{}}},
		{"second active action", []interface{}{stocked, proposed, &events.VehicleActionProposedEvent{ActionID: "other", ActionType: domain.ActionHold}}},
		{"proposal without action id", []interface{}{stocked, &events.VehicleActionProposedEvent{ActionType: domain.ActionHold}}},
		{"revision without active action", []interface{}{stocked, &events.VehicleActionRevisedEvent{ActionID: testActionID, ActionType: domain.ActionHold}}},
		{"revision of another action", []interface{}{stocked, proposed, &events.VehicleActionRevisedEvent{ActionID: "other", ActionType: domain.ActionHold}}},
		{"cancellation without active action", []interface{}{stocked, &events.VehicleActionCancelledEvent{ActionID: testActionID}}},
		{"sale that does not close the active action", []interface{}{stocked, proposed, soldEvent}},
		{"sale closing a non-existent action", []interface{}{stocked, &events.VehicleSoldEvent{SoldOn: bizDate, ClosedActionID: "ghost"}}},
		{"sale without date", []interface{}{stocked, &events.VehicleSoldEvent{}}},
		{"action after sale", []interface{}{stocked, soldEvent, proposed}},
		{"second sale", []interface{}{stocked, soldEvent, soldEvent}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := make([]eventsourcing.Event, len(tc.data))
			for i, d := range tc.data {
				history[i] = eventsourcing.Event{AggregateID: testVehicleID, Version: i + 1, Data: d}
			}
			a := &VehicleInventoryAggregate{}
			err := a.LoadFromHistory(a, history)
			if !errors.Is(err, domain.ErrInvalidEventSequence) {
				t.Fatalf("error = %v, want ErrInvalidEventSequence", err)
			}
			if a.BaseVersion() != len(history)-1 {
				t.Fatalf("replay stopped at version %d, want %d (before the bad event)", a.BaseVersion(), len(history)-1)
			}
		})
	}

	t.Run("unknown payload type", func(t *testing.T) {
		a := &VehicleInventoryAggregate{}
		err := a.LoadFromHistory(a, []eventsourcing.Event{{AggregateID: testVehicleID, Version: 1, Data: &struct{}{}}})
		if !errors.Is(err, domain.ErrUnknownEventType) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("version gap is rejected by AggregateRoot", func(t *testing.T) {
		a := &VehicleInventoryAggregate{}
		err := a.LoadFromHistory(a, []eventsourcing.Event{
			{AggregateID: testVehicleID, Version: 1, Data: stocked},
			{AggregateID: testVehicleID, Version: 3, Data: proposed},
		})
		if err == nil || a.BaseVersion() != 1 {
			t.Fatalf("err=%v base=%d", err, a.BaseVersion())
		}
		if _, ok := a.ActiveAction(); ok {
			t.Fatal("event after the gap was applied")
		}
	})
}

func TestRejectedEventDoesNotMutateState(t *testing.T) {
	// ApplyChange keeps a rejected event out of the pending list; the apply methods
	// must also leave state untouched so the aggregate can still be used.
	a := withAction(t)
	before := businessState(a)
	err := a.ApplyChange(a, &events.VehicleSoldEvent{SoldOn: bizDate, ClosedActionID: "wrong"})
	if !errors.Is(err, domain.ErrInvalidEventSequence) {
		t.Fatalf("error = %v", err)
	}
	if after := businessState(a); !reflect.DeepEqual(after, before) {
		t.Fatalf("state changed\nbefore: %+v\n after: %+v", before, after)
	}
}
