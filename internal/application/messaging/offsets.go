package messaging

import (
	"context"
	"time"
)

// commitTimeout bounds an offset commit. A commit that fails or times out loses
// nothing: the records are re-delivered and skipped as duplicates by version.
const commitTimeout = 10 * time.Second

// commitProcessed commits, per partition, the offset after the last record whose
// read-model transaction has committed. It runs only after every partition of the
// batch has finished, so an offset is never committed ahead of its transaction.
func (c *Consumer) commitProcessed(ctx context.Context, results []partitionResult) {
	var processed []Record
	for _, r := range results {
		if r.processed != nil {
			processed = append(processed, *r.processed)
			c.health.partitionProcessed(*r.processed)
		}
	}
	if len(processed) == 0 {
		return
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	if err := c.transport.Commit(commitCtx, processed); err != nil {
		c.log.WarnContext(ctx, "offset commit failed; the records will be re-delivered and skipped as duplicates",
			"error", err.Error())
	}
}

// rewindFailed makes each failed partition deliver its failing record again after a
// backoff. The partition does not advance past it: the record is never skipped.
func (c *Consumer) rewindFailed(ctx context.Context, results []partitionResult) {
	for _, r := range results {
		if r.failed == nil {
			continue
		}
		poison := isPoison(r.err)
		failures := c.health.partitionFailed(*r.failed, r.err, poison)
		delay := c.cfg.retryDelay(failures, poison)
		log := c.log.WarnContext
		if poison {
			log = c.log.ErrorContext
		}
		log(ctx, "record failed; partition halted at it and will retry",
			"topic", r.failed.Topic,
			"partition", r.failed.Partition,
			"offset", r.failed.Offset,
			"poison", poison,
			"consecutive_failures", failures,
			"retry_in_ms", delay.Milliseconds(),
			"error", r.err.Error())
		c.transport.Retry(*r.failed, delay)
	}
}
