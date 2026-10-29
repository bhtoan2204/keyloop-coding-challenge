package projectors_test

import (
	"errors"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/package/eventsourcing"
)

var at = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func ev(version int, data interface{}) eventsourcing.Event {
	return eventsourcing.Event{ID: int64(100 + version), AggregateID: "v1", Version: version, Data: data, CreatedAt: at.Unix(),
		Metadata: map[string]interface{}{"actor_id": "manager-42", "correlation_id": "req-1"}}
}

func stockedRow() *viewstore.VehicleRow {
	r, _, err := projectors.ProjectVehicle(nil, ev(1, &events.VehicleStockedEvent{DealershipID: "d-001", VIN: "VIN",
		Make: "Toyota", Model: "Corolla", ModelYear: 2023, StockedOn: domain.NewDate(2026, 7, 1)}))
	if err != nil {
		panic(err)
	}
	return &r
}

func withAction() *viewstore.VehicleRow {
	r, _, err := projectors.ProjectVehicle(stockedRow(), ev(2, &events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionHold, Note: "n"}))
	if err != nil {
		panic(err)
	}
	return &r
}

func TestProjectVehicleTransitions(t *testing.T) {
	r := stockedRow()
	if r.VehicleID != "v1" || r.Status != domain.StatusInStock || r.Version != 1 || r.LastEventID != 101 || r.ActionID != "" || !r.UpdatedAt.Equal(at) {
		t.Fatalf("stocked: %+v", r)
	}

	proposed, h, err := projectors.ProjectVehicle(r, ev(2, &events.VehicleActionProposedEvent{ActionID: "a1", ActionType: domain.ActionHold, Note: "n"}))
	if err != nil || proposed.ActionID != "a1" || proposed.ActionUpdatedBy != "manager-42" || !proposed.ActionProposedAt.Equal(at) ||
		h == nil || h.Change != "PROPOSED" || h.CorrelationID != "req-1" || h.Version != 2 || h.EventID != 102 {
		t.Fatalf("proposed: %+v %+v %v", proposed, h, err)
	}
	if r.ActionID != "" {
		t.Fatal("ProjectVehicle mutated its input row")
	}

	revised, h, err := projectors.ProjectVehicle(&proposed, ev(3, &events.VehicleActionRevisedEvent{ActionID: "a1", ActionType: domain.ActionAuction, Note: "lot"}))
	if err != nil || revised.ActionType != domain.ActionAuction || revised.ActionNote != "lot" || !revised.ActionProposedAt.Equal(at) || h.Change != "REVISED" {
		t.Fatalf("revised: %+v %v", revised, err)
	}

	cancelled, h, err := projectors.ProjectVehicle(&revised, ev(4, &events.VehicleActionCancelledEvent{ActionID: "a1", Reason: "buyer"}))
	if err != nil || cancelled.ActionID != "" || !cancelled.ActionProposedAt.IsZero() || h.Change != "CANCELLED" ||
		h.ActionType != domain.ActionAuction || h.Reason != "buyer" {
		t.Fatalf("cancelled: %+v %+v %v", cancelled, h, err)
	}

	sold, h, err := projectors.ProjectVehicle(withAction(), ev(3, &events.VehicleSoldEvent{SoldOn: domain.NewDate(2026, 10, 20), ClosedActionID: "a1"}))
	if err != nil || sold.Status != domain.StatusSold || sold.SoldOn != domain.NewDate(2026, 10, 20) || sold.ActionID != "" || h == nil || h.Change != "CLOSED_BY_SALE" {
		t.Fatalf("sold with action: %+v %+v %v", sold, h, err)
	}
	soldNoAction, h, err := projectors.ProjectVehicle(stockedRow(), ev(2, &events.VehicleSoldEvent{SoldOn: domain.NewDate(2026, 10, 20)}))
	if err != nil || soldNoAction.Status != domain.StatusSold || h != nil {
		t.Fatalf("sold without action: %+v %+v %v", soldNoAction, h, err)
	}
}

func TestProjectVehicleRejectsInvalidInput(t *testing.T) {
	soldRow, _, _ := projectors.ProjectVehicle(stockedRow(), ev(2, &events.VehicleSoldEvent{SoldOn: domain.NewDate(2026, 10, 20)}))
	cases := []struct {
		name    string
		current *viewstore.VehicleRow
		data    interface{}
		want    error
	}{
		{"stocked twice", stockedRow(), &events.VehicleStockedEvent{DealershipID: "d-001"}, projectors.ErrInconsistent},
		{"action before stock", nil, &events.VehicleActionProposedEvent{ActionID: "a"}, projectors.ErrInconsistent},
		{"second active action", withAction(), &events.VehicleActionProposedEvent{ActionID: "a2"}, projectors.ErrInconsistent},
		{"revise other action", withAction(), &events.VehicleActionRevisedEvent{ActionID: "zz"}, projectors.ErrInconsistent},
		{"cancel without action", stockedRow(), &events.VehicleActionCancelledEvent{ActionID: "a1"}, projectors.ErrInconsistent},
		{"sale closes wrong action", withAction(), &events.VehicleSoldEvent{SoldOn: domain.NewDate(2026, 10, 1)}, projectors.ErrInconsistent},
		{"event after sale", &soldRow, &events.VehicleActionProposedEvent{ActionID: "a"}, projectors.ErrInconsistent},
		{"unknown type", stockedRow(), &struct{}{}, projectors.ErrUnsupportedEvent},
		{"unknown type on sold vehicle", &soldRow, &struct{}{}, projectors.ErrUnsupportedEvent},
		{"unknown type, no row", nil, &struct{}{}, projectors.ErrUnsupportedEvent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := projectors.ProjectVehicle(tc.current, ev(9, tc.data)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
