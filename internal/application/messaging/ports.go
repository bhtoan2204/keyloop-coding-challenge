package messaging

import (
	"context"
	"time"

	"keyloop-coding-challenge/package/eventsourcing"
)

// Transport delivers the change feed and stores consumer progress (a Kafka consumer
// group in production, a fake in tests).
type Transport interface {
	// Poll waits (until ctx ends) for records and returns them grouped by partition,
	// each partition at most once and its records in offset order. The assignment of
	// the returned partitions must not change until Release, so a batch can be
	// processed and its offsets committed before a rebalance hands the partitions on.
	// A ctx that ends while waiting is not an error.
	Poll(ctx context.Context) ([]PartitionBatch, error)
	// Commit records, for the partition of each record, that every record up to and
	// including it has been processed (the next offset to read is Offset+1).
	Commit(ctx context.Context, processed []Record) error
	// Retry makes the record's partition deliver from rec again, not before delay.
	// Records after rec in that partition are not delivered before rec.
	Retry(rec Record, delay time.Duration)
	// Release lets a pending rebalance proceed; called after each batch.
	Release()
	Close()
}

// Projector applies one event to the read model in one transaction: state, history
// and the applied version commit together. It returns applied=false for a duplicate
// (version already applied) and an error wrapping ErrVersionGap when the event is not
// the next version of its aggregate. Implemented over projection.Projection.
type Projector interface {
	Apply(ctx context.Context, ev eventsourcing.Event) (applied bool, err error)
}

// EventHistory reads the event store, the source of truth (repos.EventStore).
type EventHistory interface {
	// ReadStream returns the events of one aggregate in (afterVersion, toVersion], in
	// version order.
	ReadStream(ctx context.Context, aggregateID string, afterVersion, toVersion int) ([]eventsourcing.Event, error)
}

// Synchronizer brings the read model up to date with every event committed to the
// event store before the call, read from one consistent snapshot (projection.Runner).
type Synchronizer interface {
	Sync(ctx context.Context) (applied int, err error)
}
