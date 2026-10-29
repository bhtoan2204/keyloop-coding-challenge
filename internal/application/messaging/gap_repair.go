package messaging

import (
	"context"
	"errors"
	"fmt"

	"keyloop-coding-challenge/package/eventsourcing"
)

// apply applies ev through the projector. When the projector reports a version gap
// (earlier versions of the aggregate were not delivered: a snapshot/stream boundary,
// a re-snapshot, records lost to retention), the missing versions are read from the
// event store and applied first. Nothing is skipped: if the event store does not have
// them either, the gap is unrepairable and the record fails.
func (c *Consumer) apply(ctx context.Context, ev eventsourcing.Event) (applied bool, repaired int, err error) {
	applied, err = c.projector.Apply(ctx, ev)
	if !errors.Is(err, ErrVersionGap) {
		return applied, 0, err
	}
	repaired, err = c.repairGap(ctx, ev)
	if err != nil {
		return false, repaired, err
	}
	c.log.WarnContext(ctx, "repaired version gap from the event store",
		"aggregate_id", ev.AggregateID,
		"version", ev.Version,
		"events_applied", repaired)
	applied, err = c.projector.Apply(ctx, ev)
	return applied, repaired, err
}

// repairGap applies versions 1..ev.Version-1 of ev's aggregate from the event store.
// Versions already applied are skipped by the projector as duplicates. Once ev is
// committed, every lower version is committed too (writers of one aggregate are
// serialised by the version CAS), so the read returns all of them or the history is
// broken.
func (c *Consumer) repairGap(ctx context.Context, ev eventsourcing.Event) (int, error) {
	missing, err := c.history.ReadStream(ctx, ev.AggregateID, 0, ev.Version-1)
	if err != nil {
		return 0, fmt.Errorf("repair gap before %s v%d: %w", ev.AggregateID, ev.Version, err)
	}
	for i, m := range missing {
		if m.AggregateID != ev.AggregateID || m.Version != i+1 {
			return 0, fmt.Errorf("%w: event store returned %s v%d at position %d", ErrUnrepairableGap, m.AggregateID, m.Version, i+1)
		}
	}
	if len(missing) != ev.Version-1 {
		return 0, fmt.Errorf("%w: %s has %d stored events below version %d", ErrUnrepairableGap, ev.AggregateID, len(missing), ev.Version)
	}
	repaired := 0
	for _, m := range missing {
		applied, err := c.projector.Apply(ctx, m)
		if err != nil {
			return repaired, fmt.Errorf("repair gap before %s v%d: apply v%d: %w", ev.AggregateID, ev.Version, m.Version, err)
		}
		if applied {
			repaired++
		}
	}
	return repaired, nil
}
