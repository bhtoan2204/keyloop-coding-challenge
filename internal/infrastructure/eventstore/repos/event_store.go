package repos

import (
	"context"

	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

var _ EventStore = (*eventStore)(nil)

type EventStore interface {
	Get(ctx context.Context, aggregateID string, fromVersion, toVersion int, agg eventsourcing.Aggregate) error
	// Append stores one event and returns its database-generated id.
	Append(context.Context, eventsourcing.Event) (int64, error)
	CreateIfNotExist(ctx context.Context, id, typ string) error
	CheckAndUpdateVersion(ctx context.Context, agg eventsourcing.Aggregate) error
	CreateSnapshot(ctx context.Context, agg eventsourcing.Aggregate) error
	ReadSnapshot(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) (bool, error)
	FindAggregate(ctx context.Context, id string) (version int, aggregateType string, err error)
	// ReadAll streams the whole event history, ordered by (aggregate_id, version), on
	// the caller's transaction. Read-only; used to rebuild the read model.
	ReadAll(ctx context.Context, batchSize int, fn func(eventsourcing.Event) error) error
	// ReadStream returns the events of one aggregate with afterVersion < version <=
	// toVersion, in version order, decoded like ReadAll. Read-only; used by the live
	// projection consumer to repair version gaps in the change feed.
	ReadStream(ctx context.Context, aggregateID string, afterVersion, toVersion int) ([]eventsourcing.Event, error)
	WithTransaction(ctx context.Context, fn func(EventStore) error) error
	// DB is the handle this store runs on: inside WithTransaction, the transaction, so
	// the read model can be written in the same transaction as the events.
	DB() *gorm.DB
	// InTransaction reports whether the store is bound to a transaction.
	InTransaction() bool
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

func (r *eventStore) DB() *gorm.DB {
	return r.db
}

func (r *eventStore) InTransaction() bool {
	_, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok
}

func (r *eventStore) WithTransaction(ctx context.Context, fn func(txStore EventStore) error) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewEventStore(tx, r.serializer))
	})
	return classifyTxError(err)
}
