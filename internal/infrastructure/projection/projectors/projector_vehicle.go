package projectors

import (
	"context"
	"keyloop-coding-challenge/package/eventsourcing"
)

type VehicleProjector struct {
}

func (p *VehicleProjector) Handle(ctx context.Context, ev eventsourcing.Event) error {
	return nil
}
