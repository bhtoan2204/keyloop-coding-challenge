package projectors

import (
	"context"
	"keyloop-coding-challenge/package/eventsourcing"
)

type OutboxProjector struct {
}

func (p *OutboxProjector) Handle(ctx context.Context, ev eventsourcing.Event) error {
	return nil
}
