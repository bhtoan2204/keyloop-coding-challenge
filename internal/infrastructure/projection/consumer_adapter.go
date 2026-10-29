package projection

import (
	"context"
	"errors"
	"fmt"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/package/eventsourcing"
)

var (
	_ messaging.Projector    = (*ConsumerAdapter)(nil)
	_ messaging.Synchronizer = (*ConsumerAdapter)(nil)
	_ messaging.EventHistory = (*ConsumerAdapter)(nil)
)

// StreamReader reads one aggregate's events from the event store (repos.EventStore).
type StreamReader interface {
	ReadStream(ctx context.Context, aggregateID string, afterVersion, toVersion int) ([]eventsourcing.Event, error)
}

// ConsumerAdapter connects the live change-feed consumer (application/messaging) to
// the existing Projection, Runner and event store. It delegates unchanged and
// translates their errors into the consumer's error contract, keeping the original
// error in the chain (as commandstore does for commands).
type ConsumerAdapter struct {
	projection Projection
	runner     *Runner
	history    StreamReader
}

// NewConsumerAdapter wires the adapter; runner and history may be nil in tests that
// only apply events.
func NewConsumerAdapter(projection Projection, runner *Runner, history StreamReader) *ConsumerAdapter {
	return &ConsumerAdapter{
		projection: projection,
		runner:     runner,
		history:    history,
	}
}

// Apply applies one event: a version gap is repairable, a rejected event is poison,
// anything else (database unavailable) is transient.
func (a *ConsumerAdapter) Apply(ctx context.Context, ev eventsourcing.Event) (bool, error) {
	applied, err := a.projection.Apply(ctx, ev)
	switch {
	case err == nil:
		return applied, nil
	case errors.Is(err, ErrVersionGap):
		return false, fmt.Errorf("%w: %w", messaging.ErrVersionGap, err)
	case errors.Is(err, ErrUnsupportedSchemaVersion),
		errors.Is(err, projectors.ErrUnsupportedEvent),
		errors.Is(err, projectors.ErrInconsistent):
		return false, fmt.Errorf("%w: %w", messaging.ErrRejectedEvent, err)
	default:
		return false, err
	}
}

// ReadStream reads the versions a gap repair needs. A stored event this build cannot
// decode is poison, as it is when it arrives through the change feed.
func (a *ConsumerAdapter) ReadStream(ctx context.Context, aggregateID string, afterVersion, toVersion int) ([]eventsourcing.Event, error) {
	events, err := a.history.ReadStream(ctx, aggregateID, afterVersion, toVersion)
	switch {
	case err == nil:
		return events, nil
	case errors.Is(err, repos.ErrUnknownEventType):
		return nil, fmt.Errorf("%w: %w", messaging.ErrUnknownEventType, err)
	case errors.Is(err, repos.ErrUnsupportedSchemaVersion):
		return nil, fmt.Errorf("%w: %w", messaging.ErrUnsupportedSchemaVersion, err)
	default:
		return nil, err
	}
}

// Sync is the consumer's catch-up from the event store: Runner.Sync from one
// consistent snapshot.
func (a *ConsumerAdapter) Sync(ctx context.Context) (int, error) {
	res, err := a.runner.Sync(ctx)
	return res.Applied, err
}
