package command

import (
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
)

// Target addresses an existing vehicle. ExpectedVersion, when set (If-Match), must
// equal the stored version; a stale value is rejected rather than retried.
type Target struct {
	DealershipID    string
	VehicleID       string
	ExpectedVersion *int
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
