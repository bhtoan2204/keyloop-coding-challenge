package projectors

import (
	"context"
	"keyloop-coding-challenge/package/eventsourcing"
)

type Projector interface {
	Handle(ctx context.Context, ev eventsourcing.Event) error
}
