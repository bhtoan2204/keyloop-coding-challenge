package projection

import (
	"context"

	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/pkg/eventsourcing"
)

// EventSource is the read-only event history (repos.EventStore.ReadAll).
type EventSource interface {
	ReadAll(ctx context.Context, batchSize int, fn func(eventsourcing.Event) error) error
}

// Runner rebuilds the read model from the event store (recovery; commands keep it up to
// date in their own transactions).
type Runner struct {
	// sourceFor returns the event history bound to the run's transaction, so the events
	// are read on the same connection and transaction that rewrites the read model.
	sourceFor  func(tx *gorm.DB) EventSource
	store      *viewstore.Postgres
	projectors []projectors.Projector
}

type RunResult struct {
	Read    int
	Applied int
}

func NewRunner(sourceFor func(tx *gorm.DB) EventSource, store *viewstore.Postgres, projs ...projectors.Projector) *Runner {
	return &Runner{
		sourceFor:  sourceFor,
		store:      store,
		projectors: projs,
	}
}

// Rebuild empties the projections and replays the full history in ONE transaction that
// holds the projection lock exclusively. Commands take that lock (shared) before they
// write events, so none can commit during the run and the history read in batches is
// stable. TRUNCATE takes an exclusive table lock, so queries wait until the rebuilt
// model commits; they never see a partial one, and a failure leaves the old model intact.
func (r *Runner) Rebuild(ctx context.Context) (RunResult, error) {
	read, applied, err := r.store.RunExclusive(ctx, "rebuild", true, func(tx *viewstore.Postgres) (int, int, error) {
		p := NewProjection(tx, r.projectors...)
		read, applied := 0, 0
		err := r.sourceFor(tx.Tx()).ReadAll(ctx, 500, func(ev eventsourcing.Event) error {
			read++
			ok, err := p.Apply(ctx, ev)
			if ok {
				applied++
			}
			return err
		})
		return read, applied, err
	})
	return RunResult{Read: read, Applied: applied}, err
}
