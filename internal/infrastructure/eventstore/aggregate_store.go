package eventstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"
)

var (
	_ AggregateStore = (*aggregateStore)(nil)

	ErrInvalidAggregate      = errors.New("aggregate must be a non-nil pointer")
	ErrConcurrencyConflict   = repos.ErrConcurrencyConflict
	ErrAggregateNotFound     = repos.ErrAggregateNotFound
	ErrAggregateTypeMismatch = repos.ErrAggregateTypeMismatch
	ErrDuplicateEvent        = repos.ErrDuplicateEvent
	ErrRetryable             = repos.ErrRetryable

	ErrUnsupportedSchemaVersion = repos.ErrUnsupportedSchemaVersion
)

// IsRetryable reports whether a failed Save may succeed if the caller reloads the
// aggregate, re-runs the command against the fresh state and saves again. The store
// never retries by itself: after a conflict the pending events were decided on stale
// state and must be recomputed, not re-sent.
func IsRetryable(err error) bool {
	return errors.Is(err, ErrConcurrencyConflict) || errors.Is(err, ErrRetryable)
}

const defaultSnapshotInterval = 10

type AggregateStore interface {
	Get(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) error
	Save(ctx context.Context, agg eventsourcing.Aggregate) error
}

type aggregateStore struct {
	repo             repos.EventStore
	snapshotInterval int
}

func NewAggregateStore(repositories repos.Repos) AggregateStore {
	return NewAggregateStoreWithSnapshotInterval(repositories, defaultSnapshotInterval)
}

func NewAggregateStoreWithSnapshotInterval(repositories repos.Repos, interval int) AggregateStore {
	return &aggregateStore{repo: repositories.EventStore(), snapshotInterval: interval}
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
	}
	baseVersion := root.BaseVersion()
	currentVersion := root.Version()

	if err := as.repo.WithTransaction(ctx, func(txn repos.EventStore) error {
		if err := txn.CreateIfNotExist(ctx, aggregateID, root.AggregateType()); err != nil {
			return fmt.Errorf("create aggregate %s: %w", aggregateID, err)
		}
		if err := txn.CheckAndUpdateVersion(ctx, agg); err != nil {
			return err
		}
		for _, event := range events {
			if err := txn.Append(ctx, event); err != nil {
				return fmt.Errorf("append event to aggregate %s: %w", aggregateID, err)
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
