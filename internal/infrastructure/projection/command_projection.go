package projection

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

var _ eventstore.TransactionalProjection = (*CommandProjection)(nil)

// CommandProjection applies a command's events to the read model inside the command's
// save transaction (eventstore.WithProjection), through the same Projection.Apply and
// projectors a rebuild uses. A projection failure fails the save: events, read model,
// history and applied version commit together or not at all.
type CommandProjection struct {
	projectors []projectors.Projector
}

func NewCommandProjection(projs ...projectors.Projector) *CommandProjection {
	return &CommandProjection{projectors: projs}
}

// Lock takes the projection lock (shared) before the save locks any event-store row,
// so a rebuild (exclusive) and commands always acquire locks in the same order, and no
// event can commit while a rebuild runs.
func (c *CommandProjection) Lock(ctx context.Context, tx *gorm.DB) error {
	return viewstore.InTransaction(tx).LockProjectionShared(ctx)
}

func (c *CommandProjection) Apply(ctx context.Context, tx *gorm.DB, events []eventsourcing.Event) error {
	p := NewProjection(viewstore.InTransaction(tx), c.projectors...)
	for _, ev := range events {
		applied, err := p.Apply(ctx, ev)
		if err != nil {
			return err
		}
		if !applied {
			// The read model already had this version: it is ahead of the event store
			// (e.g. restored separately). Committing would hide the divergence.
			return fmt.Errorf("%w: %s v%d is already projected", ErrVersionGap, ev.AggregateID, ev.Version)
		}
	}
	return nil
}
