package eventstore

import (
	"context"

	"gorm.io/gorm"

	"keyloop-coding-challenge/package/eventsourcing"
)

// TransactionalProjection updates a read model inside the transaction that appends the
// events, on the same *gorm.DB transaction handle, so events and their projection
// commit or roll back together. Implemented by projection.CommandProjection.
type TransactionalProjection interface {
	// Lock is called first in the save transaction, before any event-store row is
	// locked, so every writer acquires the projection lock before event-store locks
	// (a rebuild takes the same lock exclusively and never waits for event-store rows).
	Lock(ctx context.Context, tx *gorm.DB) error
	// Apply projects the events exactly as persisted, including their generated ids.
	Apply(ctx context.Context, tx *gorm.DB, events []eventsourcing.Event) error
}

// Option configures an AggregateStore.
type Option func(*aggregateStore)

// WithProjection makes every save also apply its events to p in the same transaction.
func WithProjection(p TransactionalProjection) Option {
	return func(as *aggregateStore) {
		as.projection = p
	}
}
