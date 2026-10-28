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

	ErrInvalidAggregate    = errors.New("aggregate must be a non-nil pointer")
	ErrConcurrencyConflict = errors.New("optimistic concurrency conflict")
)

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
	return &aggregateStore{
		repo:             repositories.EventStore(),
		snapshotInterval: interval,
	}
}

func (as *aggregateStore) Get(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) error {
	if err := validateAggregate(agg); err != nil {
		return err
	}

	agg.Root().SetAggregateType(aggregateType(agg))

	found, err := as.getFromSnapshot(ctx, aggregateID, agg)
	if err != nil {
		return fmt.Errorf("restore aggregate %s from snapshot: %w", aggregateID, err)
	}

	if found {
		return nil
	}

	if err := as.repo.Get(ctx, aggregateID, 0, 0, agg); err != nil {
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
	baseVersion := root.BaseVersion()
	currentVersion := root.Version()
	events := root.Events()

	if err := as.repo.CreateIfNotExist(ctx, aggregateID, aggregateType(agg)); err != nil {
		return fmt.Errorf("create aggregate %s: %w", aggregateID, err)
	}

	if len(events) == 0 {
		return nil
	}

	if err := as.repo.WithTransaction(ctx, func(txn repos.EventStore) error {
		if !txn.CheckAndUpdateVersion(ctx, agg) {
			return fmt.Errorf("%w: aggregateID=%s, expectedVersion=%d, newVersion=%d",
				ErrConcurrencyConflict, aggregateID, baseVersion, currentVersion)
		}

		for _, event := range events {
			if err := txn.Append(ctx, event); err != nil {
				return fmt.Errorf("append event to aggregate %s: %w", aggregateID, err)
			}
		}

		if as.shouldCreateSnapshot(baseVersion, currentVersion) {
			if err := txn.CreateSnapshot(ctx, agg); err != nil {
				return fmt.Errorf("create snapshot for aggregate %s at version %d: %w",
					aggregateID, currentVersion, err)
			}
		}

		return nil
	}); err != nil {
		return fmt.Errorf("save aggregate %s: %w", aggregateID, err)
	}

	root.Update()
	return nil
}

func (as *aggregateStore) getFromSnapshot(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) (bool, error) {
	found := as.repo.ReadSnapshot(ctx, aggregateID, agg.Root().Version(), agg)
	if !found {
		return false, nil
	}

	root := agg.Root()
	if root.BaseVersion() >= root.Version() {
		return true, nil
	}

	if err := as.repo.Get(ctx, aggregateID, root.BaseVersion(), root.Version(), agg); err != nil {
		return false, fmt.Errorf("replay events from version %d to %d: %w", root.BaseVersion(), root.Version(), err)
	}

	return true, nil
}

func (as *aggregateStore) shouldCreateSnapshot(baseVersion, currentVersion int) bool {
	return as.snapshotInterval > 0 && (currentVersion/as.snapshotInterval > baseVersion/as.snapshotInterval)
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
