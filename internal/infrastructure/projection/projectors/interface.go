package projectors

import (
	"context"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/package/eventsourcing"
)

// Projector applies one event to the read model through the transaction-bound store
// it is given. It must not start goroutines that use the store: the transaction is not
// safe for concurrent use.
type Projector interface {
	Handle(ctx context.Context, store projection.Store, ev eventsourcing.Event) error
}
