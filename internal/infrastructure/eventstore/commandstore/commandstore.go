// Package commandstore adapts the event store to the application command port.
package commandstore

import (
	"context"
	"errors"
	"fmt"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/package/eventsourcing"
)

var _ command.VehicleStore = (*Store)(nil)

// Store adapts eventstore.AggregateStore to command.VehicleStore: it delegates
// unchanged and translates store errors into the application's error contract,
// keeping the original error in the chain for logs.
type Store struct {
	store eventstore.AggregateStore
}

func New(store eventstore.AggregateStore) *Store {
	return &Store{store: store}
}

func (c *Store) Get(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) error {
	err := c.store.Get(ctx, aggregateID, agg)
	if errors.Is(err, eventstore.ErrAggregateNotFound) {
		return fmt.Errorf("%w: %w", command.ErrStreamNotFound, err)
	}
	return translate(err)
}

func (c *Store) SaveWithMetadata(ctx context.Context, agg eventsourcing.Aggregate, metadata map[string]interface{}) error {
	return translate(c.store.SaveWithMetadata(ctx, agg, metadata))
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, eventstore.ErrConcurrencyConflict):
		return fmt.Errorf("%w: %w", command.ErrConflict, err)
	case errors.Is(err, eventstore.ErrRetryable):
		return fmt.Errorf("%w: %w", command.ErrTransient, err)
	case repos.IsUnavailable(err):
		return fmt.Errorf("%w: %w", command.ErrUnavailable, err)
	default:
		return err
	}
}
