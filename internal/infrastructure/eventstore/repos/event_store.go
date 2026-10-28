package repos

import (
	"context"

	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

var (
	_ EventStore = (*eventStore)(nil)
)

type EventStore interface {
	Get(ctx context.Context, aggregateID string, fromVersion, toVersion int, agg eventsourcing.Aggregate) error
	Append(context.Context, eventsourcing.Event) error

	CreateIfNotExist(ctx context.Context, id, typ string) error
	CheckAndUpdateVersion(ctx context.Context, agg eventsourcing.Aggregate) bool
	CreateSnapshot(ctx context.Context, agg eventsourcing.Aggregate) error
	ReadSnapshot(ctx context.Context, aggregateID string, version int, agg eventsourcing.Aggregate) bool
	WithTransaction(ctx context.Context, fn func(EventStore) error) (err error)
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
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := NewEventStore(tx, r.serializer)
		return fn(txStore)
	})
}
