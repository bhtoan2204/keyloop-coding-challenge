package projectors

import (
	"context"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/package/eventsourcing"
)

// OutboxProjector is an unwired stub kept from the original scaffolding; CDC on
// es_event makes an outbox unnecessary (docs/02).
type OutboxProjector struct {
}

func (p *OutboxProjector) Handle(ctx context.Context, store viewstore.ViewStore, ev eventsourcing.Event) error {
	return nil
}
