package messaging

import "context"

// bootstrap runs before the first poll. With StartupSync it applies, from one
// consistent snapshot of the event store, every event committed so far; the change
// feed then only has to deliver what commits afterwards, and anything it re-delivers
// from before is skipped as a duplicate by version. This makes startup independent of
// what the feed still holds (topic retention, connector re-snapshots) and of the
// order of the connector's initial snapshot.
//
// The sync takes the projection lock exclusively and fails fast while another
// applier holds it, so it is retried with backoff until it succeeds or ctx ends.
func (c *Consumer) bootstrap(ctx context.Context) error {
	if !c.cfg.StartupSync || c.sync == nil {
		return nil
	}
	return c.syncFromEventStore(ctx, "startup")
}

// syncFromEventStore runs Synchronizer.Sync until it succeeds or ctx ends, reporting
// the syncing phase meanwhile (not ready). Used at startup and after the feed lost
// records (ErrFeedDataLoss).
func (c *Consumer) syncFromEventStore(ctx context.Context, reason string) error {
	c.health.setPhase(PhaseSyncing)
	for attempt := 1; ; attempt++ {
		applied, err := c.sync.Sync(ctx)
		if err == nil {
			c.log.InfoContext(ctx, "sync from the event store done",
				"reason", reason,
				"events_applied", applied)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := c.cfg.retryDelay(attempt, false)
		c.log.WarnContext(ctx, "sync from the event store failed; retrying",
			"reason", reason,
			"attempt", attempt,
			"retry_in_ms", delay.Milliseconds(),
			"error", err.Error())
		wait(ctx, delay)
	}
}
