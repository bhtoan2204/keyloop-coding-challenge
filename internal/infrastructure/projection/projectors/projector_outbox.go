package projectors

import (
	"context"

	"keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/package/eventsourcing"
)

// OutboxProjector is an unwired stub kept from the original scaffolding; CDC on
// es_event makes an outbox unnecessary (docs/02).
type OutboxProjector struct {
}

func (p *OutboxProjector) Handle(ctx context.Context, store projection.Store, ev eventsourcing.Event) error {
	return nil
}
