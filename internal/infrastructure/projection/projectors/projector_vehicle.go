package projectors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/package/eventsourcing"
)

var (
	// ErrUnsupportedEvent: an event payload this projector does not know. It is never
	// skipped silently: skipping would leave the read model wrong without a trace.
	ErrUnsupportedEvent = errors.New("unsupported event")
	// ErrInconsistent: the event does not fit the projected state (e.g. a revision of
	// an action that is not active). It indicates corrupt history or a projection bug.
	ErrInconsistent = errors.New("event inconsistent with projected state")
)

// VehicleProjector maintains inventory_vehicle and vehicle_action_history.
type VehicleProjector struct{}

func (p *VehicleProjector) Handle(ctx context.Context, store viewstore.ViewStore, ev eventsourcing.Event) error {
	current, err := store.Vehicle(ctx, ev.AggregateID)
	if err != nil {
		return err
	}
	next, history, err := ProjectVehicle(current, ev)
	if err != nil {
		return err
	}
	if err := store.SaveVehicle(ctx, next); err != nil {
		return fmt.Errorf("save vehicle %s: %w", ev.AggregateID, err)
	}
	if history != nil {
		if err := store.AppendHistory(ctx, *history); err != nil {
			return fmt.Errorf("append history %s v%d: %w", ev.AggregateID, ev.Version, err)
		}
	}
	return nil
}

// ProjectVehicle computes the next row (and history entry, if any) from the current
// row and one event. It is pure, so it is deterministic on replay.
func ProjectVehicle(current *viewstore.VehicleRow, ev eventsourcing.Event) (viewstore.VehicleRow, *viewstore.HistoryRow, error) {
	at := time.Unix(ev.CreatedAt, 0).UTC()
	actor, correlation := metadataString(ev.Metadata, "actor_id"), metadataString(ev.Metadata, "correlation_id")
	inconsistent := func(format string, args ...interface{}) error {
		return fmt.Errorf("%w: %s v%d %s: %s", ErrInconsistent, ev.AggregateID, ev.Version, ev.EventType, fmt.Sprintf(format, args...))
	}
	history := func(r viewstore.VehicleRow, change, actionID string, t domain.ActionType, note, reason string) *viewstore.HistoryRow {
		return &viewstore.HistoryRow{VehicleID: r.VehicleID, Version: ev.Version, EventID: ev.ID, ActionID: actionID,
			Change: change, ActionType: t, Note: note, Reason: reason, ActorID: actor, CorrelationID: correlation, OccurredAt: at}
	}

	switch ev.Data.(type) {
	case *events.VehicleStockedEvent, *events.VehicleActionProposedEvent, *events.VehicleActionRevisedEvent,
		*events.VehicleActionCancelledEvent, *events.VehicleSoldEvent:
	default:
		return viewstore.VehicleRow{}, nil, fmt.Errorf("%w: %s (%T)", ErrUnsupportedEvent, ev.EventType, ev.Data)
	}
	if e, ok := ev.Data.(*events.VehicleStockedEvent); ok {
		if current != nil {
			return viewstore.VehicleRow{}, nil, inconsistent("vehicle already projected")
		}
		return viewstore.VehicleRow{
			VehicleID: ev.AggregateID, DealershipID: e.DealershipID, VIN: e.VIN, Make: e.Make, Model: e.Model,
			ModelYear: e.ModelYear, StockedOn: e.StockedOn, Status: domain.StatusInStock,
			Version: ev.Version, LastEventID: ev.ID, UpdatedAt: at,
		}, nil, nil
	}
	if current == nil {
		return viewstore.VehicleRow{}, nil, inconsistent("vehicle not projected")
	}
	if current.Status != domain.StatusInStock {
		return viewstore.VehicleRow{}, nil, inconsistent("vehicle is %s", current.Status)
	}
	r := *current
	r.Version, r.LastEventID, r.UpdatedAt = ev.Version, ev.ID, at
	var h *viewstore.HistoryRow

	switch e := ev.Data.(type) {
	case *events.VehicleActionProposedEvent:
		if r.ActionID != "" {
			return r, nil, inconsistent("action %s already active", r.ActionID)
		}
		r.ActionID, r.ActionType, r.ActionNote = e.ActionID, e.ActionType, e.Note
		r.ActionProposedAt, r.ActionUpdatedAt, r.ActionUpdatedBy = at, at, actor
		h = history(r, "PROPOSED", e.ActionID, e.ActionType, e.Note, "")
	case *events.VehicleActionRevisedEvent:
		if r.ActionID == "" || r.ActionID != e.ActionID {
			return r, nil, inconsistent("action %s is not active", e.ActionID)
		}
		r.ActionType, r.ActionNote, r.ActionUpdatedAt, r.ActionUpdatedBy = e.ActionType, e.Note, at, actor
		h = history(r, "REVISED", e.ActionID, e.ActionType, e.Note, "")
	case *events.VehicleActionCancelledEvent:
		if r.ActionID == "" || r.ActionID != e.ActionID {
			return r, nil, inconsistent("action %s is not active", e.ActionID)
		}
		h = history(r, "CANCELLED", e.ActionID, r.ActionType, r.ActionNote, e.Reason)
		clearAction(&r)
	case *events.VehicleSoldEvent:
		if r.ActionID != e.ClosedActionID {
			return r, nil, inconsistent("closed_action_id %q, active action %q", e.ClosedActionID, r.ActionID)
		}
		if r.ActionID != "" {
			h = history(r, "CLOSED_BY_SALE", r.ActionID, r.ActionType, r.ActionNote, "")
		}
		r.Status, r.SoldOn = domain.StatusSold, e.SoldOn
		clearAction(&r)
	default:
		return r, nil, fmt.Errorf("%w: %s (%T)", ErrUnsupportedEvent, ev.EventType, ev.Data)
	}
	return r, h, nil
}

func clearAction(r *viewstore.VehicleRow) {
	r.ActionID, r.ActionType, r.ActionNote, r.ActionUpdatedBy = "", "", "", ""
	r.ActionProposedAt, r.ActionUpdatedAt = time.Time{}, time.Time{}
}

func metadataString(md interface{}, key string) string {
	m, ok := md.(map[string]interface{})
	if !ok {
		return ""
	}
	s, _ := m[key].(string)
	return s
}
