// Package viewstore is the PostgreSQL read model: the transactional write port used by
// projections and the read queries used by the query application services.
package viewstore

import (
	"context"
	"errors"
	"time"

	"keyloop-coding-challenge/internal/domain"
)

// ErrNotInTransaction: the write methods need a transaction-bound store from
// WithTransaction (the aggregate lock is transaction-scoped).
var ErrNotInTransaction = errors.New("viewstore: write outside WithTransaction")

// ViewStore is the write port of the read model, used by projections.
type ViewStore interface {
	// WithTransaction runs fn in a transaction (a savepoint when already in one) with a
	// store bound to it. Everything fn writes commits or rolls back together.
	WithTransaction(ctx context.Context, fn func(tx ViewStore) error) error
	// LockAggregate serialises processing of one aggregate until the transaction ends,
	// including an aggregate that has no row yet, and returns the last applied version
	// (0 when none).
	LockAggregate(ctx context.Context, aggregateID string) (int, error)
	SetAppliedVersion(ctx context.Context, aggregateID string, version int, eventID int64) error
	// Vehicle returns the projected vehicle, or nil when there is none.
	Vehicle(ctx context.Context, vehicleID string) (*VehicleRow, error)
	SaveVehicle(ctx context.Context, row VehicleRow) error
	AppendHistory(ctx context.Context, row HistoryRow) error
}

// VehicleRow is one inventory_vehicle row. Zero times and dates are stored as NULL.
type VehicleRow struct {
	VehicleID        string
	DealershipID     string
	VIN              string
	Make             string
	Model            string
	ModelYear        int
	StockedOn        domain.Date
	Status           domain.InventoryStatus
	SoldOn           domain.Date
	ActionID         string
	ActionType       domain.ActionType
	ActionNote       string
	ActionProposedAt time.Time
	ActionUpdatedAt  time.Time
	ActionUpdatedBy  string
	Version          int
	LastEventID      int64
	UpdatedAt        time.Time
}

// HistoryRow is one vehicle_action_history row.
type HistoryRow struct {
	VehicleID     string
	Version       int
	EventID       int64
	ActionID      string
	Change        string
	ActionType    domain.ActionType
	Note          string
	Reason        string
	ActorID       string
	CorrelationID string
	OccurredAt    time.Time
}
