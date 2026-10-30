package projection

import "context"

// Store is the read-model write port used by projections.
type Store interface {
	// WithTransaction runs fn with a store bound to one transaction: the store's own
	// transaction, or the caller's when the store is already bound to one (a command's
	// save, a rebuild), without a savepoint. Everything fn writes commits or rolls back
	// together.
	WithTransaction(ctx context.Context, fn func(tx Store) error) error
	// LockAggregate serialises processing of one aggregate until the transaction ends,
	// including an aggregate that has no row yet, and returns the last applied version
	// (0 when none).
	LockAggregate(ctx context.Context, aggregateID string) (int, error)
	SetAppliedVersion(ctx context.Context, aggregateID string, version int, eventID int64) error
	// Vehicle returns the projected vehicle, or nil when there is none.
	Vehicle(ctx context.Context, vehicleID string) (*ProjectedVehicle, error)
	SaveVehicle(ctx context.Context, vehicle ProjectedVehicle) error
	AppendHistory(ctx context.Context, entry ActionHistoryEntry) error
}
