package projection

import (
	"context"

	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/internal/infrastructure/viewstore"
	"keyloop-coding-challenge/package/eventsourcing"
)

// EventSource is the read-only event history (repos.EventStore.ReadAll).
type EventSource interface {
	ReadAll(ctx context.Context, batchSize int, fn func(eventsourcing.Event) error) error
}

// Runner brings the read model up to date from the event store. Phase 4 has no live
// change feed: the read model only changes when Sync or Rebuild runs.
//
// Both read the WHOLE history from one consistent snapshot of the event store and
// apply it through the same Projection as live delivery will use. There is no id
// cursor between runs, so a run cannot skip an event that committed late; events
// committed after the snapshot are picked up by the next run. Writes may continue
// during a run (the result is the state as of the snapshot).
type Runner struct {
	source     EventSource
	store      *viewstore.Postgres
	projectors []projectors.Projector
}

type RunResult struct {
	Read    int
	Applied int
}

func NewRunner(source EventSource, store *viewstore.Postgres, projs ...projectors.Projector) *Runner {
	return &Runner{source: source, store: store, projectors: projs}
}

// Sync applies every event not yet applied (duplicates are skipped by version).
func (r *Runner) Sync(ctx context.Context) (RunResult, error) {
	return r.run(ctx, "sync", false)
}

// Rebuild empties the projections and replays the full history in one transaction.
// TRUNCATE takes an exclusive table lock, so queries wait until the rebuilt model
// commits; they never see a partial one, and a failure leaves the old model intact.
func (r *Runner) Rebuild(ctx context.Context) (RunResult, error) {
	return r.run(ctx, "rebuild", true)
}

func (r *Runner) run(ctx context.Context, kind string, truncate bool) (RunResult, error) {
	read, applied, err := r.store.RunExclusive(ctx, kind, truncate, func(tx viewstore.ViewStore) (int, int, error) {
		p := NewProjection(tx, r.projectors...)
		read, applied := 0, 0
		err := r.source.ReadAll(ctx, 500, func(ev eventsourcing.Event) error {
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
