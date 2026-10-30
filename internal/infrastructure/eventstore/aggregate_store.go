package eventstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

var (
	_ AggregateStore = (*aggregateStore)(nil)

	ErrInvalidAggregate = errors.New("aggregate must be a non-nil pointer")
	// ErrNestedSave: Save must own the outermost transaction. Inside a caller's
	// transaction it could only commit a savepoint, and acknowledging the pending events
	// (Root.Update) before the outer transaction commits could lose them.
	ErrNestedSave = errors.New("aggregate store: save inside an existing transaction")
	// After ErrConcurrencyConflict or ErrRetryable a save may succeed if the caller
	// reloads the aggregate, re-runs the command and saves again (command.IsRetryable).
	// The store never retries by itself: the pending events were decided on stale
	// state and must be recomputed, not re-sent.
	ErrConcurrencyConflict   = repos.ErrConcurrencyConflict
	ErrAggregateNotFound     = repos.ErrAggregateNotFound
	ErrAggregateTypeMismatch = repos.ErrAggregateTypeMismatch
	ErrDuplicateEvent        = repos.ErrDuplicateEvent
	ErrRetryable             = repos.ErrRetryable

	ErrUnsupportedSchemaVersion = repos.ErrUnsupportedSchemaVersion
)

type AggregateStore interface {
	Get(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) error
	Save(ctx context.Context, agg eventsourcing.Aggregate) error
	SaveWithMetadata(ctx context.Context, agg eventsourcing.Aggregate, metadata map[string]interface{}) error
}

type aggregateStore struct {
	repo             repos.EventStore
	snapshotInterval int
	projection       TransactionalProjection
}

func NewAggregateStoreWithSnapshotInterval(repositories repos.Repos, interval int, opts ...Option) AggregateStore {
	as := &aggregateStore{
		repo:             repositories.EventStore(),
		snapshotInterval: interval,
	}
	for _, opt := range opts {
		opt(as)
	}
	return as
}

func (as *aggregateStore) Get(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) error {
	if err := validateAggregate(agg); err != nil {
		return err
	}
	if aggregateID == "" {
		return eventsourcing.ErrIDEmpty
	}
	root := agg.Root()
	if root.Version() != 0 || root.BaseVersion() != 0 || root.IsUnsaved() || root.AggregateID() != "" {
		return fmt.Errorf("%w: load requires a zero aggregate", ErrInvalidAggregate)
	}

	version, storedType, err := as.repo.FindAggregate(ctx, aggregateID)
	if err != nil {
		return fmt.Errorf("load aggregate %s: %w", aggregateID, err)
	}
	wantType := aggregateType(agg)
	if storedType != wantType {
		return fmt.Errorf("%w: aggregate %s has type %s", ErrAggregateTypeMismatch, aggregateID, storedType)
	}
	root.SetAggregateType(wantType)
	if err := root.SetID(aggregateID); err != nil {
		return err
	}

	found, err := as.repo.ReadSnapshot(ctx, aggregateID, agg)
	if err != nil {
		return fmt.Errorf("restore aggregate %s from snapshot: %w", aggregateID, err)
	}
	from := 0
	if found {
		from = root.BaseVersion()
		if from > version {
			return fmt.Errorf("snapshot version %d is ahead of aggregate %s version %d", from, aggregateID, version)
		}
	}
	if err := as.replay(ctx, aggregateID, from, version, agg); err != nil {
		return fmt.Errorf("restore aggregate %s from events: %w", aggregateID, err)
	}
	return nil
}

func (as *aggregateStore) Save(ctx context.Context, agg eventsourcing.Aggregate) error {
	return as.SaveWithMetadata(ctx, agg, nil)
}

func (as *aggregateStore) SaveWithMetadata(ctx context.Context, agg eventsourcing.Aggregate, metadata map[string]interface{}) error {
	if err := validateAggregate(agg); err != nil {
		return err
	}
	root := agg.Root()
	root.SetAggregateType(aggregateType(agg))
	aggregateID := root.AggregateID()
	if aggregateID == "" {
		return eventsourcing.ErrIDEmpty
	}
	if len(root.Events()) == 0 {
		return nil
	}
	events := root.CloneEvents()
	for i := range events {
		events[i].AggregateType = root.AggregateType()
		events[i].Metadata = mergeMetadata(events[i].Metadata, metadata)
	}
	baseVersion := root.BaseVersion()
	currentVersion := root.Version()
	if as.repo.InTransaction() {
		return ErrNestedSave
	}

	// One transaction: projection lock, create-if-missing, version CAS, appends (ids
	// read back), read-model projection, optional snapshot. Any error rolls back all of it.
	if err := as.repo.WithTransaction(ctx, func(txn repos.EventStore) error {
		if as.projection != nil {
			if err := as.projection.Lock(ctx, txn.DB()); err != nil {
				return fmt.Errorf("lock projection for aggregate %s: %w", aggregateID, err)
			}
		}
		if err := txn.CreateIfNotExist(ctx, aggregateID, root.AggregateType()); err != nil {
			return fmt.Errorf("create aggregate %s: %w", aggregateID, err)
		}
		if err := txn.CheckAndUpdateVersion(ctx, agg); err != nil {
			return err
		}
		for i := range events {
			id, err := txn.Append(ctx, events[i])
			if err != nil {
				return fmt.Errorf("append event to aggregate %s: %w", aggregateID, err)
			}
			events[i].ID = id
		}
		if as.projection != nil {
			if err := as.projection.Apply(ctx, txn.DB(), events); err != nil {
				return fmt.Errorf("project aggregate %s: %w", aggregateID, err)
			}
		}
		if as.shouldCreateSnapshot(baseVersion, currentVersion) {
			if err := txn.CreateSnapshot(ctx, agg); err != nil {
				return fmt.Errorf("create snapshot for aggregate %s at version %d: %w", aggregateID, currentVersion, err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("save aggregate %s: %w", aggregateID, err)
	}
	// Only now, after the outermost commit, are the pending events acknowledged.
	root.Update()
	return nil
}

func (as *aggregateStore) replay(ctx context.Context, aggregateID string, from, to int, agg eventsourcing.Aggregate) error {
	if from >= to {
		return nil
	}
	if err := as.repo.Get(ctx, aggregateID, from, to, agg); err != nil {
		return err
	}
	if got := agg.Root().BaseVersion(); got != to {
		return fmt.Errorf("replayed to version %d, stored version is %d", got, to)
	}
	return nil
}

func (as *aggregateStore) shouldCreateSnapshot(baseVersion, currentVersion int) bool {
	return as.snapshotInterval > 0 && currentVersion/as.snapshotInterval > baseVersion/as.snapshotInterval
}

// mergeMetadata returns a new map with the event's own keys plus the command metadata,
// which wins: actor and correlation IDs are audit facts an aggregate must not
// overwrite. The aggregate's pending events are not modified (Save works on clones).
// Non-map event metadata is kept under the "event" key.
func mergeMetadata(own interface{}, command map[string]interface{}) interface{} {
	if len(command) == 0 {
		return own
	}
	merged := make(map[string]interface{}, len(command)+1)
	switch m := own.(type) {
	case nil:
	case map[string]interface{}:
		for k, v := range m {
			merged[k] = v
		}
	default:
		merged["event"] = m
	}
	for k, v := range command {
		merged[k] = v
	}
	return merged
}

func validateAggregate(agg eventsourcing.Aggregate) error {
	if agg == nil {
		return ErrInvalidAggregate
	}
	value := reflect.ValueOf(agg)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return ErrInvalidAggregate
	}
	return nil
}

func aggregateType(agg eventsourcing.Aggregate) string {
	return reflect.TypeOf(agg).Elem().Name()
}
