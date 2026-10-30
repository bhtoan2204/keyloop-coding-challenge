package projection

import (
	"context"
	"errors"
	"fmt"

	ports "keyloop-coding-challenge/internal/application/projection"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/package/eventsourcing"
)

var (
	// ErrVersionGap: the event is not the next version of its aggregate (missing or
	// out-of-order events). Nothing is applied; the caller must not skip it.
	ErrVersionGap = errors.New("aggregate version gap")
	// ErrUnsupportedSchemaVersion: the payload schema is newer than this build.
	ErrUnsupportedSchemaVersion = errors.New("unsupported event schema version")
)

// Projection applies events to the read model. The same Apply is used by commands
// (inside their save transaction) and by the rebuild.
type Projection interface {
	// Apply applies ev in its own transaction, or joins the store's transaction when the
	// store is bound to one (a command's save, a rebuild run), without a savepoint. It
	// returns applied=false for a duplicate (version already applied).
	Apply(ctx context.Context, ev eventsourcing.Event) (applied bool, err error)
}

type projectionPipeline struct {
	store  ports.Store
	stages [][]projectors.Projector
}

func NewProjection(store ports.Store, projs ...projectors.Projector) Projection {
	return &projectionPipeline{
		store: store,
		stages: [][]projectors.Projector{
			projs,
		},
	}
}

// Apply serialises per aggregate with a transaction-scoped lock (which also covers the
// first event of a vehicle that has no row yet), skips duplicates, rejects gaps, and
// commits the projected state, history and applied version in one transaction.
func (p *projectionPipeline) Apply(ctx context.Context, ev eventsourcing.Event) (bool, error) {
	if v := ev.SchemaVersion; v != 0 && v != eventsourcing.DefaultSchemaVersion { // 0 means the default
		return false, fmt.Errorf("%w: %s v%d has schema version %d", ErrUnsupportedSchemaVersion, ev.AggregateID, ev.Version, ev.SchemaVersion)
	}
	applied := false
	err := p.store.WithTransaction(ctx, func(tx ports.Store) error {
		current, err := tx.LockAggregate(ctx, ev.AggregateID)
		if err != nil {
			return err
		}
		if ev.Version <= current {
			return nil // duplicate delivery: already applied
		}
		if ev.Version != current+1 {
			return fmt.Errorf("%w: aggregate %s at version %d received version %d", ErrVersionGap, ev.AggregateID, current, ev.Version)
		}
		for stageIdx, stage := range p.stages {
			if err := p.runStage(ctx, tx, stage, ev); err != nil {
				return fmt.Errorf("projection failed at stage %d for %s v%d: %w", stageIdx, ev.AggregateID, ev.Version, err)
			}
		}
		if err := tx.SetAppliedVersion(ctx, ev.AggregateID, ev.Version, ev.ID); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// runStage runs the stage's projectors one after another: they share one database
// transaction, which must not be used concurrently.
func (p *projectionPipeline) runStage(ctx context.Context, tx ports.Store, stage []projectors.Projector, ev eventsourcing.Event) error {
	for _, proj := range stage {
		if err := proj.Handle(ctx, tx, ev); err != nil {
			return fmt.Errorf("projector %T: %w", proj, err)
		}
	}
	return nil
}
