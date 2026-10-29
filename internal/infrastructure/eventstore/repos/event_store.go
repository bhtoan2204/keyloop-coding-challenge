package repos

import (
	"context"

	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

var _ EventStore = (*eventStore)(nil)

type EventStore interface {
	Get(ctx context.Context, aggregateID string, fromVersion, toVersion int, agg eventsourcing.Aggregate) error
	Append(context.Context, eventsourcing.Event) error
	CreateIfNotExist(ctx context.Context, id, typ string) error
	CheckAndUpdateVersion(ctx context.Context, agg eventsourcing.Aggregate) error
	CreateSnapshot(ctx context.Context, agg eventsourcing.Aggregate) error
	ReadSnapshot(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) (bool, error)
	FindAggregate(ctx context.Context, id string) (version int, aggregateType string, err error)
	// ReadAll streams the whole event history from one consistent snapshot, ordered by
	// (aggregate_id, version). Read-only; used to rebuild and sync projections.
	ReadAll(ctx context.Context, batchSize int, fn func(eventsourcing.Event) error) error
	// ReadStream returns the events of one aggregate with afterVersion < version <=
	// toVersion, in version order, decoded like ReadAll. Read-only; used by the live
	// projection consumer to repair version gaps in the change feed.
	ReadStream(ctx context.Context, aggregateID string, afterVersion, toVersion int) ([]eventsourcing.Event, error)
	WithTransaction(ctx context.Context, fn func(EventStore) error) error
}

type eventStore struct {
	*aggregateRepo
	*eventRepo
	db         *gorm.DB
	serializer eventsourcing.Serializer
}

func NewEventStore(db *gorm.DB, s eventsourcing.Serializer) EventStore {
	return &eventStore{
		db:            db,
		serializer:    s,
		aggregateRepo: newAggregateRepo(db, s),
		eventRepo:     newEventRepo(db, s),
	}
}

func (r *eventStore) WithTransaction(ctx context.Context, fn func(txStore EventStore) error) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewEventStore(tx, r.serializer))
	})
	return classifyTxError(err)
}
