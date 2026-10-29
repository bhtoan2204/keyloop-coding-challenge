// Package command contains the write-side application services: it loads the
// VehicleInventoryAggregate, resolves the dealership business date, runs the domain
// command and saves the resulting events through the existing AggregateStore.
package command

import (
	"context"
	"errors"
	"time"

	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/package/eventsourcing"
)

// Application errors (domain errors from internal/domain pass through unchanged).
var (
	ErrDealershipNotFound = errors.New("dealership not found")
	// ErrVehicleNotFound also covers a vehicle owned by another dealership, so a URL
	// cannot be used to probe other dealerships' stock.
	ErrVehicleNotFound = errors.New("vehicle not found")
	// ErrVersionMismatch means the caller's expected version (If-Match) is stale.
	ErrVersionMismatch = errors.New("aggregate version mismatch")
)

// Store error contract. VehicleStore implementations report storage outcomes with
// these sentinels (wrapping the cause), so this layer does not depend on a store.
var (
	// ErrStreamNotFound: Get found no vehicle stream under the ID.
	ErrStreamNotFound = errors.New("stream not found")
	// ErrConflict: optimistic concurrency conflict; nothing was written.
	ErrConflict = errors.New("concurrent modification")
	// ErrTransient: deadlock or lock wait timeout; the transaction was rolled back.
	ErrTransient = errors.New("transient storage failure")
	// ErrUnavailable: storage unreachable or the connection broke; a write's outcome
	// is unknown, so it must not be retried automatically.
	ErrUnavailable = errors.New("storage unavailable")
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

// Meta identifies who issued a command and how to correlate it. It is stored in the
// metadata of every event the command produces.
type Meta struct {
	ActorID       string
	CorrelationID string
	TraceParent   string
}

func (m Meta) toMap() map[string]interface{} {
	md := map[string]interface{}{"actor_id": m.ActorID, "correlation_id": m.CorrelationID}
	if m.TraceParent != "" {
		md["traceparent"] = m.TraceParent
	}
	return md
}

// Target addresses an existing vehicle. ExpectedVersion, when set (If-Match), must
// equal the stored version; a stale value is rejected rather than retried.
type Target struct {
	DealershipID    string
	VehicleID       string
	ExpectedVersion *int
}

type StockVehicle struct {
	DealershipID string
	VehicleID    string // optional; generated when empty
	VIN          string
	Make         string
	Model        string
	ModelYear    int
	StockedOn    domain.Date
}

type ProposeAction struct {
	Target
	ActionType domain.ActionType
	Note       string
}

type ReviseAction struct {
	Target
	ActionID   string
	ActionType domain.ActionType
	Note       string
}

type CancelAction struct {
	Target
	ActionID string
	Reason   string
}

type SellVehicle struct {
	Target
	SoldOn domain.Date
}

// Result is the committed write-side state after a command.
type Result struct {
	Vehicle      *aggregates.VehicleInventoryAggregate
	BusinessDate domain.Date
	// ActionID is set by ProposeAction.
	ActionID string
	// Changed is false when the command was a no-op (identical revision).
	Changed bool
}
