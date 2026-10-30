package command

import (
	"context"
	"time"

	"keyloop-coding-challenge/pkg/eventsourcing"
)

// VehicleStore is the persistence port the commands use (implemented by
// eventstore.CommandStore over the existing AggregateStore).
type VehicleStore interface {
	Get(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) error
	SaveWithMetadata(ctx context.Context, agg eventsourcing.Aggregate, metadata map[string]interface{}) error
}

// DealershipDirectory resolves a dealership's IANA time zone. It returns
// ErrDealershipNotFound for an unknown dealership.
type DealershipDirectory interface {
	Location(ctx context.Context, dealershipID string) (*time.Location, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
