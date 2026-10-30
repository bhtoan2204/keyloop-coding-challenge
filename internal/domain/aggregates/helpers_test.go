package aggregates

import (
	"errors"
	"reflect"
	"testing"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

const (
	testVehicleID = "0192c6d2-0000-7000-8000-000000000001"
	testVIN       = "1HGCM82633A004352"
	testActionID  = "0192c6e0-0000-7000-8000-0000000000a1"
)

// bizDate is the dealership business date used throughout the tests.
var bizDate = domain.NewDate(2026, 10, 29)

func newVehicle(t *testing.T) *VehicleInventoryAggregate {
	t.Helper()
	a := &VehicleInventoryAggregate{}
	if err := a.SetID(testVehicleID); err != nil {
		t.Fatal(err)
	}
	return a
}

func validStock(stockedOn domain.Date) StockVehicle {
	return StockVehicle{
		DealershipID: "d-001", VIN: testVIN, Make: "Toyota", Model: "Corolla",
		ModelYear: 2023, StockedOn: stockedOn,
	}
}

// stockedFor returns a vehicle that has been in stock for days at bizDate.
func stockedFor(t *testing.T, days int) *VehicleInventoryAggregate {
	t.Helper()
	a := newVehicle(t)
	mustDo(t, a.Stock(validStock(bizDate.AddDays(-days)), bizDate))
	return a
}

// withAction returns a 91-day-old vehicle with an active PRICE_REDUCTION action.
func withAction(t *testing.T) *VehicleInventoryAggregate {
	t.Helper()
	a := stockedFor(t, 91)
	mustDo(t, a.ProposeAction(ProposeAction{ActionID: testActionID, ActionType: domain.ActionPriceReduction, Note: "5%"}, bizDate))
	return a
}

func sold(t *testing.T) *VehicleInventoryAggregate {
	t.Helper()
	a := withAction(t)
	mustDo(t, a.Sell(SellVehicle{SoldOn: bizDate}, bizDate))
	return a
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// businessState returns the aggregate without AggregateRoot bookkeeping, so two
// aggregates can be compared field by field, including fields added later.
func businessState(a *VehicleInventoryAggregate) VehicleInventoryAggregate {
	s := *a
	s.AggregateRoot = eventsourcing.AggregateRoot{}
	return s
}

func assertSameState(t *testing.T, got, want *VehicleInventoryAggregate) {
	t.Helper()
	if g, w := businessState(got), businessState(want); !reflect.DeepEqual(g, w) {
		t.Fatalf("state mismatch\n got: %+v\nwant: %+v", g, w)
	}
}

// assertRejected checks that a command failed with want and left the aggregate unchanged.
func assertRejected(t *testing.T, a *VehicleInventoryAggregate, run func() error, want error) {
	t.Helper()
	before := businessState(a)
	pending := len(a.Events())
	err := run()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if len(a.Events()) != pending {
		t.Fatalf("rejected command recorded an event: %d → %d pending", pending, len(a.Events()))
	}
	if after := businessState(a); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected command changed state\nbefore: %+v\n after: %+v", before, after)
	}
}

func lastEventData(t *testing.T, a *VehicleInventoryAggregate) interface{} {
	t.Helper()
	evs := a.Events()
	if len(evs) == 0 {
		t.Fatal("no pending events")
	}
	return evs[len(evs)-1].Data
}
