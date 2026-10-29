package aggregates

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
)

func TestStock(t *testing.T) {
	t.Run("valid stocking normalises input and sets state", func(t *testing.T) {
		a := newVehicle(t)
		cmd := StockVehicle{
			DealershipID: " d-001 ", VIN: " 1hgcm82633a004352 ", Make: " Toyota ", Model: "Corolla ",
			ModelYear: 2023, StockedOn: bizDate.AddDays(-10),
		}
		mustDo(t, a.Stock(cmd, bizDate))

		want := &events.VehicleStockedEvent{
			DealershipID: "d-001", VIN: testVIN, Make: "Toyota", Model: "Corolla",
			ModelYear: 2023, StockedOn: bizDate.AddDays(-10),
		}
		if got := lastEventData(t, a); !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %+v, want %+v", got, want)
		}
		if a.VehicleID() != testVehicleID || a.DealershipID() != "d-001" || a.VIN() != testVIN ||
			a.Make() != "Toyota" || a.Model() != "Corolla" || a.ModelYear() != 2023 ||
			a.StockedOn() != bizDate.AddDays(-10) || a.Status() != domain.StatusInStock {
			t.Fatalf("unexpected state: %+v", businessState(a))
		}
		if _, ok := a.ActiveAction(); ok {
			t.Fatal("new vehicle has an active action")
		}
	})

	t.Run("boundaries accepted", func(t *testing.T) {
		for name, edit := range map[string]func(*StockVehicle){
			"stocked on business date": func(c *StockVehicle) { c.StockedOn = bizDate },
			"next model year":          func(c *StockVehicle) { c.ModelYear = 2027 },
			"model year 1900":          func(c *StockVehicle) { c.ModelYear = 1900 },
			"64-char make":             func(c *StockVehicle) { c.Make = strings.Repeat("é", 64) },
		} {
			t.Run(name, func(t *testing.T) {
				cmd := validStock(bizDate.AddDays(-1))
				edit(&cmd)
				mustDo(t, newVehicle(t).Stock(cmd, bizDate))
			})
		}
	})

	t.Run("duplicate stocking is rejected", func(t *testing.T) {
		a := stockedFor(t, 10)
		assertRejected(t, a, func() error { return a.Stock(validStock(bizDate), bizDate) }, domain.ErrVehicleAlreadyStocked)
	})

	t.Run("stocking a sold vehicle is rejected", func(t *testing.T) {
		a := sold(t)
		assertRejected(t, a, func() error { return a.Stock(validStock(bizDate), bizDate) }, domain.ErrVehicleAlreadyStocked)
	})

	t.Run("missing aggregate id", func(t *testing.T) {
		a := &VehicleInventoryAggregate{}
		assertRejected(t, a, func() error { return a.Stock(validStock(bizDate), bizDate) }, domain.ErrValidation)
	})

	invalid := []struct {
		name  string
		field string
		edit  func(*StockVehicle)
	}{
		{"missing dealership", "dealership_id", func(c *StockVehicle) { c.DealershipID = "  " }},
		{"VIN too short", "vin", func(c *StockVehicle) { c.VIN = "1HGCM82633A00435" }},
		{"VIN too long", "vin", func(c *StockVehicle) { c.VIN = "1HGCM82633A0043521" }},
		{"VIN contains O", "vin", func(c *StockVehicle) { c.VIN = "1HGCM82633A0O4352" }},
		{"VIN contains symbol", "vin", func(c *StockVehicle) { c.VIN = "1HGCM82633A00435-" }},
		{"empty make", "make", func(c *StockVehicle) { c.Make = " " }},
		{"empty model", "model", func(c *StockVehicle) { c.Model = "" }},
		{"model too long", "model", func(c *StockVehicle) { c.Model = strings.Repeat("x", 65) }},
		{"model year too old", "model_year", func(c *StockVehicle) { c.ModelYear = 1899 }},
		{"model year too new", "model_year", func(c *StockVehicle) { c.ModelYear = 2028 }},
		{"missing stock date", "stocked_on", func(c *StockVehicle) { c.StockedOn = domain.Date{} }},
		{"future stock date", "stocked_on", func(c *StockVehicle) { c.StockedOn = bizDate.AddDays(1) }},
	}
	for _, tc := range invalid {
		t.Run("invalid: "+tc.name, func(t *testing.T) {
			a := newVehicle(t)
			cmd := validStock(bizDate.AddDays(-1))
			tc.edit(&cmd)
			assertRejected(t, a, func() error { return a.Stock(cmd, bizDate) }, domain.ErrValidation)
			var ve *domain.ValidationError
			if err := a.Stock(cmd, bizDate); !errors.As(err, &ve) || ve.Field != tc.field {
				t.Fatalf("got %v, want field %s", err, tc.field)
			}
		})
	}
}

func TestIsAging(t *testing.T) {
	d := domain.NewDate
	cases := []struct {
		name      string
		stockedOn domain.Date
		asOf      domain.Date
		wantAge   int
		wantAging bool
	}{
		{"stock-in day is day 0", d(2026, 10, 29), d(2026, 10, 29), 0, false},
		{"89 days", d(2026, 7, 30), d(2026, 10, 27), 89, false},
		{"exactly 90 days is not aging", d(2026, 7, 30), d(2026, 10, 28), 90, false},
		{"91 days is aging", d(2026, 7, 30), d(2026, 10, 29), 91, true},
		{"90 days across leap day", d(2028, 1, 1), d(2028, 3, 31), 90, false},
		{"91 days across leap day", d(2028, 1, 1), d(2028, 4, 1), 91, true},
		{"90 days across non-leap February", d(2027, 1, 1), d(2027, 4, 1), 90, false},
		{"91 days across year boundary", d(2025, 11, 1), d(2026, 1, 31), 91, true},
		{"90 days ending on the DST change", d(2026, 7, 27), d(2026, 10, 25), 90, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newVehicle(t)
			mustDo(t, a.Stock(validStock(tc.stockedOn), tc.asOf))
			if got := a.AgeInDays(tc.asOf); got != tc.wantAge {
				t.Errorf("AgeInDays = %d, want %d", got, tc.wantAge)
			}
			if got := a.IsAging(tc.asOf); got != tc.wantAging {
				t.Errorf("IsAging = %v, want %v", got, tc.wantAging)
			}
		})
	}

	t.Run("not stocked is never aging", func(t *testing.T) {
		if newVehicle(t).IsAging(bizDate) {
			t.Fatal("unstocked vehicle reported aging")
		}
	})

	t.Run("sold is never aging and its age stops at the sale date", func(t *testing.T) {
		a := stockedFor(t, 200)
		mustDo(t, a.Sell(SellVehicle{SoldOn: bizDate.AddDays(-50)}, bizDate))
		if a.IsAging(bizDate) {
			t.Fatal("sold vehicle reported aging")
		}
		if got := a.AgeInDays(bizDate.AddDays(365)); got != 150 {
			t.Fatalf("sold AgeInDays = %d, want 150", got)
		}
	})
}

func TestProposeAction(t *testing.T) {
	valid := ProposeAction{ActionID: testActionID, ActionType: domain.ActionPriceReduction, Note: "  Reduce by 5%  "}

	t.Run("valid proposal on aging vehicle", func(t *testing.T) {
		a := stockedFor(t, 91)
		mustDo(t, a.ProposeAction(valid, bizDate))
		want := &events.VehicleActionProposedEvent{ActionID: testActionID, ActionType: domain.ActionPriceReduction, Note: "Reduce by 5%"}
		if got := lastEventData(t, a); !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %+v, want %+v", got, want)
		}
		if got, ok := a.ActiveAction(); !ok || got != (Action{ID: testActionID, Type: domain.ActionPriceReduction, Note: "Reduce by 5%"}) {
			t.Fatalf("active action = %+v, %v", got, ok)
		}
	})

	t.Run("OTHER with note and 1000-character note accepted", func(t *testing.T) {
		a := stockedFor(t, 91)
		mustDo(t, a.ProposeAction(ProposeAction{ActionID: "x", ActionType: domain.ActionOther, Note: strings.Repeat("é", 1000)}, bizDate))
	})

	t.Run("new proposal allowed after cancellation", func(t *testing.T) {
		a := withAction(t)
		mustDo(t, a.CancelAction(CancelAction{ActionID: testActionID}))
		mustDo(t, a.ProposeAction(ProposeAction{ActionID: "second", ActionType: domain.ActionAuction}, bizDate))
	})

	rejected := []struct {
		name    string
		vehicle func(*testing.T) *VehicleInventoryAggregate
		cmd     ProposeAction
		want    error
	}{
		{"vehicle not stocked", newVehicle, valid, domain.ErrVehicleNotStocked},
		{"exactly 90 days", func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 90) }, valid, domain.ErrVehicleNotAging},
		{"89 days", func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 89) }, valid, domain.ErrVehicleNotAging},
		{"action already active", withAction, ProposeAction{ActionID: "other", ActionType: domain.ActionHold}, domain.ErrActionAlreadyActive},
		{"sold vehicle", sold, valid, domain.ErrVehicleNotInStock},
		{"missing action id", func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 91) },
			ProposeAction{ActionType: domain.ActionHold}, domain.ErrValidation},
		{"unknown action type", func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 91) },
			ProposeAction{ActionID: "x", ActionType: "DISCOUNT"}, domain.ErrValidation},
		{"OTHER without note", func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 91) },
			ProposeAction{ActionID: "x", ActionType: domain.ActionOther, Note: "  "}, domain.ErrValidation},
		{"note too long", func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 91) },
			ProposeAction{ActionID: "x", ActionType: domain.ActionHold, Note: strings.Repeat("é", 1001)}, domain.ErrValidation},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			a := tc.vehicle(t)
			assertRejected(t, a, func() error { return a.ProposeAction(tc.cmd, bizDate) }, tc.want)
		})
	}

	t.Run("aging is judged at the supplied business date", func(t *testing.T) {
		a := stockedFor(t, 91) // stocked 2026-07-30
		assertRejected(t, a, func() error { return a.ProposeAction(valid, bizDate.AddDays(-1)) }, domain.ErrVehicleNotAging)
		mustDo(t, a.ProposeAction(valid, bizDate))
	})
}

func TestReviseAction(t *testing.T) {
	t.Run("revision replaces type and note", func(t *testing.T) {
		a := withAction(t)
		mustDo(t, a.ReviseAction(ReviseAction{ActionID: testActionID, ActionType: domain.ActionAuction, Note: " send to auction "}))
		want := &events.VehicleActionRevisedEvent{ActionID: testActionID, ActionType: domain.ActionAuction, Note: "send to auction"}
		if got := lastEventData(t, a); !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %+v, want %+v", got, want)
		}
		if got, _ := a.ActiveAction(); got.Type != domain.ActionAuction || got.Note != "send to auction" {
			t.Fatalf("active action = %+v", got)
		}
	})

	t.Run("identical revision records no event", func(t *testing.T) {
		a := withAction(t)
		pending := len(a.Events())
		mustDo(t, a.ReviseAction(ReviseAction{ActionID: testActionID, ActionType: domain.ActionPriceReduction, Note: " 5% "}))
		if len(a.Events()) != pending {
			t.Fatal("no-op revision recorded an event")
		}
	})

	rejected := []struct {
		name    string
		vehicle func(*testing.T) *VehicleInventoryAggregate
		cmd     ReviseAction
		want    error
	}{
		{"vehicle not stocked", newVehicle, ReviseAction{ActionID: testActionID, ActionType: domain.ActionHold}, domain.ErrVehicleNotStocked},
		{"no active action", func(t *testing.T) *VehicleInventoryAggregate { return stockedFor(t, 91) },
			ReviseAction{ActionID: testActionID, ActionType: domain.ActionHold}, domain.ErrActionNotActive},
		{"different action id", withAction, ReviseAction{ActionID: "stale", ActionType: domain.ActionHold}, domain.ErrActionNotActive},
		{"invalid action type", withAction, ReviseAction{ActionID: testActionID, ActionType: ""}, domain.ErrValidation},
		{"sold vehicle", sold, ReviseAction{ActionID: testActionID, ActionType: domain.ActionHold}, domain.ErrVehicleNotInStock},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			a := tc.vehicle(t)
			assertRejected(t, a, func() error { return a.ReviseAction(tc.cmd) }, tc.want)
		})
	}
}

func TestCancelAction(t *testing.T) {
	t.Run("cancellation clears the active action", func(t *testing.T) {
		a := withAction(t)
		mustDo(t, a.CancelAction(CancelAction{ActionID: testActionID, Reason: " customer found "}))
		want := &events.VehicleActionCancelledEvent{ActionID: testActionID, Reason: "customer found"}
		if got := lastEventData(t, a); !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %+v, want %+v", got, want)
		}
		if _, ok := a.ActiveAction(); ok {
			t.Fatal("action still active")
		}
	})

	cancelled := func(t *testing.T) *VehicleInventoryAggregate {
		a := withAction(t)
		mustDo(t, a.CancelAction(CancelAction{ActionID: testActionID}))
		return a
	}
	rejected := []struct {
		name    string
		vehicle func(*testing.T) *VehicleInventoryAggregate
		cmd     CancelAction
		want    error
	}{
		{"vehicle not stocked", newVehicle, CancelAction{ActionID: testActionID}, domain.ErrVehicleNotStocked},
		{"already cancelled", cancelled, CancelAction{ActionID: testActionID}, domain.ErrActionNotActive},
		{"different action id", withAction, CancelAction{ActionID: "stale"}, domain.ErrActionNotActive},
		{"reason too long", withAction, CancelAction{ActionID: testActionID, Reason: strings.Repeat("x", 1001)}, domain.ErrValidation},
		{"sold vehicle", sold, CancelAction{ActionID: testActionID}, domain.ErrVehicleNotInStock},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			a := tc.vehicle(t)
			assertRejected(t, a, func() error { return a.CancelAction(tc.cmd) }, tc.want)
		})
	}
}

func TestSell(t *testing.T) {
	t.Run("sale closes the active action", func(t *testing.T) {
		a := withAction(t)
		mustDo(t, a.Sell(SellVehicle{SoldOn: bizDate}, bizDate))
		want := &events.VehicleSoldEvent{SoldOn: bizDate, ClosedActionID: testActionID}
		if got := lastEventData(t, a); !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %+v, want %+v", got, want)
		}
		if a.Status() != domain.StatusSold || a.SoldOn() != bizDate {
			t.Fatalf("status=%s soldOn=%s", a.Status(), a.SoldOn())
		}
		if _, ok := a.ActiveAction(); ok {
			t.Fatal("action still active after sale")
		}
	})

	t.Run("sale without action, on stock-in day", func(t *testing.T) {
		a := stockedFor(t, 5)
		mustDo(t, a.Sell(SellVehicle{SoldOn: a.StockedOn()}, bizDate))
		if got := lastEventData(t, a).(*events.VehicleSoldEvent); got.ClosedActionID != "" {
			t.Fatalf("closed action = %q", got.ClosedActionID)
		}
	})

	rejected := []struct {
		name    string
		vehicle func(*testing.T) *VehicleInventoryAggregate
		soldOn  domain.Date
		want    error
	}{
		{"vehicle not stocked", newVehicle, bizDate, domain.ErrVehicleNotStocked},
		{"already sold", sold, bizDate, domain.ErrVehicleAlreadySold},
		{"missing date", withAction, domain.Date{}, domain.ErrValidation},
		{"before stock-in", withAction, bizDate.AddDays(-92), domain.ErrValidation},
		{"after business date", withAction, bizDate.AddDays(1), domain.ErrValidation},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			a := tc.vehicle(t)
			assertRejected(t, a, func() error { return a.Sell(SellVehicle{SoldOn: tc.soldOn}, bizDate) }, tc.want)
		})
	}
}

func TestCommandsAfterSale(t *testing.T) {
	commands := []struct {
		name string
		run  func(*VehicleInventoryAggregate) error
		want error
	}{
		{"stock", func(a *VehicleInventoryAggregate) error { return a.Stock(validStock(bizDate), bizDate) }, domain.ErrVehicleAlreadyStocked},
		{"propose", func(a *VehicleInventoryAggregate) error {
			return a.ProposeAction(ProposeAction{ActionID: "n", ActionType: domain.ActionHold}, bizDate)
		}, domain.ErrVehicleNotInStock},
		{"revise", func(a *VehicleInventoryAggregate) error {
			return a.ReviseAction(ReviseAction{ActionID: testActionID, ActionType: domain.ActionHold})
		}, domain.ErrVehicleNotInStock},
		{"cancel", func(a *VehicleInventoryAggregate) error {
			return a.CancelAction(CancelAction{ActionID: testActionID})
		}, domain.ErrVehicleNotInStock},
		{"sell", func(a *VehicleInventoryAggregate) error { return a.Sell(SellVehicle{SoldOn: bizDate}, bizDate) }, domain.ErrVehicleAlreadySold},
	}
	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			a := sold(t)
			assertRejected(t, a, func() error { return tc.run(a) }, tc.want)
		})
	}
}

func TestMultiplePendingEvents(t *testing.T) {
	a := newVehicle(t)
	mustDo(t, a.Stock(validStock(bizDate.AddDays(-100)), bizDate))
	mustDo(t, a.ProposeAction(ProposeAction{ActionID: "a1", ActionType: domain.ActionHold}, bizDate))
	mustDo(t, a.ReviseAction(ReviseAction{ActionID: "a1", ActionType: domain.ActionPromote, Note: "web"}))
	mustDo(t, a.CancelAction(CancelAction{ActionID: "a1"}))
	mustDo(t, a.ProposeAction(ProposeAction{ActionID: "a2", ActionType: domain.ActionAuction}, bizDate))

	if a.BaseVersion() != 0 || a.Version() != 5 || !a.IsUnsaved() {
		t.Fatalf("base=%d version=%d unsaved=%v", a.BaseVersion(), a.Version(), a.IsUnsaved())
	}
	for i, e := range a.Events() {
		if e.Version != i+1 || e.AggregateID != testVehicleID {
			t.Fatalf("event %d: version=%d aggregate=%s", i, e.Version, e.AggregateID)
		}
	}
	if got, _ := a.ActiveAction(); got.ID != "a2" {
		t.Fatalf("active action = %+v", got)
	}

	// After the store commits, the next command continues from the persisted version.
	a.Update()
	mustDo(t, a.Sell(SellVehicle{SoldOn: bizDate}, bizDate))
	if evs := a.Events(); len(evs) != 1 || evs[0].Version != 6 {
		t.Fatalf("pending after update: %+v", evs)
	}
}
